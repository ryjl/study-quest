package model

import "time"

// Code split from models.go for navigability. See models.go for the
// package overview.

// Setting represents KV configuration parameters managed by Admin.
type Setting struct {
	Key         string `gorm:"primaryKey"`
	Value       string `gorm:"type:text"`
	Description string `gorm:"type:text"`
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
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	Name      string `gorm:"size:100;not null"` // display name, e.g. "家长追剧盘"
	Type      string `gorm:"size:20;not null"`  // "alist" | "webdav"
	URL       string `gorm:"size:1024;not null"`
	Username  string `gorm:"size:255"`
	Password  string `gorm:"size:255"`
	Token     string `gorm:"size:1024"` // alist only
	IsDefault bool   `gorm:"default:false"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// UserStorageSource is one row of a user's storage-source allow-list (防呆).
// The list is DEFAULT-DENY: a user may access a source if and only if it
// appears in their list. An EMPTY set means the user is allowed NOTHING (an
// admin must explicitly grant at least one source before the user can stream).
// The grant-time and access-time gates both enforce this via
// storage_source_repo.IsAllowed. Staff roles (admin) bypass entirely.
type UserStorageSource struct {
	UserID    uint          `gorm:"primaryKey"`
	SourceID  uint          `gorm:"primaryKey"`
	CreatedAt time.Time     `gorm:"default:CURRENT_TIMESTAMP"`
	User      User          `gorm:"foreignKey:UserID;constraint:OnDelete:CASCADE"`
	Source    StorageSource `gorm:"foreignKey:SourceID;constraint:OnDelete:CASCADE"`
}

// User represents a user (student or admin).
type User struct {
	ID        uint   `gorm:"primaryKey;autoIncrement"`
	Nickname  string `gorm:"size:255;not null"`
	AvatarURL string `gorm:"size:1024"`
	PinHash   string `gorm:"size:255;not null"`                  // bcrypt hash of 6-digit PIN
	Role      string `gorm:"size:50;not null;default:'student'"` // student, admin
	Grade     string `gorm:"size:32"`                            // free-text grade label, e.g. "四年级"/"初二" (admin-edited)
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Role constants. Stored as strings on User.Role; these consts centralize the
// values so inline string comparisons can't drift. IsStaffRole identifies the
// role that bypasses student-level access gating (see all course/reading
// services).
const (
	RoleStudent = "student"
	RoleAdmin   = "admin"
)

func IsStaffRole(role string) bool {
	return role == RoleAdmin
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
	DeviceName string    `gorm:"size:255"`  // client-supplied OS device name (primary id)
	UserAgent  string    `gorm:"type:text"` // HTTP UA (fallback)
	Note       string    `gorm:"size:255"`  // admin-set device note
	CreatedAt  time.Time // first login
	LastSeenAt time.Time // updated on each successful Validate
	ExpiresAt  time.Time `gorm:"index"` // fixed TTL, no sliding renewal
}

// PointsLedger reason-type constants. Stored as strings on
// PointsLedger.ReasonType; these consts keep the ledger values in sync with
// the code that writes them.
const (
	ReasonSystemWatch   = "system_watch"   // completed a learning episode
	ReasonBadgeUnlocked = "badge_unlocked" // badge tier cleared
)

// Badge rule-type constants. Stored as strings on Badge.RuleType.
const (
	RuleWatchDuration    = "watch_duration"
	RuleConsecutiveDays  = "consecutive_days"
	RuleSubjectCount     = "subject_count"
	RuleEpisodeCount     = "episode_completed_count"
	RulePointsEarned     = "points_earned"
	RuleDistinctSubject  = "distinct_subject_count"
	RuleCourseCompletion = "course_completion"
	RuleWeeklyAllPresent = "weekly_all_present"
	RuleComposite        = "composite"
)

// UserCourseAccess defines which courses a user can see/access. FK CASCADE on
// both axes ensures access rows are cleaned up automatically when a user or

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
