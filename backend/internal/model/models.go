package model

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// Setting represents KV configuration parameters managed by Admin.
type Setting struct {
	Key         string    `gorm:"primaryKey"`
	Value       string    `gorm:"type:text"`
	Description string    `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// User represents a user (student or parent).
type User struct {
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	Nickname  string `gorm:"size:255;not null"`
	AvatarURL string `gorm:"size:1024"`
	PinHash   string `gorm:"size:255;not null"` // bcrypt hash of 4-6 digit PIN
	Role      string `gorm:"size:50;not null;default:'student'"` // student, teen, parent, admin
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserCourseAccess defines which courses a user can see/access.
type UserCourseAccess struct {
	UserID    uint      `gorm:"primaryKey"`
	CourseID  uint      `gorm:"primaryKey"`
	GrantedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

// Grade represents the grade enum.
type Grade string

const (
	Grade1         Grade = "1"
	Grade2         Grade = "2"
	Grade3         Grade = "3"
	Grade4         Grade = "4"
	Grade5         Grade = "5"
	Grade6         Grade = "6"
	Grade7         Grade = "7"
	Grade8         Grade = "8"
	Grade9         Grade = "9"
	GradeUniversal Grade = "universal"
)

// Valid checks if the grade matches one of the enum values or is a comma-separated list of them.
func (g Grade) Valid() bool {
	if g == "" {
		return false
	}
	parts := strings.Split(string(g), ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		switch Grade(p) {
		case Grade1, Grade2, Grade3, Grade4, Grade5, Grade6, Grade7, Grade8, Grade9, GradeUniversal:
			// valid
		default:
			return false
		}
	}
	return true
}

// Subject represents a user-editable course subject (科目), e.g. 语文/数学/英语.
// Stored as its own table so it can be renamed or deleted independently of
// courses. `Key` is the stable identifier referenced by badge rules; Label /
// Emoji / Color carry the display metadata the frontend needs.
//
// IsSystem marks rows seeded by SeedDefaultSubjects: they can be renamed or
// recolored but never deleted (so the catalog always retains the canonical
// core subjects). User-created rows have IsSystem=false and are freely
// deletable (subject to the course-FK guard).
type Subject struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Key       string    `gorm:"size:100;uniqueIndex;not null"` // stable identifier, e.g. "math"
	Label     string    `gorm:"size:100;not null"`             // display name, e.g. "数学"
	Emoji     string    `gorm:"size:32"`                       // e.g. "📐"
	Color     string    `gorm:"size:32"`                       // hex e.g. "#f59e0b"
	SortOrder int       `gorm:"default:0"`
	IsSystem  bool      `gorm:"default:false"` // true = seeded default, protected from deletion
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Tag represents a user-editable course tag (标签), e.g. 必修/思维训练/拓展.
// Stored as its own table with a many-to-many relation to courses, so a tag
// can be renamed/deleted independently and applied to many courses.
//
// IsSystem mirrors Subject: seeded defaults are protected from deletion but
// may be renamed/recolored.
type Tag struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Key       string    `gorm:"size:100;uniqueIndex;not null"` // stable identifier, e.g. "required"
	Label     string    `gorm:"size:100;not null"`             // display name, e.g. "必修"
	Color     string    `gorm:"size:32"`                       // hex e.g. "#ef4444"
	SortOrder int       `gorm:"default:0"`
	IsSystem  bool      `gorm:"default:false"` // true = seeded default, protected from deletion
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Course represents a multi-episode course.
type Course struct {
	ID             uint      `gorm:"primaryKey;autoIncrement"`
	Title          string    `gorm:"size:255;not null"`
	Grade          Grade     `gorm:"type:varchar(50);not null"`   // "1" to "9" or "universal" (or comma-separated)
	SubjectID      uint      `gorm:"not null;index"`              // FK → subjects.id (ON DELETE RESTRICT)
	Subject        Subject   `gorm:"foreignKey:SubjectID;constraint:OnDelete:RESTRICT"`
	CoverURL       string    `gorm:"size:1024"`
	Tags           []Tag     `gorm:"many2many:course_tags;constraint:OnDelete:CASCADE"`
	AttachmentJSON string    `gorm:"type:text"`          // JSON array of attachments
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// TagsList returns the display labels of the course's tags, in tag sort order.
// Kept for DTO back-compat (the /api/v1 contract emits a string array) and
// for any caller that still wants the legacy "comma string" via TagsJoined().
func (c Course) TagsList() []string {
	out := make([]string, 0, len(c.Tags))
	for _, t := range c.Tags {
		out = append(out, t.Label)
	}
	return out
}

// TagsJoined returns the comma-joined tag labels, matching the legacy
// Course.Tags string field shape that older Flutter clients still parse.
func (c Course) TagsJoined() string {
	return strings.Join(c.TagsList(), ",")
}

// Grades splits a comma-separated grade list into a slice.
func (c Course) Grades() []string {
	if c.Grade == "" {
		return []string{}
	}
	parts := strings.Split(string(c.Grade), ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
	}
	return parts
}

// GradeDisplay formats the course grade for display.
func (c Course) GradeDisplay() string {
	if c.Grade == "universal" {
		return "全学段通用"
	}
	parts := c.Grades()
	for i, p := range parts {
		if p == "universal" {
			parts[i] = "通用"
		} else {
			parts[i] = p + "年级"
		}
	}
	return strings.Join(parts, ", ")
}

// Chapter represents a chapter/module within a course.
type Chapter struct {
	ID             uint   `gorm:"primaryKey;autoIncrement"`
	CourseID       uint   `gorm:"index;not null"`
	Title          string `gorm:"size:255;not null"`
	Description    string `gorm:"type:text"`
	CoverURL       string `gorm:"size:1024"`
	AttachmentJSON string `gorm:"type:text"` // JSON array of attachments
	SortOrder      int    `gorm:"default:0"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Episode represents a specific episode in a course.
type Episode struct {
	ID                   uint   `gorm:"primaryKey;autoIncrement"`
	CourseID             uint   `gorm:"index:idx_course_sort;not null"`
	ChapterID            uint   `gorm:"index;not null;default:0"` // Belonging chapter, 0 means default/unassigned
	SortOrder            int    `gorm:"index:idx_course_sort;not null"`
	Title                string `gorm:"size:255;not null"`
	VideoRelativePath    string `gorm:"type:text;not null"`
	CoverURL             string `gorm:"size:1024"`
	AttachmentJSON       string `gorm:"type:text"` // JSON array of attachments
	FileHash             string `gorm:"size:255;index"` // SHA1/MD5 for disaster recovery matching
	OriginalRelativePath string `gorm:"type:text"` // Original multi-layer path to prevent name collision
	FileSize             *int64 // Nullable file size in bytes
	DurationSeconds      *int   // Nullable video duration in seconds
	MediaMetaJSON        string `gorm:"type:text"` // Serialized MediaMeta from ffprobe (codecs, resolution, streams, ...)
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// MediaMeta is the parsed ffprobe result persisted on an episode. It captures
// the container-level media information (duration, codecs, resolution,
// bit-rate, full stream list) so listings and the player can show real
// metadata without re-probing the netdisk on every view.
type MediaMeta struct {
	DurationSeconds int           `json:"duration_seconds"`
	FormatName      string        `json:"format_name"` // e.g. "mov,mp4,m4a,3gp,3g2,mj2"
	BitRate         int64         `json:"bit_rate"`    // total bit-rate in bps
	Width           int           `json:"width"`       // video width, 0 if no video
	Height          int           `json:"height"`      // video height, 0 if no video
	VideoCodec      string        `json:"video_codec"` // e.g. "h264", "hevc"
	Fps             string        `json:"fps"`         // e.g. "30/1"
	AudioCodec      string        `json:"audio_codec"` // e.g. "aac"
	AudioChannels   int           `json:"audio_channels"`
	Streams         []MediaStream `json:"streams"` // full stream list, kept for future use
}

// MediaStream is a single track inside the container (video / audio / subtitle).
type MediaStream struct {
	Index    int    `json:"index"`
	Type     string `json:"type"` // "video" | "audio" | "subtitle"
	Codec    string `json:"codec"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	BitRate  int64  `json:"bit_rate"`
	Channels int    `json:"channels"` // audio only
	Language string `json:"language"`
}

// Subtitle holds the raw SRT subtitle content.
type Subtitle struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	EpisodeID  uint   `gorm:"index:idx_episode_lang;not null"`
	Language   string `gorm:"size:50;index:idx_episode_lang;not null;default:'zh-CN'"` // e.g. zh-CN, en-US, bi
	Label      string `gorm:"size:100;not null;default:'中文'"` // User-facing label
	SrtContent string `gorm:"type:text;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AILessonContent holds AI pre-adventure and post-review questions.
type AILessonContent struct {
	EpisodeID         uint   `gorm:"primaryKey"`
	PreAdventureJSON  string `gorm:"type:text"` // JSON array of 3 exploration prompts
	PostReviewJSON     string `gorm:"type:text"` // JSON of summary + 3 multiple choice questions
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// UserPoint holds active point balance.
type UserPoint struct {
	UserID            uint `gorm:"primaryKey"`
	CurrentPoints     int  `gorm:"default:0"`
	TotalEarnedPoints int  `gorm:"default:0"`
	UpdatedAt         time.Time
}

// PointsLedger tracks points transaction history.
type PointsLedger struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	UserID       uint      `gorm:"index;not null"`
	ChangeAmount int       `gorm:"not null"`
	ReasonType   string    `gorm:"size:50;not null"` // 'system_watch', 'parent_grant', 'redeem_gift'
	Description  string    `gorm:"size:1024"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

// UserProgress tracks student's course playback progress.
type UserProgress struct {
	ID                  uint      `gorm:"primaryKey;autoIncrement"`
	UserID              uint      `gorm:"uniqueIndex:idx_user_episode;not null"`
	EpisodeID           uint      `gorm:"uniqueIndex:idx_user_episode;not null"`
	LastPositionSeconds int       `gorm:"default:0"`
	WatchSeconds        int       `gorm:"default:0"` // Accumulated playback seconds
	IsCompleted         int       `gorm:"default:0"` // 0 = false, 1 = true (when watch_seconds > 80% duration)
	UnlockedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Badge represents an achievement badge that can be earned by a student.
//
// Two evaluation modes:
//   - Single rule (legacy + simple badges): RuleType + RuleTarget + Threshold.
//   - Composite rule: RuleJSON holds a serialized CompositeRule tree, and the
//     top-level RuleType/Threshold are kept only for display/back-compat
//     (set to "composite" when RuleJSON is populated).
//
// IsSystem marks seeded defaults (protected from deletion, still editable).
type Badge struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	Code        string    `gorm:"size:100;uniqueIndex;not null"`
	Title       string    `gorm:"size:255;not null"`
	Description string    `gorm:"type:text"`
	IconName    string    `gorm:"size:255;not null"`
	RuleType    string    `gorm:"size:50;not null"` // watch_duration, consecutive_days, subject_count, night_owl_count, points_earned, distinct_subject_count, composite
	RuleTarget  string    `gorm:"size:100"`         // target e.g. "math" or empty
	Threshold   int       `gorm:"not null"`         // threshold to reach e.g. 100, 7, 5
	RuleJSON    string    `gorm:"type:text"`        // composite rule tree (empty = single rule)
	IsSystem    bool      `gorm:"default:false"`    // true = seeded default, protected from deletion
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// CompositeRule is the parsed shape of Badge.RuleJSON. Logic joins sub-rules
// with AND (all must pass) or OR (any passes). A leaf rule mirrors the
// single-rule fields. A rule with no SubRules is a leaf evaluated by Type.
type CompositeRule struct {
	Logic   string          `json:"logic"`              // "and" | "or"
	SubRules []CompositeRule `json:"rules,omitempty"`    // for group nodes
	Type     string          `json:"type,omitempty"`     // leaf: watch_duration | consecutive_days | ...
	Target   string          `json:"target,omitempty"`   // leaf: subject key for subject_count
	Threshold int           `json:"threshold,omitempty"` // leaf: threshold
}

// UserBadge stores which badges have been unlocked by which users.
type UserBadge struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UserID     uint      `gorm:"uniqueIndex:idx_user_badge;not null"`
	BadgeID    uint      `gorm:"uniqueIndex:idx_user_badge;not null"`
	UnlockedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

// Unlock strategy constants. A strategy decides how the "unlock water level"
// (how many of the course's episodes, in SortOrder, are visible) and the
// explicit allowlist combine for a (user, course) pair. See unlock_service.go
// for the resolution logic.
const (
	// StrategyAllOpen: every episode visible (default; backward compatible —
	// courses/assignments with no template behave this way).
	StrategyAllOpen = "all_open"
	// StrategyManual: only episode 1 is visible initially; each admin
	// "manual unlock" bumps the water level by 1. No automatic progression.
	StrategyManual = "manual"
	// StrategyInterval: water level = 1 + floor((now-granted_at)/interval) +
	// manual_count. Interval is stored in seconds (interval_seconds).
	StrategyInterval = "interval"
	// StrategyWeekly: water level = 1 + (number of configured weekly time
	// points elapsed between granted_at and now) + manual_count. Time points
	// are stored in weekly_times_json.
	StrategyWeekly = "weekly"
	// StrategySelected: water level is always 0; visibility comes entirely
	// from the admin-curated allowlist stored on the user override. Supports
	// arbitrary/cherry-picked episode selection.
	StrategySelected = "selected"
)

// WeeklyTime is one configured unlock time point for the weekly strategy.
// Weekday follows Go's time.Weekday convention: 0=Sunday ... 6=Saturday.
// Hour/Minute are interpreted in the business timezone (appclock).
type WeeklyTime struct {
	Weekday int `json:"weekday"` // 0 (Sunday) .. 6 (Saturday)
	Hour    int `json:"hour"`    // 0..23 (business timezone)
	Minute  int `json:"minute"`  // 0..59
}

// CourseUnlockTemplate is the course-level default unlock strategy. At most
// one row per course; absence means StrategyAllOpen (backward compatible).
// Templates do NOT carry an allowlist — the allowlist is per (user, course)
// on UserUnlockOverride, since "which exact episodes" is a per-student choice.
type CourseUnlockTemplate struct {
	CourseID        uint      `gorm:"primaryKey"`
	Strategy        string    `gorm:"size:20;not null;default:'all_open'"`
	IntervalSeconds int       `gorm:"default:0"` // StrategyInterval
	WeeklyTimesJSON string    `gorm:"type:text"` // StrategyWeekly: []WeeklyTime
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// UserUnlockOverride stores a per-(user, course) unlock configuration that
// overrides the course template. Absence means "inherit the template" (or
// AllOpen if there's no template either).
//
// AllowedEpisodeIDsJSON holds the admin-curated allowlist (JSON []uint of
// episode ids). It is the SOLE source of visibility under StrategySelected,
// and an ADDITIVE source (unioned with the water level) under the other
// strategies — so an admin can always hand-unlock a specific episode without
// disturbing the drip schedule.
type UserUnlockOverride struct {
	UserID                uint      `gorm:"primaryKey"`
	CourseID              uint      `gorm:"primaryKey"`
	Strategy              string    `gorm:"size:20;not null;default:'all_open'"`
	IntervalSeconds       int       `gorm:"default:0"`
	WeeklyTimesJSON       string    `gorm:"type:text"`
	ManualUnlockCount     int       `gorm:"default:0"` // bumps water level under manual/interval/weekly
	AllowedEpisodeIDsJSON string    `gorm:"type:text"` // JSON []uint allowlist
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// AutoMigrate runs GORM schema auto-migration for all tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Setting{},
		&User{},
		&UserCourseAccess{},
		&Subject{},
		&Tag{},
		&Course{},
		&Chapter{},
		&Episode{},
		&Subtitle{},
		&AILessonContent{},
		&UserPoint{},
		&PointsLedger{},
		&UserProgress{},
		&Badge{},
		&UserBadge{},
		&CourseUnlockTemplate{},
		&UserUnlockOverride{},
	)
}
