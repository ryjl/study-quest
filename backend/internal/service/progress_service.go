package service

import (
	"errors"
	"fmt"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"time"
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
	progressRepo repository.ProgressRepository
	episodeRepo  repository.EpisodeRepository
	badgeService BadgeService
}

// NewProgressService creates an instance of ProgressService.
func NewProgressService(pr repository.ProgressRepository, er repository.EpisodeRepository, bs BadgeService) ProgressService {
	return &progressService{
		progressRepo: pr,
		episodeRepo:  er,
		badgeService: bs,
	}
}

func (s *progressService) GetProgress(userID, episodeID uint) (*model.UserProgress, error) {
	return s.progressRepo.GetProgress(userID, episodeID)
}

func (s *progressService) ReportProgress(userID, episodeID uint, positionSec, deltaWatchSec int) (*model.UserProgress, error) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, errors.New("episode not found")
	}

	prog, err := s.progressRepo.GetProgress(userID, episodeID)
	if err != nil {
		return nil, err
	}

	if prog == nil {
		// New progress record
		now := time.Now()
		prog = &model.UserProgress{
			UserID:              userID,
			EpisodeID:           episodeID,
			LastPositionSeconds: positionSec,
			WatchSeconds:        deltaWatchSec,
			IsCompleted:         0,
			UnlockedAt:          &now,
		}
	} else {
		// Update existing
		prog.LastPositionSeconds = positionSec
		prog.WatchSeconds += deltaWatchSec
	}

	// Completeness verification: mark complete only when the playhead has
	// actually reached ≥90% of the video. The previous logic summed WatchSeconds
	// (cumulative delta) which falsely marked episodes complete if the user
	// re-watched the first 80% in many short sessions without ever reaching
	// the end. Position-based gating matches what "watched" intuitively means.
	// Note: completion does NOT reset the resume position — reopening a
	// completed episode still resumes at the saved position.
	if prog.IsCompleted == 0 && ep.DurationSeconds != nil && *ep.DurationSeconds > 0 {
		duration := *ep.DurationSeconds
		threshold := int(float64(duration) * 0.9)
		if prog.LastPositionSeconds >= threshold {
			prog.IsCompleted = 1

			// Reward user points (10 points per episode watched)
			pointsLedger := &model.PointsLedger{
				UserID:       userID,
				ChangeAmount: 10,
				ReasonType:   "system_watch",
				Description:  fmt.Sprintf("Completed watching episode %d: %s", ep.ID, ep.Title),
			}

			// AddPoints updates points ledger and updates user_points table in a single transaction
			if err := s.progressRepo.AddPoints(pointsLedger); err != nil {
				// Log error but don't fail the progress reporting transaction
				fmt.Printf("Error adding user points: %v\n", err)
			}
		}
	}

	if err := s.progressRepo.SaveProgress(prog); err != nil {
		return nil, err
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
