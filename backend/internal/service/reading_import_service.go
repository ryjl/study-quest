package service

import (
	"errors"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
)

// ReadingImportService scans a storage folder and creates a ReadingSeries +
// ReadingBook rows from it, mirroring ImportService's PreviewDeepScan +
// ExecuteTreeImport pattern but simplified: folder → series, PDF files → books,
// no chapters, no probe worker.

// ReadingPreviewNode reuses the same shape as ImportPreviewNode so the SPA can
// reuse the PathBrowser + tree-rendering infrastructure. Type values for the
// reading flow: "series" (root), "book" (PDF leaf), "pass-through" (nested dir
// that just descends), "exclude" (user deselected).
type ReadingPreviewNode struct {
	Name     string                 `json:"name"`
	Path     string                 `json:"path"`
	IsDir    bool                   `json:"is_dir"`
	Size     int64                  `json:"size"`
	Hash     string                 `json:"hash"`
	Type     string                 `json:"type"`
	Children []*ReadingPreviewNode  `json:"children"`
}

// ExecuteReadingImportRequest mirrors ExecuteTreeImportRequest.
type ExecuteReadingImportRequest struct {
	TargetSeriesID uint                  `json:"target_series_id"`
	NewSeries      *NewReadingSeriesReq  `json:"new_series"`
	Tree           *ReadingPreviewNode   `json:"tree"`
}

type NewReadingSeriesReq struct {
	Title    string `json:"title"`
	Grade    string `json:"grade"`
	Subject  string `json:"subject"` // subject key
	CoverURL string `json:"cover_url"`
	TagIDs   []uint `json:"tag_ids"`
}

type ReadingImportService interface {
	PreviewReadingFolder(path string) (*ReadingPreviewNode, error)
	ExecuteReadingImport(req *ExecuteReadingImportRequest) error
}

type readingImportService struct {
	db          *gorm.DB
	seriesRepo  repository.ReadingSeriesRepository
	bookRepo    repository.ReadingBookRepository
	subjectRepo repository.SubjectRepository
	settingsRepo repository.SettingsRepository
}

func NewReadingImportService(
	db *gorm.DB,
	sr repository.ReadingSeriesRepository,
	br repository.ReadingBookRepository,
	subj repository.SubjectRepository,
	settings repository.SettingsRepository,
) ReadingImportService {
	return &readingImportService{
		db:           db,
		seriesRepo:   sr,
		bookRepo:     br,
		subjectRepo:  subj,
		settingsRepo: settings,
	}
}

func (s *readingImportService) getActiveProvider() (storage.StorageProvider, error) {
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

func isPdfFile(filename string) bool {
	return strings.ToLower(filepath.Ext(filename)) == ".pdf"
}

// PreviewReadingFolder walks the storage tree and builds a preview the admin can
// edit before committing. The root folder name becomes the series title;
// PDF leaves become books. Mirrors importService.PreviewDeepScan.
func (s *readingImportService) PreviewReadingFolder(path string) (*ReadingPreviewNode, error) {
	provider, err := s.getActiveProvider()
	if err != nil {
		return nil, err
	}

	root, err := s.scanReadingRecursive(provider, path, 0, 5)
	if err != nil {
		return nil, err
	}

	if root != nil {
		pruneReadingEmptyNodes(root)
	}

	return root, nil
}

func (s *readingImportService) scanReadingRecursive(provider storage.StorageProvider, path string, currentDepth, maxDepth int) (*ReadingPreviewNode, error) {
	if currentDepth > maxDepth {
		return nil, nil
	}

	dirName := filepath.Base(path)
	if path == "/" || path == "" {
		dirName = "Root"
	}

	node := &ReadingPreviewNode{
		Name:  dirName,
		Path:  path,
		IsDir: true,
		Type:  "pass-through",
	}

	if currentDepth == 0 {
		node.Type = "series"
	}

	files, err := provider.ListDir(path)
	if err != nil {
		return nil, err
	}

	for _, f := range files {
		if f.IsDir {
			childNode, err := s.scanReadingRecursive(provider, f.Path, currentDepth+1, maxDepth)
			if err == nil && childNode != nil {
				node.Children = append(node.Children, childNode)
			}
		} else {
			if isPdfFile(f.Name) {
				ext := filepath.Ext(f.Name)
				title := strings.TrimSuffix(f.Name, ext)
				title = strings.ReplaceAll(title, "_", " ")
				title = strings.ReplaceAll(title, "-", " ")

				node.Children = append(node.Children, &ReadingPreviewNode{
					Name:  title,
					Path:  f.Path,
					IsDir: false,
					Size:  f.Size,
					Hash:  f.Hash,
					Type:  "book",
				})
			}
		}
	}

	return node, nil
}

// pruneReadingEmptyNodes removes subtrees that contain no book leaves. Returns
// true if the node should be kept (is a book or has kept children).
func pruneReadingEmptyNodes(node *ReadingPreviewNode) bool {
	if !node.IsDir {
		return node.Type == "book"
	}

	kept := make([]*ReadingPreviewNode, 0, len(node.Children))
	for _, child := range node.Children {
		if pruneReadingEmptyNodes(child) {
			kept = append(kept, child)
		}
	}
	node.Children = kept

	// Keep the root (series) even if empty — the admin may add books later.
	// Non-root empty dirs are pruned.
	if node.Type == "series" {
		return true
	}
	return len(kept) > 0
}

// ExecuteReadingImport creates a ReadingSeries + ReadingBook rows from the
// preview tree in one transaction. Mirrors importService.ExecuteTreeImport but
// simpler: no chapters, no probe worker. Existing books in the target series
// are matched by title or path for idempotent re-import.
func (s *readingImportService) ExecuteReadingImport(req *ExecuteReadingImportRequest) error {
	if req.Tree == nil {
		return errors.New("empty tree payload")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		seriesRepo := s.seriesRepo.WithTx(tx)
		bookRepo := s.bookRepo.WithTx(tx)

		var seriesID uint
		if req.TargetSeriesID > 0 {
			series, err := seriesRepo.FindByID(req.TargetSeriesID)
			if err != nil {
				return err
			}
			if series == nil {
				return errors.New("target series not found")
			}
			seriesID = series.ID
		} else {
			if req.NewSeries == nil || req.NewSeries.Title == "" {
				return errors.New("new series title is required")
			}
			gradeStr := strings.TrimSpace(req.NewSeries.Grade)
			if gradeStr == "" {
				gradeStr = "universal"
			}
			g := model.Grade(gradeStr)
			if !g.Valid() {
				return errors.New("invalid series grade value: " + req.NewSeries.Grade)
			}
			var subjectID uint
			if subj, _ := s.subjectRepo.FindByKey(req.NewSeries.Subject); subj != nil {
				subjectID = subj.ID
			} else if list, err := s.subjectRepo.List(); err == nil && len(list) > 0 {
				subjectID = list[0].ID
			} else {
				return errors.New("no subject available; create a subject first")
			}
			series := &model.ReadingSeries{
				Title:     req.NewSeries.Title,
				Grade:     g,
				SubjectID: subjectID,
				CoverURL:  req.NewSeries.CoverURL,
				SortOrder: 0,
			}
			if err := seriesRepo.Create(series); err != nil {
				return err
			}
			if len(req.NewSeries.TagIDs) > 0 {
				if err := seriesRepo.SetTags(series.ID, req.NewSeries.TagIDs); err != nil {
					return err
				}
			}
			seriesID = series.ID
		}

		// Load existing books for idempotent upsert (match by title or path).
		existingBooks, err := bookRepo.ListBySeries(seriesID)
		if err != nil {
			return err
		}
		existingByTitle := make(map[string]*model.ReadingBook, len(existingBooks))
		existingByPath := make(map[string]*model.ReadingBook, len(existingBooks))
		for i := range existingBooks {
			existingByTitle[existingBooks[i].Title] = &existingBooks[i]
			existingByPath[filepath.Base(existingBooks[i].FileRelativePath)] = &existingBooks[i]
		}

		nextSortOrder := 1
		if len(existingBooks) > 0 {
			nextSortOrder = existingBooks[len(existingBooks)-1].SortOrder + 1
		}

		// Resolve subject + grade for books: inherit from the series.
		series, _ := seriesRepo.FindByID(seriesID)
		var bookSubjectID uint
		var bookGrade model.Grade = "universal"
		if series != nil {
			bookSubjectID = series.SubjectID
			bookGrade = series.Grade
		}

		return s.importReadingNode(req.Tree, seriesID, bookSubjectID, bookGrade, bookRepo, existingByTitle, existingByPath, &nextSortOrder)
	})
}

// importReadingNode recursively walks the preview tree and creates/updates
// ReadingBook rows. Directories are descended into (pass-through); "book" leaves
// become ReadingBook rows; "exclude" nodes are skipped.
func (s *readingImportService) importReadingNode(
	node *ReadingPreviewNode,
	seriesID, subjectID uint,
	grade model.Grade,
	bookRepo repository.ReadingBookRepository,
	existingByTitle map[string]*model.ReadingBook,
	existingByPath map[string]*model.ReadingBook,
	sortOrder *int,
) error {
	if node.Type == "exclude" {
		return nil
	}

	if !node.IsDir {
		// Book leaf — create or update.
		if node.Type != "book" {
			return nil
		}
		basename := filepath.Base(node.Path)

		// Match existing by title or path basename.
		if existing, ok := existingByTitle[node.Name]; ok {
			existing.FileRelativePath = node.Path
			existing.FileHash = node.Hash
			if node.Size > 0 {
				sz := node.Size
				existing.FileSize = &sz
			}
			return bookRepo.Update(existing)
		}
		if existing, ok := existingByPath[basename]; ok {
			existing.Title = node.Name
			existing.FileRelativePath = node.Path
			existing.FileHash = node.Hash
			if node.Size > 0 {
				sz := node.Size
				existing.FileSize = &sz
			}
			return bookRepo.Update(existing)
		}

		book := &model.ReadingBook{
			SeriesID:         seriesID,
			SortOrder:        *sortOrder,
			Title:            node.Name,
			FileRelativePath: node.Path,
			FileHash:         node.Hash,
			Grade:            grade,
			SubjectID:        subjectID,
		}
		if node.Size > 0 {
			sz := node.Size
			book.FileSize = &sz
		}
		if err := bookRepo.Create(book); err != nil {
			return err
		}
		*sortOrder++
		return nil
	}

	// Directory — descend into children.
	for _, child := range node.Children {
		if err := s.importReadingNode(child, seriesID, subjectID, grade, bookRepo, existingByTitle, existingByPath, sortOrder); err != nil {
			return err
		}
	}
	return nil
}
