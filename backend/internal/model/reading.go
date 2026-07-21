package model

import (
	"strings"
	"time"
)

// Code split from models.go for navigability. See models.go for the
// package overview.

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
// 2026-07-20: 改开放 tag 后调用 gradeLabelForDisplay —— 预设值本地化、
// 自定义 tag 原样输出、legacy 数字按"N年级"格式。
func readingGradeDisplay(gs []Grade) string {
	for _, g := range gs {
		if g == GradeUniversal {
			return "全学段通用"
		}
	}
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, gradeLabelForDisplay(string(g)))
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

func (s ReadingSeries) GradeDisplay() string { return readingGradeDisplay(readingSeriesGrades(s.Grades)) }

func (b ReadingBook) GradeDisplay() string { return readingGradeDisplay(readingBookGrades(b.Grades)) }

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
