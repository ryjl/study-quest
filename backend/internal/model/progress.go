package model

import "time"

// Code split from models.go for navigability. See models.go for the
// package overview.

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
	ReasonType   string    `gorm:"size:50;not null"` // see Reason* consts above
	Description  string    `gorm:"size:255"`
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
	IsCompleted         bool      `gorm:"default:false"` // true when watch_seconds > 80% duration
	UnlockedAt          *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Badge represents an achievement badge that can be earned by a student.
//
// Two evaluation modes:
//   - Single rule: RuleType + RuleTarget + Threshold.
//   - Composite rule: RuleJSON holds a serialized CompositeRule tree, and the
//     top-level RuleType/Threshold are kept only for display/back-compat
//     (set to "composite" when RuleJSON is populated).
//
// SubjectID links a subject-scoped badge (e.g. "数学达人") to its subject via
// FK. A badge with SubjectID=nil is a global badge (streak, points, etc.).
//
// The display icon is NOT stored on the badge: both clients derive it from
// Code/RuleType (admin: badgeIcon.tsx maps code substrings to a color ring
// around a shared Award icon; Flutter: _badgeIcon maps ruleType to a Material
// IconData). IsSystem marks seeded defaults (protected from deletion).
type Badge struct {
	ID          uint      `gorm:"primaryKey;autoIncrement"`
	Code        string    `gorm:"size:100;uniqueIndex;not null"`
	Title       string    `gorm:"size:255;not null"`
	Description string    `gorm:"type:text"`
	RuleType    string    `gorm:"size:50;not null"` // see Rule* consts below
	RuleTarget  string    `gorm:"size:100"`        // target e.g. "math" or empty
	Threshold   int       `gorm:"not null"`        // threshold to reach e.g. 100, 7, 5 (single-tier only)
	RuleJSON    string    `gorm:"type:text"`       // composite rule tree (empty = single rule)
	SubjectID   *uint     `gorm:"index"`           // FK → subjects.id (nullable; SET NULL on subject delete)
	// Tiers holds a multi-tier progression as JSON (see TierDef). Empty =
	// single-tier badge using Threshold. Non-empty = the badge is evaluated as
	// a progression: the user advances through each tier as their stat crosses
	// each tier's threshold, earning that tier's reward points. Adding a new
	// tier later is just appending to this array — no migration needed.
	Tiers    string   `gorm:"type:text"`
	IsSystem bool     `gorm:"default:false"` // true = seeded default, protected from deletion
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
