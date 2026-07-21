// Command polishpoc is the PR2 PoC driver for the subtitle-polish pipeline.
//
// It connects READ-ONLY to the real studyquest.db, loads episode_id=<n>'s
// subtitle (from the post-migration `vtt_content` column), runs polish.Polish
// against the live configured AIProvider, and writes the before/after/diff/
// glossary/stats artifacts to backend/data/polish-poc-output/.
//
// It writes NOTHING to the database. The production server keeps running
// undisturbed (read-only DSN + busy_timeout).
//
// Usage:
//
//	go run ./cmd/polishpoc -episode 32 -run run2
//
// The -run flag tags the output subdirectory so multiple runs don't overwrite
// each other (e.g. .../polish-poc-output/ep32-run2/). Useful for stability
// testing (same episode, several runs) and generalization testing (different
// episodes).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/polish"
	"studyquest/backend/internal/subtitle"
)

func main() {
	var (
		episodeID = flag.Int("episode", 32, "episode id to polish")
		dbPath    = flag.String("db", "data/studyquest.db", "path to studyquest.db (read-only)")
		outDir    = flag.String("out", "data/polish-poc-output", "output root directory")
		runTag    = flag.String("run", "default", "run tag (subdir under -out, to avoid overwriting)")
	)
	flag.Parse()

	abs := func(p string) string {
		if filepath.IsAbs(p) {
			return p
		}
		ap, err := filepath.Abs(p)
		if err != nil {
			return p
		}
		return ap
	}
	dbAbs := abs(*dbPath)
	// out layout: <outDir>/ep<episode>-<runTag>/
	runDir := filepath.Join(abs(*outDir), fmt.Sprintf("ep%d-%s", *episodeID, *runTag))

	if err := os.MkdirAll(runDir, 0755); err != nil {
		fatalf("create out dir: %v", err)
	}

	// Open READ-ONLY. SQLite's ?mode=ro plus ?_readonly force no writes even
	// if some code path tried. busy_timeout so a running server doesn't
	// trip us with "database is locked".
	dsn := "file:" + dbAbs + "?mode=ro&_busy_timeout=5000&_foreign_keys=on"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		fatalf("open db %s: %v", dbAbs, err)
	}

	// Read the subtitle. Use raw SQL: the column is `vtt_content` post-migration,
	// and going through the model would trigger AutoMigrate which we don't want
	// on a read-only PoC connection.
	var vttContent string
	if err := db.Raw(
		"SELECT vtt_content FROM subtitles WHERE episode_id = ? LIMIT 1",
		*episodeID,
	).Row().Scan(&vttContent); err != nil {
		fatalf("read subtitle for episode %d: %v", *episodeID, err)
	}
	if vttContent == "" {
		fatalf("episode %d has empty subtitle", *episodeID)
	}
	infof("loaded subtitle: episode=%d  chars=%d", *episodeID, len(vttContent))

	// Look up the episode's subject so we can pick a TermDict + subject label
	// matching what production would feed (Course.EffectiveTermDict(subject)).
	// Joins: episodes.course_id → courses.subject_id → subjects.key.
	var subjectKey string
	_ = db.Raw(
		`SELECT s.key FROM episodes e
		  JOIN courses c ON e.course_id = c.id
		  JOIN subjects s ON c.subject_id = s.id
		 WHERE e.id = ? LIMIT 1`,
		*episodeID,
	).Row().Scan(&subjectKey)
	termDict, subjectLabel := termDictFor(subjectKey)
	infof("subject=%s  term_dict_len=%d", subjectLabel, len(termDict))

	// Read the enabled chat provider row. Build a minimal provider inline
	// rather than going through the full ProviderResolver — PoC doesn't
	// need the caching layer.
	type providerRow struct {
		BaseURL   string
		APIKey    string
		ModelName string
	}
	var pr providerRow
	if err := db.Raw(
		"SELECT base_url, api_key, model_name FROM ai_providers WHERE capability = ? AND is_enabled = 1 ORDER BY id ASC LIMIT 1",
		"chat",
	).Row().Scan(&pr.BaseURL, &pr.APIKey, &pr.ModelName); err != nil {
		fatalf("read ai_providers: %v", err)
	}
	infof("provider: base=%s model=%s key_len=%d (key not shown)",
		pr.BaseURL, pr.ModelName, len(pr.APIKey))

	llm := ai.NewOpenAICompatProvider(pr.BaseURL, pr.APIKey)
	llm.SetModel(pr.ModelName)

	// Subtitle in DB is already VTT (post-migration). Stash the VttToSrt
	// baseline so we can write original.srt with the exact same text the
	// polish pipeline saw (it does VttToSrt internally).
	originalSRT := subtitle.VttToSrt(vttContent)

	// Run polish with a generous overall deadline (single chunk can take
	// 60+ seconds on a slow relay; we have ~24 chunks at concurrency 3).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	infof("starting polish: subject=%s ...", subjectLabel)
	result, err := polish.Polish(ctx, llm, pr.ModelName, polish.PolishRequest{
		VttContent: vttContent,
		TermDict:   termDict,
		Subject:    subjectLabel,
	})
	if err != nil {
		fatalf("polish: %v", err)
	}
	infof("polish done in %s", result.Stats.Duration)

	// --- write artifacts ---
	polishedSRT := subtitle.VttToSrt(result.PolishedVtt)

	if err := writeText(filepath.Join(runDir, "original.srt"), originalSRT); err != nil {
		fatalf("write original.srt: %v", err)
	}
	if err := writeText(filepath.Join(runDir, "polished.srt"), polishedSRT); err != nil {
		fatalf("write polished.srt: %v", err)
	}

	diffJSON, _ := json.MarshalIndent(result.Diff, "", "  ")
	if err := writeText(filepath.Join(runDir, "diff.json"), string(diffJSON)); err != nil {
		fatalf("write diff.json: %v", err)
	}

	glossaryJSON, _ := json.MarshalIndent(result.Glossary, "", "  ")
	if err := writeText(filepath.Join(runDir, "glossary.json"), string(glossaryJSON)); err != nil {
		fatalf("write glossary.json: %v", err)
	}

	statsTxt := buildStatsText(result.Stats, pr.ModelName, *episodeID)
	if err := writeText(filepath.Join(runDir, "stats.txt"), statsTxt); err != nil {
		fatalf("write stats.txt: %v", err)
	}

	// Echo stats + first 20 diffs to stdout for quick review.
	fmt.Println()
	fmt.Println("=================== STATS ===================")
	fmt.Print(statsTxt)
	fmt.Println()
	fmt.Printf("=================== FIRST 20 DIFFS (of %d) ===================\n", len(result.Diff))
	for i := 0; i < len(result.Diff) && i < 20; i++ {
		d := result.Diff[i]
		fmt.Printf("  [#%d] %q\n        → %q\n", d.ID, d.Before, d.After)
	}
	fmt.Println()
	fmt.Printf("=================== GLOSSARY (%d entries) ===================\n", len(result.Glossary))
	for _, g := range result.Glossary {
		fmt.Printf("  %s → %s   conf=%.2f   evidence=%v\n", g.Original, g.Corrected, g.Confidence, g.EvidenceIDs)
		if g.Context != "" {
			fmt.Printf("      ctx: %s\n", g.Context)
		}
	}
	fmt.Println()
	fmt.Printf("Artifacts written to: %s\n", runDir)
}

// termDictFor returns a hardcoded TermDict + subject label for the PoC. This
// mirrors what production will derive from Course.EffectiveTermDict(subject)
// at polish time. For subjects without a known dict, we return empty so we
// can observe the "no TermDict, LLM relies on common sense" behavior.
func termDictFor(subjectKey string) (dict, label string) {
	switch subjectKey {
	case "xiangqi":
		// Mirrors the seed in SeedDefaultSubjects for xiangqi (象棋术语 —
		// Whisper 常见同音错字纠错字典).
		return "军→车（象棋术语，棋子）;局→车（象棋术语，棋子）;何→和（象棋术语，和棋）;合→和（象棋术语，和棋）;金→进（象棋走法）;足→卒（象棋术语，棋子）;猎→列（象棋术语，列手炮）", "象棋"
	case "math":
		// 数学课字幕术语相对少，留空观察 LLM 在无字典时的"常识纠错"能力
		// 和"过度纠错"风险（数学课主要内容是数字和计算，错字相对少）。
		return "", "数学"
	default:
		return "", subjectKey
	}
}

// buildStatsText renders the stats block including an estimated cost using
// DeepSeek-chat pricing (input ¥0.001/1k, output ¥0.004/1k) per the PoC brief.
func buildStatsText(s polish.PolishStats, model string, episodeID int) string {
	costIn := float64(s.PromptTokens) * 0.001 / 1000.0
	costOut := float64(s.CompletionTokens) * 0.004 / 1000.0
	cost := costIn + costOut
	changeRate := 0.0
	if s.TotalCues > 0 {
		changeRate = float64(s.ChangedCues) / float64(s.TotalCues) * 100
	}
	// Failed-chunk error details (only when some chunk failed). Helps diagnose
	// why a run came back partial — validation reject vs JSON parse vs network.
	var failDetail string
	if len(s.FailedChunkErrors) > 0 {
		// Numeric sort by chunk idx (lexical would put chunk#10 before chunk#2).
		indices := make([]int, 0, len(s.FailedChunkErrors))
		for idx := range s.FailedChunkErrors {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		var b strings.Builder
		for _, idx := range indices {
			e := s.FailedChunkErrors[idx]
			if rs := []rune(e); len(rs) > 120 {
				e = string(rs[:120]) + "…"
			}
			fmt.Fprintf(&b, "  chunk#%d: %s\n", idx, e)
		}
		failDetail = "failed_chunk_errors:\n" + b.String()
	}
	return fmt.Sprintf(`episode_id        : %d
model             : %s
total_cues        : %d
changed_cues      : %d
change_rate       : %.2f%%
chunk_count       : %d
llm_calls         : %d  (successful)
failed_chunks     : %d  (used original text)
retries           : %d
prompt_tokens     : %d
completion_tokens : %d
duration          : %s
partial_optimized : %v
estimated_cost    : ¥%.4f  (DeepSeek price: input ¥0.001/1k, output ¥0.004/1k)
%s`,
		episodeID, model,
		s.TotalCues, s.ChangedCues, changeRate,
		s.ChunkCount, s.LLMCalls, s.FailedChunks, s.Retries,
		s.PromptTokens, s.CompletionTokens,
		s.Duration.Round(time.Millisecond),
		s.PartialOptimized,
		cost,
		failDetail,
	)
}

func writeText(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

func infof(format string, args ...any) {
	fmt.Printf("[polishpoc] "+format+"\n", args...)
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "[polishpoc] FATAL: "+format+"\n", args...)
	os.Exit(1)
}
