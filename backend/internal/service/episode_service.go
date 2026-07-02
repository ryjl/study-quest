package service

import (
	"errors"
	"fmt"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
)

// EpisodeService manages business operations for Course Episodes.
type EpisodeService interface {
	GetEpisodesByCourse(courseID uint) ([]model.Episode, error)
	GetEpisodeByID(id uint) (*model.Episode, error)
	CreateEpisode(courseID, chapterID uint, title, videoPath, attachments string, sortOrder int, fileHash string, origPath string, size *int64, dur *int) (*model.Episode, error)
	UpdateEpisode(id uint, chapterID uint, title, videoPath, attachments string, sortOrder int, fileHash string, origPath string, size *int64, dur *int) (*model.Episode, error)
	DeleteEpisode(id uint) error
	ReorderEpisodes(episodeIDs []uint) error

	// Subtitles
	GetSubtitle(episodeID uint) (*model.Subtitle, error)
	ListSubtitles(episodeID uint) ([]model.Subtitle, error)
	GetSubtitleByID(id uint) (*model.Subtitle, error)
	SaveSubtitle(episodeID uint, lang, label, srtContent string) error
	DeleteSubtitle(id uint) error

	// AI Content
	GetAIContent(episodeID uint) (*model.AILessonContent, error)
	SaveAIContent(episodeID uint, preJSON, postJSON string) error

	// Streaming Stream Link Resolution
	GetStreamURL(episodeID uint, userAgent string) (*storage.DownloadLink, error)
}

type episodeService struct {
	episodeRepo  repository.EpisodeRepository
	settingsRepo repository.SettingsRepository
}

// NewEpisodeService creates an instance of EpisodeService.
func NewEpisodeService(er repository.EpisodeRepository, sr repository.SettingsRepository) EpisodeService {
	return &episodeService{
		episodeRepo:  er,
		settingsRepo: sr,
	}
}

func (s *episodeService) getActiveProvider() (storage.StorageProvider, error) {
	sType := s.settingsRepo.GetWithDefault("storage_type", "alist")
	sURL := s.settingsRepo.GetWithDefault("storage_url", "http://localhost:5244")
	sUser, _ := s.settingsRepo.Get("storage_username")
	sPass, _ := s.settingsRepo.Get("storage_password")
	sToken, _ := s.settingsRepo.Get("storage_token")

	if sType == "alist" {
		return storage.NewAListProvider(sURL, sUser, sPass, sToken), nil
	} else if sType == "webdav" {
		return storage.NewWebDAVProvider(sURL, sUser, sPass), nil
	}
	return nil, errors.New("unsupported storage_type configured: " + sType)
}

func (s *episodeService) GetEpisodesByCourse(courseID uint) ([]model.Episode, error) {
	return s.episodeRepo.ListByCourse(courseID)
}

func (s *episodeService) GetEpisodeByID(id uint) (*model.Episode, error) {
	return s.episodeRepo.FindByID(id)
}

func (s *episodeService) CreateEpisode(courseID, chapterID uint, title, videoPath, attachments string, sortOrder int, fileHash string, origPath string, size *int64, dur *int) (*model.Episode, error) {
	ep := &model.Episode{
		CourseID:             courseID,
		ChapterID:            chapterID,
		SortOrder:            sortOrder,
		Title:                title,
		VideoRelativePath:    videoPath,
		AttachmentJSON:       attachments,
		FileHash:             fileHash,
		OriginalRelativePath: origPath,
		FileSize:             size,
		DurationSeconds:      dur,
	}
	if err := s.episodeRepo.Create(ep); err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *episodeService) UpdateEpisode(id uint, chapterID uint, title, videoPath, attachments string, sortOrder int, fileHash string, origPath string, size *int64, dur *int) (*model.Episode, error) {
	ep, err := s.episodeRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, nil
	}

	ep.ChapterID = chapterID
	ep.Title = title
	ep.VideoRelativePath = videoPath
	ep.AttachmentJSON = attachments
	ep.SortOrder = sortOrder
	ep.FileHash = fileHash
	ep.OriginalRelativePath = origPath
	ep.FileSize = size
	ep.DurationSeconds = dur

	if err := s.episodeRepo.Update(ep); err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *episodeService) DeleteEpisode(id uint) error {
	return s.episodeRepo.Delete(id)
}

func (s *episodeService) ReorderEpisodes(episodeIDs []uint) error {
	for i, id := range episodeIDs {
		ep, err := s.episodeRepo.FindByID(id)
		if err != nil {
			return err
		}
		if ep != nil {
			ep.SortOrder = i + 1
			if err := s.episodeRepo.Update(ep); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *episodeService) GetSubtitle(episodeID uint) (*model.Subtitle, error) {
	return s.episodeRepo.GetSubtitle(episodeID)
}

func (s *episodeService) ListSubtitles(episodeID uint) ([]model.Subtitle, error) {
	return s.episodeRepo.ListSubtitles(episodeID)
}

func (s *episodeService) GetSubtitleByID(id uint) (*model.Subtitle, error) {
	return s.episodeRepo.GetSubtitleByID(id)
}

func (s *episodeService) SaveSubtitle(episodeID uint, lang, label, srtContent string) error {
	if lang == "" {
		lang = "zh-CN"
	}
	if label == "" {
		label = "中文"
	}
	sub := &model.Subtitle{
		EpisodeID:  episodeID,
		Language:   lang,
		Label:      label,
		SrtContent: srtContent,
	}
	return s.episodeRepo.SaveSubtitle(sub)
}

func (s *episodeService) DeleteSubtitle(id uint) error {
	return s.episodeRepo.DeleteSubtitle(id)
}

func (s *episodeService) GetAIContent(episodeID uint) (*model.AILessonContent, error) {
	return s.episodeRepo.GetAIContent(episodeID)
}

func (s *episodeService) SaveAIContent(episodeID uint, preJSON, postJSON string) error {
	ai := &model.AILessonContent{
		EpisodeID:        episodeID,
		PreAdventureJSON: preJSON,
		PostReviewJSON:    postJSON,
	}
	return s.episodeRepo.SaveAIContent(ai)
}

func (s *episodeService) GetStreamURL(episodeID uint, userAgent string) (*storage.DownloadLink, error) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, errors.New("episode not found")
	}

	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}

	// Try regular path lookup
	link, err := provider.GetDownloadURL(ep.VideoRelativePath, userAgent)
	if err == nil {
		return link, nil
	}

	// Dynamic disaster recovery: Path changed, check by file size or hash
	// If provider supports hash, check hash
	if provider.SupportsHash() && ep.FileHash != "" {
		resolved, err := s.episodeRepo.FindByHash(ep.FileHash)
		if err == nil && resolved != nil && resolved.VideoRelativePath != ep.VideoRelativePath {
			// Update locally cached path for next requests
			ep.VideoRelativePath = resolved.VideoRelativePath
			_ = s.episodeRepo.Update(ep)
			return provider.GetDownloadURL(resolved.VideoRelativePath, userAgent)
		}
	}

	// Check by size + original path fallback
	if ep.FileSize != nil {
		resolved, err := s.episodeRepo.FindByPathAndSize(ep.VideoRelativePath, *ep.FileSize)
		if err == nil && resolved != nil {
			return provider.GetDownloadURL(resolved.VideoRelativePath, userAgent)
		}
	}

	return nil, fmt.Errorf("failed to stream episode: resource unavailable (path mismatches)")
}
