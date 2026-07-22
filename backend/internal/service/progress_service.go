package service

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"gorm.io/gorm"
)

// ProgressService manages playback tracking, completing lessons, and points accumulation.
type ProgressService interface {
	GetProgress(userID, episodeID uint) (*model.UserProgress, error)
	ReportProgress(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.UserProgress, error)
	GetPoints(userID uint) (*model.UserPoint, error)
	GetUserProgressOverview(userID uint) ([]model.UserProgress, error)
	GetLastWatchedEpisode(userID, courseID uint) (*model.Episode, *model.UserProgress, error)
	GetPointsLedger(userID uint, limit, offset int) ([]model.PointsLedger, error)
}

type progressService struct {
	db                *gorm.DB
	progressRepo      repository.ProgressRepository
	episodeRepo       repository.EpisodeRepository
	badgeService      BadgeService
	courseRepo        repository.CourseRepository
	entertainmentRepo repository.EntertainmentRepository
	watchEventRepo    repository.WatchEventRepository
	// mergeWindow is how long a gap between two heartbeats is still folded
	// into the same WatchEvent row. 0 disables merging. From WATCH_MERGE_WINDOW.
	mergeWindow time.Duration
	// userLocks serializes the completion+points path per user. The player's
	// 5s heartbeat and a quiz-triggered report can overlap; without this, the
	// daily-first-completion bonus (HasCompletedToday) could fire twice.
	userLocks sync.Map // map[uint]*sync.Mutex
}

// NewProgressService creates an instance of ProgressService.
func NewProgressService(db *gorm.DB, pr repository.ProgressRepository, er repository.EpisodeRepository, bs BadgeService, cr repository.CourseRepository, er2 repository.EntertainmentRepository, wer repository.WatchEventRepository, mergeWindow time.Duration) ProgressService {
	return &progressService{
		db:                db,
		progressRepo:      pr,
		episodeRepo:       er,
		badgeService:      bs,
		courseRepo:        cr,
		entertainmentRepo: er2,
		watchEventRepo:    wer,
		mergeWindow:       mergeWindow,
	}
}

// isEntertainment reports whether the episode belongs to an entertainment
// course. Entertainment videos skip completion/points/badges entirely — their
// progress is tracked in a separate table so learning-stat queries stay
// zero-contaminated.
func (s *progressService) isEntertainment(episodeID uint) bool {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		return false
	}
	return s.isEntertainmentCourse(ep.CourseID)
}

// isEntertainmentCourse reports whether the course is entertainment type.
// Split out so callers that already have the courseID (e.g. ReportProgress
// which loaded the episode) avoid a duplicate episode fetch.
func (s *progressService) isEntertainmentCourse(courseID uint) bool {
	c, err := s.courseRepo.FindByID(courseID)
	if err != nil || c == nil {
		return false
	}
	return c.ContentType == model.ContentEntertainment
}

// lockUser returns (and lazily creates) the per-user mutex, then locks it.
func (s *progressService) lockUser(userID uint) *sync.Mutex {
	v, _ := s.userLocks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *progressService) GetProgress(userID, episodeID uint) (*model.UserProgress, error) {
	// Entertainment: read from the separate table, project into UserProgress
	// shape so the handler/DTO layer stays uniform. IsCompleted is always 0
	// (entertainment has no completion concept).
	if s.isEntertainment(episodeID) {
		ep, err := s.entertainmentRepo.GetProgress(userID, episodeID)
		if err != nil {
			return nil, err
		}
		if ep == nil {
			return nil, nil
		}
		return &model.UserProgress{
			UserID:              ep.UserID,
			EpisodeID:           ep.EpisodeID,
			LastPositionSeconds: ep.LastPositionSeconds,
			WatchSeconds:        ep.WatchSeconds,
			IsCompleted:         false,
		}, nil
	}
	return s.progressRepo.GetProgress(userID, episodeID)
}

// ReportProgress records a playback heartbeat from the client.
//
// watch_seconds accumulation is done atomically via UpsertAndAccumulateWatch
// (a single INSERT ... ON CONFLICT DO UPDATE), NOT the old
// GetProgress → mutate → SaveProgress sequence. The old path lost
// watch_seconds whenever two reports interleaved (the player's 5s timer and
// the quiz ping can overlap): both read the same WatchSeconds, each added its
// own delta, and the second SaveProgress clobbered the first — so the admin
// "learning time" column could stay at 0 even after minutes of watching.
// Completion gating still runs after the atomic upsert and persists only the
// IsCompleted flag (plus the resume position) via SaveProgress; it never
// rewrites watch_seconds, so the increment survives.
func (s *progressService) ReportProgress(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.UserProgress, error) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, errors.New("episode not found")
	}

	isEnt := s.isEntertainmentCourse(ep.CourseID)

	// Clamp the delta ONCE at the service layer so every downstream write
	// (event log + learning aggregate + entertainment aggregate) sees the same
	// value. Previously each repo clamped independently — learning capped at
	// 600s, entertainment didn't cap at all, and the event log used the raw
	// value — so SUM(events) could diverge from watch_seconds whenever a client
	// sent delta > 600 (e.g. a catch-up upload after reconnect). Centralizing
	// the clamp here makes the dual-write invariant hold for any delta.
	if positionSec < 0 {
		positionSec = 0
	}
	if deltaWatchSec < 0 {
		deltaWatchSec = 0
	}
	if deltaWatchSec > 600 {
		deltaWatchSec = 600
	}

	// Append (or merge) a watch-history event for this heartbeat. Done BEFORE
	// the entertainment/learning fork so both branches are covered by a single
	// write. A failure here is logged but does NOT abort the report — the
	// aggregate tables (dashboard/leaderboard depend on them) must still update.
	// The worst case is one missing timeline row, which the "total watch time"
	// (served from the aggregate) hides entirely.
	if deltaWatchSec > 0 && s.watchEventRepo != nil {
		contentType := model.ContentLearning
		if isEnt {
			contentType = model.ContentEntertainment
		}
		if _, werr := s.watchEventRepo.AppendOrMerge(userID, episodeID, ep.CourseID, string(contentType), deltaWatchSec, time.Now().UTC(), s.mergeWindow); werr != nil {
			log.Printf("watch_event append failed (user=%d ep=%d): %v", userID, episodeID, werr)
		}
	}

	// Entertainment branch: record resume position + accumulate watch_seconds
	// (for the future time-limit feature) in the separate table, then return.
	// No completion, no points, no badges — learning stats stay uncontaminated.
	// Uses isEntertainmentCourse (not isEntertainment) to avoid re-fetching the
	// episode we already loaded above.
	if isEnt {
		entProg, err := s.entertainmentRepo.UpsertProgress(userID, episodeID, positionSec, deltaWatchSec)
		if err != nil {
			return nil, err
		}
		return &model.UserProgress{
			UserID:              entProg.UserID,
			EpisodeID:           entProg.EpisodeID,
			LastPositionSeconds: entProg.LastPositionSeconds,
			WatchSeconds:        entProg.WatchSeconds,
			IsCompleted:         false,
		}, nil
	}

	// 1. Atomically accumulate watch_seconds + set resume position. This is the
	//    authoritative watch-time write; it cannot lose deltas to concurrency.
	prog, err := s.progressRepo.UpsertAndAccumulateWatch(userID, episodeID, positionSec, deltaWatchSec)
	if err != nil {
		return nil, err
	}

	// 2. Completeness verification: mark complete only when the playhead has
	//    actually reached ≥90% of the video. Position-based gating matches what
	//    "watched" intuitively means and avoids false completes from re-watching
	//    the first 80% in many short sessions. We persist ONLY the is_completed
	//    flag via MarkCompleted (a single-column UPDATE) — NOT SaveProgress,
	//    which rewrites the whole row and would let two requests crossing 90%
	//    near-simultaneously each write a stale watch_seconds and clobber the
	//    atomic increment above. watch_seconds is never touched here.
	//    Note: completion does NOT reset the resume position — reopening a
	//    completed episode still resumes at the saved position.
	//
	//    The completion flag write AND the points award run inside ONE
	//    transaction: if awarding points fails, the completion is rolled back
	//    too. The old code swallowed the AddPoints error via fmt.Printf, which
	//    could leave an episode "completed" with no points recorded — and the
	//    next heartbeat wouldn't re-award them (IsCompleted==1 skips this
	//    block). Wrapping both in a tx guarantees they're all-or-nothing.
	if !prog.IsCompleted && ep.DurationSeconds != nil && *ep.DurationSeconds > 0 {
		duration := *ep.DurationSeconds
		threshold := int(float64(duration) * 0.9)
		if prog.LastPositionSeconds >= threshold {
			// Serialize the completion path per user so two overlapping
			// heartbeats can't both claim the daily-first bonus.
			mu := s.lockUser(userID)
			mu.Lock()
			defer mu.Unlock()

			// Points by duration tier: longer videos need more focus, so they
			// award more. <10min→5, 10-25min→10, >25min→15.
			watchPoints := 10
			if duration < 600 {
				watchPoints = 5
			} else if duration > 1500 {
				watchPoints = 15
			}
			// Daily first-completion bonus: the FIRST episode completed each
			// day gets +5 extra, so even a "quiet" day (no new badge tier)
			// still feels rewarding. Check BEFORE marking complete.
			alreadyDoneToday, _ := s.progressRepo.HasCompletedToday(userID)
			firstBonus := 0
			if !alreadyDoneToday {
				firstBonus = 5
			}
			desc := fmt.Sprintf("完成视频学习：%s", ep.Title)
			if firstBonus > 0 {
				desc += "（含每日首胜 +5）"
			}
			pointsLedger := &model.PointsLedger{
				UserID:       userID,
				ChangeAmount: watchPoints + firstBonus,
				ReasonType:   model.ReasonSystemWatch,
				Description:  desc,
			}
			// Run the completion flag + points award atomically. On any error
			// the whole completion is aborted (neither the flag nor points land).
			if err := s.db.Transaction(func(tx *gorm.DB) error {
				txProgress := s.progressRepo.WithTx(tx)
				if err := txProgress.MarkCompleted(userID, episodeID); err != nil {
					return err
				}
				return txProgress.AddPoints(pointsLedger)
			}); err != nil {
				return nil, err
			}
			prog.IsCompleted = true
		}
	}

	// Trigger Badge rules evaluation on every watch activity update
	if s.badgeService != nil {
		_, _ = s.badgeService.EvaluateRules(userID)
	}

	return prog, nil
}

func (s *progressService) GetPoints(userID uint) (*model.UserPoint, error) {
	return s.progressRepo.GetPoints(userID)
}

func (s *progressService) GetUserProgressOverview(userID uint) ([]model.UserProgress, error) {
	return s.progressRepo.GetUserProgressOverview(userID)
}

func (s *progressService) GetLastWatchedEpisode(userID, courseID uint) (*model.Episode, *model.UserProgress, error) {
	prog, err := s.progressRepo.GetLastWatchedEpisode(userID, courseID)
	if err != nil {
		return nil, nil, err
	}
	if prog == nil {
		return nil, nil, nil
	}

	ep, err := s.episodeRepo.FindByID(prog.EpisodeID)
	if err != nil {
		return nil, nil, err
	}
	return ep, prog, nil
}

func (s *progressService) GetPointsLedger(userID uint, limit, offset int) ([]model.PointsLedger, error) {
	return s.progressRepo.GetPointsLedger(userID, limit, offset)
}
