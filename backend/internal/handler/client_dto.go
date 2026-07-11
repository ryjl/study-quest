package handler

// Client DTOs for /api/v1/* (Flutter PAD/TV client).
//
// The GORM models in internal/model have no json tags, so emitting them
// directly would use GORM's default PascalCase field names. The Flutter client
// expects that PascalCase shape (and additionally tolerates snake_case via
// dual-key `json['ID'] ?? json['id']` reads), so these DTOs PRESERVE the
// existing PascalCase JSON keys — this is the frozen client contract, do not
// change casing on existing fields.
//
// Why typed structs instead of inline gin.H maps: the old code built client
// responses as anonymous gin.H maps scattered across handlers (no field
// listing at the type level, easy to typo a key, and the episode shape was
// duplicated field-by-field). Typing them here gives one source of truth per
// shape and a compile-time field list, without changing the wire format.
//
// Conventions:
//   • Keys are PascalCase (matching the shipped Flutter client).
//   • Add fields only — never rename or retype an existing key (contract).

import (
	"time"

	"studyquest/backend/internal/model"
)

// clientCourseDTO is the course-listing shape the Flutter client parses.
// `subject` is the subject KEY string (not the nested GORM relation), and the
// Unlock* fields carry the drip-schedule summary (zero values = "no schedule").
type clientCourseDTO struct {
	ID             uint   `json:"ID"`
	Title          string `json:"Title"`
	Grade          string `json:"Grade"`
	Subject        string `json:"Subject"` // subject key, e.g. "math"
	CoverURL       string `json:"CoverURL"`
	Tags           string `json:"Tags"`     // comma-joined labels (legacy)
	TagsList       []string `json:"TagsList"` // tag labels in sort order
	TagIDs         []uint   `json:"TagIDs"`   // tag ids (for ID-based filtering)
	GradeDisplay   string `json:"GradeDisplay"`
	AttachmentJSON string `json:"AttachmentJSON"`
	// Unlock summary — populated for student/teen roles only (admin/parent see
	// everything, so the drip state is meaningless for them). Zero values
	// (UnlockStrategy="" / UnlockedCount=0) signal "no drip schedule" to the
	// client, which then hides the badge.
	UnlockStrategy      string `json:"UnlockStrategy"`
	UnlockStrategyLabel string `json:"UnlockStrategyLabel"`
	UnlockedCount       int    `json:"UnlockedCount"`
	EpisodeTotal        int    `json:"EpisodeTotal"`
	NextUnlockAt        string `json:"NextUnlockAt"`
	CreatedAt           string `json:"CreatedAt"`
	UpdatedAt           string `json:"UpdatedAt"`
}

// clientEpisodeDTO is the per-episode shape on the course-detail endpoint. The
// `locked` field (lowercase, intentional) signals the drip-schedule visibility
// for the requesting user — the only client-facing field NOT mirrored from the
// model.
type clientEpisodeDTO struct {
	ID                  uint    `json:"ID"`
	CourseID            uint    `json:"CourseID"`
	ChapterID           uint    `json:"ChapterID"`
	SortOrder           int     `json:"SortOrder"`
	Title               string  `json:"Title"`
	VideoRelativePath   string  `json:"VideoRelativePath"`
	CoverURL            string  `json:"CoverURL"`
	AttachmentJSON      string  `json:"AttachmentJSON"`
	FileHash            string  `json:"FileHash"`
	OriginalRelativePath string  `json:"OriginalRelativePath"`
	FileSize            *int64  `json:"FileSize"`
	DurationSeconds     *int    `json:"DurationSeconds"`
	MediaMetaJSON       string     `json:"MediaMetaJSON"`
	CreatedAt           time.Time  `json:"CreatedAt"`
	UpdatedAt           time.Time  `json:"UpdatedAt"`
	Locked              bool       `json:"locked"`
}

// toClientEpisodeDTO projects a model.Episode into the client shape. `locked`
// is the caller's per-user visibility flag (from the unlock resolver).
func toClientEpisodeDTO(ep model.Episode, locked bool) clientEpisodeDTO {
	return clientEpisodeDTO{
		ID:                   ep.ID,
		CourseID:             ep.CourseID,
		ChapterID:            ep.ChapterID,
		SortOrder:            ep.SortOrder,
		Title:                ep.Title,
		VideoRelativePath:    ep.VideoRelativePath,
		CoverURL:             ep.CoverURL,
		AttachmentJSON:       ep.AttachmentJSON,
		FileHash:             ep.FileHash,
		OriginalRelativePath: ep.OriginalRelativePath,
		FileSize:             ep.FileSize,
		DurationSeconds:      ep.DurationSeconds,
		MediaMetaJSON:        ep.MediaMetaJSON,
		CreatedAt:            ep.CreatedAt,
		UpdatedAt:            ep.UpdatedAt,
		Locked:               locked,
	}
}
