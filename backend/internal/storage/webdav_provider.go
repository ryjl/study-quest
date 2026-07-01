package storage

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/studio-b12/gowebdav"
)

// WebDAVProvider implements StorageProvider for standard WebDAV protocol.
type WebDAVProvider struct {
	client   *gowebdav.Client
	baseURL  string // absolute url to webdav end point (e.g. http://localhost:5244/dav/)
	username string
	password string
}

// NewWebDAVProvider creates a new instance of WebDAVProvider.
func NewWebDAVProvider(baseURL, username, password string) *WebDAVProvider {
	// Ensure base URL has trailing slash
	if !strings.HasSuffix(baseURL, "/") {
		baseURL = baseURL + "/"
	}
	client := gowebdav.NewClient(baseURL, username, password)
	return &WebDAVProvider{
		client:   client,
		baseURL:  baseURL,
		username: username,
		password: password,
	}
}

func (w *WebDAVProvider) SupportsHash() bool { return false }
func (w *WebDAVProvider) Type() string       { return "webdav" }

func (w *WebDAVProvider) ListDir(path string) ([]FileInfo, error) {
	// Normalize path for gowebdav
	path = strings.TrimPrefix(path, "/")

	infos, err := w.client.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("webdav readdir failed: %w", err)
	}

	files := make([]FileInfo, 0, len(infos))
	for _, info := range infos {
		name := info.Name()
		// Reconstruct file path
		itemPath := "/" + strings.TrimSuffix(path, "/") + "/" + name
		if path == "" {
			itemPath = "/" + name
		}

		files = append(files, FileInfo{
			Name:     name,
			Path:     itemPath,
			Size:     info.Size(),
			IsDir:    info.IsDir(),
			Modified: info.ModTime(),
			Hash:     "", // WebDAV doesn't support file hash natively
		})
	}
	return files, nil
}

func (w *WebDAVProvider) GetFileInfo(path string) (*FileInfo, error) {
	path = strings.TrimPrefix(path, "/")

	info, err := w.client.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("webdav stat failed: %w", err)
	}

	name := info.Name()
	if name == "" {
		// Extract name from path ifStat returns empty name for root or certain paths
		parts := strings.Split(strings.TrimSuffix(path, "/"), "/")
		if len(parts) > 0 {
			name = parts[len(parts)-1]
		}
	}

	return &FileInfo{
		Name:     name,
		Path:     "/" + path,
		Size:     info.Size(),
		IsDir:    info.IsDir(),
		Modified: info.ModTime(),
		Hash:     "",
	}, nil
}

func (w *WebDAVProvider) GetDownloadURL(path string) (*DownloadLink, error) {
	// Clean relative path prefix
	cleanPath := strings.TrimPrefix(path, "/")

	// We embed Basic Auth credentials in the URL to allow client players to stream without prompting.
	u, err := url.Parse(w.baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid webdav base URL: %w", err)
	}

	if w.username != "" {
		u.User = url.UserPassword(w.username, w.password)
	}

	// Join base path with target file path
	u.Path = u.Path + cleanPath

	return &DownloadLink{
		URL: u.String(),
	}, nil
}

func (w *WebDAVProvider) Ping() error {
	_, err := w.client.ReadDir("")
	return err
}
