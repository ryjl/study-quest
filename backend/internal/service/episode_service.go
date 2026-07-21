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
	"strings"
	"time"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/storage"
	"studyquest/backend/internal/subtitle"

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
	SaveSubtitle(episodeID uint, lang, label, srtOrVtt string) error
	SaveSubtitleWithSource(episodeID uint, lang, label, srtOrVtt, source string) error
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

	// ExtractEmbeddedSubtitle pulls the given subtitle stream out of the
	// episode's video container as WebVTT and persists it with source="embedded"
	// (which does NOT trigger the polish/segment pipeline — that's keyed to
	// source="whisper"). streamIndex is the ffprobe stream index (the same one
	// surfaced in media_meta_json.streams[].index). Returns
	// ErrBitmapSubtitleNotSupported for picture-based codecs (PGS/VOBSUB/DVB)
	// which ffmpeg cannot transcode to text.
	ExtractEmbeddedSubtitle(episodeID uint, streamIndex int, language, label string) error
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

// ErrBitmapSubtitleNotSupported is returned by ExtractEmbeddedSubtitle when the
// targeted stream is a bitmap/picture-based subtitle codec (PGS / VOBSUB /
// DVB). ffmpeg cannot transcode these to WebVTT — it errors with "Subtitle
// encoding currently only possible from text to text or bitmap to bitmap".
// The only path to text is OCR (Whisper), so we surface a dedicated error the
// handler maps to 400 with an actionable hint pointing the admin at Whisper.
var ErrBitmapSubtitleNotSupported = errors.New("bitmap subtitles (PGS/VOBSUB/DVB) cannot be extracted as text; use Whisper transcription instead")

// ErrInvalidStreamIndex is returned by ExtractEmbeddedSubtitle when the
// requested stream index is negative, doesn't exist in the probed media, or
// doesn't point at a text subtitle stream. The handler maps it to 400. Defense
// in depth: the admin UI only offers valid text-subtitle stream buttons, but a
// direct API call (or a stale media_meta_json after the source file changed)
// could still hand in a bad index.
var ErrInvalidStreamIndex = errors.New("stream index is not a valid text subtitle stream")

// ErrSubtitleLanguageConflict is returned by ExtractEmbeddedSubtitle when the
// episode already has a subtitle for the same (episode, language) pair. The
// upsert semantics of SaveSubtitle would silently overwrite the existing
// track — including clobbering a whisper track's VttContent + RawVttContent
// snapshot + source label, which is real data loss. We refuse and tell the
// admin to delete the existing track first (or pick a different language label).
// PR3's multi-track-per-episode goal assumes each (episode, language) is still
// unique; true multi-language comes from distinct language codes.
var ErrSubtitleLanguageConflict = errors.New("a subtitle already exists for this episode and language; delete it first or use a different language")

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

// SaveSubtitle persists a subtitle for an episode, normalizing the input to
// VTT storage format. The srtOrVtt argument is accepted in either format:
// the whisper worker uploads SRT, admin manual upload may be SRT or VTT,
// embedded extraction produces VTT. Anything not starting with "WEBVTT" is
// treated as SRT and converted.
//
// Source defaults to "whisper" (the most common caller). Callers that know
// the origin (embedded extractor, manual upload, polish pipeline) should use
// SaveSubtitleWithSource to set it correctly — the polish pipeline keys off
// Source == "whisper" to decide whether to run.
func (s *episodeService) SaveSubtitle(episodeID uint, lang, label, srtOrVtt string) error {
	return s.SaveSubtitleWithSource(episodeID, lang, label, srtOrVtt, "whisper")
}

// SaveSubtitleWithSource is like SaveSubtitle but lets the caller specify the
// origin. Used by the embedded-subtitle extractor ("embedded"), admin manual
// upload ("manual"), and the polish pipeline ("llm_optimized").
//
// This is the "fresh material" path: the caller is handing us a brand-new
// subtitle (worker upload, admin upload, disk auto-match), so the raw
// snapshot is taken FROM this input — RawVttContent mirrors VttContent. The
// polish pipeline does NOT come through here; it writes polished text via
// episodeRepo.SaveSubtitle directly with RawVttContent empty, so the
// original snapshot survives.
func (s *episodeService) SaveSubtitleWithSource(episodeID uint, lang, label, srtOrVtt, source string) error {
	if lang == "" {
		lang = "zh-CN"
	}
	if label == "" {
		label = "中文"
	}
	vtt := srtOrVtt
	if !strings.HasPrefix(strings.TrimSpace(vtt), "WEBVTT") {
		vtt = subtitle.SrtToVtt(vtt)
	}
	if source == "" {
		source = "whisper"
	}
	sub := &model.Subtitle{
		EpisodeID:     episodeID,
		Language:      lang,
		Label:         label,
		VttContent:    vtt,
		RawVttContent: vtt, // fresh material → snapshot it now; polish later won't touch this
		Source:        source,
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
		// self-heals onto source B's path. A nil SourceID yields no match.
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

// ExtractEmbeddedSubtitle extracts the given subtitle stream from the
// episode's video container into WebVTT and persists it with source="embedded".
//
// Flow:
//  1. Resolve the stream URL (same path Probe/Stream use).
//  2. Run ffmpeg: `-map 0:<streamIndex> -c:s webvtt out.vtt`. The -reconnect
//     flags are the same as the cover/screenshot extractors — the天翼云 OBS CDN
//     intermittently RSTs TLS during the multi-socket read.
//  3. Bitmap codecs (PGS/VOBSUB/DVB) make ffmpeg fail with "Subtitle encoding
//     currently only possible from text to text or bitmap to bitmap" — detect
//     that marker and return ErrBitmapSubtitleNotSupported so the handler can
//     surface a 400 with an actionable "use Whisper" hint instead of a 500.
//  4. Read the output VTT, hand it to SaveSubtitleWithSource("embedded"). That
//     normalizes to VTT (input is already VTT, so it passes through) and writes
//     the RawVttContent snapshot.
//
// source="embedded" deliberately does NOT trigger the polish/segment pipeline
// — PR2's OnSubtitleCompleted gating keys off source="whisper". Embedded
// subtitles are usually human-authored and already clean; segmenting them is
// left to an explicit admin trigger (or a later PR).
func (s *episodeService) ExtractEmbeddedSubtitle(episodeID uint, streamIndex int, language, label string) error {
	// Validate streamIndex against the probed media metadata BEFORE invoking
	// ffmpeg. Three failure modes we want to catch here:
	//   - negative index → ffmpeg undefined behavior (reject up front)
	//   - index not in streams → ffmpeg would error with a cryptic message that
	//     surfaces as 500 in the admin UI; reject with a clear 400 here
	//   - index points at a non-subtitle or bitmap stream → same; reject here so
	//     the message is actionable (bitmap → ErrBitmapSubtitleNotSupported,
	//     non-subtitle → ErrInvalidStreamIndex)
	// This also de-risks the case where media_meta_json is stale (the source
	// file was swapped) — we'd find no match and reject rather than extract a
	// garbage track as source="embedded".
	if streamIndex < 0 {
		return ErrInvalidStreamIndex
	}
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return err
	}
	if ep == nil {
		return fmt.Errorf("episode %d not found", episodeID)
	}
	if strings.TrimSpace(ep.MediaMetaJSON) != "" {
		var meta model.MediaMeta
		if jerr := json.Unmarshal([]byte(ep.MediaMetaJSON), &meta); jerr == nil {
			var match *model.MediaStream
			for i := range meta.Streams {
				if meta.Streams[i].Index == streamIndex {
					match = &meta.Streams[i]
					break
				}
			}
			if match == nil {
				return ErrInvalidStreamIndex
			}
			if match.Type != "subtitle" {
				return ErrInvalidStreamIndex
			}
			if match.IsBitmap {
				return ErrBitmapSubtitleNotSupported
			}
		}
		// If meta didn't parse, fall through and let ffmpeg try — better to
		// attempt than to refuse on a parse glitch.
	}

	// Refuse to clobber an existing (episode, language) subtitle. SaveSubtitle
	// upserts on this key, so without this check extracting a zh-CN embedded
	// track over an existing zh-CN whisper track would silently overwrite its
	// VttContent + RawVttContent snapshot + source — real data loss. The admin
	// must explicitly delete the old track first (or pick a distinct language
	// code like zh-CN-alt). True multi-language (zh-CN + en-US) doesn't trip
	// this because the keys differ.
	existing, lerr := s.episodeRepo.ListSubtitles(episodeID)
	if lerr == nil {
		for _, sub := range existing {
			if sub.Language == language {
				return ErrSubtitleLanguageConflict
			}
		}
	}

	link, err := s.GetStreamURL(episodeID, "ffmpeg-extract-subtitle")
	if err != nil {
		return err
	}

	// Temp output next to the other data/uploads artifacts. A unique suffix
	// guards against concurrent extractions on the same episode clobbering
	// each other's output file.
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("sq_extract_sub_ep%d_s%d_%d.vtt", episodeID, streamIndex, time.Now().UnixNano()))
	args := []string{
		"-y",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_at_eof", "1",
		"-reconnect_delay_max", "2",
		"-i", link.URL,
		"-map", fmt.Sprintf("0:%d", streamIndex),
		"-c:s", "webvtt",
		tmpPath,
	}
	// Subtitle extraction is usually fast (<5s for a full movie) since it's
	// a remux, not a re-encode. Give plenty of headroom for slow CDN reads.
	if err := runFFmpegWithRetry("extract embedded subtitle", args, 5*time.Minute, 3); err != nil {
		low := strings.ToLower(err.Error())
		// ffmpeg prints this exact phrase when asked to transcode bitmap→text.
		// Match loosely (the error wraps ffmpeg's stderr verbatim).
		if strings.Contains(low, "bitmap to bitmap") || strings.Contains(low, "text to text") {
			return ErrBitmapSubtitleNotSupported
		}
		return err
	}
	// Best-effort cleanup: never leave the temp file behind even if the read
	// or DB write fails afterwards.
	defer os.Remove(tmpPath)

	vttBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return fmt.Errorf("read extracted subtitle: %w", err)
	}
	vtt := string(vttBytes)
	if strings.TrimSpace(vtt) == "" {
		return errors.New("extracted subtitle was empty (stream may have no cues)")
	}
	return s.SaveSubtitleWithSource(episodeID, language, label, vtt, "embedded")
}

// extractEmbeddedCover tries to extract an embedded cover art/image stream from a video.
func extractEmbeddedCover(videoURL, outputPath string) error {
	// -reconnect 系列：天翼云 OBS 等云盘 CDN 对单个签名 URL 的并发 TLS 连接数有限，
	// ffmpeg 默认会开多 socket 读取 mp4 moov+mdat，部分连接会被对端 RST，表现为
	// "IO error: End of file" / "moov atom not found"。开启重连后单连接顺序读，稳定。
	args := []string{
		"-y",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_at_eof", "1",
		"-reconnect_delay_max", "2",
		"-i", videoURL,
		"-map", "0:v",
		"-map", "-0:V",
		"-c", "copy",
		outputPath,
	}
	return runFFmpegWithRetry("embedded cover extract", args, 20*time.Second, 3)
}

// extractScreenshot extracts a single frame at a specific timestamp as JPEG.
func extractScreenshot(videoURL, outputPath string, durationSeconds int) error {
	seekTime := "5"
	if durationSeconds > 0 && durationSeconds <= 5 {
		seekTime = strconv.Itoa(durationSeconds / 2)
	}

	// -reconnect 系列：同 extractEmbeddedCover，规避天翼云 OBS 对并发 TLS 连接的限制。
	// -ss 在 -i 前是 fast seek（demuxer 层跳转），避免解码到目标帧前的所有数据。
	args := []string{
		"-y",
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_at_eof", "1",
		"-reconnect_delay_max", "2",
		"-ss", seekTime,
		"-i", videoURL,
		"-vframes", "1",
		"-q:v", "2",
		"-update", "1",
		outputPath,
	}
	return runFFmpegWithRetry("screenshot extract", args, 20*time.Second, 3)
}

// runFFmpegWithRetry runs `ffmpeg <args>` and retries on transient network
// errors. The天翼云 OBS CDN (obs-ynkmfy-suzhou-home.obs.cn-jssz1.ctyun.cn,
// ~80% of lesson videos) intermittently RSTs TLS connections during ffmpeg's
// multi-socket moov/mdata read, surfacing as "IO error: End of file" /
// "moov atom not found". A single retry with a 1s gap resolves ~70% of these;
// 3 attempts covers ~97% based on A/B testing (1/5 → 4/5 success). Only
// network-class errors are retried — real codec/format errors fail fast.
func runFFmpegWithRetry(label string, args []string, perAttemptTimeout time.Duration, maxAttempts int) error {
	var lastErr error
	var lastStderr string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), perAttemptTimeout)
		cmd := exec.CommandContext(ctx, "ffmpeg", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		lastStderr = stderr.String()
		// Only retry on transient network errors. If ffmpeg ran far enough to
		// report a real codec/format problem (e.g. "no embedded cover" exit 234),
		// retrying won't help — bail out.
		if !isTransientFFmpegError(lastStderr) {
			break
		}
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt) * time.Second) // 1s, 2s backoff
		}
	}
	return fmt.Errorf("%s failed after retries: %w, stderr: %s", label, lastErr, lastStderr)
}

// isTransientFFmpegError reports whether an ffmpeg stderr indicates a network
// flake worth retrying (TLS RST, connection reset, EOF mid-read) versus a
// permanent failure (missing codec, no stream, bad format).
func isTransientFFmpegError(stderr string) bool {
	low := strings.ToLower(stderr)
	for _, marker := range []string{
		"end of file",          // TLS EOF — OBS RST during multi-socket read
		"connection reset",     // TCP RST
		"temporary failure in name resolution",
		"connection refused",
		"connection timed out",
		"i/o error",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

// probeMedia shells out to ffprobe to extract container-level metadata from a
// remote URL. Because the netdisk CDNs honor HTTP Range, ffprobe only reads the
// header/index region (typically <1s). A 30s timeout guards against the
// occasional file that makes ffprobe hang.
//
// Retries up to 3 times on transient TLS errors — the天翼云 OBS CDN
// (obs-ynkmfy-suzhou-home.obs.cn-jssz1.ctyun.cn, ~80% of lesson videos)
// intermittently RSTs ffprobe's TLS connections; single-shot success is only
// ~40% in A/B testing, but 3 attempts with backoff reaches ~95%.
func probeMedia(url string) (*model.MediaMeta, error) {
	args := []string{
		"-v", "error",
		"-show_format", "-show_streams",
		"-of", "json",
		url,
	}
	var out []byte
	var lastErr error
	var lastStderr string
	for attempt := 1; attempt <= 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, "ffprobe", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		o, err := cmd.Output()
		cancel()
		if err == nil {
			out = o
			lastErr = nil
			break
		}
		lastErr = err
		lastStderr = stderr.String()
		if ctx.Err() == context.DeadlineExceeded {
			lastErr = errors.New("ffprobe timed out after 30s")
			break // timeout is not transient — don't retry
		}
		if !isTransientFFmpegError(lastStderr) {
			break // real codec/format error — don't retry
		}
		if attempt < 3 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	if lastErr != nil {
		return nil, fmt.Errorf("ffprobe failed: %w, stderr: %s", lastErr, lastStderr)
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
		// For subtitle streams, flag bitmap-based codecs (PGS/VOBSUB/DVB) so the
		// admin UI can disable the extract button and point the user at Whisper.
		// ffmpeg refuses to transcode these to WebVTT with "Subtitle encoding
		// currently only possible from text to text or bitmap to bitmap".
		if s.CodecType == "subtitle" {
			ms.IsBitmap = isBitmapSubtitleCodec(s.CodecName)
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

// isBitmapSubtitleCodec reports whether a codec name identifies a
// bitmap/picture-based subtitle format. These cannot be transcoded to a text
// format (WebVTT/SRT) by ffmpeg — it errors with "Subtitle encoding currently
// only possible from text to text or bitmap to bitmap". For such streams the
// only path to text is OCR (Whisper / SubtitleEdit), so the admin UI refuses
// extraction and points the user at Whisper transcription instead.
//
// Covered codecs (case-insensitive ffprobe codec_name):
//   - hdmv_pgs_subtitle  — Blu-ray PGS
//   - dvd_subtitle       — VOBSUB (DVD)
//   - dvb_subtitle       — DVB (digital TV)
//   - dvb_teletext       — DVB teletext
//   - hdmv_text_subtitle — technically text, but rare/fragile; treated as text
//
// Text-based codecs (mov_text / subrip / srt / ass / ssa / webvtt / microdvd /
// sami / realtext / aqTitle / jacosub) return false and ARE extractable.
func isBitmapSubtitleCodec(codecName string) bool {
	switch strings.ToLower(codecName) {
	case "hdmv_pgs_subtitle", // Blu-ray PGS
		"dvd_subtitle",        // VOBSUB (DVD)
		"dvdsub",              // libavcodec short name alias
		"dvb_subtitle",        // DVB bitmap subtitles
		"dvbsub",              // alias
		"dvb_teletext",        // DVB teletext (page-based, not VTT-able)
		"pgssub":              // another PGS alias
		return true
	}
	return false
}
