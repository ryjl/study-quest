package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"

	"gorm.io/gorm"
)

type ImportPreviewNode struct {
	Name     string               `json:"name"`
	Path     string               `json:"path"`
	IsDir    bool                 `json:"is_dir"`
	Size     int64                `json:"size"`
	Type     string               `json:"type"` // "course", "chapter", "episode", "pass-through", "exclude"
	Children []*ImportPreviewNode `json:"children"`
}

type ExecuteTreeImportRequest struct {
	TargetCourseID uint               `json:"target_course_id"`
	NewCourse      *NewCourseRequest  `json:"new_course"`
	Tree           *ImportPreviewNode `json:"tree"`
	// SourceID stamps every imported episode with its storage source. Nil =
	// legacy (resolved via the global settings fallback at play time).
	SourceID *uint `json:"source_id"`
}

type NewCourseRequest struct {
	Title    string `json:"title"`
	Grade    string `json:"grade"`
	Subject  string `json:"subject"`  // subject key (resolved to subject_id at import time)
	CoverURL string `json:"cover_url"`
	TagIDs   []uint `json:"tag_ids"`  // tag ids to attach to the new course
}

// ImportService coordinates scanning AList/WebDAV and registering episodes.
// Scan/preview/ping methods take a sourceID to select which storage source to
// operate on; a nil sourceID selects the global settings fallback (legacy).
type ImportService interface {
	ScanPath(path string, sourceID *uint) ([]storage.FileInfo, error)
	PreviewDeepScan(path string, sourceID *uint) (*ImportPreviewNode, error)
	ExecuteTreeImport(req *ExecuteTreeImportRequest) error
	ScanDirectoryAttachments(path string, sourceID *uint) ([]storage.FileInfo, error)
	PingStorage(sourceID *uint) error
}

type importService struct {
	db           *gorm.DB
	episodeRepo  repository.EpisodeRepository
	courseRepo   repository.CourseRepository
	resolver     *StorageProviderResolver
	chapterRepo  repository.ChapterRepository
	subjectRepo  repository.SubjectRepository
	enqueueProbe func(uint) // optional: backfill media metadata after import

	// testFailOnEpisodeNth, when > 0, makes the Nth episode Create return an
	// error — a test-only hook for verifying the import transaction rolls back
	// on a mid-tree failure. Always 0 in production (the constructor leaves it
	// unset). Accessed only from ExecuteTreeImport's episode branch.
	testFailOnEpisodeNth int
}

// SetTestFailHook is a test-only seam (exported so tests in other packages can
// set it) that injects a forced failure on the Nth episode creation. Production
// code never calls this. The zero value means "no failure".
func (s *importService) SetTestFailHook(failOnEpisodeNth int) {
	s.testFailOnEpisodeNth = failOnEpisodeNth
}

// NewImportService creates an instance of ImportService. enqueueProbe is an
// optional callback (pass nil to skip) invoked after each episode is created
// or updated, so a background worker can ffprobe its media metadata. The
// resolver replaces the old settingsRepo-backed getActiveProvider.
func NewImportService(
	db *gorm.DB,
	er repository.EpisodeRepository,
	cr repository.CourseRepository,
	resolver *StorageProviderResolver,
	ch repository.ChapterRepository,
	subj repository.SubjectRepository,
	enqueueProbe func(uint),
) ImportService {
	return &importService{
		db:           db,
		episodeRepo:  er,
		courseRepo:   cr,
		resolver:     resolver,
		chapterRepo:  ch,
		subjectRepo:  subj,
		enqueueProbe: enqueueProbe,
	}
}

func (s *importService) ScanPath(path string, sourceID *uint) ([]storage.FileInfo, error) {
	provider, err := s.resolver.Resolve(sourceID)
	if err != nil {
		return nil, err
	}
	return provider.ListDir(path)
}

func (s *importService) PreviewDeepScan(path string, sourceID *uint) (*ImportPreviewNode, error) {
	provider, err := s.resolver.Resolve(sourceID)
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

// ExecuteTreeImport registers a previewed tree as course/chapters/episodes.
//
// The entire write set — new course (if any), chapters, episodes, and their
// auto-matched subtitles — runs inside ONE transaction. A failure partway
// through (e.g. a bad episode insert) rolls back every prior insert, so we
// never leave a half-imported course with orphan chapters/episodes. The old
// code created rows across 3 repos with no rollback. Episode probe enqueueing
// (a channel send, not a DB write) is deferred until after commit, so a rolled-
// back episode is never probed.
func (s *importService) ExecuteTreeImport(req *ExecuteTreeImportRequest) error {
	if req.Tree == nil {
		return errors.New("empty tree payload")
	}

	// Collect episode IDs to probe after commit (only if the tx succeeds).
	var probePending []uint
	err := s.db.Transaction(func(tx *gorm.DB) error {
		// Bind repos to the transaction for all writes below.
		courseRepo := s.courseRepo.WithTx(tx)
		episodeRepo := s.episodeRepo.WithTx(tx)
		chapterRepo := s.chapterRepo.WithTx(tx)

		var courseID uint
		if req.TargetCourseID > 0 {
			course, err := courseRepo.FindByID(req.TargetCourseID)
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
			// Grade is optional on import — default to "universal" so admins can
			// import a course without first picking a grade in the wizard.
			gradeStr := strings.TrimSpace(req.NewCourse.Grade)
			if gradeStr == "" {
				gradeStr = "universal"
			}
			var grades []model.Grade
			for _, part := range strings.Split(gradeStr, ",") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				g := model.Grade(part)
				if !g.Valid() {
					return errors.New("invalid course grade value: " + part)
				}
				grades = append(grades, g)
			}
			if len(grades) == 0 {
				grades = []model.Grade{model.GradeUniversal}
			}
			// Resolve the subject key to its ID. Fall back to the first subject
			// when the key is missing/unknown so import never fails on this.
			var subjectID uint
			if subj, _ := s.subjectRepo.FindByKey(req.NewCourse.Subject); subj != nil {
				subjectID = subj.ID
			} else if list, err := s.subjectRepo.List(); err == nil && len(list) > 0 {
				subjectID = list[0].ID
			} else {
				return errors.New("no subject available; create a subject first")
			}
			contentType := model.ContentLearning
			if req.NewCourse.Subject == "entertainment" {
				contentType = model.ContentEntertainment
			}
			c := &model.Course{
				Title:       req.NewCourse.Title,
				SubjectID:   subjectID,
				ContentType: contentType,
				CoverURL:    req.NewCourse.CoverURL,
			}
			if err := courseRepo.Create(c); err != nil {
				return err
			}
			// Set the grade set (course_grades join table).
			if err := courseRepo.SetGrades(c.ID, grades); err != nil {
				return err
			}
			// Attach any requested tags via the many2many association.
			if len(req.NewCourse.TagIDs) > 0 {
				if err := courseRepo.SetTags(c.ID, req.NewCourse.TagIDs); err != nil {
					return err
				}
			}
			courseID = c.ID
		}

		currentEpisodes, err := episodeRepo.ListByCourse(courseID)
		if err != nil {
			return err
		}

		existingChapters, _ := chapterRepo.ListByCourse(courseID)

		nextSortOrder := 1
		if len(currentEpisodes) > 0 {
			nextSortOrder = currentEpisodes[len(currentEpisodes)-1].SortOrder + 1
		}

		chapterSortOrder := 1
		if len(existingChapters) > 0 {
			chapterSortOrder = existingChapters[len(existingChapters)-1].SortOrder + 1
		}

		var parseNode func(node *ImportPreviewNode, currentChapterID *uint) error
		parseNode = func(node *ImportPreviewNode, currentChapterID *uint) error {
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
						if err := chapterRepo.Create(chap); err != nil {
							return err
						}
						chapterSortOrder++
						existingChapters = append(existingChapters, *chap)
					}
					currentChapterID = &chap.ID
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
					if curr.Title == node.Name || filepath.Base(curr.VideoRelativePath) == filepath.Base(node.Path) {
						existing = curr
						break
					}
				}

				if existing != nil {
					existing.ChapterID = currentChapterID
					existing.VideoRelativePath = node.Path
					existing.OriginalRelativePath = node.Path
					existing.FileSize = &node.Size
					if err := episodeRepo.Update(existing); err == nil {
						s.autoMapSubtitleTx(episodeRepo, existing)
						probePending = append(probePending, existing.ID)
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
					OriginalRelativePath: node.Path,
					FileSize:             &node.Size,
					SourceID:             req.SourceID,
				}
				// Test-only forced failure: simulates a DB error on the Nth
				// episode create, so the import transaction's rollback path can
				// be exercised. No-op in production (testFailOnEpisodeNth == 0).
				if s.testFailOnEpisodeNth > 0 {
					s.testFailOnEpisodeNth--
					if s.testFailOnEpisodeNth == 0 {
						return errors.New("test-injected episode create failure")
					}
				}
				if err := episodeRepo.Create(ep); err != nil {
					return err
				}
				nextSortOrder++
				s.autoMapSubtitleTx(episodeRepo, ep)
				probePending = append(probePending, ep.ID)
			}

			return nil
		}

		for _, child := range req.Tree.Children {
			if err := parseNode(child, nil); err != nil {
				return err
			}
		}

		return nil
	})
	if err != nil {
		return err
	}

	// Probe enqueueing is deferred until the transaction commits — if the tx
	// rolled back, those episode IDs don't exist and must not be probed.
	for _, id := range probePending {
		s.maybeEnqueueProbeByID(id)
	}
	return nil
}

// autoMapSubtitleTx is the transaction-aware variant of autoMapSubtitle: it
// saves the matched subtitle through the tx-bound episode repo so the subtitle
// write participates in the import transaction (rolls back with it on failure).
//
// Subtitle file matching is by video basename (e.g. "lesson01.mp4" →
// "lesson01.srt"). This replaces the old hash-based matching (the hash column
// has been removed). The subtitles directory holds <basename>.srt files pushed
// by the desktop transcription pipeline (ADR-008).
func (s *importService) autoMapSubtitleTx(episodeRepo repository.EpisodeRepository, ep *model.Episode) {
	subtitlesDir := "./data/subtitles"
	_ = os.MkdirAll(subtitlesDir, 0755)

	var srtPath string
	path := ep.OriginalRelativePath
	if path == "" {
		path = ep.VideoRelativePath
	}
	if path != "" {
		basename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		srtPath = filepath.Join(subtitlesDir, basename+".srt")
	}

	if srtPath != "" {
		if _, err := os.Stat(srtPath); err == nil {
			content, err := os.ReadFile(srtPath)
			if err == nil {
				sub := &model.Subtitle{
					EpisodeID:  ep.ID,
					SrtContent: string(content),
				}
				_ = episodeRepo.SaveSubtitle(sub)
			}
		}
	}
}

// maybeEnqueueProbeByID enqueues a probe for a single episode ID, gated on the
// episode actually lacking duration. Used by the post-commit probe flush.
func (s *importService) maybeEnqueueProbeByID(episodeID uint) {
	if s.enqueueProbe == nil {
		return
	}
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil || ep.DurationSeconds != nil {
		return
	}
	s.enqueueProbe(ep.ID)
}

// maybeEnqueueProbe hands the episode to the background probe worker when
// media metadata is still missing. No-op when no worker is wired (nil callback)
// or when the episode already has a duration (e.g. supplied by upstream ingest).
func (s *importService) maybeEnqueueProbe(ep *model.Episode) {
	if s.enqueueProbe == nil || ep == nil {
		return
	}
	if ep.DurationSeconds != nil {
		return
	}
	s.enqueueProbe(ep.ID)
}

func (s *importService) autoMapSubtitle(ep *model.Episode) {
	subtitlesDir := "./data/subtitles"
	_ = os.MkdirAll(subtitlesDir, 0755)

	var srtPath string
	path := ep.OriginalRelativePath
	if path == "" {
		path = ep.VideoRelativePath
	}
	if path != "" {
		basename := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		srtPath = filepath.Join(subtitlesDir, basename+".srt")
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

func (s *importService) PingStorage(sourceID *uint) error {
	provider, err := s.resolver.Resolve(sourceID)
	if err != nil {
		return err
	}
	return provider.Ping()
}

func (s *importService) ScanDirectoryAttachments(path string, sourceID *uint) ([]storage.FileInfo, error) {
	provider, err := s.resolver.Resolve(sourceID)
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
