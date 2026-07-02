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

// Course represents a multi-episode course.
type Course struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Title     string    `gorm:"size:255;not null"`
	Grade     Grade     `gorm:"type:varchar(50);not null"`   // "1" to "9" or "universal" (or comma-separated)
	Subject   string    `gorm:"size:100;not null"`  // e.g., "chinese", "math", "english", "physics"
	CoverURL  string    `gorm:"size:1024"`
	Tags      string    `gorm:"type:text"`          // Comma-separated tags, e.g. "上学期,作文,重难点"
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TagsList splits comma-separated tags into a slice.
func (c Course) TagsList() []string {
	if c.Tags == "" {
		return []string{}
	}
	parts := strings.Split(c.Tags, ",")
	var result []string
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
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
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	CourseID    uint   `gorm:"index;not null"`
	Title       string `gorm:"size:255;not null"`
	Description string `gorm:"type:text"`
	CoverURL    string `gorm:"size:1024"`
	SortOrder   int    `gorm:"default:0"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Episode represents a specific episode in a course.
type Episode struct {
	ID                   uint   `gorm:"primaryKey;autoIncrement"`
	CourseID             uint   `gorm:"index:idx_course_sort;not null"`
	ChapterID            uint   `gorm:"index;not null;default:0"` // Belonging chapter, 0 means default/unassigned
	SortOrder            int    `gorm:"index:idx_course_sort;not null"`
	Title                string `gorm:"size:255;not null"`
	VideoRelativePath    string `gorm:"type:text;not null"`
	AttachmentJSON       string `gorm:"type:text"` // JSON array of attachments
	FileHash             string `gorm:"size:255;index"` // SHA1/MD5 for disaster recovery matching
	OriginalRelativePath string `gorm:"type:text"` // Original multi-layer path to prevent name collision
	FileSize             *int64 // Nullable file size in bytes
	DurationSeconds      *int   // Nullable video duration in seconds
	CreatedAt            time.Time
	UpdatedAt            time.Time
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

// AutoMigrate runs GORM schema auto-migration for all tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Setting{},
		&User{},
		&UserCourseAccess{},
		&Course{},
		&Chapter{},
		&Episode{},
		&Subtitle{},
		&AILessonContent{},
		&UserPoint{},
		&PointsLedger{},
		&UserProgress{},
	)
}
