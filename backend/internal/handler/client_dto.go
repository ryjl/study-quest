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
	Grade          string `json:"Grade"`       // comma-joined grade keys (e.g. "3,4,5")
	Subject        string `json:"Subject"` // subject key, e.g. "math"
	ContentType    string `json:"ContentType"` // learning | entertainment
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
	OriginalRelativePath string  `json:"OriginalRelativePath"`
	FileSize            *int64  `json:"FileSize"`
	DurationSeconds     *int    `json:"DurationSeconds"`
	MediaMetaJSON       string     `json:"MediaMetaJSON"`
	CreatedAt           time.Time  `json:"CreatedAt"`
	UpdatedAt           time.Time  `json:"UpdatedAt"`
	Locked              bool       `json:"locked"`
	// 三个 add-only 字段(契约:PascalCase,与既有字段一致,只增不改):
	//   - AISummaryEnabled/AIQuizEnabled:课程级 AI 开关回显,客户端据此决定是否
	//     展示"AI 总结/练习"入口卡片。来自所属课程,同课程内所有 episode 相同。
	//   - HasSubtitle:该 episode 是否有字幕。客户端据此决定是否提示"字幕就绪"
	//     /能否播放(AI 之外,字幕本身也是播放体验的一部分)。
	AISummaryEnabled    bool       `json:"AISummaryEnabled"`
	AIQuizEnabled       bool       `json:"AIQuizEnabled"`
	HasSubtitle         bool       `json:"HasSubtitle"`
}

// toClientEpisodeDTO projects a model.Episode into the client shape. `locked`
// is the caller's per-user visibility flag (from the unlock resolver).
// aiSummaryEnabled/aiQuizEnabled 来自所属课程,hasSubtitle 来自批量查询结果
// (调用方一次性 CountSubtitlesByEpisodes,避免每个 episode 单独查库)。
func toClientEpisodeDTO(ep model.Episode, locked, aiSummaryEnabled, aiQuizEnabled, hasSubtitle bool) clientEpisodeDTO {
	var chapterID uint
	if ep.ChapterID != nil {
		chapterID = *ep.ChapterID
	}
	return clientEpisodeDTO{
		ID:                   ep.ID,
		CourseID:             ep.CourseID,
		ChapterID:            chapterID,
		SortOrder:            ep.SortOrder,
		Title:                ep.Title,
		VideoRelativePath:    ep.VideoRelativePath,
		CoverURL:             ep.CoverURL,
		AttachmentJSON:       ep.AttachmentJSON,
		OriginalRelativePath: ep.OriginalRelativePath,
		FileSize:             ep.FileSize,
		DurationSeconds:      ep.DurationSeconds,
		MediaMetaJSON:        ep.MediaMetaJSON,
		CreatedAt:            ep.CreatedAt,
		UpdatedAt:            ep.UpdatedAt,
		Locked:               locked,
		AISummaryEnabled:     aiSummaryEnabled,
		AIQuizEnabled:        aiQuizEnabled,
		HasSubtitle:          hasSubtitle,
	}
}

// ── Reading Room DTOs (PascalCase, matching the client contract) ──

type clientReadingSeriesDTO struct {
	ID           uint     `json:"ID"`
	Title        string   `json:"Title"`
	Description  string   `json:"Description"`
	Grade        string   `json:"Grade"`
	Subject      string   `json:"Subject"`
	CoverURL     string   `json:"CoverURL"`
	Tags         string   `json:"Tags"`
	TagsList     []string `json:"TagsList"`
	TagIDs       []uint   `json:"TagIDs"`
	GradeDisplay string   `json:"GradeDisplay"`
	SortOrder    int      `json:"SortOrder"`
	BookCount    int64    `json:"BookCount"`
	ArticleCount int64    `json:"ArticleCount"`
}

type clientReadingBookDTO struct {
	ID        uint   `json:"ID"`
	SeriesID  uint   `json:"SeriesID"`
	SortOrder int    `json:"SortOrder"`
	Title     string `json:"Title"`
	PageCount *int   `json:"PageCount"`
	CoverURL  string `json:"CoverURL"`
	Grade     string `json:"Grade"`
	Subject   string `json:"Subject"`
}

type clientReadingArticleDTO struct {
	ID               uint     `json:"ID"`
	SeriesID         uint     `json:"SeriesID"`
	SortOrder        int      `json:"SortOrder"`
	Title            string   `json:"Title"`
	SourceURL        string   `json:"SourceURL"`
	WhitelistDomains []string `json:"WhitelistDomains"`
	CoverURL         string   `json:"CoverURL"`
	Grade            string   `json:"Grade"`
	Subject          string   `json:"Subject"`
}

// clientReadingRoomDTO is the aggregated shelf payload. Books/Articles here are
// the standalone (散本/散文) items only; series-internal items are reachable via
// the GetSeries endpoint.
type clientReadingRoomDTO struct {
	Series   []clientReadingSeriesDTO   `json:"Series"`
	Books    []clientReadingBookDTO     `json:"Books"`
	Articles []clientReadingArticleDTO  `json:"Articles"`
}
