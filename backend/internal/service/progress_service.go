package service

import (
	"errors"
	"fmt"
	"sync"
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
	db           *gorm.DB
	progressRepo repository.ProgressRepository
	episodeRepo  repository.EpisodeRepository
	badgeService BadgeService
	// userLocks serializes the completion+points path per user. The player's
	// 5s heartbeat and a quiz-triggered report can overlap; without this, the
	// daily-first-completion bonus (HasCompletedToday) could fire twice.
	userLocks sync.Map // map[uint]*sync.Mutex
}

// NewProgressService creates an instance of ProgressService.
func NewProgressService(db *gorm.DB, pr repository.ProgressRepository, er repository.EpisodeRepository, bs BadgeService) ProgressService {
	return &progressService{
		db:           db,
		progressRepo: pr,
		episodeRepo:  er,
		badgeService: bs,
	}
}

// lockUser returns (and lazily creates) the per-user mutex, then locks it.
func (s *progressService) lockUser(userID uint) *sync.Mutex {
	v, _ := s.userLocks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func (s *progressService) GetProgress(userID, episodeID uint) (*model.UserProgress, error) {
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
	if prog.IsCompleted == 0 && ep.DurationSeconds != nil && *ep.DurationSeconds > 0 {
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
				ReasonType:   "system_watch",
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
			prog.IsCompleted = 1
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
