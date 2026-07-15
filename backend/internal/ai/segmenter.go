package ai

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SRTCue is one timed subtitle block: a start/end timestamp and the text shown
// during that window. The segmenter parses SRT into these, then aggregates
// adjacent cues into chunks.
type SRTCue struct {
	Index     int    // 1-based SRT cue number
	StartMs   int    // start time in milliseconds
	EndMs     int    // end time in milliseconds
	Text      string // the subtitle text, newlines collapsed to spaces
}

// ContentChunkDraft is a segmenter-produced chunk BEFORE it's persisted. It
// carries everything the repo needs to write a ContentChunk row except the
// embedding (which is computed separately and attached by the caller). Keeping
// the segmenter free of DB/persistence concerns makes it unit-testable in
// isolation.
type ContentChunkDraft struct {
	ChunkIndex int
	StartTime  int    // seconds
	EndTime    int    // seconds
	Text       string
}

// SegmentConfig tunes the chunker. Defaults are chosen for spoken Chinese
// course subtitles: ~90s windows hit a good balance between topical coherence
// (long enough to cover a sub-topic) and retrieval granularity (short enough
// that a hit points at a specific part of the lesson, not "the whole first
// half"). See DefaultSegmentConfig.
type SegmentConfig struct {
	// TargetSeconds is the soft target chunk duration. The chunker accumulates
	// cues until the elapsed time crosses this, then closes the chunk at the
	// next cue boundary. So actual chunks are >= TargetSeconds (never shorter
	// than one cue), which is fine — boundary alignment matters more than exact
	// size.
	TargetSeconds int

	// MaxSeconds hard-caps a chunk's span. A single very long cue (e.g. a 4-min
	// silence-filler) won't drag a chunk absurdly long; reaching this cap forces
	// a close even mid-topic. Must be >= TargetSeconds.
	MaxSeconds int

	// MaxChars caps a chunk by text length, a backstop for very dense cues
	// (fast-talking teacher) where time alone would let a chunk balloon past
	// what fits in a retrieval context. 0 = no char cap.
	MaxChars int
}

// DefaultSegmentConfig returns sane defaults for Chinese course subtitles:
// ~90s target, 180s hard cap, 800-char text cap. These keep chunks coherent
// (a sub-topic usually spans 30-120s) and retrieval-sized (a few hundred chars
// each, plenty for a quiz/chat to reference).
func DefaultSegmentConfig() SegmentConfig {
	return SegmentConfig{TargetSeconds: 90, MaxSeconds: 180, MaxChars: 800}
}

// timeRe matches an SRT timestamp "HH:MM:SS,mmm". Used for parsing both the
// start and end of a cue's time line. The comma (not period) is the SRT spec;
// VTT uses a period, but our source is always SRT from the Whisper worker.
var timeRe = regexp.MustCompile(`(\d{1,2}):(\d{2}):(\d{2})[,.](\d{1,3})`)

// ParseSRT turns raw SRT text into a flat list of cues. It is lenient: it
// ignores blank lines, missing indices, and minor formatting drift, since the
// source is machine-generated (Whisper) and occasionally irregular. A cue is
// recognized by a line containing "-->" — everything after the timestamp line
// until the next blank line is the cue text.
//
// Returns an error only if the input yields zero cues (likely not SRT at all).
func ParseSRT(srt string) ([]SRTCue, error) {
	srt = strings.ReplaceAll(srt, "\r\n", "\n")
	lines := strings.Split(srt, "\n")

	var cues []SRTCue
	var current *SRTCue
	flush := func() {
		if current != nil && strings.TrimSpace(current.Text) != "" {
			current.Text = strings.TrimSpace(current.Text)
			cues = append(cues, *current)
		}
		current = nil
	}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.Contains(line, "-->") {
			// New cue starts. Flush any in-progress cue first.
			flush()
			start, end, ok := parseTimeLine(line)
			if !ok {
				continue // malformed time line — skip, don't start a cue
			}
			current = &SRTCue{StartMs: start, EndMs: end}
			continue
		}
		if line == "" {
			flush()
			continue
		}
		// A pure-numeric line is the SRT cue index; we don't need it (order is
		// implicit) so skip it unless we're mid-cue, in which case it's text.
		if current == nil {
			if _, err := strconv.Atoi(line); err == nil {
				continue // standalone index line before any time line
			}
			continue // stray text outside a cue — ignore
		}
		// Append to current cue's text (collapse newlines to spaces).
		if current.Text != "" {
			current.Text += " "
		}
		current.Text += line
	}
	flush()

	if len(cues) == 0 {
		return nil, fmt.Errorf("no cues parsed from SRT (input may not be valid SRT)")
	}
	return cues, nil
}

// parseTimeLine extracts start/end milliseconds from a line like
// "00:01:23,456 --> 00:01:25,000". Returns ok=false if either timestamp is
// missing/malformed.
func parseTimeLine(line string) (startMs, endMs int, ok bool) {
	matches := timeRe.FindAllStringSubmatch(line, -1)
	if len(matches) < 2 {
		return 0, 0, false
	}
	start, s1 := parseTimestamp(matches[0])
	end, s2 := parseTimestamp(matches[1])
	if !s1 || !s2 {
		return 0, 0, false
	}
	return start, end, true
}

// parseTimestamp converts one regex match group ([full, H, M, S, ms]) to ms.
func parseTimestamp(m []string) (int, bool) {
	if len(m) < 5 {
		return 0, false
	}
	h, e1 := strconv.Atoi(m[1])
	min, e2 := strconv.Atoi(m[2])
	s, e3 := strconv.Atoi(m[3])
	ms, e4 := strconv.Atoi(m[4])
	if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
		return 0, false
	}
	return ((h*60+min)*60+s)*1000 + ms, true
}

// SegmentChunks aggregates parsed cues into content chunks per the config.
//
// Strategy: walk cues in order, accumulating into the current chunk. Close and
// start a new chunk when ANY of these fires:
//   - the chunk's time span reaches TargetSeconds (normal case — close at the
//     cue boundary just past the target, so chunks stay topically aligned), or
//   - the span would exceed MaxSeconds (hard cap, even mid-topic), or
//   - the accumulated text exceeds MaxChars (density cap).
//
// This is "time-window with boundary alignment": we don't cut mid-cue, so a
// sentence is never split across chunks. Each chunk's text is the concatenation
// of its cues' text. Returns drafts carrying time + text; the caller (repo
// layer) fills in episode_id/source/etc. and computes the embedding.
func SegmentChunks(cues []SRTCue, cfg SegmentConfig) []ContentChunkDraft {
	if len(cues) == 0 {
		return nil
	}
	if cfg.TargetSeconds <= 0 {
		cfg = DefaultSegmentConfig()
	}
	if cfg.MaxSeconds < cfg.TargetSeconds {
		cfg.MaxSeconds = cfg.TargetSeconds
	}

	var drafts []ContentChunkDraft
	var buf strings.Builder
	chunkStart := cues[0].StartMs
	chunkStartSet := false
	lastEnd := 0
	idx := 0

	closeChunk := func() {
		if buf.Len() == 0 {
			return
		}
		drafts = append(drafts, ContentChunkDraft{
			ChunkIndex: idx,
			StartTime:  chunkStart / 1000,
			EndTime:    lastEnd / 1000,
			Text:       strings.TrimSpace(buf.String()),
		})
		idx++
		buf.Reset()
		chunkStartSet = false
	}

	for _, cue := range cues {
		if !chunkStartSet {
			chunkStart = cue.StartMs
			chunkStartSet = true
		}
		if buf.Len() > 0 {
			buf.WriteByte(' ')
		}
		buf.WriteString(cue.Text)
		lastEnd = cue.EndMs

		spanSec := (lastEnd - chunkStart) / 1000
		textLen := buf.Len()
		// Decide whether to close after this cue.
		// Normal close: reached target. Hard caps: max time or max chars.
		if spanSec >= cfg.TargetSeconds || spanSec >= cfg.MaxSeconds ||
			(cfg.MaxChars > 0 && textLen >= cfg.MaxChars) {
			closeChunk()
		}
	}
	closeChunk() // flush trailing chunk

	return drafts
}

// mmss formats a seconds value as M:SS or H:MM:SS for human-readable logging
// (chunk "12:38 → 15:02"). Not used in persisted data, handy in run logs.
func mmss(seconds int) string {
	h := seconds / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
