package storage

import (
	"time"
)

// FileInfo represents file or directory metadata retrieved from the storage source.
type FileInfo struct {
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
}

// DownloadLink represents the result containing a download URL and additional headers required for retrieval.
type DownloadLink struct {
	URL    string            `json:"url"`
	Header map[string]string `json:"header,omitempty"`
}

// StorageProvider is the unified interface abstraction for interacting with remote file storage.
type StorageProvider interface {
	// ListDir returns file info lists inside a directory path.
	ListDir(path string) ([]FileInfo, error)

	// GetFileInfo returns metadata for a single file path.
	GetFileInfo(path string) (*FileInfo, error)

	// GetDownloadURL generates the link (possibly with headers) for streaming.
	GetDownloadURL(path string, userAgent string) (*DownloadLink, error)

	// Type returns the name of this provider (e.g. "alist", "webdav").
	Type() string

	// Ping checks if the storage source is reachable and authenticated.
	Ping() error
}
