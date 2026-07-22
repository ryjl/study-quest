package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/polish"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/subtitle"
)

// Code split from ai_service.go for navigability.
// Polish pipeline: runPolishJob, glossary workflow.

func (s *aiService) runPolishJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		// No resolver = AI entirely off. Block (failed) so the admin notices
		// once they configure a provider; they can retry or skip then.
		s.failJob(job, "AI not configured (no resolver)")
		return
	}
	if job.EpisodeID == nil || job.CourseID == nil {
		s.failJob(job, "polish job missing episode_id/course_id")
		return
	}
	episodeID, courseID := *job.EpisodeID, *job.CourseID

	sub, err := s.episodeRepo.GetSubtitle(episodeID)
	if err != nil {
		s.failJob(job, "load subtitle: "+err.Error())
		return
	}
	if sub == nil {
		s.failJob(job, "no primary subtitle for this episode")
		return
	}
	if !isPolishableSource(sub.Source) {
		// Shouldn't happen (EnqueuePolish and OnSubtitleCompleted gate on
		// source via the same isPolishableSource helper), but if a misrouted
		// job slips through, skip it cleanly rather than polishing a track
		// that's human-corrected (embedded/manual). This is NOT a failure —
		// chain to segment so downstream proceeds.
		//
		// Note (2026-07-21): isPolishableSource now accepts BOTH "whisper"
		// (raw transcript) AND "llm_optimized" (already polished once). The
		// latter is the re-polish path — admin accepts new glossary terms
		// → Course.TermDict grows → they want the new terminology applied
		// to an already-polished episode. Re-polish is drift-safe because
		// polish reads RawVttContent (the immutable whisper snapshot), not
		// the current VttContent. See model.Subtitle.RawVttContent doc.
		s.contentRepo.UpdateJobStatus(job.ID, "skipped",
			"source="+sub.Source+" not eligible for polish", nil)
		s.enqueueSegmentForPolish(episodeID, courseID)
		return
	}

	// Resolve the chat provider. The polishLLMOverride seam (non-nil only in tests) lets a
	// service-level test drive this path with a fake LLM instead of a real relay; production leaves it
	// nil and falls through to the resolver.
	var llm ai.LLMProvider
	var modelName string
	if s.polishLLMOverride != nil {
		llm = s.polishLLMOverride
		modelName = "test-model"
	} else {
		resolved, err := s.resolver.ResolveChatByPurpose("polish")
		if err != nil {
			// Provider not configured / misconfigured. Block — admin fixes the
			// provider config and retries. We do NOT fall through to segment,
			// because the whole point of polish is to fix the raw transcript
			// before AI consumes it.
			s.failJob(job, "resolve chat provider: "+err.Error())
			return
		}
		llm = resolved
		modelName = s.resolver.ChatModelNameByPurpose("polish")
	}

	// Build the polish request: TermDict comes from Course + Subject merge
	// (Course.EffectiveTermDict). Subject is also passed to the LLM as domain
	// context ("xiangqi" vs "math" primes it toward the right terminology).
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		s.failJob(job, "load course: "+err.Error())
		return
	}
	if course == nil {
		// courseRepo.FindByID returns (nil, nil) when the row was deleted
		// between enqueue and run. Split from the err branch above so we don't
		// dereference a nil err — that panic would kill the AI worker goroutine
		// (runWorker has no recover).
		s.failJob(job, fmt.Sprintf("course %d not found (deleted after polish enqueue?)", courseID))
		return
	}
	var subject model.Subject
	if s.subjectRepo != nil {
		if subj, serr := s.subjectRepo.FindByID(course.SubjectID); serr == nil && subj != nil {
			subject = *subj
		}
	}
	termDict := course.EffectiveTermDict(subject)
	subjectLabel := subject.Label
	if subjectLabel == "" {
		// Fall back to the key (e.g. "math") when Label is empty — the
		// polish prompt just needs SOME domain hint.
		subjectLabel = subject.Key
	}

	// Polish deadline: the PoC ran 7m13s for a 157k-char episode at concurrency 3.
	// 20 min is a generous ceiling that still catches a stuck relay.
	polishCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	// Source the polish input from RawVttContent (the immutable pre-polish
	// snapshot) when it exists, falling back to VttContent for legacy rows
	// that predate the RawVttContent column. This is the WHOLE POINT of
	// RawVttContent (see model.Subtitle doc): re-running polish must start
	// from the original whisper transcript each time, not from a prior polish
	// result — otherwise LLM drift compounds across re-polishes. The snapshot
	// is written by SaveSubtitleWithSource (Complete/upload/embedded extract
	// paths) and never overwritten by polish itself.
	polishInput := sub.RawVttContent
	if strings.TrimSpace(polishInput) == "" {
		polishInput = sub.VttContent
	}

	// SkipGlossaryMining on re-polish: when the source is already llm_optimized,
	// the subtitle content hasn't changed (we re-polish from the immutable
	// RawVttContent snapshot), so the term-mining the model could do here it
	// already did last run. Turning it off uses the lean system prompt (no
	// 术语挖矿 section) and skips the glossary completion tokens.
	skipMining := sub.Source == "llm_optimized"

	// 断点续润 (checkpoint/resume): seed + load chunk skeletons so a retry of a
	// partially-failed job skips the chunks that already succeeded. This is the
	// biggest token saver — without it a RetryJob re-burns ALL chunks; with it,
	// only the failed chunks re-call the LLM. Skipped entirely when
	// polishChunkRepo is nil (tests / pre-断点续润 behavior).
	priorOutcomes, onChunkDone, totalChunks := s.setupPolishCheckpoint(polishCtx, job, polishInput)

	result, err := polish.Polish(polishCtx, llm, modelName, polish.PolishRequest{
		VttContent:         polishInput,
		TermDict:           termDict,
		Subject:            subjectLabel,
		SkipGlossaryMining: skipMining,
		PriorOutcomes:      priorOutcomes,
		OnChunkDone:        onChunkDone,
	})
	if err != nil {
		s.failJob(job, "polish: "+err.Error())
		return
	}
	// Stamp final progress to 100% (or close) on the way out so the progress bar
	// lands cleanly regardless of outcome. 1.0 even on partial — the run IS over.
	if totalChunks > 0 {
		done := float64(totalChunks-len(result.Stats.FailedChunkErrors)) / float64(totalChunks)
		s.contentRepo.UpdateJobStatus(job.ID, "processing", "", &done)
	}

	// Build the human-readable detail string. Shared by both the partial-fail
	// and full-success paths so the admin sees consistent telemetry regardless
	// of outcome. Always includes: changed/total cues, glossary count, cost,
	// high-edit-distance count (the new informational stat from the 2026-07-21
	// validation relaxation — how many applied changes the admin should
	// spot-check in the diff UI).
	detail := fmt.Sprintf("polished: %d/%d cues changed, %d glossary candidates, high_edit_distance=%d, cost≈%s",
		result.Stats.ChangedCues, result.Stats.TotalCues, len(result.Glossary),
		result.Stats.HighEditDistanceCount, result.Stats.Duration.Truncate(time.Second))

	// Partial failure: at least one chunk exhausted retries without producing
	// a valid response (network error, JSON parse failure, or structural
	// corruption). This is the ONLY failure mode left after the 2026-07-21
	// validation relaxation — "suspicious but well-formed" changes no longer
	// fail, they apply with a high_edit_distance flag. So a partial now means
	// "the LLM genuinely didn't return usable output for some chunks", not
	// "we rejected the LLM's output as too aggressive".
	//
	// Behavior change (2026-07-21): partial now FAILS the job instead of
	// marking it done. Rationale: a half-polished subtitle (some chunks raw,
	// some polished) is worse than either pure-raw or pure-polished — it
	// produces inconsistent terminology downstream and the admin has no clean
	// way to tell which cues were corrected. Failing forces a conscious
	// decision: RetryJob (re-run with the same/fixed provider) or SkipPolish
	// (give up on polish, fall back to the raw subtitle via segment chain).
	// We do NOT write back the partial result, so the subtitle stays at its
	// pre-polish state (source=whisper, optimized=false) and no downstream
	// segment/summary runs off a contaminated transcript.
	if result.Stats.PartialOptimized {
		// Append per-chunk failure detail so the admin can see WHY each chunk
		// failed (parse error vs network vs unknown-id). Capped + sorted the
		// same way the old done-with-partial path did it.
		detail += fmt.Sprintf(" (partial: %d/%d chunks failed", result.Stats.FailedChunks, result.Stats.ChunkCount)
		type failEntry struct {
			idx int
			err string
		}
		entries := make([]failEntry, 0, len(result.Stats.FailedChunkErrors))
		for idx, e := range result.Stats.FailedChunkErrors {
			// Truncate by RUNE count, not bytes — error strings carry Chinese
			// (ffmpeg/relay messages localized) and a byte cut would produce
			// invalid UTF-8 mid-character.
			if rs := []rune(e); len(rs) > 120 {
				e = string(rs[:120]) + "…"
			}
			entries = append(entries, failEntry{idx, e})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
		if len(entries) > 0 {
			parts := make([]string, len(entries))
			for i, e := range entries {
				parts[i] = fmt.Sprintf("chunk#%d: %s", e.idx, e.err)
			}
			detail += "; " + strings.Join(parts, "; ")
		}
		detail += ")"
		// Record the (partial) run before failing so the admin can see how much
		// token/time was spent before the chunk died — mirrors course_summary's
		// record-on-failure. note carries a short summary of which chunks failed.
		s.recordPolishRun(job.ID, modelName, result, "fail", detail, skipMining)
		s.failJob(job, detail)
		return
	}

	// Full success: persist the polished subtitle. We write ONLY VttContent +
	// Optimized + Source — RawVttContent stays empty here so episodeRepo.
	// SaveSubtitle's "non-empty only" guard leaves the original snapshot
	// untouched. IsPrimary is echoed back from the loaded sub so the upsert
	// doesn't accidentally demote the primary track.
	if err := s.episodeRepo.SaveSubtitle(&model.Subtitle{
		ID:            sub.ID,
		EpisodeID:     sub.EpisodeID,
		Language:      sub.Language,
		Label:         sub.Label,
		VttContent:    result.PolishedVtt,
		Source:        "llm_optimized",
		Optimized:     true,
		IsPrimary:     sub.IsPrimary,
	}); err != nil {
		s.failJob(job, "persist polished subtitle: "+err.Error())
		return
	}

	// Mine term candidates for the admin review queue (PR2.5 UI). Best-effort:
	// a failure here doesn't unwind the polish itself (the subtitle is already
	// corrected and useful); we just log and move on. nil glossaryRepo in tests.
	if s.glossaryRepo != nil && len(result.Glossary) > 0 {
		candidates := polishGlossaryToModel(courseID, result.Glossary)
		if err := s.glossaryRepo.UpsertCandidates(candidates); err != nil {
			log.Printf("AI: polish job %d: glossary upsert failed (non-fatal): %v", job.ID, err)
		}
	}

	// Surface high-edit-distance warning in the detail even on full success,
	// so the admin knows how many cues to review in the subtitle diff UI.
	// (The count is also in the detail prefix above; this is a more visible
	// call-out when the number is non-zero.) Plain-text marker (not emoji) per the
	// project's no-emoji convention — job detail is a text field, not a lucide icon.
	if result.Stats.HighEditDistanceCount > 0 {
		detail += fmt.Sprintf(" [注意] %d cue(s) 改动较大，建议在字幕版本 UI 复核",
			result.Stats.HighEditDistanceCount)
	}
	// Record the successful run for the AI Console's RunList / 「查看回放」(token
	// spend, model, system prompt). Polish was the only AI capability not writing
	// ai_runs — this brings it in line with summary/quiz/advice/course_summary.
	s.recordPolishRun(job.ID, modelName, result, "pass", detail, skipMining)
	// Structured log entry (polishStats): lets the admin see polish runs in the
	// /admin/logs event stream with token/chunk counts as structured fields.
	s.appendLog("info", "polish", fmt.Sprintf("polish done: %d/%d cues", result.Stats.ChangedCues, result.Stats.TotalCues),
		fmt.Sprintf(`{"job_id":%d,"chunks":%d,"llm_calls":%d,"retries":%d,"prompt_tokens":%d,"completion_tokens":%d,"high_edit_distance":%d,"duration_ms":%d}`,
			job.ID, result.Stats.ChunkCount, result.Stats.LLMCalls, result.Stats.Retries,
			result.Stats.PromptTokens, result.Stats.CompletionTokens, result.Stats.HighEditDistanceCount,
			result.Stats.Duration.Milliseconds()),
		job)
	s.contentRepo.UpdateJobStatus(job.ID, "done", detail, nil)

	// Chain to segment NOW that the polished subtitle is in place. Mirrors
	// runSegmentJob's chain-to-summary: same priority, same hasPendingJob guard.
	s.enqueueSegmentForPolish(episodeID, courseID)
}

// recordPolishRun writes the ai_run for a polish job, mirroring
// recordCourseSummaryRun / recordQuizRun. Polish was the only AI capability
// not persisting a run — this lets the AI Console's RunList show polish token
// spend and 「查看回放」 show the system prompt used.
//
//   - result status: "pass" on full success, "fail" on partial (reuses the
//     SelfCheckResult column the way advice/course_summary do).
//   - note: the assembled job detail string (changed/total, glossary, partial
//     chunk list) — capped inside the run's note column, gives the admin the
//     outcome at a glance.
//   - skipMining: which system prompt variant was used (so SystemPromptText
//     matches what the model actually saw).
func (s *aiService) recordPolishRun(jobID uint, modelName string, result *polish.PolishResult, status, note string, skipMining bool) {
	preview, _ := json.Marshal(map[string]any{
		"changed_cues":     result.Stats.ChangedCues,
		"total_cues":       result.Stats.TotalCues,
		"glossary":         len(result.Glossary),
		"high_edit_distance": result.Stats.HighEditDistanceCount,
		"skipped_edits":    result.Stats.SkippedEdits,
		"failed_chunks":    result.Stats.FailedChunks,
	})
	// Cap the note: the detail string can get long on a partial with many failed
	// chunks, and ai_runs is a telemetry row not a log dump. 400 runes matches
	// the advice-preview convention.
	if rs := []rune(note); len(rs) > 400 {
		note = string(rs[:400]) + "…"
	}
	if err := s.contentRepo.CreateRun(&model.AIRun{
		JobID:            jobID,
		Capability:       "polish",
		InputJSON: fmt.Sprintf(`{"job_id":%d,"chunks":%d,"llm_calls":%d,"retries":%d,"changed_cues":%d}`,
			jobID, result.Stats.ChunkCount, result.Stats.LLMCalls, result.Stats.Retries, result.Stats.ChangedCues),
		PromptTokens:     result.Stats.PromptTokens,
		CompletionTokens: result.Stats.CompletionTokens,
		ModelUsed:        modelName,
		ResponseText:     string(preview),
		SelfCheckResult:  status, // 复用字段：pass/fail（polish 无 self-check，记生成结果）
		SelfCheckNote:    note,
		DurationMs:       int(result.Stats.Duration.Milliseconds()),
		// polish 的 user prompt 是 per-chunk 拼的（buildUserPrompt），没有单一值；
		// 留空，RunDetail 有「(空)」兜底。system prompt 是常量，按本次实际用的版本取。
		SystemPromptText: polish.SystemPrompt(skipMining),
	}); err != nil {
		log.Printf("AI: recordPolishRun failed for job %d: %v", jobID, err)
	}
}

// polishedChunkPayload is the JSON shape stored in AIPolishChunk.PolishedChunkJSON
// and replayed back as PriorOutcomes on resume. Kept in sync with polish's
// CueChange + GlossaryCandidate — if those change, the JSON tags must too.
type polishedChunkPayload struct {
	Changes  []polish.CueChange        `json:"changes"`
	Glossary []polish.GlossaryCandidate `json:"glossary"`
}

// setupPolishCheckpoint wires up 断点续润: seeds the chunk skeleton rows
// (idempotent), loads any chunks already done from a prior attempt, and returns:
//   - priorOutcomes: done chunks fed to polish.Polish so it skips re-calling the
//     LLM for them (the token saver).
//   - onChunkDone: callback for polish.Polish that marks each finished chunk
//     done/failed in the DB + bumps the job's progress bar.
//   - totalChunks: for progress math; 0 when checkpointing is off (nil repo).
//
// When s.polishChunkRepo is nil (tests / pre-断点续润), returns empty prior +
// nil callback + 0 total — polish.Polish runs as a plain full job. The VTT is
// parsed here just to count cues for chunk-layout math; polish.Polish re-parses
// internally (cheap, and keeps the package self-contained).
func (s *aiService) setupPolishCheckpoint(_ context.Context, job *model.AIJob, polishInput string) (map[int]polish.PriorChunkOutcome, func(int, polish.ChunkOutcome), int) {
	if s.polishChunkRepo == nil {
		return nil, nil, 0
	}
	// Count cues → chunk layout. Parse failure here is non-fatal: fall back to no
	// checkpointing rather than failing the whole polish (the job still produces
	// a correct subtitle, just without resume support).
	cues, perr := ai.ParseSRT(subtitle.VttToSrt(polishInput))
	if perr != nil || len(cues) == 0 {
		log.Printf("AI: polish job %d: checkpoint setup parse failed (resume disabled): %v", job.ID, perr)
		return nil, nil, 0
	}
	layout := polish.ChunkLayout(len(cues))

	// Seed skeleton rows (queued). Idempotent: a retry re-seeding the same job's
	// chunks won't clobber rows a prior attempt already wrote done/failed.
	skeleton := make([]model.AIPolishChunk, len(layout))
	for i, span := range layout {
		skeleton[i] = model.AIPolishChunk{
			ChunkIndex:          i,
			ChunkFirstGlobalIdx: span[0],
			ChunkLastGlobalIdx:  span[1],
			Status:              "queued",
		}
	}
	if err := s.polishChunkRepo.SeedChunksForJob(job.ID, skeleton); err != nil {
		// Non-fatal: resume won't work this run, but polish itself is unaffected.
		log.Printf("AI: polish job %d: seed chunk rows failed (resume disabled): %v", job.ID, err)
		return nil, nil, 0
	}

	// Load done chunks → priorOutcomes. A done chunk's {changes,glossary} was
	// serialized on the prior attempt; deserialize now so polish.Polish can fold
	// them into the final reassembly without re-calling the LLM.
	rows, lerr := s.polishChunkRepo.ListChunksForJob(job.ID)
	priorOutcomes := make(map[int]polish.PriorChunkOutcome)
	if lerr != nil {
		log.Printf("AI: polish job %d: list chunk rows failed (resume disabled): %v", job.ID, lerr)
		// proceed without priors — all chunks run fresh
	} else {
		doneCount := 0
		for _, r := range rows {
			if r.Status != "done" || r.PolishedChunkJSON == "" {
				continue
			}
			var payload polishedChunkPayload
			if json.Unmarshal([]byte(r.PolishedChunkJSON), &payload) == nil {
				priorOutcomes[r.ChunkIndex] = polish.PriorChunkOutcome{
					Changes:          payload.Changes,
					Glossary:         payload.Glossary,
					PromptTokens:     r.PromptTokens,
					CompletionTokens: r.CompletionTokens,
					HighEditDistance: r.HighEditDistanceCount,
				}
				doneCount++
			}
		}
		if doneCount > 0 {
			log.Printf("AI: polish job %d: resuming, %d/%d chunks already done (skipping their LLM calls)",
				job.ID, doneCount, len(layout))
		}
	}

	totalChunks := len(layout)
	// progressCounter tracks done-chunks for the progress bar. Seeded with the
	// prior-done count so the bar starts at the right place on a resume instead
	// of jumping from 0%. atomic.Float64 (Go 1.19+) — the callback runs from
	// polish.Polish's per-chunk goroutines (3-way concurrency).
	progressCounter := &atomic.Int32{} // we count chunks (ints), derive float at display time
	progressCounter.Store(int32(len(priorOutcomes)))

	// onChunkDone: persist each chunk as it finishes + bump the progress bar.
	// Runs inside polish.Polish's per-chunk goroutine — must be cheap and must
	// not error-out the pipeline (errors here are logged, not returned).
	onChunkDone := func(idx int, oc polish.ChunkOutcome) {
		if idx < 0 || idx >= totalChunks {
			return
		}
		if oc.Failed {
			errStr := oc.ErrStr
			if rs := []rune(errStr); len(rs) > 256 {
				errStr = string(rs[:256]) + "…"
			}
			if err := s.polishChunkRepo.MarkChunkFailed(job.ID, idx, oc.PromptTokens, oc.CompletionTokens, oc.Retries, errStr); err != nil {
				log.Printf("AI: polish job %d chunk %d: markFailed failed (non-fatal): %v", job.ID, idx, err)
			}
		} else {
			payload, _ := json.Marshal(polishedChunkPayload{Changes: oc.Changes, Glossary: oc.Glossary})
			if err := s.polishChunkRepo.MarkChunkDone(job.ID, idx, oc.PromptTokens, oc.CompletionTokens, oc.Retries, oc.HighEditDistance, countDistinctCueChanges(oc.Changes), string(payload)); err != nil {
				log.Printf("AI: polish job %d chunk %d: markDone failed (non-fatal): %v", job.ID, idx, err)
			}
		}
		// A chunk "settled" (done OR failed) counts toward progress — the bar
		// measures "how far along the run is", and a failed chunk is still a
		// chunk we're done waiting on.
		done := float64(progressCounter.Add(1)) / float64(totalChunks)
		s.contentRepo.UpdateJobStatus(job.ID, "processing", "", &done)
	}

	return priorOutcomes, onChunkDone, totalChunks
}

// countDistinctCueChanges counts how many distinct cue ids a chunk's changes
// touch — stored on AIPolishChunk.ChangedCues for per-chunk telemetry. A single
// cue with multiple edits counts once (it's one cue changed).
func countDistinctCueChanges(changes []polish.CueChange) int {
	seen := make(map[int]struct{}, len(changes))
	for _, c := range changes {
		seen[c.ID] = struct{}{}
	}
	return len(seen)
}

// isPolishableSource is the single source of truth for which subtitle sources
// the polish pipeline will run on. Used by BOTH the enqueue path
// (EnqueuePolish, to decide whether to admit a job) AND the execution path
// (runPolishJob, to decide whether to skip a claimed job). Centralizing this
// eliminates the prior contradiction where EnqueuePolish admitted
// source=llm_optimized (for re-polish with a richer TermDict) but runPolishJob
// rejected it — leaving re-polish jobs in a "queued → skipped" loop that
// confused admins (see "source=llm_optimized not eligible for polish" in
// ai_jobs.error).
//
// Accepted sources:
//   - "whisper"        — the primary case: raw machine transcript full of
//                        homophone errors, exactly what polish exists to fix.
//   - "llm_optimized"  — re-polish: an episode already polished once, now
//                        being re-run because admin accepted new glossary
//                        terms. Re-polish is drift-safe because runPolishJob
//                        reads RawVttContent (immutable whisper snapshot), so
//                        the LLM never sees its own prior output.
//
// Rejected sources:
//   - "embedded" / "manual" — human-corrected tracks; polishing them is a
//                             no-op at best and confusing at worst.
func isPolishableSource(source string) bool {
	return source == "whisper" || source == "llm_optimized"
}

// enqueueSegmentForPolish chains a segment job after a successful polish (or
// after a non-whisper subtitle completed, or after the admin skips polish).
// Centralized so runPolishJob, SkipPolish, and OnSubtitleCompleted's non-whisper
// branch all share the exact same gate logic. The hasPendingJob guard prevents
// stacking: if a segment job is already queued/processing (e.g. a previous run
// left one), this is a no-op.
func (s *aiService) enqueueSegmentForPolish(episodeID, courseID uint) {
	if s.hasPendingJob("segment", episodeID) {
		return
	}
	epID, cID := episodeID, courseID
	job := &model.AIJob{
		JobType:   "segment",
		EpisodeID: &epID,
		CourseID:  &cID,
		Status:    "queued",
		Priority:  prioritySegment,
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to chain-enqueue segment job for episode %d: %v", episodeID, err)
	}
}

// polishGlossaryToModel converts the polish package's mined candidates into
// model rows ready for UpsertCandidates. EvidenceCount is seeded from the
// number of cue ids the LLM cited, so the first sighting of a rule starts at
// the right count instead of 1. EvidenceSample is left empty for now — the
// polish package carries cue ids (not text), and PR2.5's accept UI will
// re-derive sample text from the persisted diff if the admin wants to see
// examples. The schema field is kept ready for that.
func polishGlossaryToModel(courseID uint, in []polish.GlossaryCandidate) []model.GlossaryCandidate {
	out := make([]model.GlossaryCandidate, 0, len(in))
	for _, g := range in {
		out = append(out, model.GlossaryCandidate{
			CourseID:      courseID,
			Original:      g.Original,
			Corrected:     g.Corrected,
			Context:       g.Context,
			Confidence:    g.Confidence,
			EvidenceCount: len(g.EvidenceIDs),
			Status:        "pending",
		})
	}
	return out
}


// runSummaryJob reads an episode's chunks and asks the summarizer to summarize.
// Requires chunks to exist (a segment job must have run first). If none exist,
// the job is marked failed with a clear message rather than silently producing
// an empty summary.
func formatTermDictEntry(original, corrected, context string) string {
	context = strings.TrimSpace(context)
	if context == "" {
		return original + "→" + corrected
	}
	return original + "→" + corrected + "（" + context + "）"
}

// appendTermDict appends one entry to a course's TermDict string, respecting
// the ';' separator. Handles the empty-existing case (no leading separator)
// and dedup: if the exact entry is already present (admin re-accepting after a
// context edit), it's a no-op.
func appendTermDict(existing, entry string) string {
	existing = strings.TrimSpace(existing)
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return existing
	}
	// Dedup: scan existing semicolon-separated entries for an exact match.
	for _, e := range strings.Split(existing, ";") {
		if strings.TrimSpace(e) == entry {
			return existing
		}
	}
	if existing == "" {
		return entry
	}
	return existing + ";" + entry
}

// applyGlossaryToCourse mutates one course's AIConfig.TermDict in place by
// appending the entry, then persists the course. Centralized so the accept and
// the apply-to-siblings paths share the exact same write logic.
func (s *aiService) applyGlossaryToCourse(courseID uint, entry string) error {
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		return fmt.Errorf("load course %d: %w", courseID, err)
	}
	if course == nil {
		return nil // course deleted between polish and review — skip silently
	}
	cfg := course.AIConfig()
	cfg.TermDict = appendTermDict(cfg.TermDict, entry)
	course.SetAIConfig(cfg)
	return s.courseRepo.Update(course)
}

// ListGlossaryCandidates delegates to the repo. The handler passes "" for
// status to show all, or "pending" for the default review list.
func (s *aiService) ListGlossaryCandidates(courseID uint, status string) ([]model.GlossaryCandidate, error) {
	if s.glossaryRepo == nil {
		return nil, errors.New("glossary subsystem not configured")
	}
	return s.glossaryRepo.ListByCourse(courseID, status)
}

// AcceptGlossaryCandidate promotes one pending candidate into TermDict. The
// admin may override corrected/context (e.g. the LLM suggested 居 but the admin
// knows it should be 車) — the overrides are applied both to the candidate row
// (so the record reflects what was actually accepted) and to the TermDict entry.
// applyToSubjectSiblings repeats the TermDict append on every other course
// under the same subject, sparing the admin from per-course review.
func (s *aiService) AcceptGlossaryCandidate(id uint, correctedOverride, contextOverride string, applyToSubjectSiblings bool) error {
	if s.glossaryRepo == nil {
		return errors.New("glossary subsystem not configured")
	}
	c, err := s.glossaryRepo.FindByID(id)
	if err != nil {
		return err
	}
	if c == nil {
		return repository.ErrGlossaryNotFound
	}
	if c.Status != "pending" {
		return ErrGlossaryNotPending
	}
	// Apply admin overrides (empty = keep the LLM's values).
	corrected := strings.TrimSpace(correctedOverride)
	if corrected == "" {
		corrected = c.Corrected
	}
	context := strings.TrimSpace(contextOverride)
	if context == "" {
		context = c.Context
	}
	// Stamp the row as accepted with the FINAL (possibly admin-edited) values.
	c.Corrected = corrected
	c.Context = context
	c.Status = "accepted"
	now := time.Now().UTC()
	c.AcceptedAt = &now
	if err := s.glossaryRepo.Update(c); err != nil {
		return err
	}

	// Promote to the originating course's TermDict.
	entry := formatTermDictEntry(c.Original, corrected, context)
	if err := s.applyGlossaryToCourse(c.CourseID, entry); err != nil {
		return err
	}

	// Optional cross-course推广: same subject, every other course gets the rule
	// too. Best-effort — a failure on one sibling course doesn't unwind the
	// accept itself (the candidate is already marked accepted on the origin
	// course); we log and continue so one bad course doesn't block the rest.
	if applyToSubjectSiblings {
		origin, err := s.courseRepo.FindByID(c.CourseID)
		// Guard against SubjectID==0: courseRepo.List("", 0, ...) skips the
		// subject filter entirely (0 means "no filter" in that query), so
		// without this check we'd推广 the rule to EVERY course in the DB — a
		// xiangqi term would land in math/english/... TermDicts. A course with
		// no subject has no siblings by definition; skip the推广 silently.
		if err == nil && origin != nil && origin.SubjectID != 0 {
		// contentType=ContentLearning: 推广只覆盖同学科的学习课程。
		// entertainment 课程（动画片/电影）即使共享学科 key，其术语需求和
		// 学习课也不同——象棋术语不该被强加给一部电影。接受的术语进的是
		// 每门课独立的 TermDict（admin 在 Prompt 配置 tab 可随时改/删），
		// 所以如果某门娱乐课确实需要该术语，admin 可手动去那门课加。
		// 用具体的 ContentLearning 而非 ""，因为后者也会 fallback 到 learning
		// 但语义不显式；显式更清晰且未来想放开时只改这一处。
		siblings, lerr := s.courseRepo.List("", origin.SubjectID, model.ContentLearning, nil)
			if lerr != nil {
				log.Printf("glossary accept: list subject %d siblings failed (non-fatal): %v", origin.SubjectID, lerr)
			}
			for i := range siblings {
				sib := &siblings[i]
				if sib.ID == origin.ID {
					continue
				}
				if err := s.applyGlossaryToCourse(sib.ID, entry); err != nil {
					log.Printf("glossary accept: apply to sibling course %d failed (non-fatal): %v", sib.ID, err)
				}
			}
		} else if err == nil && origin != nil && origin.SubjectID == 0 {
			log.Printf("glossary accept: course %d has no subject; skipping sibling推广", origin.ID)
		}
	}
	return nil
}

// RejectGlossaryCandidate marks one candidate rejected. The row stays (it's
// the dedup anchor that stops UpsertCandidate re-creating it), it just leaves
// the default review list (which filters status=pending).
func (s *aiService) RejectGlossaryCandidate(id uint) error {
	if s.glossaryRepo == nil {
		return errors.New("glossary subsystem not configured")
	}
	c, err := s.glossaryRepo.FindByID(id)
	if err != nil {
		return err
	}
	if c == nil {
		return repository.ErrGlossaryNotFound
	}
	if c.Status != "pending" {
		return ErrGlossaryNotPending
	}
	c.Status = "rejected"
	return s.glossaryRepo.Update(c)
}


// view can render names without an N+1 (one query per distinct episode/course/
// user, not one per job). Titles are best-effort: a missing id (deleted row)
// simply isn't in the map, and forJob returns "" for it.
