package model

import (
	"strings"
	"time"
)

// Code split from models.go for navigability. See models.go for the
// package overview.

type Subject struct {
	ID        uint      `gorm:"primaryKey;autoIncrement"`
	Key       string    `gorm:"size:100;uniqueIndex;not null"` // stable identifier + icon lookup key, e.g. "math"
	Label     string    `gorm:"size:100;not null"`             // display name, e.g. "数学"
	Color     string    `gorm:"size:32"`                       // hex e.g. "#f59e0b"
	SortOrder int       `gorm:"default:0"`
	IsSystem  bool      `gorm:"default:false"` // true = seeded default, protected from deletion
	// Category 区分 subject 用途:"academic"(学术学科,默认)或"entertainment"
	// (娱乐子类如动画片/电影/纪录片/综艺)。让 admin CourseModal 按 content type
	// 过滤科目下拉(学习课只选学术科目,娱乐课只选娱乐子类),也避免"动画片"这种
	// 娱乐子类出现在学习大厅的科目过滤里。Course.ContentType 仍是功能开关
	// (是否计时长/badge/AI),Category 只是分类标签。default 'academic' 让
	// schema 迁移时老行自动归到学术类。
	Category string `gorm:"size:20;default:'academic'"`
	// AIConfigJSON 存学科级默认 AI 配置(同 Course.AIConfigJSON 的模式)。课程级
	// AIConfig 对应字段为空时,Effective* 方法回退到这里,让自定义学科也有默认 prompt。
	// 解析/序列化走 AIConfig()/SetAIConfig()。
	AIConfigJSON string `gorm:"column:ai_config_json;type:text"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
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
	// AIConfigJSON holds the course's AI configuration as a single JSON blob
	// (see AIConfig / EffectiveWhisperHint / EffectiveQuizHint below). Storing
	// the whole AI config in ONE text column means we can add new config knobs
	// (difficulty bias, question-type mix, language prefs, …) WITHOUT a schema
	// migration — just extend the AIConfig struct + the admin form. Mirrors the
	// forward-compatible design used by Question.Scoring.
	//
	// JSON shape: {"whisper_hint":"...", "quiz_hint":"...", ...future fields}.
	// Empty string = no AI config (the default for courses that never had one).
	AIConfigJSON string `gorm:"column:ai_config_json;type:text"`
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

// courseGradeDisplay formats a grade set for the UI. 2026-07-20 grade 改开放
// tag 后:预设值用 PresetGradeLabel 本地化(如 primary→"小学"),自定义 tag
// 原样输出。Universal 单独处理为"全学段通用"。历史上 "1"-"9" 这种纯数字
// 兼容值仍走数字+年级拼接(向后兼容已有 DB 数据)。
func courseGradeDisplay(gs []CourseGrade) string {
	for _, g := range gs {
		if g.Grade == GradeUniversal {
			return "全学段通用"
		}
	}
	parts := make([]string, 0, len(gs))
	for _, g := range gs {
		parts = append(parts, gradeLabelForDisplay(string(g.Grade)))
	}
	return strings.Join(parts, ", ")
}

// gradeLabelForDisplay renders a single grade tag value for the UI:
//   - Preset values (primary/junior/senior/adult) → localized Chinese label.
//   - Pure-digit values (1-9 legacy) → "<n>年级" for backward compat.
//   - Universal → "通用".
//   - Anything else (custom tags like "考研","职场") → as-is.
func gradeLabelForDisplay(s string) string {
	if label := PresetGradeLabel(Grade(s)); label != "" {
		return label
	}
	if len(s) > 0 && strings.Trim(s, "0123456789") == "" {
		return s + "年级"
	}
	return s
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
	// IsBitmap is set only for subtitle streams. true = bitmap/picture-based
	// subtitle codec (PGS / VOBSUB / DVB), which ffmpeg CANNOT transcode to a
	// text format like WebVTT — extraction is impossible and the admin must use
	// Whisper transcription instead. false for text-based subtitles (srt/ass/
	// mov_text/webvtt/...) and for non-subtitle streams.
	IsBitmap bool `json:"is_bitmap"`
}

// Subtitle holds subtitle content stored as WebVTT internally.
//
// Storage format is ALWAYS VTT (VttContent) so the playback path can serve it
// directly without per-request conversion, and embedded subtitles can preserve
// their inline styling. The AI path converts to SRT on the fly via
// ai.VttToSrt() — AI doesn't need styles, and existing SRT parsers/segmenters
// stay untouched.
//
// The worker protocol (subtitle_job_handler.Complete) and the admin manual
// upload still accept SRT; SaveSubtitle converts SRT→VTT before persisting,
// so callers that already have SRT don't need to change.
//
// Source tracks origin so the polish pipeline can decide whether to run:
//   - "whisper"        — machine-transcribed, needs polish
//   - "embedded"       — extracted from the video container (usually human-made)
//   - "manual"         — admin-uploaded or auto-matched from disk
//   - "llm_optimized"  — set by the polish pipeline after a successful run
//
// Optimized is flipped to true by the polish pipeline once it has rewritten
// VttContent with corrected homophones/terminology. False means raw.
//
// RawVttContent is the immutable snapshot of the original subtitle taken at
// Complete time. The polish pipeline NEVER touches it — it only overwrites
// VttContent. This is what makes polish retryable / reversible: admin can
// always see (or restore) the pre-polish text, and re-running polish starts
// from the same baseline each time instead of compounding drift.
// Empty when the row predates the field (legacy data) — treat empty as
// "raw unknown, fall back to VttContent".
//
// IsPrimary marks the subtitle the AI pipeline (segment/summary/quiz) reads.
// A multilingual episode may have several subtitle rows (zh-CN, en-US, ...);
// only the primary one feeds the AI chain. Set by whoever creates the row
// (whisper/embedded extractor/manual upload); the first subtitle for an
// episode becomes primary by default, others are non-primary. Playback is
// unaffected — the player lists all subtitle tracks and lets the user pick.
type Subtitle struct {
	ID            uint    `gorm:"primaryKey;autoIncrement"`
	EpisodeID     uint    `gorm:"index:idx_episode_lang;not null"`
	Episode       Episode `gorm:"foreignKey:EpisodeID;constraint:OnDelete:CASCADE"`
	Language      string  `gorm:"size:50;index:idx_episode_lang;not null;default:'zh-CN'"` // e.g. zh-CN, en-US, bi
	Label         string  `gorm:"size:100;not null;default:'中文'"` // User-facing label
	VttContent    string  `gorm:"type:text;not null"`               // Stored as WebVTT (was SrtContent)
	RawVttContent string  `gorm:"type:text"`                         // Original snapshot, never overwritten by polish
	Source        string  `gorm:"size:32;not null;default:'whisper'"` // whisper / embedded / manual / llm_optimized
	Optimized     bool    `gorm:"not null;default:false"`             // flipped by polish pipeline
	IsPrimary     bool    `gorm:"not null;default:false"`             // AI chain reads primary only
	CreatedAt     time.Time
	UpdatedAt     time.Time
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
