package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// staleTimeout is how long a processing job may run without a heartbeat before
// the reaper assumes the worker died and flips it back to queued. A long video
// takes minutes to transcribe; the worker heartbeats every ~30s, so 30 min of
// silence reliably means the worker is gone.
const staleTimeout = 30 * time.Minute

// ErrSubtitleJobNotFound is returned by Complete/Retry/etc when no job matches
// the given id. Handlers map it to HTTP 404 (compare via errors.Is).
var ErrSubtitleJobNotFound = errors.New("subtitle job not found")

// ErrSubtitleJobStaleComplete is returned by Complete when the job is no longer
// 'processing' by the time the SRT arrives — the reaper or a retry moved it
// (likely another worker already re-claimed and completed it). The caller MUST
// discard the SRT it was about to persist. Handlers map it to HTTP 409.
var ErrSubtitleJobStaleComplete = errors.New("subtitle job completion is stale")

// SubtitleJobService owns the subtitle-queue business rules: who may be queued
// (learning episodes only, no existing subtitle, no active job), how a worker
// claims work, and what happens when a job completes (SRT lands in the
// subtitles table and is immediately playable).
//
// Design notes:
//   - The VPS only coordinates. It never runs whisper. The download URL handed
//     to a claiming worker is minted fresh on every claim because alist sign
//     URLs expire — it is never cached.
//   - De-duplication (one active job per episode) is enforced here, in the
//     service layer, not via a DB unique constraint, because we want to keep a
//     history of completed/failed jobs while preventing two concurrent ones.
type SubtitleJobService interface {
	// Enqueue adds an episode to the queue after running the gate checks.
	// Returns the created/existing job, whether it was skipped, a machine
	// reason code if so (has_subtitle | already_queued | entertainment |
	// not_found), and any error. Skipped is non-error: a batch enqueue wants
	// to know what was skipped and why, not abort.
	Enqueue(episodeID uint, priority int, language string) (job *model.SubtitleJob, skipped bool, reason string, err error)

	// EnqueueBatch runs Enqueue for each id, collecting which were enqueued vs
	// skipped (with reasons). Used by the admin bulk action. It never aborts
	// the whole batch on a single bad id — it reports it skipped.
	EnqueueBatch(episodeIDs []uint, priority int) (enqueued, skipped []uint, reasons map[uint]string, err error)

	// ClaimNext is the worker entry point. It atomically claims the next job
	// and mints a fresh download URL for it. Returns nil result with nil error
	// when there is nothing to do. workerID is the claiming worker's self-id
	// (stamped on the job for observability — which machine is working what).
	ClaimNext(workerID, userAgent string) (*ClaimResult, error)

	// Complete saves the SRT into the subtitles table and marks the job done.
	// A subtitle landing here is immediately consumable by the player.
	Complete(jobID uint, srtContent, language, label string) error

	// Heartbeat refreshes claimed_at on a processing job (worker ping during a
	// long transcription). progress, when non-nil, also records the worker's
	// transcription ratio (0.0..1.0) for the admin progress display. No-op if
	// the job isn't processing (e.g. already reaped/failed).
	Heartbeat(jobID uint, progress *float64) error

	// SetOnSubtitleCompleted registers a callback fired after a transcript is
	// successfully saved. This is the Step 2→3 seam: main.go wires the AI
	// service's auto-segment trigger here, keeping this service AI-agnostic.
	// Optional; nil callback = no-op (pre-AI behavior).
	SetOnSubtitleCompleted(fn func(episodeID uint))

	Fail(jobID uint, errStr string) error
	Skip(jobID uint) error
	// Retry moves a failed job back to queued (attempt count is preserved so
	// the operator can see it has been tried before).
	Retry(jobID uint) error

	ReapStale() (int, error)

	// GetStats returns queue counters + the currently-processing job (if any)
	// for the admin progress display.
	GetStats() (SubtitleJobStats, error)
	// ListQueue returns jobs (joined with episode display fields) for the
	// admin queue view. status=="" means all.
	ListQueue(status string, limit int) ([]repository.SubtitleJobWithEpisode, error)
}

// ClaimResult is the payload returned to a worker on a successful claim. The
// download URL + headers are minted fresh from the episode's storage provider
// and MUST be used promptly — they expire.
//
// Beyond the download link, the result carries everything the worker needs to
// (a) match a local cache copy (Filename + FileSize) and skip the netdisk, and
// (b) build a Whisper initial_prompt from course context (subject/course/chapter
// titles + the admin-authored AIHint). All of these are best-effort: a missing
// course/chapter (NULL chapter, deleted course) just yields empty strings, and
// the worker degrades gracefully (no cache hit, generic prompt).
type ClaimResult struct {
	Job            *model.SubtitleJob `json:"job"`
	DownloadURL    string             `json:"download_url"`
	DownloadHeader map[string]string  `json:"download_header,omitempty"`
	EpisodeTitle   string             `json:"episode_title"`
	DurationSec    *int               `json:"duration_seconds,omitempty"`
	// Filename is the basename of the episode's video path (the trailing path
	// segment, e.g. "lesson01.mp4"), preferring OriginalRelativePath and falling
	// back to VideoRelativePath. Worker matches this against local cache dirs.
	Filename string `json:"filename"`
	// FileSize is the episode's stored byte size (nil if unknown). Worker uses
	// it together with Filename for an unambiguous cache match.
	FileSize *int64 `json:"file_size,omitempty"`
	// Course context for the worker's Whisper initial_prompt. Subject is the
	// subject key (e.g. "math"), not the display label. Empty when unavailable.
	Subject      string `json:"subject,omitempty"`
	CourseTitle  string `json:"course_title,omitempty"`
	ChapterTitle string `json:"chapter_title,omitempty"`
	AIHint       string `json:"ai_hint,omitempty"`
}

// SubtitleJobStats is the progress snapshot polled by the admin UI, mirroring
// the shape of ProbeStats so the same polling pattern applies.
type SubtitleJobStats struct {
	Running          bool   `json:"running"`
	CurrentJobID     uint   `json:"current_job_id"`
	CurrentEpisodeID uint   `json:"current_episode_id"`
	CurrentTitle     string `json:"current_title"`
	CurrentClaimedBy string `json:"current_claimed_by"`
	Queued           int    `json:"queued"`
	Processing       int    `json:"processing"`
	Done             int    `json:"done"`
	Failed           int    `json:"failed"`
	Skipped          int    `json:"skipped"`
	LastFinishedAt   string `json:"last_finished_at"`
}

// Skip reason codes (machine-readable, surfaced to the admin as toast text).
const (
	SkipReasonHasSubtitle  = "has_subtitle"  // episode already has a subtitle row
	SkipReasonAlreadyQueue = "already_queued" // episode already has an active job
	SkipReasonEntertainment = "entertainment" // entertainment content, no subtitles needed
	SkipReasonNotFound     = "not_found"      // episode id doesn't exist
)

type subtitleJobService struct {
	repo           repository.SubtitleJobRepository
	episodeRepo    repository.EpisodeRepository
	episodeService EpisodeService
	courseRepo     repository.CourseRepository
	chapterRepo    repository.ChapterRepository
	subjectRepo    repository.SubjectRepository
	// onSubtitleCompleted is an optional callback fired after a transcript
	// successfully lands. It's the seam where the AI subsystem hooks in to
	// auto-segment new subtitles — kept as a callback (not a direct AIService
	// import) so the subtitle service stays decoupled from AI. nil = no-op
	// (AI not wired). Set via SetOnSubtitleCompleted.
	onSubtitleCompleted func(episodeID uint)
}

// SetOnSubtitleCompleted registers a callback invoked after a subtitle is
// successfully saved. Used by main.go to connect AIService.OnSubtitleCompleted
// without the subtitle service importing the AI package. Optional: if never
// called, subtitle completion is a no-op beyond persistence (pre-AI behavior).
func (s *subtitleJobService) SetOnSubtitleCompleted(fn func(episodeID uint)) {
	s.onSubtitleCompleted = fn
}

// NewSubtitleJobService constructs a SubtitleJobService. episodeService backs
// the download-URL minting and subtitle persistence; episodeRepo backs the
// gate checks (entertainment filter, existing-subtitle lookup). courseRepo,
// chapterRepo and subjectRepo are only read in ClaimNext to enrich the claim
// payload with the course context the worker builds its Whisper prompt from.
func NewSubtitleJobService(repo repository.SubtitleJobRepository, episodeRepo repository.EpisodeRepository, es EpisodeService, courseRepo repository.CourseRepository, chapterRepo repository.ChapterRepository, subjectRepo repository.SubjectRepository) SubtitleJobService {
	return &subtitleJobService{repo: repo, episodeRepo: episodeRepo, episodeService: es, courseRepo: courseRepo, chapterRepo: chapterRepo, subjectRepo: subjectRepo}
}

func (s *subtitleJobService) Enqueue(episodeID uint, priority int, language string) (*model.SubtitleJob, bool, string, error) {
	if language == "" {
		language = "zh-CN"
	}

	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, false, "", err
	}
	if ep == nil {
		return nil, true, SkipReasonNotFound, nil
	}
	// Entertainment content doesn't get subtitles — it's pure playback. The
	// flag lives on the course, not the episode, so we join through the course.
	contentType, cerr := s.episodeRepo.FindCourseContentType(episodeID)
	if cerr != nil {
		return nil, false, "", cerr
	}
	if contentType == string(model.ContentEntertainment) {
		return nil, true, SkipReasonEntertainment, nil
	}

	// Already has a subtitle? Don't re-queue.
	hasSub, herr := s.episodeRepo.HasSubtitle(episodeID)
	if herr != nil {
		return nil, false, "", herr
	}
	if hasSub {
		return nil, true, SkipReasonHasSubtitle, nil
	}

	// Already queued or processing? Return that active job, don't duplicate.
	//
	// NOTE: this is a check-then-act, not atomic. Two truly-concurrent enqueues
	// of the same episode could both pass this check and each create a queued
	// job. We accept that: the only caller is the admin (a single human), so
	// concurrency is rare; and a duplicate queued row is self-healing — both
	// get transcribed, the second Complete is rejected as stale (see Complete),
	// and the episode ends up with exactly one subtitle. A DB unique constraint
	// can't express "one active, many historical", so we don't fight it here.
	if active, aerr := s.repo.FindActiveByEpisode(episodeID); aerr == nil && active != nil {
		return active, true, SkipReasonAlreadyQueue, nil
	} else if aerr != nil {
		return nil, false, "", aerr
	}

	job := &model.SubtitleJob{
		EpisodeID: episodeID,
		Status:    model.SubtitleJobQueued,
		Priority:  priority,
		Language:  language,
	}
	if err := s.repo.Create(job); err != nil {
		return nil, false, "", err
	}
	return job, false, "", nil
}

func (s *subtitleJobService) EnqueueBatch(episodeIDs []uint, priority int) ([]uint, []uint, map[uint]string, error) {
	enqueued := make([]uint, 0, len(episodeIDs))
	skipped := make([]uint, 0)
	reasons := make(map[uint]string, len(episodeIDs))
	for _, id := range episodeIDs {
		_, skip, reason, err := s.Enqueue(id, priority, "")
		if err != nil {
			// A hard error on one id aborts the batch: the cause is likely a DB
			// issue that will recur for every id.
			return enqueued, skipped, reasons, fmt.Errorf("enqueue episode %d: %w", id, err)
		}
		if skip {
			skipped = append(skipped, id)
			reasons[id] = reason
		} else {
			enqueued = append(enqueued, id)
		}
	}
	return enqueued, skipped, reasons, nil
}

func (s *subtitleJobService) ClaimNext(workerID, userAgent string) (*ClaimResult, error) {
	job, err := s.repo.ClaimNext(workerID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}

	// Mint a FRESH download link. alist sign URLs expire, so we never cache the
	// URL on the job — every claim re-resolves it from the episode's source.
	link, err := s.episodeService.GetStreamURL(job.EpisodeID, userAgent)
	if err != nil {
		// Can't resolve a download URL → fail this job rather than strand the
		// worker (it would loop claiming a job it can't download).
		_ = s.repo.MarkFailed(job.ID, "resolve download url: "+err.Error())
		return nil, fmt.Errorf("resolve stream url for job %d: %w", job.ID, err)
	}

	// Gather episode display fields + the cache/prompt context. All lookups are
	// best-effort: a missing course/chapter just yields empty strings and the
	// worker degrades (no cache hit, generic prompt) rather than failing.
	title := ""
	var dur *int
	var filename string
	var fileSize *int64
	subject, courseTitle, chapterTitle, aiHint := "", "", "", ""
	if ep, eerr := s.episodeRepo.FindByID(job.EpisodeID); eerr == nil && ep != nil {
		title = ep.Title
		dur = ep.DurationSeconds
		fileSize = ep.FileSize
		// Prefer OriginalRelativePath (stable across admin renames); fall back
		// to VideoRelativePath. Keep the extension — the worker matches the full
		// filename against its cache dirs.
		path := ep.OriginalRelativePath
		if path == "" {
			path = ep.VideoRelativePath
		}
		filename = filepath.Base(path)
		if course, cerr := s.courseRepo.FindByID(ep.CourseID); cerr == nil && course != nil {
			courseTitle = course.Title
			aiHint = course.AIHint
			if subj, serr := s.subjectRepo.FindByID(course.SubjectID); serr == nil && subj != nil {
				subject = subj.Key
			}
		}
		if ep.ChapterID != nil {
			if ch, herr := s.chapterRepo.FindByID(*ep.ChapterID); herr == nil && ch != nil {
				chapterTitle = ch.Title
			}
		}
	}

	headers := link.Header
	if headers == nil {
		headers = map[string]string{}
	}
	return &ClaimResult{
		Job:            job,
		DownloadURL:    link.URL,
		DownloadHeader: headers,
		EpisodeTitle:   title,
		DurationSec:    dur,
		Filename:       filename,
		FileSize:       fileSize,
		Subject:        subject,
		CourseTitle:    courseTitle,
		ChapterTitle:   chapterTitle,
		AIHint:         aiHint,
	}, nil
}

func (s *subtitleJobService) Complete(jobID uint, srtContent, language, label string) error {
	job, err := s.repo.FindByID(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return ErrSubtitleJobNotFound
	}
	lang := language
	if lang == "" {
		lang = job.Language
	}

	// Order matters for the reaper/retry race: a worker can take longer than
	// the stale timeout (its heartbeats may have been lost), so by the time it
	// posts Complete the job may have been reaped back to queued and even
	// re-claimed by another worker. We MUST NOT persist this SRT unless we still
	// own the job. So: first atomically flip processing→done (status-guarded),
	// and only on success write the subtitle. If the flip fails because the job
	// isn't processing anymore, we discard this SRT rather than risk a duplicate
	// or clobbering another worker's result.
	if err := s.repo.MarkDone(jobID); err != nil {
		if errors.Is(err, repository.ErrSubtitleJobNotClaimed) {
			// Stale completion: another worker may already own this job. Tell
			// the caller to drop the SRT and carry on (not a hard error for the
			// worker loop). Surfaced as a distinct error so the handler can map
			// it to 409 rather than 500.
			return ErrSubtitleJobStaleComplete
		}
		return fmt.Errorf("mark done: %w", err)
	}

	if err := s.episodeService.SaveSubtitle(job.EpisodeID, lang, label, srtContent); err != nil {
		// We already marked the job done, but the subtitle write failed. That's
		// an inconsistent state, but failing the whole Complete here would hide
		// the (more recoverable) subtitle-save error behind a done job the admin
		// can't retry cleanly. Log the mismatch via the returned error so it's
		// visible; the job is done but subtitle-less — admin can re-save a
		// subtitle manually via the existing subtitle UI.
		return fmt.Errorf("job marked done but subtitle save failed: %w", err)
	}

	// Fire the AI hook (if wired) so new subtitles get auto-segmented. The hook
	// is a fire-and-forget enqueue — failures here must NOT fail Complete (the
	// subtitle itself is already safely persisted). Best-effort, logged.
	if s.onSubtitleCompleted != nil {
		s.onSubtitleCompleted(job.EpisodeID)
	}
	return nil
}

func (s *subtitleJobService) Heartbeat(jobID uint, progress *float64) error {
	job, err := s.repo.FindByID(jobID)
	if err != nil || job == nil {
		return err
	}
	if job.Status != model.SubtitleJobProcessing {
		return nil // no-op: not currently being worked
	}
	return s.repo.TouchClaim(jobID, progress)
}

func (s *subtitleJobService) Fail(jobID uint, errStr string) error {
	// Trim long error blobs so a worker panic stack doesn't bloat the column.
	if len(errStr) > 2000 {
		errStr = errStr[:2000]
	}
	return s.repo.MarkFailed(jobID, errStr)
}

func (s *subtitleJobService) Skip(jobID uint) error {
	return s.repo.MarkSkipped(jobID)
}

func (s *subtitleJobService) Retry(jobID uint) error {
	job, err := s.repo.FindByID(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return ErrSubtitleJobNotFound
	}
	if job.Status != model.SubtitleJobFailed && job.Status != model.SubtitleJobSkipped {
		return fmt.Errorf("only failed/skipped jobs can be retried (status=%s)", job.Status)
	}
	return s.repo.MarkQueued(jobID)
}

func (s *subtitleJobService) ReapStale() (int, error) {
	return s.repo.ReapStale(staleTimeout)
}

func (s *subtitleJobService) GetStats() (SubtitleJobStats, error) {
	counts, err := s.repo.CountByStatus()
	if err != nil {
		return SubtitleJobStats{}, err
	}
	stats := SubtitleJobStats{
		Queued:     counts[model.SubtitleJobQueued],
		Processing: counts[model.SubtitleJobProcessing],
		Done:       counts[model.SubtitleJobDone],
		Failed:     counts[model.SubtitleJobFailed],
		Skipped:    counts[model.SubtitleJobSkipped],
		Running:    counts[model.SubtitleJobProcessing] > 0 || counts[model.SubtitleJobQueued] > 0,
	}

	// Surface the currently-processing job (there should be ≤1 per worker).
	if procs, perr := s.repo.ListByStatus(model.SubtitleJobProcessing, 1); perr == nil && len(procs) > 0 {
		p := procs[0]
		stats.CurrentJobID = p.ID
		stats.CurrentEpisodeID = p.EpisodeID
		stats.CurrentClaimedBy = p.ClaimedBy
		if ep, eerr := s.episodeRepo.FindByID(p.EpisodeID); eerr == nil && ep != nil {
			stats.CurrentTitle = ep.Title
		}
	}
	if dones, derr := s.repo.ListByStatus(model.SubtitleJobDone, 1); derr == nil && len(dones) > 0 {
		stats.LastFinishedAt = dones[0].UpdatedAt.Format(time.RFC3339)
	}
	return stats, nil
}

func (s *subtitleJobService) ListQueue(status string, limit int) ([]repository.SubtitleJobWithEpisode, error) {
	if limit <= 0 {
		limit = 200
	}
	status = strings.TrimSpace(status)
	return s.repo.ListWithEpisode(status, limit)
}
