package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"
	"studyquest/backend/internal/ai/polish"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
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
	if sub.Source != "whisper" {
		// Shouldn't happen (OnSubtitleCompleted gates on source), but if a
		// misrouted job slips through, skip it cleanly rather than polishing
		// a track that's already human-corrected. This is NOT a failure —
		// chain to segment so downstream proceeds.
		s.contentRepo.UpdateJobStatus(job.ID, "skipped",
			"source="+sub.Source+" not eligible for polish", nil)
		s.enqueueSegmentForPolish(episodeID, courseID)
		return
	}

	llm, err := s.resolver.ResolveChatByPurpose("polish")
	if err != nil {
		// Provider not configured / misconfigured. Block — admin fixes the
		// provider config and retries. We do NOT fall through to segment,
		// because the whole point of polish is to fix the raw transcript
		// before AI consumes it.
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("polish")

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

	result, err := polish.Polish(polishCtx, llm, modelName, polish.PolishRequest{
		VttContent: polishInput,
		TermDict:   termDict,
		Subject:    subjectLabel,
	})
	if err != nil {
		s.failJob(job, "polish: "+err.Error())
		return
	}

	// Persist the polished subtitle. We write ONLY VttContent + Optimized +
	// Source — RawVttContent stays empty here so episodeRepo.SaveSubtitle's
	// "non-empty only" guard leaves the original snapshot untouched. IsPrimary
	// is echoed back from the loaded sub so the upsert doesn't accidentally
	// demote the primary track.
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

	detail := fmt.Sprintf("polished: %d/%d cues changed, %d glossary candidates, cost≈%s",
		result.Stats.ChangedCues, result.Stats.TotalCues, len(result.Glossary),
		result.Stats.Duration.Truncate(time.Second))
	if result.Stats.PartialOptimized {
		// List which chunks failed + their last error, capped so a pathological
		// relay doesn't blow up the error column. The chunk index is 0-based
		// and maps to a contiguous cue range, so the admin can tell which part
		// of the subtitle wasn't polished.
		detail += fmt.Sprintf(" (partial: %d/%d chunks failed", result.Stats.FailedChunks, result.Stats.ChunkCount)
		// Collect + sort by chunk idx NUMERICALLY (not lexically — "chunk#10"
		// would sort before "chunk#2" under string sort). Cap each error at 120
		// runes so one verbose parse failure doesn't dominate the job detail.
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
	}
	s.contentRepo.UpdateJobStatus(job.ID, "done", detail, nil)

	// Chain to segment NOW that the polished subtitle is in place. Mirrors
	// runSegmentJob's chain-to-summary: same priority, same hasPendingJob guard.
	s.enqueueSegmentForPolish(episodeID, courseID)
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
	now := time.Now()
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
