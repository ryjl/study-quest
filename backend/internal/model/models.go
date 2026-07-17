package model

import (
	"fmt"
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

// StorageSource is one netdisk backend configuration (alist or webdav). The
// admin configures N of these globally. Content (episode/book) points at one
// via SourceID; users never hold storage credentials directly. This is the
// sole source of storage connection config (the old global storage_* settings
// keys were removed once every deployment migrated).
//
// At most one row should have IsDefault=true; it is the selection used when an
// import does not specify a source and no other default is implied. The
// backfill_sources tool creates the default from the legacy settings.
type StorageSource struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Name      string    `gorm:"size:100;not null"`          // display name, e.g. "家长追剧盘"
	Type      string    `gorm:"size:20;not null"`           // "alist" | "webdav"
	URL       string    `gorm:"size:1024;not null"`
	Username  string    `gorm:"size:255"`
	Password  string    `gorm:"size:255"`
	Token     string    `gorm:"size:1024"`                  // alist only
	IsDefault bool      `gorm:"default:false"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserStorageSource is one row of a user's storage-source allow-list (防呆).
// The list is DEFAULT-DENY: a user may access a source if and only if it
// appears in their list. An EMPTY set means the user is allowed NOTHING (an
// admin must explicitly grant at least one source before the user can stream).
// The grant-time and access-time gates both enforce this via
// storage_source_repo.IsAllowed. Staff roles (admin/parent) bypass entirely.
type UserStorageSource struct {
	UserID    uint      `gorm:"primaryKey"`
	SourceID  uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	User      User          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Source    StorageSource `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE"`
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

// Role constants. Stored as strings on User.Role; these consts centralize the
// values so inline string comparisons can't drift. IsStaff() identifies roles
// that bypass student-level access gating (see all course/reading services).
const (
	RoleStudent = "student"
	RoleTeen    = "teen"
	RoleParent  = "parent"
	RoleAdmin   = "admin"
)

func IsStaffRole(role string) bool {
	return role == RoleAdmin || role == RoleParent
}

// Session represents a user's authenticated client session. One user may hold
// many concurrent sessions (one per device). The token is an opaque 32-byte
// hex string carried in the `Authorization: Bearer <token>` header. Sessions
// have a fixed TTL (no sliding renewal) and can be revoked individually by the
// admin (kick a single device) or in bulk (revoke all of a user's devices).
//
// DeviceName is the friendly OS-level device identifier sent by the client on
// login (e.g. "客厅 iPad"); UserAgent is the HTTP UA kept as a fallback so an
// older client that doesn't send DeviceName still shows something identifiable
// in the admin device list. Note is an admin-editable freeform label.
type Session struct {
	Token      string    `gorm:"primaryKey;size:64"` // 32-byte hex (crypto/rand)
	UserID     uint      `gorm:"index;not null"`
	DeviceName string    `gorm:"size:255"`           // client-supplied OS device name (primary id)
	UserAgent  string    `gorm:"type:text"`          // HTTP UA (fallback)
	Note       string    `gorm:"size:255"`           // admin-set device note
	CreatedAt  time.Time                             // first login
	LastSeenAt time.Time                             // updated on each successful Validate
	ExpiresAt  time.Time `gorm:"index"`              // fixed TTL, no sliding renewal
}

// PointsLedger reason-type constants. Stored as strings on
// PointsLedger.ReasonType; these consts keep the ledger values in sync with
// the code that writes them (previously drifted between docs and code).
const (
	ReasonSystemWatch   = "system_watch"    // completed a learning episode
	ReasonBadgeUnlocked = "badge_unlocked"  // badge tier cleared
	ReasonParentGrant   = "parent_grant"    // parent-awarded bonus (future)
)

// Badge rule-type constants. Stored as strings on Badge.RuleType.
const (
	RuleWatchDuration       = "watch_duration"
	RuleConsecutiveDays     = "consecutive_days"
	RuleSubjectCount        = "subject_count"
	RuleEpisodeCount        = "episode_completed_count"
	RulePointsEarned        = "points_earned"
	RuleDistinctSubject     = "distinct_subject_count"
	RuleCourseCompletion    = "course_completion"
	RuleWeeklyAllPresent    = "weekly_all_present"
	RuleComposite           = "composite"
)

// UserCourseAccess defines which courses a user can see/access. FK CASCADE on
// both axes ensures access rows are cleaned up automatically when a user or
// course is deleted (previously done manually in the service layer).
type UserCourseAccess struct {
	UserID    uint      `gorm:"primaryKey"`
	CourseID  uint      `gorm:"primaryKey"`
	GrantedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	User      User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Course    Course    `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
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

// Valid checks if the grade matches one of the enum values.
func (g Grade) Valid() bool {
	switch g {
	case Grade1, Grade2, Grade3, Grade4, Grade5, Grade6, Grade7, Grade8, Grade9, GradeUniversal:
		return true
	}
	return false
}

// ContentType distinguishes learning content (counts towards watch time,
// points, badges) from entertainment content (pure playback, no learning
// stats). Stored on Course so the progress service can branch at the choke
// point without touching the 11+ learning-stat queries.
type ContentType string

const (
	ContentLearning      ContentType = "learning"
	ContentEntertainment ContentType = "entertainment"
)

func (c ContentType) Valid() bool {
	return c == ContentLearning || c == ContentEntertainment
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

// Course represents a multi-episode course. A course's applicable grades live
// in the CourseGrade join table (one row per grade). ContentType separates
// learning courses (stats/points/badges) from entertainment (pure playback).
type Course struct {
	ID             uint      `gorm:"primaryKey;autoIncrement"`
	Title          string    `gorm:"size:255;not null"`
	SubjectID      uint      `gorm:"not null;index"`              // FK → subjects.id (ON DELETE RESTRICT)
	Subject        Subject   `gorm:"foreignKey:SubjectID;constraint:OnDelete:RESTRICT"`
	ContentType    ContentType `gorm:"size:20;not null;default:'learning'"` // learning | entertainment
	CoverURL       string    `gorm:"size:1024"`
	Tags           []Tag     `gorm:"many2many:course_tags;constraint:OnDelete:CASCADE"`
	Grades         []CourseGrade `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
	AttachmentJSON string    `gorm:"type:text"`          // JSON array of attachments
	// AIHint is an optional, admin-authored hint fed to the subtitle worker's
	// Whisper initial_prompt (and later the quiz agent): terminology, teacher
	// accent notes, the key theorem to listen for, etc. Empty by default. Free
	// text — kept short by the consumer (Whisper's prompt budget is ~244 tokens).
	AIHint string `gorm:"type:text"`
	// AISummaryEnabled controls whether the AI agent generates summaries for
	// this course's episodes. Off by default: AI is an opt-in add-on, not every
	// course needs it. The agent only processes episodes belonging to a course
	// with this (and/or AIQuizEnabled) on. When off, the course behaves exactly
	// as before — pure video viewing, no AI surface.
	AISummaryEnabled bool `gorm:"default:false"`
	// AIQuizEnabled controls whether the AI agent generates quizzes for this
	// course's episodes. Independent from AISummaryEnabled so an admin can have
	// summaries without quizzes (or vice versa) per course.
	AIQuizEnabled bool `gorm:"default:false"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// CourseGrade is one row of a course's applicable-grade set. A course with no
// rows matches no grade filter; GradeUniversal rows match any filter.
type CourseGrade struct {
	CourseID uint  `gorm:"primaryKey"`
	Grade    Grade `gorm:"primaryKey;type:varchar(20);not null"`
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

// GradeKeys returns the course's applicable grades as a string slice (loaded
// from the CourseGrade association). Callers must have preloaded Grades.
func (c Course) GradeKeys() []string {
	out := make([]string, 0, len(c.Grades))
	for _, g := range c.Grades {
		out = append(out, string(g.Grade))
	}
	return out
}

// GradeDisplay formats the course grade for display.
func (c Course) GradeDisplay() string {
	return courseGradeDisplay(c.Grades)
}

// courseGradeDisplay formats a grade set for the UI. Universal → "全学段通用".
func courseGradeDisplay(gs []CourseGrade) string {
	for _, g := range gs {
		if g.Grade == GradeUniversal {
			return "全学段通用"
		}
	}
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, string(g.Grade)+"年级")
	}
	return strings.Join(parts, ", ")
}

// Chapter represents a chapter/module within a course.
type Chapter struct {
	ID             uint   `gorm:"primaryKey;autoIncrement"`
	CourseID       uint   `gorm:"index;not null"`
	Course         Course `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
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
	ID                   uint      `gorm:"primaryKey;autoIncrement"`
	CourseID             uint      `gorm:"index:idx_course_sort;not null"`
	Course               Course    `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
	ChapterID            *uint     `gorm:"index"` // NULL = unassigned to any chapter
	Chapter              *Chapter  `gorm:"foreignKey:ChapterID;constraint:OnDelete:SET NULL"`
	SortOrder            int       `gorm:"index:idx_course_sort;not null"`
	Title                string    `gorm:"size:255;not null"`
	VideoRelativePath    string    `gorm:"type:text;not null"`
	// SourceID points at the StorageSource this episode was imported from.
	// Should always be set post-import; NULL means the episode can't stream
	// (no source bound). See StorageProviderResolver.
	SourceID             *uint     `gorm:"index"`
	CoverURL             string    `gorm:"size:1024"`
	AttachmentJSON       string    `gorm:"type:text"` // JSON array of attachments
	OriginalRelativePath string    `gorm:"type:text"` // Original multi-layer path to prevent name collision
	FileSize             *int64    // Nullable file size in bytes
	DurationSeconds      *int      // Nullable video duration in seconds
	MediaMetaJSON        string    `gorm:"type:text"` // Serialized MediaMeta from ffprobe (codecs, resolution, streams, ...)
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
	ID         uint    `gorm:"primaryKey;autoIncrement"`
	EpisodeID  uint    `gorm:"index:idx_episode_lang;not null"`
	Episode    Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE"`
	Language   string  `gorm:"size:50;index:idx_episode_lang;not null;default:'zh-CN'"` // e.g. zh-CN, en-US, bi
	Label      string  `gorm:"size:100;not null;default:'中文'"` // User-facing label
	SrtContent string  `gorm:"type:text;not null"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// SubtitleJob is one row in the subtitle-generation queue. The admin opts
// episodes into the queue (priority controls order); a Python whisper worker
// on a GPU box claims the highest-priority queued job, transcribes it, and
// posts the SRT back. The VPS only coordinates — it never runs whisper.
//
// The worker is a separate machine, so unlike the in-process ProbeWorker this
// queue is DB-backed and the claim is an atomic compare-and-swap (a single
// UPDATE ... WHERE id=(SELECT ... LIMIT 1)) so two workers can't grab the same
// job. A processing job whose claimed_at is older than the stale timeout is
// reaped back to queued by a background ticker (the desktop may have crashed
// or been powered off mid-transcription).
//
// De-duplication is enforced in the service layer (not a DB constraint) so a
// completed/failed history can accumulate: an episode may have many done/failed
// rows but at most one queued/processing at a time.
type SubtitleJob struct {
	ID          uint       `gorm:"primaryKey;autoIncrement"`
	EpisodeID   uint       `gorm:"index;not null"`
	Episode     Episode    `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE"`
	Status      string     `gorm:"size:20;not null;default:'queued';index"` // queued|processing|done|failed|skipped
	Priority    int        `gorm:"default:0"`                               // higher = claimed first
	Attempt     int        `gorm:"default:0"`                               // bumped on each ClaimNext
	ClaimedAt   *time.Time `gorm:"index"`                                   // last claim time; for stale reaping
	ClaimedBy   string     `gorm:"size:100"`                                // worker self-id (X-Worker-ID), for observability
	CompletedAt *time.Time
	Error       string `gorm:"type:text"`
	Language    string `gorm:"size:50;default:'zh-CN'"` // target subtitle language
	// Progress is the worker-reported transcription ratio in [0.0, 1.0], or nil
	// when none has been reported (queued, or a worker not yet past the audio
	// extraction step). Updated alongside claimed_at by the heartbeat. Cleared
	// on terminal transitions so a requeued job doesn't show a stale percentage.
	Progress    *float64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Subtitle job status constants. Stored as strings on SubtitleJob.Status; these
// consts keep the state-machine values in sync with the code that transitions
// them (see SubtitleJobService / SubtitleJobRepository).
const (
	SubtitleJobQueued     = "queued"     // waiting for a worker to claim
	SubtitleJobProcessing = "processing" // claimed by a worker, not yet completed
	SubtitleJobDone       = "done"       // SRT posted back and saved to subtitles
	SubtitleJobFailed     = "failed"     // worker reported failure; admin decides retry/skip
	SubtitleJobSkipped    = "skipped"    // admin gave up; terminal
)

// IsTerminalSubtitleJobStatus reports whether a status is terminal (no further
// state transitions). Non-terminal statuses (queued, processing) are the ones
// de-duplication cares about: an episode may have many terminal rows but at
// most one non-terminal.
func IsTerminalSubtitleJobStatus(status string) bool {
	return status == SubtitleJobDone || status == SubtitleJobFailed || status == SubtitleJobSkipped
}

// UserPoint holds active point balance.
type UserPoint struct {
	UserID            uint `gorm:"primaryKey"`
	User              User `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	CurrentPoints     int  `gorm:"default:0"`
	TotalEarnedPoints int  `gorm:"default:0"`
	UpdatedAt         time.Time
}

// PointsLedger tracks points transaction history.
type PointsLedger struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	UserID       uint      `gorm:"index;not null"`
	User         User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	ChangeAmount int       `gorm:"not null"`
	ReasonType   string    `gorm:"size:50;not null"` // see Reason* consts above (system_watch, badge_unlocked, parent_grant)
	Description  string    `gorm:"size:1024"`
	CreatedAt    time.Time `gorm:"default:CURRENT_TIMESTAMP"`
}

// UserProgress tracks student's course playback progress.
type UserProgress struct {
	ID                  uint      `gorm:"primaryKey;autoIncrement"`
	UserID              uint      `gorm:"uniqueIndex:idx_user_episode;not null"`
	User                User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	EpisodeID           uint      `gorm:"uniqueIndex:idx_user_episode;not null"`
	Episode             Episode   `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE"`
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
// SubjectID links a subject-scoped badge (e.g. "数学达人") to its subject via
// FK, replacing the old string-convention coupling (Code "subject_<key>"). A
// badge with SubjectID=nil is a global badge (streak, points, etc.).
//
// IsSystem marks seeded defaults (protected from deletion, still editable).
type Badge struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	Code        string    `gorm:"size:100;uniqueIndex;not null"`
	Title       string    `gorm:"size:255;not null"`
	Description string    `gorm:"type:text"`
	IconName    string    `gorm:"size:255;not null"`
	RuleType    string    `gorm:"size:50;not null"` // watch_duration, consecutive_days, subject_count, episode_completed_count, points_earned, distinct_subject_count, course_completion, weekly_all_present, composite
	RuleTarget  string    `gorm:"size:100"`         // target e.g. "math" or empty
	Threshold   int       `gorm:"not null"`         // threshold to reach e.g. 100, 7, 5 (single-tier only)
	RuleJSON    string    `gorm:"type:text"`        // composite rule tree (empty = single rule)
	SubjectID   *uint     `gorm:"index"`            // FK → subjects.id (nullable; SET NULL on subject delete)
	// Tiers holds a multi-tier progression as JSON (see TierDef). Empty =
	// single-tier badge using Threshold. Non-empty = the badge is evaluated as
	// a progression: the user advances through each tier as their stat crosses
	// each tier's threshold, earning that tier's reward points. Adding a new
	// tier later is just appending to this array — no migration needed.
	// Subject badges are linked to their subject via SubjectID (FK); the Code
	// "subject_<key>" convention is kept for human-readability only.
	Tiers    string `gorm:"type:text"`
	IsSystem bool   `gorm:"default:false"` // true = seeded default, protected from deletion
	Subject  *Subject `gorm:"foreignKey:SubjectID;constraint:OnDelete:SET NULL"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TierDef is one tier of a multi-tier badge. Serialized in Badge.Tiers as
// [{"t":3,"r":10},{"t":7,"r":20},...] — short keys keep the column compact.
// T = the threshold the user's stat must reach to clear this tier;
// R = reward points awarded the first time this tier is cleared.
type TierDef struct {
	T int `json:"t"`
	R int `json:"r"`
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

// UserBadge stores which badges have been unlocked by which users. A row's
// existence means "unlocked"; for multi-tier badges Tier records the highest
// tier (0-based) cleared so far. For single-tier badges Tier is 0.
//
// NOTE: no gorm default tag on Tier — GORM applies `default` to zero values,
// which would clobber tier 0 (the first tier) with the default. 0 is a valid
// tier, so we let it be stored as-is.
type UserBadge struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UserID     uint      `gorm:"uniqueIndex:idx_user_badge;not null"`
	User       User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	BadgeID    uint      `gorm:"uniqueIndex:idx_user_badge;not null"`
	Badge      Badge     `gorm:"foreignKey:BadgeID;constraint:OnDelete:CASCADE"`
	Tier       int       // 0-based highest cleared tier (0 for single-tier badges)
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
	Course          Course    `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
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
// The admin-curated allowlist lives in the UserUnlockAllowedEpisode join table
// (not a JSON blob here). It is the SOLE source of visibility under
// StrategySelected, and an ADDITIVE source (unioned with the water level)
// under the other strategies — so an admin can always hand-unlock a specific
// episode without disturbing the drip schedule.
type UserUnlockOverride struct {
	UserID            uint      `gorm:"primaryKey"`
	User              User      `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	CourseID          uint      `gorm:"primaryKey"`
	Course            Course    `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
	Strategy          string    `gorm:"size:20;not null;default:'all_open'"`
	IntervalSeconds   int       `gorm:"default:0"`
	WeeklyTimesJSON   string    `gorm:"type:text"`
	ManualUnlockCount int       `gorm:"default:0"` // bumps water level under manual/interval/weekly
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// UserUnlockAllowedEpisode is one row of a (user, course) override's
// admin-curated episode allowlist. FK CASCADE on all three axes ensures no
// stale ids survive when a user, course, or episode is deleted.
type UserUnlockAllowedEpisode struct {
	UserID    uint    `gorm:"primaryKey"`
	User      User    `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	CourseID  uint    `gorm:"primaryKey"`
	Course    Course  `gorm:"foreignKey:CourseID;constraint:OnDelete:CASCADE"`
	EpisodeID uint    `gorm:"primaryKey"`
	Episode   Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE"`
}

// AppRelease is one Android APK build published for over-the-air distribution.
// The (version_code, abi) pair is unique: one build per ABI per version.
//
// STABILITY INVARIANT — this table backs the frozen client contract
// /api/v1/app/latest and /api/v1/app/download. Clients are addressed by
// (version_code, abi) — NEVER by the DB primary key (id), which can change if
// the database is rebuilt. Physical APKs live at a deterministic path
// (./data/releases/<version_code>/<abi>.apk) so files survive a DB loss too.
type AppRelease struct {
	ID           uint      `gorm:"primaryKey;autoIncrement"`
	VersionCode  int       `gorm:"uniqueIndex:idx_release_abi;not null"`
	VersionName  string    `gorm:"size:50;not null"` // display, e.g. "1.2.0"
	ABI          string    `gorm:"size:20;uniqueIndex:idx_release_abi;not null"` // arm64-v8a / armeabi-v7a / x86_64
	Filepath     string    `gorm:"type:text;not null"`  // relative to data dir, e.g. "releases/12/arm64-v8a.apk"
	FileSize     int64     // bytes
	SHA256       string    `gorm:"size:64;index"`      // hex digest, for integrity checks
	ReleaseNotes string    `gorm:"type:text"`
	ForceUpdate  bool      // client must install, dialog not dismissible
	// IsActive: false = withdrawn (bad build), hidden from OTA clients.
	// NOTE: no `default:true` GORM tag — that tag makes GORM omit a false value
	// on INSERT and the column default then persists it as true, so withdrawn
	// builds leak to clients. Defaults are applied in code (repo/service) instead.
	IsActive     bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// ReadingSeries is the container/series for reading material — a curated
// collection of related books and articles (e.g. "上博展厅系列"). Mirrors the
// Course role in the video module: it carries its own cover/subject/grade/tags
// and can be assigned to users independently. A book/article with SeriesID=nil
// is a standalone item (散本/散文) shown outside any series.
type ReadingSeries struct {
	ID          uint    `gorm:"primaryKey;autoIncrement"`
	Title       string  `gorm:"size:255;not null"`
	Description string  `gorm:"type:text"`
	SubjectID   uint    `gorm:"not null;index"`
	Subject     Subject `gorm:"foreignKey:SubjectID;constraint:OnDelete:RESTRICT"`
	CoverURL    string  `gorm:"size:1024"`
	Tags        []Tag   `gorm:"many2many:reading_series_tags;constraint:OnDelete:CASCADE"`
	Grades      []ReadingSeriesGrade `gorm:"foreignKey:SeriesID;constraint:OnDelete:CASCADE"`
	SortOrder   int     `gorm:"default:0;not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ReadingSeriesGrade is one row of a reading series's applicable-grade set.
type ReadingSeriesGrade struct {
	SeriesID uint  `gorm:"primaryKey"`
	Grade    Grade `gorm:"primaryKey;type:varchar(20);not null"`
}

// readingGradeDisplay formats a grade set for the reading-room UI. Shared by
// all three reading models; each delegates its loaded grade rows here.
func readingGradeDisplay(gs []Grade) string {
	for _, g := range gs {
		if g == GradeUniversal {
			return "全学段通用"
		}
	}
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, string(g)+"年级")
	}
	return strings.Join(parts, ", ")
}

func readingSeriesGrades(gs []ReadingSeriesGrade) []Grade {
	out := make([]Grade, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Grade)
	}
	return out
}
func readingBookGrades(gs []ReadingBookGrade) []Grade {
	out := make([]Grade, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Grade)
	}
	return out
}
func readingArticleGrades(gs []ReadingArticleGrade) []Grade {
	out := make([]Grade, 0, len(gs))
	for _, g := range gs {
		out = append(out, g.Grade)
	}
	return out
}

func (s ReadingSeries) TagsList() []string {
	out := make([]string, 0, len(s.Tags))
	for _, t := range s.Tags {
		out = append(out, t.Label)
	}
	return out
}
func (s ReadingSeries) TagsJoined() string  { return strings.Join(s.TagsList(), ",") }
func (s ReadingSeries) GradeDisplay() string { return readingGradeDisplay(readingSeriesGrades(s.Grades)) }

func (b ReadingBook) TagsList() []string {
	out := make([]string, 0, len(b.Tags))
	for _, t := range b.Tags {
		out = append(out, t.Label)
	}
	return out
}
func (b ReadingBook) TagsJoined() string  { return strings.Join(b.TagsList(), ",") }
func (b ReadingBook) GradeDisplay() string { return readingGradeDisplay(readingBookGrades(b.Grades)) }

func (a ReadingArticle) TagsList() []string {
	out := make([]string, 0, len(a.Tags))
	for _, t := range a.Tags {
		out = append(out, t.Label)
	}
	return out
}
func (a ReadingArticle) TagsJoined() string  { return strings.Join(a.TagsList(), ",") }
func (a ReadingArticle) GradeDisplay() string { return readingGradeDisplay(readingArticleGrades(a.Grades)) }

// ReadingBook is a PDF document in the reading room. Mirrors the Episode role:
// FileRelativePath + FileSize follow the Alist 302-stream + basename-based
// disaster-recovery pattern. PageCount is nullable (unknown until the client
// opens the PDF and reports it back — there is no backend probe worker, unlike
// the ffprobe pipeline for episodes).
type ReadingBook struct {
	ID               uint           `gorm:"primaryKey;autoIncrement"`
	SeriesID         *uint          `gorm:"index"` // NULL = standalone (散本)
	Series           *ReadingSeries `gorm:"foreignKey:SeriesID;constraint:OnDelete:SET NULL"`
	SortOrder        int            `gorm:"default:0;not null"`
	Title            string         `gorm:"size:255;not null"`
	FileRelativePath string         `gorm:"type:text;not null"` // Alist/WebDAV relative path
	FileSize         *int64                                     // nullable, not yet probed
	PageCount        *int                                       // nullable, client reports on first open
	// SourceID points at the StorageSource this book was imported from. Should
	// always be set post-import; NULL means the book can't stream (no source
	// bound). See StorageProviderResolver. Mirror of Episode.SourceID.
	SourceID         *uint         `gorm:"index"`
	CoverURL         string         `gorm:"size:1024"`
	SubjectID        uint           `gorm:"not null;index"`
	Subject          Subject        `gorm:"foreignKey:SubjectID;constraint:OnDelete:RESTRICT"`
	Tags             []Tag          `gorm:"many2many:reading_books_tags;constraint:OnDelete:CASCADE"`
	Grades           []ReadingBookGrade `gorm:"foreignKey:BookID;constraint:OnDelete:CASCADE"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// ReadingBookGrade is one row of a reading book's applicable-grade set.
type ReadingBookGrade struct {
	BookID uint  `gorm:"primaryKey"`
	Grade  Grade `gorm:"primaryKey;type:varchar(20);not null"`
}

// ReadingArticle is a web/rich-text article (e.g. a WeChat 公众号 H5) loaded
// in-app via WebView with domain-whitelist navigation interception.
//
// Phase 2 offline-mirror fields (MirrorStatus / MirroredURL) are reserved here
// but NOT used by Phase 1 logic: GetArticle always returns SourceURL. When a
// future scraper service populates MirroredURL and flips MirrorStatus to
// "ready", the article reader will load the self-hosted mirror instead — the
// reading path is transparent to the status today.
type ReadingArticle struct {
	ID               uint           `gorm:"primaryKey;autoIncrement"`
	SeriesID         *uint          `gorm:"index"` // NULL = standalone (散文)
	Series           *ReadingSeries `gorm:"foreignKey:SeriesID;constraint:OnDelete:SET NULL"`
	SortOrder        int            `gorm:"default:0;not null"`
	Title            string         `gorm:"size:255;not null"`
	SourceURL        string         `gorm:"type:text;not null"`
	WhitelistDomains string         `gorm:"type:text"` // JSON []string; empty = use global default whitelist
	// —— Phase 2 offline-mirror reservation (no logic today) ——
	MirrorStatus string `gorm:"size:20;not null;default:'none'"` // none | pending | ready | failed
	MirroredURL  string `gorm:"type:text"`
	// —— reservation end ——
	CoverURL   string         `gorm:"size:1024"`
	SubjectID  uint           `gorm:"not null;index"`
	Subject    Subject        `gorm:"foreignKey:SubjectID;constraint:OnDelete:RESTRICT"`
	Tags       []Tag          `gorm:"many2many:reading_articles_tags;constraint:OnDelete:CASCADE"`
	Grades     []ReadingArticleGrade `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ReadingArticleGrade is one row of a reading article's applicable-grade set.
type ReadingArticleGrade struct {
	ArticleID uint  `gorm:"primaryKey"`
	Grade     Grade `gorm:"primaryKey;type:varchar(20);not null"`
}

// UserReadingSeriesAccess / UserReadingBookAccess / UserReadingArticleAccess
// define which reading resources a user can see. Mirrors UserCourseAccess:
// composite PK on (UserID, resourceID), FK CASCADE on both axes. Three separate
// tables (rather than one polymorphic table) keep foreign keys clean.
type UserReadingSeriesAccess struct {
	UserID    uint      `gorm:"primaryKey"`
	SeriesID  uint      `gorm:"primaryKey"`
	GrantedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	User      User           `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Series    ReadingSeries  `gorm:"foreignKey:SeriesID;constraint:OnDelete:CASCADE"`
}

type UserReadingBookAccess struct {
	UserID    uint      `gorm:"primaryKey"`
	BookID    uint      `gorm:"primaryKey"`
	GrantedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	User      User         `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Book      ReadingBook  `gorm:"foreignKey:BookID;constraint:OnDelete:CASCADE"`
}

type UserReadingArticleAccess struct {
	UserID    uint      `gorm:"primaryKey"`
	ArticleID uint      `gorm:"primaryKey"`
	GrantedAt time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	User      User            `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Article   ReadingArticle  `gorm:"foreignKey:ArticleID;constraint:OnDelete:CASCADE"`
}

// ReadingBookProgress remembers the last-read page of a PDF book. Mirrors the
// UserProgress atomic-upsert pattern: composite PK (UserID, BookID), upsert via
// INSERT ... ON CONFLICT DO UPDATE. Unlike watch_seconds there is no concurrent
// accumulation — last page is a simple overwrite, so the upsert does not add.
type ReadingBookProgress struct {
	UserID    uint        `gorm:"primaryKey"`
	User      User        `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	BookID    uint        `gorm:"primaryKey"`
	Book      ReadingBook `gorm:"foreignKey:BookID;constraint:OnDelete:CASCADE"`
	LastPage  int         `gorm:"default:0;not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// EntertainmentProgress tracks playback of entertainment videos. Physically
// separate from UserProgress so learning-stat queries are zero-contaminated:
// entertainment watching never lands in user_progresses, so no badge / dashboard
// query needs an exclusion filter. WatchSeconds accumulates for the future
// time-limit feature (daily/weekly caps). Resume position works identically to
// UserProgress (last-writer-wins on LastPositionSeconds).
type EntertainmentProgress struct {
	UserID              uint    `gorm:"uniqueIndex:idx_ent_user_episode;not null"`
	User                User    `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	EpisodeID           uint    `gorm:"uniqueIndex:idx_ent_user_episode;not null"`
	Episode             Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE"`
	LastPositionSeconds int     `gorm:"default:0"`
	WatchSeconds        int     `gorm:"default:0"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// WatchEvent records one "continuous viewing session" for the watch-history
// feature (admin per-day timeline + heatmap). The client sends a heartbeat
// every ~5–30s; the backend merges consecutive heartbeats into a single row
// when the gap is within the merge window (default 60s, see WATCH_MERGE_WINDOW).
// So one row ≈ one uninterrupted viewing stretch.
//
// Both learning and entertainment playback write to this table (ContentType
// distinguishes them), giving the admin a unified history regardless of which
// progress-aggregate table (UserProgress vs EntertainmentProgress) holds the
// running total.
//
// DurationSeconds accumulates ONLY real client-reported deltas — paused/away
// time never adds to it (the client doesn't send a delta while paused).
// StartedAt..EndedAt is the wall-clock span and DOES contain any pause inside
// the merge window. The admin UI shows both so the operator can judge the
// difference ("9–10am span but only 45min watched ⇒ 15min paused").
//
// CourseID/ContentType are denormalized at write time (taken from the episode)
// so timeline queries avoid an episodes+courses join on every read.
type WatchEvent struct {
	ID              uint   `gorm:"primaryKey;autoIncrement"`
	UserID          uint   `gorm:"index:idx_we_user_ep_started,priority:1;index:idx_we_user_started,priority:1"`
	EpisodeID       uint   `gorm:"index:idx_we_user_ep_started,priority:2"`
	CourseID        uint   `gorm:"index"`                       // denormalized from episode → course
	ContentType     string `gorm:"size:20;index;default:'learning'"` // "learning" | "entertainment"
	StartedAt       time.Time `gorm:"index:idx_we_user_ep_started,priority:3;index:idx_we_user_started,priority:2"`
	EndedAt         time.Time `gorm:"index"`                      // last heartbeat merged into this row
	DurationSeconds int    `gorm:"default:0"`                    // real watch seconds (delta sum, pauses excluded)
	CreatedAt       time.Time
}

// AutoMigrate runs GORM schema auto-migration for all tables.
// ---------------------------------------------------------------------------
// AI module (Step 3 — learning agent: summary / quiz / chat)
//
// These tables are the agent's PRIVATE state. The rest of the backend never
// reads or writes them directly; only internal/ai and its handler/service do.
// AI is an opt-in add-on: if no provider is configured and no course has
// AISummaryEnabled/AIQuizEnabled on, the system behaves exactly as before and
// these tables sit empty.
//
// Capability constants for AIProvider.Capability.
const (
	AICapabilityChat      = "chat"      // LLM chat completion
	AICapabilityEmbedding = "embedding" // text → vector
	AICapabilityRerank    = "rerank"    // (reserved, not wired in MVP)
)

// AIProvider is one row of admin-configured provider credentials for one
// capability. Modeled on StorageSource (multi-row, admin CRUD, test-connection
// button). The three capabilities are independent: you can run chat against a
// remote relay while embedding runs locally, and swap either without touching
// the other. Credentials are stored plaintext (same posture as StorageSource;
// at-rest encryption is a separate cross-cutting PR).
type AIProvider struct {
	ID           uint   `gorm:"primaryKey;autoIncrement"`
	Capability   string `gorm:"size:20;not null;index"` // chat | embedding | rerank
	Name         string `gorm:"size:100;not null"`      // display name, e.g. "主聊天模型"
	ProviderType string `gorm:"size:30;not null"`       // openai_compat | onnx_local | ...
	BaseURL      string `gorm:"size:1024"`              // chat relay base (no /v1); empty for onnx_local
	APIKey       string `gorm:"size:1024"`              // bearer token; empty for onnx_local
	ModelName    string `gorm:"size:255;not null"`      // model id (chat) or model dir (onnx)
	ExtraJSON    string `gorm:"type:text"`              // capability-specific knobs (temperature, dim, seqLen...)
	IsEnabled    bool   `gorm:"default:false"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AIJob is one asynchronous AI generation task (segment/summary/quiz), modeled
// on SubtitleJob's queue/claim/complete pattern. Generated offline so the
// client reads already-produced content with zero latency (the user never waits
// for a 20s LLM call). Status values mirror SubtitleJob: queued|processing|
// done|failed|skipped.
type AIJob struct {
	ID          uint       `gorm:"primaryKey;autoIncrement"`
	JobType     string     `gorm:"size:20;not null;index"` // segment | summary | quiz | advice | course_summary | user_report
	EpisodeID   uint       `gorm:"index;not null"`
	CourseID    uint       `gorm:"index;not null"`
	UserID      *uint      `gorm:"index"` // nullable: segment/summary leave it NULL; quiz jobs bind to a specific user (per-user adaptive generation)
	Status      string     `gorm:"size:20;not null;default:'queued';index"`
	Priority    int        `gorm:"default:0"`
	Attempt     int        `gorm:"default:0"`
	ClaimedAt   *time.Time `gorm:"index"`
	CompletedAt *time.Time
	Error       string `gorm:"type:text"`
	Progress    *float64
	// PayloadJSON 存 job 类型特定的参数(Phase C advice 用:scope/scope_id/subject_id,
	// 因为 AIJob 表是 episode-centric 的,subject 级 advice 没有专门列)。quiz/segment/
	// summary job 留空。JSON 文本,宽松解析(buildAdviceRequest 容忍缺字段)。
	PayloadJSON string `gorm:"type:text"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ContentChunk is one retrievable unit of the RAG corpus, source-agnostic so
// subtitle segments AND attachment extracts (PDF/workbook text, future) live in
// one table and are retrievable together. For subtitle chunks, StartTime/
// EndTime let quiz/chat answers link back to an exact video timestamp ("this
// concept is explained at 12:38") — the knowledge-point → video-jump feature.
// Embedding is the JSON-serialized float32 vector from the Embedder; cosine
// similarity is computed in Go (brute force) since per-episode chunk counts are
// small (hundreds). StartTime/EndTime are NULL for attachment chunks.
type ContentChunk struct {
	ID         uint    `gorm:"primaryKey;autoIncrement"`
	EpisodeID  uint    `gorm:"index:idx_chunk_ep_src;not null"`
	CourseID   uint    `gorm:"index;not null"`
	SourceType string  `gorm:"size:20;not null;index:idx_chunk_ep_src;default:'subtitle'"` // subtitle | attachment
	SourceRef  string  `gorm:"size:255"`                                                 // subtitle_id or attachment identifier
	ChunkIndex int     `gorm:"not null"`
	StartTime  *int    // seconds; NULL for attachment
	EndTime    *int    // seconds; NULL for attachment
	Text       string  `gorm:"type:text;not null"`
	Embedding  string  `gorm:"type:text"` // JSON []float32, length = embedder Dim
	CreatedAt  time.Time
}

// AISummary is the agent-generated summary for one episode (one row per
// episode). SummaryJSON is structured (key points, key concepts) so the client
// can render it richly. Generated by the summarizer capability via an AIJob.
type AISummary struct {
	ID         uint   `gorm:"primaryKey;autoIncrement"`
	EpisodeID  uint   `gorm:"uniqueIndex;not null"`
	CourseID   uint   `gorm:"index;not null"`
	SummaryJSON string `gorm:"type:text;not null"`
	ModelUsed  string `gorm:"size:255"`
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// KnowledgeMemory is the per-user learning state for one knowledge-point chunk
// — the heart of the feedback loop. mastery (0.0–1.0) is updated on each answer
// (correct +0.1 / wrong −0.2, clamped; a decay curve is a planned later step)
// and READ by the quiz agent on the next generation, so it adapts to the
// student's weak points. This is what makes the system an agent (state-driven,
// self-adapting) rather than a stateless quiz generator.
type KnowledgeMemory struct {
	ID           uint       `gorm:"primaryKey;autoIncrement"`
	UserID       uint       `gorm:"uniqueIndex:idx_mem_user_chunk;not null"`
	EpisodeID    uint       `gorm:"index;not null"`
	CourseID     uint       `gorm:"index;not null"`
	ChunkID      uint       `gorm:"uniqueIndex:idx_mem_user_chunk;index;not null"` // the knowledge-point chunk
	Mastery      float64    `gorm:"default:0"`                                      // 0.0–1.0
	CorrectCount int        `gorm:"default:0"`
	WrongCount   int        `gorm:"default:0"`
	LastReviewed *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Quiz is one generated quiz set for a (user, episode). Questions belong to it.
// Generated by the quizzer capability (which runs the agent loop with tool
// calling) via an AIJob, then served read-only to the client.
//
// One ACTIVE row per (user, episode): a student has a SINGLE current quiz per
// lesson. "重做" (redo) answers the same set again (Answer rows accumulate,
// memory updates); "换题" (regenerate) ARCHIVES the current row + its questions
// (Status active→archived, ArchivedAt set) and inserts a fresh active one, so
// the student's past attempts stay readable in the history panel instead of
// being wiped. The single-active invariant is enforced by a partial unique
// index (see AutoMigrate: WHERE status='active'); GORM can't express partial
// indexes, so the model itself carries no unique tag — the index is created in
// raw SQL after AutoMigrate.
type Quiz struct {
	ID            uint       `gorm:"primaryKey;autoIncrement"`
	EpisodeID     uint       `gorm:"index;not null"`
	UserID        uint       `gorm:"index;not null"`
	CourseID      uint       `gorm:"index;not null"`
	Difficulty    string     `gorm:"size:20;default:'adaptive'"` // adaptive = agent decides from memory
	AgentFeedback string     `gorm:"type:text"`                   // LLM's analysis of this student's weak points + study advice, a byproduct of generation (the agent already read memory to pick questions). Shown to the student on the AI study page and to the admin in the user view — both a learning aid and an observability signal.
	// Status is 'active' (the one current quiz the student plays) or 'archived'
	// (a superseded prior generation, kept read-only for history). Default
	// 'active' also back-fills pre-existing rows on migration, so the install's
	// current quizzes all become the active one — matching prior behavior where
	// there was exactly one row per (user, episode).
	Status     string     `gorm:"size:16;default:'active'"`
	ArchivedAt *time.Time // set when Status flips to 'archived'; nil while active
	// SubmittedAt 标记"已交卷"时间。Phase B 改成统一提交(一次提交 = 一次考试):
	// 学生点"提交全部"后,该 quiz 被锁定,不可再改答案。nil = 尚未交卷(仍可作答)。
	// 用专门字段而不是"是否存在 answer"来判断,因为单题 submit 端点(兼容保留)也会
	// 产生 answer 行,后者不能误判为已交卷。
	SubmittedAt *time.Time
	CreatedAt  time.Time
}

// Question is one question in a Quiz. ChunkID links it to the knowledge-point
// chunk it tests, which (via ContentChunk.StartTime) gives the "jump to video"
// timestamp.
//
// Two question types, selected by the agent based on the knowledge point:
//   - choice: Options is a JSON []string of options; Answer is the 0-based
//     correct index. Used for discrimination/understanding questions.
//   - fill:   a fill-in-the-blank with a single canonical answer. Options is
//     empty; AnswerText is a JSON []string of ACCEPTABLE answers (multiple
//     equivalent forms, e.g. ["12","十二"]) graded by normalization. Used ONLY
//     for knowledge points with a unique answer (typical: math computation,
//     factual recall). The prompt forbids fill questions when the answer is
//     subjective or ambiguous — a fill question whose "correct" answer is a
//     matter of opinion can't be graded.
type Question struct {
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	QuizID      uint   `gorm:"index;not null"`
	ChunkID     uint   `gorm:"index"` // nullable: a question may be synthetic, not tied to one chunk
	Type        string `gorm:"size:20;default:'choice'"` // choice | fill
	Stem        string `gorm:"type:text;not null"`
	Options     string `gorm:"type:text"` // JSON []string (choice only)
	Answer      int    // choice: 0-based index into Options
	AnswerText  string `gorm:"type:text"` // fill: JSON []string of acceptable answers
	Explanation string `gorm:"type:text"`
	// HasJump 标记该题是否对应一个明确的视频片段(anchor chunk)。agent 出题时
	// 判断:能锚定到具体知识点 chunk 的题 has_jump=true(答错可跳视频复习);
	// 贯穿全文/综合性的题 has_jump=false(没有单一跳转锚点)。默认 false 兼容老数据
	// (Phase B 之前生成的题没有此字段,视为不可跳转)。
	HasJump bool `gorm:"default:false"`
	CreatedAt   time.Time
}

// Answer records one user answer to one Question (append-only). Written on
// submit, then used to update KnowledgeMemory (the feedback loop).
//
// QuizID is a DENORMALIZED snapshot of the question's quiz at answer time. It
// lets us list a user's answer history for an episode WITHOUT joining questions
// — which matters because regenerate (换题) DELETES old questions, breaking a
// question-join. With QuizID we can still show past attempts after a regen by
// scoping to the (user, episode)'s quiz lineage. (There's at most one quiz row
// per (user, episode) at a time, but the quiz row gets a new ID on each regen;
// the old QuizID values on historical answers point to deleted quiz rows, which
// is fine — we group by the current quiz's episode instead.)
//
// Two answer shapes coexist by question type:
//   - choice: UserAnswer holds the 0-based option index; UserAnswerText is "".
//   - fill:   UserAnswerText holds the student's free-text answer verbatim
//     (previously discarded after grading — the answer could only be shown as
//     correct/wrong, never "你当时填的 X"). UserAnswer is -1 (meaningless for
//     fill). Grading still uses NormalizeText matching against
//     Question.AnswerText; this column is purely for回放 in submitted review +
//     history, so the student can see what they typed.
type Answer struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	QuestionID uint      `gorm:"index;not null"`
	QuizID     uint      `gorm:"index"` // snapshot of the question's quiz at answer time; survives question deletion on regen
	UserID     uint      `gorm:"index;not null"`
	UserAnswer int       // choice: 0-based index the user picked; fill: -1 (meaningless)
	// UserAnswerText 是填空题学生的原文(choice 题为空)。交卷后 / 历史 review 里回放
	// "你当时填的什么"用这个字段;判分仍走 Question.AnswerText 的归一化匹配,不依赖它。
	UserAnswerText string `gorm:"type:text"`
	Correct        bool
	AnsweredAt     time.Time
}

// AIRun records ONE LLM call's decision trace — input snapshot, the raw model
// response, token usage, self-check outcome. Written for every agent step so
// the admin AI Workflow page can REPLAY how the agent reasoned: what chunks it
// retrieved, what memory weaknesses it saw, what it answered, whether its
// self-check passed. This is both the observability layer (debug bad output)
// and the learning material (see agent decision flow in action).
type AIRun struct {
	ID              uint   `gorm:"primaryKey;autoIncrement" json:"id"`
	JobID           uint   `gorm:"index" json:"job_id"`               // 0 for ad-hoc (e.g. chat) runs not tied to a job
	Capability      string `gorm:"size:20;not null;index" json:"capability"` // summary | quiz | chat
	InputJSON       string `gorm:"type:text" json:"input_json"`      // snapshot: retrieved chunks, memory weaknesses
	PromptTokens    int    `json:"prompt_tokens"`
	CompletionTokens int   `json:"completion_tokens"`
	ModelUsed       string `gorm:"size:255" json:"model_used"`
	ResponseText    string `gorm:"type:text" json:"response_text"` // the raw model output
	// TraceJSON records the agent's ReAct step-by-step reasoning for quiz runs:
	// a JSON array of {step, thought, action:{tool, args}, observation}, one
	// entry per loop iteration. Empty for single-shot capabilities (summary) —
	// those have no loop. This is the observability centerpiece: the admin
	// "思考时间线" view replays exactly which tools the agent called, with what
	// arguments, and what each returned, so a bad quiz can be traced to the
	// retrieval/memory step that misled it. Observations are truncated per step
	// (tool output can be large) to keep the field scannable.
	TraceJSON       string `gorm:"type:text" json:"trace_json"`
	SelfCheckResult string `gorm:"size:20;default:'skipped'" json:"self_check_result"` // pass | fail | skipped
	SelfCheckNote   string `gorm:"type:text" json:"self_check_note"`
	DurationMs      int    `json:"duration_ms"`
	CreatedAt       time.Time `json:"created_at"`
}

// ChatSession / ChatMessage hold the multi-turn chat (Phase D capability) so a
// user can discuss a lesson with the agent. Tables are created in this phase so
// the schema is stable, but the chat capability itself is implemented later.
type ChatSession struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	UserID     uint      `gorm:"index;not null"`
	EpisodeID  uint      `gorm:"index;not null"`
	CreatedAt  time.Time
}

type ChatMessage struct {
	ID         uint      `gorm:"primaryKey;autoIncrement"`
	SessionID  uint      `gorm:"index;not null"`
	Role       string    `gorm:"size:20;not null"` // user | assistant
	Content    string    `gorm:"type:text;not null"`
	ChunkRefs  string    `gorm:"type:text"`        // JSON [{text,start_time,end_time}] for video-jump links
	CreatedAt  time.Time
}

// StudyAdvice 是 Phase C 的 agent 驱动学习建议产物。和 quiz 不同,advice 的产出是
// 自然语言文本(不是结构化 JSON),由 advice agent 跑 ReAct loop 跨课程查 mastery 后
// 生成。按 (user, scope, scope_id) 唯一存储:
//   - scope="episode", scope_id=episode_id:某节课交卷后的复习建议
//   - scope="course",  scope_id=course_id:某门课的整体弱点分析
//   - scope="subject", scope_id=subject_id:某科目(跨多门课)的弱点分析
//
// 重新生成替换旧记录(同 quiz 的 Upsert 语义,但 advice 不保留历史——建议是"当前
// 快照",过期了就覆盖)。MasterySnapshotJSON 存当时 mastery 的 JSON 快照,供后续对比
// "上次建议后学生进步了多少"(Phase D 可用,Phase C 先存下来)。
type StudyAdvice struct {
	ID                  uint      `gorm:"primaryKey;autoIncrement" json:"-"`
	UserID              uint      `gorm:"uniqueIndex:idx_advice_user_scope;not null" json:"-"`
	Scope               string    `gorm:"size:16;uniqueIndex:idx_advice_user_scope;not null" json:"scope"`        // episode | course | subject
	ScopeID             uint      `gorm:"uniqueIndex:idx_advice_user_scope;not null" json:"scope_id"`             // episode_id / course_id / subject_id
	AdviceText          string    `gorm:"type:text;not null" json:"advice_text"`                                   // 自然语言建议(agent 的 FinalText)
	MasterySnapshotJSON string    `gorm:"type:text" json:"-"`                                                      // 生成时 mastery 快照,内部用,不下发客户端
	ModelUsed           string    `gorm:"size:255" json:"model_used,omitempty"`
	GeneratedAt         time.Time `gorm:"not null" json:"generated_at"`
	CreatedAt           time.Time `json:"-"`
	UpdatedAt           time.Time `json:"-"`
}

// AICourseSummary 是 Phase D 的课程级总结产物(course-unique)。和 StudyAdvice 的关键
// 差异:它按 course 唯一存储(不含 user_id),是**纯内容总结**——课程整体脉络 + 学习路径
// 建议,与具体学生无关。这样所有学生共享同一条总结,admin 生成一次即可,不必按 user
// 重复跑(不同学生的"针对建议"走 advice,那是 per-user 的,不在这里)。
//
// 重新生成替换旧记录(同 AISummary.UpsertSummary / StudyAdvice.UpsertAdvice 语义)。
// SummaryText 存 agent 的自然语言 FinalText(整体脉络 + 学习路径);课程总结是给所有
// 学生看的导览,不是结构化题库,所以用纯文本而不是 JSON。
type AICourseSummary struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	CourseID    uint      `gorm:"uniqueIndex;not null"`
	SummaryText string    `gorm:"type:text;not null"` // agent 的自然语言 FinalText(整体脉络 + 学习路径)
	ModelUsed   string    `gorm:"size:255"`
	GeneratedAt time.Time `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// UserStudyReport 是 Phase E 的产物:admin 视角下"某学生跨课程学习情况"的 agent 报告。
// 和 StudyAdvice 的差异——advice 是给学生本人看的"复习建议"(episode/course/subject 级),
// UserStudyReport 是给 admin 看的"这个学生整体学得怎么样"的跨课程画像报告。每用户一份
// 最新报告(unique on user_id),重新生成替换。Agent 走和 advice 同一套 ReAct loop,但
// 工具集是 user_study 专用(list_user_courses / get_course_mastery / get_course_summary /
// get_user_advice),agent 自己遍历该学生所有课程交叉分析。
type UserStudyReport struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	UserID      uint      `gorm:"uniqueIndex;not null"` // 每用户一份最新报告(unique on user_id,重新生成替换)
	ReportText  string    `gorm:"type:text;not null"`   // 自然语言报告(agent 的 FinalText)
	ModelUsed   string    `gorm:"size:255"`
	GeneratedAt time.Time `gorm:"not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func AutoMigrate(db *gorm.DB) error {
	err := db.AutoMigrate(
		&Setting{},
		&User{},
		&UserCourseAccess{},
		&Subject{},
		&Tag{},
		&Course{},
		&CourseGrade{},
		&Chapter{},
		&Episode{},
		&Subtitle{},
		&SubtitleJob{},
		&UserPoint{},
		&PointsLedger{},
		&UserProgress{},
		&Badge{},
		&UserBadge{},
		&CourseUnlockTemplate{},
		&UserUnlockOverride{},
		&UserUnlockAllowedEpisode{},
		&AppRelease{},
		&EntertainmentProgress{},
		// Reading Room module
		&ReadingSeries{},
		&ReadingSeriesGrade{},
		&ReadingBook{},
		&ReadingBookGrade{},
		&ReadingArticle{},
		&ReadingArticleGrade{},
		&UserReadingSeriesAccess{},
		&UserReadingBookAccess{},
		&UserReadingArticleAccess{},
		&ReadingBookProgress{},
		// Auth module
		&Session{},
		// Watch history module (per-session viewing events; coexists with the
		// aggregate progress tables above, written alongside them on each report).
		&WatchEvent{},
		// Storage sources module (multi-source: admin configures N netdisk
		// backends; content points at one; user whitelist is the 防呆 gate).
		&StorageSource{},
		&UserStorageSource{},
		// AI module (Step 3 — learning agent). Private to internal/ai; empty and
		// inert when no provider is configured / no course has AI enabled.
		&AIProvider{},
		&AIJob{},
		&ContentChunk{},
		&AISummary{},
		&KnowledgeMemory{},
		&Quiz{},
		&Question{},
		&Answer{},
		&AIRun{},
		&ChatSession{},
		&ChatMessage{},
		// Phase C — agent 驱动的学习建议(advice agent 产出)。
		&StudyAdvice{},
		// Phase D — 课程级总结(course-unique 纯内容总结,agent 驱动综合所有 episode)。
		&AICourseSummary{},
		// Phase E — admin 用户学习报告(agent 驱动,跨课程交叉分析)。
		&UserStudyReport{},
	)
	if err != nil {
		return err
	}
	return migrateQuizActiveUniqueIndex(db)
}

// migrateQuizActiveUniqueIndex swaps the quizzes table's uniqueness guarantee
// from "one row per (user, episode)" to "one ACTIVE row per (user, episode)".
//
// Background: Quiz historically carried a GORM uniqueIndex tag producing a
// plain UNIQUE(user_id, episode_id). Phase 3 keeps old quizzes on regen by
// archiving them (Status active→archived) instead of deleting, so N archived
// rows may now coexist with 1 active row for the same pair. A plain unique
// index would reject that, so the tag was removed from the struct and the
// invariant moves to a partial unique index (SQLite + Postgres support the
// WHERE clause form). The plain GORM tag can't express a WHERE, hence the raw
// SQL here.
//
// Steps (idempotent, runs every boot):
//  1. Drop the legacy non-partial unique index if it still exists (installs
//     that predate Phase 3 have it; AutoMigrate alone won't drop a tag-removed
//     index). IF EXISTS keeps this a no-op on fresh DBs.
//  2. CREATE the partial unique index IF NOT EXISTS so the active-row
//     invariant is still enforced at the DB layer (defense in depth on top of
//     CreateQuiz's archive-then-insert transaction).
//
// MySQL has no partial-index syntax; if a future port targets MySQL, this needs
// to fall back to app-layer-only enforcement (CreateQuiz already guarantees it
// within its transaction). Today the only supported DB is SQLite.
func migrateQuizActiveUniqueIndex(db *gorm.DB) error {
	// Drop the legacy full-unique index left over from the pre-Phase-3 schema.
	// IF EXISTS makes this safe on fresh installs (no-op) and on boots after the
	// first migration (the index is already gone).
	if err := db.Exec(`DROP INDEX IF EXISTS idx_quiz_user_ep`).Error; err != nil {
		return fmt.Errorf("drop legacy idx_quiz_user_ep: %w", err)
	}
	// Create the partial unique index. Only active rows participate, so an
	// arbitrary number of archived rows may coexist for the same pair.
	if err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_quiz_user_ep_active ON quizzes(user_id, episode_id) WHERE status = 'active'`).Error; err != nil {
		return fmt.Errorf("create partial unique idx_quiz_user_ep_active: %w", err)
	}
	return nil
}
