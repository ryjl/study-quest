package model

import "time"

// Code split from models.go for navigability. See models.go for the
// package overview.

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
