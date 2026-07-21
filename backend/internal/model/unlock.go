package model

import "time"

// Code split from models.go for navigability. See models.go for the
// package overview.

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
