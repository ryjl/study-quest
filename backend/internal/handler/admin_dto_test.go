package handler

import (
	"encoding/json"
	"testing"
	"time"

	"studyquest/backend/internal/model"
)

// toEpisodeDTO / toChapterDTO / toSubtitleDTO / toLedgerDTO are pure functions
// (no repo deps), so we can test their snake_case projection directly.

func TestToEpisodeDTO(t *testing.T) {
	size := int64(5242880)
	dur := 3661
	created := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	ep := model.Episode{
		ID:                7,
		CourseID:          3,
		ChapterID:         2,
		SortOrder:         4,
		Title:             "测试课时",
		VideoRelativePath: "/x/y.mp4",
		CoverURL:          "/uploads/c.jpg",
		AttachmentJSON:    `[]`,
		FileHash:          "deadbeef",
		OriginalRelativePath: "/x/orig/y.mp4",
		FileSize:          &size,
		DurationSeconds:   &dur,
		MediaMetaJSON:     `{"duration_seconds":3661}`,
		CreatedAt:         created,
		UpdatedAt:         created,
	}

	dto := toEpisodeDTO(ep)

	// JSON field names must be snake_case — verify by marshalling.
	raw, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}

	requiredKeys := []string{
		"id", "course_id", "chapter_id", "sort_order", "title",
		"video_relative_path", "cover_url", "attachment_json",
		"file_hash", "original_relative_path", "file_size",
		"duration_seconds", "media_meta_json", "created_at", "updated_at",
	}
	for _, k := range requiredKeys {
		if _, ok := parsed[k]; !ok {
			t.Errorf("episodeDTO missing snake_case key %q (have: %v)", k, keys(parsed))
		}
	}
	// And no PascalCase leakage.
	forbidden := []string{"ID", "CourseID", "VideoRelativePath", "DurationSeconds", "FileSize"}
	for _, k := range forbidden {
		if _, ok := parsed[k]; ok {
			t.Errorf("episodeDTO leaked PascalCase key %q", k)
		}
	}

	// Value checks on the nullable fields.
	if dto.FileSize == nil || *dto.FileSize != size {
		t.Errorf("FileSize = %v, want %d", dto.FileSize, size)
	}
	if dto.DurationSeconds == nil || *dto.DurationSeconds != dur {
		t.Errorf("DurationSeconds = %v, want %d", dto.DurationSeconds, dur)
	}
}

func TestToChapterDTO(t *testing.T) {
	ch := model.Chapter{
		ID: 1, CourseID: 2, Title: "章节", Description: "说明",
		CoverURL: "/c.jpg", AttachmentJSON: `[]`, SortOrder: 3,
	}
	dto := toChapterDTO(ch)
	raw, _ := json.Marshal(dto)
	var parsed map[string]json.RawMessage
	_ = json.Unmarshal(raw, &parsed)
	for _, k := range []string{"id", "course_id", "title", "description", "cover_url", "attachment_json", "sort_order"} {
		if _, ok := parsed[k]; !ok {
			t.Errorf("chapterDTO missing %q", k)
		}
	}
}

func TestFormatTimeZeroIsEmpty(t *testing.T) {
	if got := formatTime(time.Time{}); got != "" {
		t.Errorf("formatTime(zero) = %q, want empty", got)
	}
	// Non-zero should be RFC3339-parseable.
	got := formatTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Errorf("formatTime result %q not RFC3339: %v", got, err)
	}
}

func keys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
