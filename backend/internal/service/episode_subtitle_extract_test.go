package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
	"studyquest/backend/internal/media"
)

// TestExtractEmbeddedSubtitle_RejectsInvalidStreamIndex covers the W2 guard:
// negative / missing / non-subtitle / bitmap stream indices must be rejected
// BEFORE ffmpeg is invoked, with a clear sentinel error rather than a cryptic
// 500 from ffmpeg. We don't need a real video — the validation happens against
// media_meta_json only, so we craft a meta with one text subtitle stream + one
// bitmap stream + one video stream and assert the right rejection per case.
func TestExtractEmbeddedSubtitle_RejectsInvalidStreamIndex(t *testing.T) {
	db := testutil.NewFileDB(t)
	episodeRepo := repository.NewEpisodeRepository(db)
	svc := NewEpisodeService(episodeRepo, NewStorageProviderResolver(repository.NewStorageSourceRepository(db)))

	// One course + episode so we have a row to attach media_meta_json to.
	course := model.Course{Title: "t", ContentType: model.ContentLearning}
	db.Create(&course)
	ep := model.Episode{CourseID: course.ID, Title: "e", VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(&ep)

	// meta: streams 0=video, 1=text subtitle (subrip), 2=bitmap subtitle (PGS).
	meta := model.MediaMeta{
		Streams: []model.MediaStream{
			{Index: 0, Type: "video", Codec: "h264"},
			{Index: 1, Type: "subtitle", Codec: "subrip", IsBitmap: false},
			{Index: 2, Type: "subtitle", Codec: "hdmv_pgs_subtitle", IsBitmap: true},
		},
	}
	buf, _ := json.Marshal(meta)
	db.Model(&model.Episode{}).Where("id = ?", ep.ID).Update("media_meta_json", string(buf))

	cases := []struct {
		name    string
		idx     int
		wantErr error
	}{
		{"negative index rejected", -1, ErrInvalidStreamIndex},
		{"missing index rejected", 99, ErrInvalidStreamIndex},
		{"non-subtitle stream rejected", 0, ErrInvalidStreamIndex}, // video stream
		{"bitmap subtitle surfaces bitmap error", 2, ErrBitmapSubtitleNotSupported},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// We can't actually call ffmpeg here (no real video URL). But the
			// validation runs BEFORE GetStreamURL — so we expect the sentinel
			// error to come back before any network/storage path is touched.
			// (GetStreamURL would fail on no storage source; if we see that
			// instead, the validation didn't fire.)
			err := svc.ExtractEmbeddedSubtitle(ep.ID, c.idx, "zh-CN", "中文")
			if err == nil {
				t.Fatalf("expected %v, got nil (validation didn't fire)", c.wantErr)
			}
			if !errors.Is(err, c.wantErr) {
				t.Errorf("err = %v, want %v", err, c.wantErr)
			}
		})
	}
}

// TestExtractEmbeddedSubtitle_RejectsLanguageConflict covers the C3 guard:
// extracting an embedded track for a language that already has a subtitle row
// would silently clobber it via SaveSubtitle's upsert. We refuse so the admin
// has to explicitly delete the old track first (real multi-language uses
// distinct language codes, which don't trip this).
func TestExtractEmbeddedSubtitle_RejectsLanguageConflict(t *testing.T) {
	db := testutil.NewFileDB(t)
	episodeRepo := repository.NewEpisodeRepository(db)
	svc := NewEpisodeService(episodeRepo, NewStorageProviderResolver(repository.NewStorageSourceRepository(db)))

	course := model.Course{Title: "t", ContentType: model.ContentLearning}
	db.Create(&course)
	ep := model.Episode{CourseID: course.ID, Title: "e", VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(&ep)

	// Pre-existing zh-CN whisper subtitle (the track we must NOT clobber).
	if err := svc.SaveSubtitleWithSource(ep.ID, "zh-CN", "中文", "1\n00:00:01,000 --> 00:00:02,000\n原", "whisper"); err != nil {
		t.Fatalf("seed whisper subtitle: %v", err)
	}

	// Build meta with a valid text subtitle stream so we get past the
	// streamIndex validation and reach the language-conflict check.
	meta := model.MediaMeta{
		Streams: []model.MediaStream{
			{Index: 0, Type: "video", Codec: "h264"},
			{Index: 1, Type: "subtitle", Codec: "subrip", IsBitmap: false},
		},
	}
	buf, _ := json.Marshal(meta)
	db.Model(&model.Episode{}).Where("id = ?", ep.ID).Update("media_meta_json", string(buf))

	// Same language → must refuse before touching ffmpeg.
	err := svc.ExtractEmbeddedSubtitle(ep.ID, 1, "zh-CN", "中文")
	if !errors.Is(err, ErrSubtitleLanguageConflict) {
		t.Fatalf("err = %v, want ErrSubtitleLanguageConflict", err)
	}

	// A different language code passes the conflict check (it'll fail later at
	// GetStreamURL/ffmpeg since we have no storage source, but that's a
	// different error — NOT the conflict sentinel).
	err = svc.ExtractEmbeddedSubtitle(ep.ID, 1, "en-US", "English")
	if errors.Is(err, ErrSubtitleLanguageConflict) {
		t.Errorf("different language should NOT trip conflict check")
	}
}

// TestSaveSubtitle_PreservesIsPrimaryOnUpdate covers the C2 guard: a re-save
// via SaveSubtitleWithSource (which doesn't set IsPrimary) must NOT demote an
// existing primary track. Without the "promote but never demote" guard in
// episodeRepo.SaveSubtitle, re-transcribing an episode would silently clear
// is_primary and leave the AI chain with no readable subtitle.
func TestSaveSubtitle_PreservesIsPrimaryOnUpdate(t *testing.T) {
	db := testutil.NewFileDB(t)
	episodeRepo := repository.NewEpisodeRepository(db)
	svc := NewEpisodeService(episodeRepo, NewStorageProviderResolver(repository.NewStorageSourceRepository(db)))

	course := model.Course{Title: "t", ContentType: model.ContentLearning}
	db.Create(&course)
	ep := model.Episode{CourseID: course.ID, Title: "e", VideoRelativePath: "/x.mp4", SortOrder: 1}
	db.Create(&ep)

	// First save: auto-promoted to primary (first subtitle for this episode).
	if err := svc.SaveSubtitleWithSource(ep.ID, "zh-CN", "中文", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n原", "whisper"); err != nil {
		t.Fatal(err)
	}
	sub, _ := episodeRepo.GetSubtitle(ep.ID)
	if !sub.IsPrimary {
		t.Fatalf("first subtitle should be auto-primary, got is_primary=false")
	}
	originalID := sub.ID

	// Re-save same (episode, language) via the "fresh material" path (simulates
	// re-transcribe). SaveSubtitleWithSource doesn't set IsPrimary → zero value.
	if err := svc.SaveSubtitleWithSource(ep.ID, "zh-CN", "中文", "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n新", "whisper"); err != nil {
		t.Fatal(err)
	}

	// Reload and verify is_primary survived.
	sub2, _ := episodeRepo.GetSubtitle(ep.ID)
	if !sub2.IsPrimary {
		t.Errorf("re-save demoted is_primary to false (C2 regression): track %d lost primary", originalID)
	}
}

// TestIsBitmapSubtitleCodec covers the codec-name → bitmap classification used
// by probeMedia (to flag streams in media_meta_json) and by ExtractEmbeddedSubtitle
// (to return ErrBitmapSubtitleNotSupported). ffmpeg end-to-end extraction is
// intentionally NOT tested here — it needs a real ffmpeg + video file and is
// flaky in CI; only the pure classification logic is covered.
func TestIsBitmapSubtitleCodec(t *testing.T) {
	t.Run("bitmap codecs", func(t *testing.T) {
		// Every codec ffmpeg reports as picture-based: transcoding any of these
		// to WebVTT errors with "Subtitle encoding currently only possible from
		// text to text or bitmap to bitmap".
		bitmap := []string{
			"hdmv_pgs_subtitle", // Blu-ray PGS (most common in study-quest's MP4s)
			"dvd_subtitle",      // VOBSUB (DVD rips)
			"dvb_subtitle",      // DVB bitmap (digital TV captures)
			"dvb_teletext",      // DVB teletext
		}
		for _, c := range bitmap {
			if !media.IsBitmapSubtitleCodec(c) {
				t.Errorf("expected %q to be bitmap (un-extractable)", c)
			}
		}
	})

	t.Run("text codecs are extractable", func(t *testing.T) {
		// Text-based subtitle codecs that ffmpeg CAN transcode to WebVTT.
		// These must return false so the admin UI shows the 提取 button.
		text := []string{
			"subrip",     // SRT — the most common embedded text subtitle
			"srt",        // alias
			"mov_text",   // MP4 tx3g (the default for .mp4 soft subs)
			"ass",        // Advanced SubStation Alpha (styled text)
			"ssa",        // SubStation Alpha
			"webvtt",     // already WebVTT
			"microdvd",   // MicroDVD {.}{350}{400} frame format
			"jacosub",    // JACOsub
			"sami",       // SAMI (HTML)
			"realtext",   // RealText
		}
		for _, c := range text {
			if media.IsBitmapSubtitleCodec(c) {
				t.Errorf("expected %q to be text (extractable), got bitmap", c)
			}
		}
	})

	t.Run("case-insensitive", func(t *testing.T) {
		// ffprobe lowercases codec_name, but guard against mixed-case input
		// anyway (some forks / wrappers don't normalize).
		for _, c := range []string{"HDMV_PGS_SUBTITLE", "Dvd_Subtitle", "DVB_SUBTITLE"} {
			if !media.IsBitmapSubtitleCodec(c) {
				t.Errorf("expected case-insensitive match for %q to be bitmap", c)
			}
		}
	})

	t.Run("empty and unknown codecs default to text", func(t *testing.T) {
		// An unknown / empty codec name shouldn't block extraction — worst case
		// ffmpeg errors at runtime and the user sees a normal failure, which is
		// better than pre-emptively disabling the button for a codec we just
		// haven't enumerated yet.
		for _, c := range []string{"", "unknown_codec", "some_future_text_codec"} {
			if media.IsBitmapSubtitleCodec(c) {
				t.Errorf("expected %q to default to text, got bitmap", c)
			}
		}
	})
}

// TestExtractEmbeddedSubtitleBitmapErrorString sanity-checks that the sentinel
// error message carries the actionable hint the handler surfaces to the admin.
// This is a string-level assertion (not a behavioral test) — it guards against
// someone silently rewording the error and dropping the "Whisper" guidance.
func TestExtractEmbeddedSubtitleBitmapErrorString(t *testing.T) {
	msg := ErrBitmapSubtitleNotSupported.Error()
	if !strings.Contains(strings.ToLower(msg), "whisper") {
		t.Errorf("ErrBitmapSubtitleNotSupported message must mention Whisper, got: %s", msg)
	}
}
