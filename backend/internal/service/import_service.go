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

type ImportPreviewNode struct {
	Name     string               `json:"name"`
	Path     string               `json:"path"`
	IsDir    bool                 `json:"is_dir"`
	Size     int64                `json:"size"`
	Hash     string               `json:"hash"`
	Type     string               `json:"type"` // "course", "chapter", "episode", "pass-through", "exclude"
	Children []*ImportPreviewNode `json:"children"`
}

type ExecuteTreeImportRequest struct {
	TargetCourseID uint               `json:"target_course_id"`
	NewCourse      *NewCourseRequest  `json:"new_course"`
	Tree           *ImportPreviewNode `json:"tree"`
}

type NewCourseRequest struct {
	Title    string `json:"title"`
	Grade    string `json:"grade"`
	Subject  string `json:"subject"`
	CoverURL string `json:"cover_url"`
	Tags     string `json:"tags"`
}

// ImportService coordinates scanning AList/WebDAV and registering episodes.
type ImportService interface {
	ScanPath(path string) ([]storage.FileInfo, error)
	PreviewDeepScan(path string) (*ImportPreviewNode, error)
	ExecuteTreeImport(req *ExecuteTreeImportRequest) error
	ScanDirectoryAttachments(path string) ([]storage.FileInfo, error)
	PingStorage() error
}

type importService struct {
	episodeRepo  repository.EpisodeRepository
	courseRepo   repository.CourseRepository
	settingsRepo repository.SettingsRepository
	chapterRepo  repository.ChapterRepository
}

// NewImportService creates an instance of ImportService.
func NewImportService(
	er repository.EpisodeRepository,
	cr repository.CourseRepository,
	sr repository.SettingsRepository,
	ch repository.ChapterRepository,
) ImportService {
	return &importService{
		episodeRepo:  er,
		courseRepo:   cr,
		settingsRepo: sr,
		chapterRepo:  ch,
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

func (s *importService) PreviewDeepScan(path string) (*ImportPreviewNode, error) {
	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}

	root, err := s.scanRecursive(provider, path, 0, 5)
	if err != nil {
		return nil, err
	}

	if root != nil {
		pruneEmptyNodes(root)
	}

	return root, nil
}

func (s *importService) scanRecursive(provider storage.StorageProvider, path string, currentDepth int, maxDepth int) (*ImportPreviewNode, error) {
	if currentDepth > maxDepth {
		return nil, nil
	}

	dirName := filepath.Base(path)
	if path == "/" || path == "" {
		dirName = "Root"
	}

	node := &ImportPreviewNode{
		Name:  dirName,
		Path:  path,
		IsDir: true,
		Type:  "pass-through",
	}

	if currentDepth == 0 {
		node.Type = "course"
	} else if currentDepth == 1 {
		node.Type = "chapter"
	} else {
		node.Type = "pass-through"
	}

	files, err := provider.ListDir(path)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if f.IsDir {
			childNode, err := s.scanRecursive(provider, f.Path, currentDepth+1, maxDepth)
			if err == nil && childNode != nil {
				node.Children = append(node.Children, childNode)
			}
		} else {
			if isVideoFile(f.Name) {
				ext := filepath.Ext(f.Name)
				title := strings.TrimSuffix(f.Name, ext)
				title = strings.ReplaceAll(title, "_", " ")
				title = strings.ReplaceAll(title, "-", " ")

				childNode := &ImportPreviewNode{
					Name:  title,
					Path:  f.Path,
					IsDir: false,
					Size:  f.Size,
					Hash:  f.Hash,
					Type:  "episode",
				}
				node.Children = append(node.Children, childNode)
			}
		}
	}

	return node, nil
}

func pruneEmptyNodes(node *ImportPreviewNode) bool {
	if !node.IsDir {
		return node.Type == "episode"
	}

	var activeChildren []*ImportPreviewNode
	for _, child := range node.Children {
		if keep := pruneEmptyNodes(child); keep {
			activeChildren = append(activeChildren, child)
		}
	}
	node.Children = activeChildren

	return len(node.Children) > 0
}

func (s *importService) ExecuteTreeImport(req *ExecuteTreeImportRequest) error {
	if req.Tree == nil {
		return errors.New("empty tree payload")
	}

	var courseID uint
	if req.TargetCourseID > 0 {
		course, err := s.courseRepo.FindByID(req.TargetCourseID)
		if err != nil {
			return err
		}
		if course == nil {
			return errors.New("target course not found")
		}
		courseID = course.ID
	} else {
		if req.NewCourse == nil || req.NewCourse.Title == "" {
			return errors.New("new course title is required")
		}
		g := model.Grade(req.NewCourse.Grade)
		if !g.Valid() {
			return errors.New("invalid course grade value: " + req.NewCourse.Grade)
		}
		c := &model.Course{
			Title:    req.NewCourse.Title,
			Grade:    g,
			Subject:  req.NewCourse.Subject,
			CoverURL: req.NewCourse.CoverURL,
			Tags:     req.NewCourse.Tags,
		}
		if err := s.courseRepo.Create(c); err != nil {
			return err
		}
		courseID = c.ID
	}

	currentEpisodes, err := s.episodeRepo.ListByCourse(courseID)
	if err != nil {
		return err
	}

	existingChapters, _ := s.chapterRepo.ListByCourse(courseID)

	nextSortOrder := 1
	if len(currentEpisodes) > 0 {
		nextSortOrder = currentEpisodes[len(currentEpisodes)-1].SortOrder + 1
	}

	chapterSortOrder := 1
	if len(existingChapters) > 0 {
		chapterSortOrder = existingChapters[len(existingChapters)-1].SortOrder + 1
	}

	var parseNode func(node *ImportPreviewNode, currentChapterID uint) error
	parseNode = func(node *ImportPreviewNode, currentChapterID uint) error {
		if node.Type == "exclude" {
			return nil
		}

		if node.IsDir {
			if node.Type == "chapter" {
				var chap *model.Chapter
				for i := range existingChapters {
					if existingChapters[i].Title == node.Name {
						chap = &existingChapters[i]
						break
					}
				}
				if chap == nil {
					chap = &model.Chapter{
						CourseID:  courseID,
						Title:     node.Name,
						SortOrder: chapterSortOrder,
					}
					if err := s.chapterRepo.Create(chap); err != nil {
						return err
					}
					chapterSortOrder++
					existingChapters = append(existingChapters, *chap)
				}
				currentChapterID = chap.ID
			}

			for _, child := range node.Children {
				if err := parseNode(child, currentChapterID); err != nil {
					return err
				}
			}
			return nil
		}

		if node.Type == "episode" {
			var existing *model.Episode
			for i := range currentEpisodes {
				curr := &currentEpisodes[i]
				if curr.Title == node.Name || filepath.Base(curr.VideoRelativePath) == filepath.Base(node.Path) || (node.Hash != "" && curr.FileHash == node.Hash) {
					existing = curr
					break
				}
			}

			if existing != nil {
				existing.ChapterID = currentChapterID
				existing.VideoRelativePath = node.Path
				existing.OriginalRelativePath = node.Path
				existing.FileHash = node.Hash
				existing.FileSize = &node.Size
				if err := s.episodeRepo.Update(existing); err == nil {
					s.autoMapSubtitle(existing)
				}
				return nil
			}

			ep := &model.Episode{
				CourseID:             courseID,
				ChapterID:            currentChapterID,
				SortOrder:            nextSortOrder,
				Title:                node.Name,
				VideoRelativePath:    node.Path,
				AttachmentJSON:       "[]",
				FileHash:             node.Hash,
				OriginalRelativePath: node.Path,
				FileSize:             &node.Size,
			}
			if err := s.episodeRepo.Create(ep); err != nil {
				return err
			}
			nextSortOrder++
			s.autoMapSubtitle(ep)
		}

		return nil
	}

	for _, child := range req.Tree.Children {
		if err := parseNode(child, 0); err != nil {
			return err
		}
	}

	return nil
}

func (s *importService) autoMapSubtitle(ep *model.Episode) {
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

func (s *importService) ScanDirectoryAttachments(path string) ([]storage.FileInfo, error) {
	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}
	files, err := provider.ListDir(path)
	if err != nil {
		return nil, err
	}
	var documents []storage.FileInfo
	for _, f := range files {
		if !f.IsDir && isDocumentFile(f.Name) {
			documents = append(documents, f)
		}
	}
	return documents, nil
}

func isDocumentFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf", ".docx", ".doc", ".txt", ".md", ".xlsx", ".xls", ".pptx", ".ppt", ".zip":
		return true
	}
	return false
}

func isVideoFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp4", ".mkv", ".avi", ".mov", ".flv", ".webm", ".ts", ".m4v", ".3gp":
		return true
	}
	return false
}
