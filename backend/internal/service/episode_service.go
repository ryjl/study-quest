package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"

	"gorm.io/gorm"
)

// EpisodeService manages business operations for Course Episodes.
type EpisodeService interface {
	GetEpisodesByCourse(courseID uint) ([]model.Episode, error)
	GetEpisodeByID(id uint) (*model.Episode, error)
	CreateEpisode(courseID uint, chapterID *uint, title, videoPath, attachments string, sortOrder int, origPath string, size *int64, dur *int) (*model.Episode, error)
	UpdateEpisode(id uint, chapterID *uint, title, videoPath, attachments string, sortOrder int, origPath string, size *int64, dur *int) (*model.Episode, error)
	// UpdateEpisodeAdmin performs a PATCH-style update of the admin-editable
	// fields only (title, path, chapter, sort). Media metadata fields
	// (file_size, duration_seconds, media_meta_json) are preserved so editing a
	// title from the admin UI never clobbers ffprobe results.
	UpdateEpisodeAdmin(id uint, chapterID *uint, title, videoPath string, sortOrder int) (*model.Episode, error)
	DeleteEpisode(id uint) error
	ReorderEpisodes(episodeIDs []uint) error
	// BulkMoveEpisodes reassigns the given episodes to a different chapter
	// (chapterID == 0 → uncategorized). It validates that every episode belongs
	// to the SAME course as the target chapter (refuses cross-course moves),
	// resets each moved episode's sort_order so it appends to the end of the
	// destination chapter's existing ordering, and applies all writes in one
	// transaction. Returns ErrEpisodeMoveCrossCourse on a membership mismatch.
	BulkMoveEpisodes(episodeIDs []uint, chapterID uint) error

	// Subtitles
	GetSubtitle(episodeID uint) (*model.Subtitle, error)
	ListSubtitles(episodeID uint) ([]model.Subtitle, error)
	GetSubtitleByID(id uint) (*model.Subtitle, error)
	SaveSubtitle(episodeID uint, lang, label, srtContent string) error
	DeleteSubtitle(id uint) error

	// Streaming Stream Link Resolution
	GetStreamURL(episodeID uint, userAgent string) (*storage.DownloadLink, error)

	// Attachment Resolution: returns a download link for the Nth attachment of an episode.
	// The attachment list is the JSON array stored in episodes.attachment_json (list of relative paths).
	GetAttachmentStreamURL(episodeID uint, index int, userAgent string) (*storage.DownloadLink, string, error)

	// Probe resolves the episode's stream URL and runs ffprobe against it to
	// extract media metadata (duration, codec, resolution, bitrate, streams).
	// Results are persisted onto the episode (duration_seconds + media_meta_json)
	// so subsequent listings show correct durations without re-probing. Returns
	// the parsed metadata.
	Probe(episodeID uint) (*model.MediaMeta, error)
}

// ErrEpisodeMoveCrossCourse is returned by BulkMoveEpisodes when one or more
// episodes don't belong to the same course as the target chapter. Episodes are
// strictly scoped to their owning course, so a cross-course move would orphan
// them in the wrong tree; the handler surfaces this as 400.
var ErrEpisodeMoveCrossCourse = errors.New("episodes must belong to the target chapter's course")

// ErrEpisodesDifferentCourses is returned by ReorderEpisodes when the supplied
// episode IDs don't all belong to the same course. Sorting is a per-course
// concern (sort_order is only meaningful within a course), so mixing courses
// would produce a nonsense ordering; the handler surfaces this as 400.
var ErrEpisodesDifferentCourses = errors.New("batch-reordered episodes must belong to the same course")

type episodeService struct {
	db          *gorm.DB
	episodeRepo repository.EpisodeRepository
	chapterRepo repository.ChapterRepository
	resolver    *StorageProviderResolver
}

// NewEpisodeService creates an instance of EpisodeService. The resolver
// replaces the old settingsRepo-backed getActiveProvider: episodes resolve
// their provider via ep.SourceID (nil → global settings fallback). db and
// chapterRepo back the transactional bulk operations (BulkMoveEpisodes,
// ReorderEpisodes); nil db is tolerated by the non-transactional paths.
func NewEpisodeService(er repository.EpisodeRepository, resolver *StorageProviderResolver) EpisodeService {
	return &episodeService{
		episodeRepo: er,
		resolver:    resolver,
	}
}

// NewEpisodeServiceWithDB is the transactional variant of NewEpisodeService,
// wiring the *gorm.DB and ChapterRepository needed by BulkMoveEpisodes and
// ReorderEpisodes. Prefer this at the top-level wiring (main.go); tests that
// only exercise non-transactional paths can keep using NewEpisodeService.
func NewEpisodeServiceWithDB(db *gorm.DB, er repository.EpisodeRepository, cr repository.ChapterRepository, resolver *StorageProviderResolver) EpisodeService {
	return &episodeService{
		db:          db,
		episodeRepo: er,
		chapterRepo: cr,
		resolver:    resolver,
	}
}

func (s *episodeService) GetEpisodesByCourse(courseID uint) ([]model.Episode, error) {
	return s.episodeRepo.ListByCourse(courseID)
}

func (s *episodeService) GetEpisodeByID(id uint) (*model.Episode, error) {
	return s.episodeRepo.FindByID(id)
}

func (s *episodeService) CreateEpisode(courseID uint, chapterID *uint, title, videoPath, attachments string, sortOrder int, origPath string, size *int64, dur *int) (*model.Episode, error) {
	ep := &model.Episode{
		CourseID:             courseID,
		ChapterID:            chapterID,
		SortOrder:            sortOrder,
		Title:                title,
		VideoRelativePath:    videoPath,
		AttachmentJSON:       attachments,
		OriginalRelativePath: origPath,
		FileSize:             size,
		DurationSeconds:      dur,
	}
	if err := s.episodeRepo.Create(ep); err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *episodeService) UpdateEpisode(id uint, chapterID *uint, title, videoPath, attachments string, sortOrder int, origPath string, size *int64, dur *int) (*model.Episode, error) {
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
	ep.OriginalRelativePath = origPath
	ep.FileSize = size
	ep.DurationSeconds = dur

	if err := s.episodeRepo.Update(ep); err != nil {
		return nil, err
	}
	return ep, nil
}

// UpdateEpisodeAdmin patches only the admin-editable fields. Media metadata
// (hash, size, duration, ffprobe JSON) is left untouched on disk.
func (s *episodeService) UpdateEpisodeAdmin(id uint, chapterID *uint, title, videoPath string, sortOrder int) (*model.Episode, error) {
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
	ep.SortOrder = sortOrder
	if err := s.episodeRepo.Update(ep); err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *episodeService) DeleteEpisode(id uint) error {
	return s.episodeRepo.Delete(id)
}

// ReorderEpisodes rewrites sort_order for the given episode IDs (in array
// order: first id gets sort_order 1, next 2, ...). All writes run in ONE
// transaction so a failure partway through can't leave the course with a
// half-applied, gappy ordering. It also validates that every id belongs to the
// SAME course — sort_order is only meaningful within a course, so reordering a
// mixed set would silently corrupt two courses at once. Returns
// ErrEpisodesDifferentCourses on a mismatch.
func (s *episodeService) ReorderEpisodes(episodeIDs []uint) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	// Pre-load once for the same-course check; the loaded rows are reused
	// inside the tx so we don't re-fetch.
	loaded := make(map[uint]*model.Episode, len(episodeIDs))
	refCourseID := uint(0)
	haveRef := false
	for _, id := range episodeIDs {
		ep, err := s.episodeRepo.FindByID(id)
		if err != nil {
			return err
		}
		if ep == nil {
			// Missing id: skip silently (matches prior behavior — a deleted id
			// in the client's snapshot shouldn't abort the whole reorder).
			continue
		}
		loaded[id] = ep
		if !haveRef {
			// First LOADED episode sets the reference course (not the first id
			// in the array — leading missing ids must not skew the compare).
			refCourseID = ep.CourseID
			haveRef = true
		} else if ep.CourseID != refCourseID {
			return ErrEpisodesDifferentCourses
		}
	}
	if s.db == nil {
		// No transactional wiring (test path): fall back to the per-episode loop.
		return s.reorderEpisodesLoop(episodeIDs, loaded)
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		repo := s.episodeRepo.WithTx(tx)
		return s.reorderEpisodesLoop(episodeIDs, loaded, repo)
	})
}

// reorderEpisodesLoop applies the sort_order rewrite. It accepts an optional
// tx-scoped repo so the same body serves both the transactional and
// non-transactional (nil-db) paths.
func (s *episodeService) reorderEpisodesLoop(episodeIDs []uint, loaded map[uint]*model.Episode, repo ...repository.EpisodeRepository) error {
	r := s.episodeRepo
	if len(repo) > 0 && repo[0] != nil {
		r = repo[0]
	}
	for i, id := range episodeIDs {
		ep, ok := loaded[id]
		if !ok {
			continue
		}
		ep.SortOrder = i + 1
		if err := r.Update(ep); err != nil {
			return err
		}
	}
	return nil
}

// BulkMoveEpisodes reassigns episodes to a target chapter (0 = uncategorized).
// It refuses cross-course moves: when chapterID > 0, the target chapter's
// CourseID must match every episode's CourseID. Moved episodes are appended to
// the END of the destination's existing ordering (sort_order = max+1, max+2,
// ... in array order) so a move never collapses two episodes onto the same
// sort_order. All writes run in one transaction.
func (s *episodeService) BulkMoveEpisodes(episodeIDs []uint, chapterID uint) error {
	if len(episodeIDs) == 0 {
		return nil
	}
	if s.db == nil {
		return errors.New("BulkMoveEpisodes requires a transactional DB wiring")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		episodeRepo := s.episodeRepo.WithTx(tx)
		var chapterRepo repository.ChapterRepository
		if s.chapterRepo != nil {
			chapterRepo = s.chapterRepo.WithTx(tx)
		}

		// Validate course membership when moving INTO a chapter. Moving to
		// "uncategorized" (chapterID == 0) is always allowed — the episode
		// keeps its CourseID, it just leaves its chapter.
		if chapterID != 0 && chapterRepo != nil {
			ch, err := chapterRepo.FindByID(chapterID)
			if err != nil {
				return err
			}
			if ch == nil {
				return fmt.Errorf("目标章节 %d 不存在", chapterID)
			}
			for _, id := range episodeIDs {
				ep, err := episodeRepo.FindByID(id)
				if err != nil {
					return err
				}
				if ep == nil {
					continue
				}
				if ep.CourseID != ch.CourseID {
					return fmt.Errorf("课时「%s」不属于章节「%s」所在的课程%w",
						ep.Title, ch.Title, ErrEpisodeMoveCrossCourse)
				}
			}
		}

		// Append moved episodes to the END of the destination's ordering so
		// they don't collide with existing sort_order values in that chapter.
		// max(sort_order) across episodes already in the destination scope
		// (chapter_id = chapterID, or NULL when uncategorized) is the base.
		var maxSort int
		if chapterID == 0 {
			if err := tx.Model(&model.Episode{}).
				Where("chapter_id IS NULL").
				Select("COALESCE(MAX(sort_order), 0)").
				Scan(&maxSort).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Model(&model.Episode{}).
				Where("chapter_id = ?", chapterID).
				Select("COALESCE(MAX(sort_order), 0)").
				Scan(&maxSort).Error; err != nil {
				return err
			}
		}

		var chapterIDPtr *uint
		if chapterID > 0 {
			chapterIDPtr = &chapterID
		}
		for offset, id := range episodeIDs {
			ep, err := episodeRepo.FindByID(id)
			if err != nil {
				return err
			}
			if ep == nil {
				continue
			}
			ep.ChapterID = chapterIDPtr
			ep.SortOrder = maxSort + offset + 1
			if err := episodeRepo.Update(ep); err != nil {
				return err
			}
		}
		return nil
	})
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

func (s *episodeService) GetStreamURL(episodeID uint, userAgent string) (*storage.DownloadLink, error) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, errors.New("episode not found")
	}

	provider, err := s.resolver.Resolve(ep.SourceID)
	if err != nil {
		return nil, err
	}

	// Try regular path lookup
	link, err := provider.GetDownloadURL(ep.VideoRelativePath, userAgent)
	if err == nil {
		return link, nil
	}

	// Disaster recovery: the primary path 404'd on the storage backend (file
	// moved/renamed, or the storage backend changed). Try to re-resolve by
	// matching the stored filename basename + file_size against another episode
	// row that has a different (presumably still-valid) path. This replaces the
	// old hash-based recovery (hash is now removed) and fixes the broken
	// FindByPathAndSize which queried the same failing path.
	if ep.FileSize != nil && ep.OriginalRelativePath != "" {
		basename := filepath.Base(ep.OriginalRelativePath)
		// Scope the lookup to ep's own source so a file in source A never
		// self-heals onto source B's path. nil SourceID → legacy unscoped.
		if resolved, rErr := s.episodeRepo.FindByBasenameAndSizeScoped(basename, *ep.FileSize, ep.SourceID); rErr == nil && resolved != nil && resolved.VideoRelativePath != ep.VideoRelativePath {
			// Found another row with the same file at a different path — borrow it.
			ep.VideoRelativePath = resolved.VideoRelativePath
			_ = s.episodeRepo.Update(ep)
			return provider.GetDownloadURL(resolved.VideoRelativePath, userAgent)
		}
	}

	return nil, fmt.Errorf("failed to stream episode: resource unavailable (path mismatches)")
}

// GetAttachmentStreamURL resolves the download link for the attachment at the
// given index inside the episode's attachment_json array. Returns the link
// along with the original filename (for Content-Disposition friendly handling
// on the client side).
func (s *episodeService) GetAttachmentStreamURL(episodeID uint, index int, userAgent string) (*storage.DownloadLink, string, error) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, "", err
	}
	if ep == nil {
		return nil, "", errors.New("episode not found")
	}

	var paths []string
	if ep.AttachmentJSON != "" {
		if err := json.Unmarshal([]byte(ep.AttachmentJSON), &paths); err != nil {
			return nil, "", fmt.Errorf("invalid attachment_json: %w", err)
		}
	}
	if index < 0 || index >= len(paths) {
		return nil, "", errors.New("attachment index out of range")
	}

	provider, err := s.resolver.Resolve(ep.SourceID)
	if err != nil {
		return nil, "", err
	}

	link, err := provider.GetDownloadURL(paths[index], userAgent)
	if err != nil {
		return nil, "", err
	}

	// Derive a clean filename for the client.
	filename := paths[index]
	for i := len(filename) - 1; i >= 0; i-- {
		if filename[i] == '/' || filename[i] == '\\' {
			filename = filename[i+1:]
			break
		}
	}
	return link, filename, nil
}

// Probe resolves the episode's download URL via the active storage provider,
// then shells out to ffprobe to parse container-level media metadata. Because
// the netdisk CDNs honor HTTP Range, ffprobe only reads the header/index
// region — typically well under a second per file. The parsed result is
// persisted (duration_seconds + media_meta_json) so listings can show
// durations without re-probing every time.
func (s *episodeService) Probe(episodeID uint) (*model.MediaMeta, error) {
	link, err := s.GetStreamURL(episodeID, "ffprobe-probe")
	if err != nil {
		return nil, err
	}

	meta, err := probeMedia(link.URL)
	if err != nil {
		return nil, err
	}

	// Persist onto the episode row.
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return nil, err
	}
	if ep == nil {
		return nil, errors.New("episode not found")
	}
	if meta.DurationSeconds > 0 {
		d := meta.DurationSeconds
		ep.DurationSeconds = &d
	}
	if buf, err := json.Marshal(meta); err == nil {
		ep.MediaMetaJSON = string(buf)
	}

	// Extract cover image or screenshot
	uploadDir := "./data/uploads"
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Printf("[probe] failed to create upload directory %s: %v", uploadDir, err)
	} else {
		// Output path for cover/screenshot (always JPEG for simplicity)
		localFileName := fmt.Sprintf("episode_%d_cover.jpg", episodeID)
		localFullPath := filepath.Join(uploadDir, localFileName)

		// coverFileNonEmpty reports whether the output file exists and has at
		// least one byte. ffmpeg can exit 0 after writing a 0-byte JPEG (e.g.
		// when the embedded "cover" is a stub or the source has no real video
		// stream at the seek point), so we must verify the output before
		// trusting it — otherwise the client gets a broken image URL.
		coverFileNonEmpty := func() bool {
			info, statErr := os.Stat(localFullPath)
			return statErr == nil && info.Size() > 0
		}

		// 1. Try to extract embedded cover art first
		log.Printf("[probe] attempting to extract embedded cover for episode %d...", episodeID)
		err = extractEmbeddedCover(link.URL, localFullPath)
		if err == nil && coverFileNonEmpty() {
			ep.CoverURL = "/uploads/" + localFileName
			log.Printf("[probe] successfully extracted embedded cover for episode %d", episodeID)
		} else {
			if err == nil {
				log.Printf("[probe] embedded cover extracted but output empty for episode %d, falling back to screenshot extraction...", episodeID)
			} else {
				log.Printf("[probe] no embedded cover found or extraction failed for episode %d: %v. falling back to screenshot extraction...", episodeID, err)
			}
			// 2. Fallback to extracting screenshot at 5s (or duration/2)
			durSecs := 0
			if ep.DurationSeconds != nil {
				durSecs = *ep.DurationSeconds
			}
			err = extractScreenshot(link.URL, localFullPath, durSecs)
			if err == nil && coverFileNonEmpty() {
				ep.CoverURL = "/uploads/" + localFileName
				log.Printf("[probe] successfully extracted screenshot cover for episode %d", episodeID)
			} else {
				if err == nil {
					log.Printf("[probe] screenshot extracted but output empty for episode %d, leaving CoverURL unset", episodeID)
				} else {
					log.Printf("[probe] failed to extract screenshot cover for episode %d: %v", episodeID, err)
				}
			}
		}
	}

	if err := s.episodeRepo.Update(ep); err != nil {
		return nil, err
	}
	return meta, nil
}

// extractEmbeddedCover tries to extract an embedded cover art/image stream from a video.
func extractEmbeddedCover(videoURL, outputPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-i", videoURL,
		"-map", "0:v",
		"-map", "-0:V",
		"-c", "copy",
		outputPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("embedded cover extract failed: %w, stderr: %s", err, stderr.String())
	}
	return nil
}

// extractScreenshot extracts a single frame at a specific timestamp as JPEG.
func extractScreenshot(videoURL, outputPath string, durationSeconds int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	seekTime := "5"
	if durationSeconds > 0 && durationSeconds <= 5 {
		seekTime = strconv.Itoa(durationSeconds / 2)
	}

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-ss", seekTime,
		"-i", videoURL,
		"-vframes", "1",
		"-q:v", "2",
		outputPath,
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("screenshot extract failed: %w, stderr: %s", err, stderr.String())
	}
	return nil
}

// probeMedia shells out to ffprobe to extract container-level metadata from a
// remote URL. Because the netdisk CDNs honor HTTP Range, ffprobe only reads
// the header/index region (typically <1s). A 30s timeout guards against the
// occasional file that makes ffprobe hang.
func probeMedia(url string) (*model.MediaMeta, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_format", "-show_streams",
		"-of", "json",
		url,
	)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, errors.New("ffprobe timed out after 30s")
		}
		return nil, fmt.Errorf("ffprobe failed: %w", err)
	}

	// ffprobe JSON structure (only the fields we consume).
	var probe struct {
		Format struct {
			Duration   string `json:"duration"`
			BitRate    string `json:"bit_rate"`
			FormatName string `json:"format_name"`
		} `json:"format"`
		Streams []struct {
			Index       int    `json:"index"`
			CodecType   string `json:"codec_type"`
			CodecName   string `json:"codec_name"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			BitRate     string `json:"bit_rate"`
			AvgFrameRate string `json:"avg_frame_rate"`
			Channels    int    `json:"channels"`
			Tags        struct {
				Language string `json:"language"`
			} `json:"tags"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("parse ffprobe json: %w", err)
	}

	meta := &model.MediaMeta{
		FormatName: probe.Format.FormatName,
	}
	if d, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil {
		meta.DurationSeconds = int(d)
	}
	if br, err := strconv.ParseInt(probe.Format.BitRate, 10, 64); err == nil {
		meta.BitRate = br
	}

	for _, s := range probe.Streams {
		ms := model.MediaStream{
			Index:    s.Index,
			Type:     s.CodecType,
			Codec:    s.CodecName,
			Width:    s.Width,
			Height:   s.Height,
			Channels: s.Channels,
			Language: s.Tags.Language,
		}
		if br, err := strconv.ParseInt(s.BitRate, 10, 64); err == nil {
			ms.BitRate = br
		}
		meta.Streams = append(meta.Streams, ms)

		// Promote the first video/audio stream to top-level convenience
		// fields so consumers don't have to scan the stream list.
		if s.CodecType == "video" && meta.VideoCodec == "" {
			meta.VideoCodec = s.CodecName
			meta.Width = s.Width
			meta.Height = s.Height
			meta.Fps = s.AvgFrameRate
		} else if s.CodecType == "audio" && meta.AudioCodec == "" {
			meta.AudioCodec = s.CodecName
			meta.AudioChannels = s.Channels
		}
	}
	return meta, nil
}
