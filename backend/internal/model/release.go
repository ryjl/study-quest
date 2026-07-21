package model

import "time"

// Code split from models.go for navigability. See models.go for the
// package overview.

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
