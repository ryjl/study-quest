package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
)

// ImportService coordinates scanning AList/WebDAV and registering episodes.
type ImportService interface {
	ScanPath(path string) ([]storage.FileInfo, error)
	ImportEpisodes(courseID uint, paths []string) ([]model.Episode, error)
	PingStorage() error
}

type importService struct {
	episodeRepo  repository.EpisodeRepository
	courseRepo   repository.CourseRepository
	settingsRepo repository.SettingsRepository
}

// NewImportService creates an instance of ImportService.
func NewImportService(er repository.EpisodeRepository, cr repository.CourseRepository, sr repository.SettingsRepository) ImportService {
	return &importService{
		episodeRepo:  er,
		courseRepo:   cr,
		settingsRepo: sr,
	}
}

func (s *importService) getActiveProvider() (storage.StorageProvider, error) {
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

func (s *importService) ScanPath(path string) ([]storage.FileInfo, error) {
	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}
	return provider.ListDir(path)
}

func (s *importService) ImportEpisodes(courseID uint, paths []string) ([]model.Episode, error) {
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		return nil, err
	}
	if course == nil {
		return nil, errors.New("course not found")
	}

	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}

	// Fetch current max sort order for this course
	currentEpisodes, err := s.episodeRepo.ListByCourse(courseID)
	if err != nil {
		return nil, err
	}
	nextSortOrder := 1
	if len(currentEpisodes) > 0 {
		nextSortOrder = currentEpisodes[len(currentEpisodes)-1].SortOrder + 1
	}

	imported := make([]model.Episode, 0, len(paths))

	for _, p := range paths {
		info, err := provider.GetFileInfo(p)
		if err != nil {
			// Skip single file failures
			continue
		}

		if info.IsDir {
			continue // Skip directories in direct imports
		}

		// Title formatting (Clean extension e.g., "01.mp4" -> "01")
		ext := filepath.Ext(info.Name)
		title := strings.TrimSuffix(info.Name, ext)
		// Try to replace symbols with spaces or format nicely
		title = strings.ReplaceAll(title, "_", " ")
		title = strings.ReplaceAll(title, "-", " ")

		ep := &model.Episode{
			CourseID:             courseID,
			SortOrder:            nextSortOrder,
			Title:                title,
			VideoRelativePath:    p,
			AttachmentJSON:       "[]",
			FileHash:             info.Hash,
			OriginalRelativePath: p,
			FileSize:             &info.Size,
			DurationSeconds:      nil, // Will be updated by Python automation later
		}

		if err := s.episodeRepo.Create(ep); err != nil {
			continue
		}
		nextSortOrder++

		// Attempt auto subtitle mapping from server storage
		s.autoMapSubtitle(ep)

		imported = append(imported, *ep)
	}

	return imported, nil
}

func (s *importService) autoMapSubtitle(ep *model.Episode) {
	// Look inside local data/subtitles folder
	subtitlesDir := "./data/subtitles"
	_ = os.MkdirAll(subtitlesDir, 0755)

	var srtPath string
	if ep.FileHash != "" {
		srtPath = filepath.Join(subtitlesDir, ep.FileHash+".srt")
	}

	if srtPath != "" {
		if _, err := os.Stat(srtPath); err == nil {
			content, err := os.ReadFile(srtPath)
			if err == nil {
				sub := &model.Subtitle{
					EpisodeID:  ep.ID,
					SrtContent: string(content),
				}
				_ = s.episodeRepo.SaveSubtitle(sub)
			}
		}
	}
}

func (s *importService) PingStorage() error {
	provider, err := s.getActiveProvider()
	if err != nil {
		return err
	}
	return provider.Ping()
}
