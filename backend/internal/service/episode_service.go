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

	provider, err := s.getActiveProvider()
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

		// 1. Try to extract embedded cover art first
		log.Printf("[probe] attempting to extract embedded cover for episode %d...", episodeID)
		err = extractEmbeddedCover(link.URL, localFullPath)
		if err == nil {
			ep.CoverURL = "/uploads/" + localFileName
			log.Printf("[probe] successfully extracted embedded cover for episode %d", episodeID)
		} else {
			log.Printf("[probe] no embedded cover found or extraction failed for episode %d: %v. falling back to screenshot extraction...", episodeID, err)
			// 2. Fallback to extracting screenshot at 5s (or duration/2)
			durSecs := 0
			if ep.DurationSeconds != nil {
				durSecs = *ep.DurationSeconds
			}
			err = extractScreenshot(link.URL, localFullPath, durSecs)
			if err == nil {
				ep.CoverURL = "/uploads/" + localFileName
				log.Printf("[probe] successfully extracted screenshot cover for episode %d", episodeID)
			} else {
				log.Printf("[probe] failed to extract screenshot cover for episode %d: %v", episodeID, err)
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
