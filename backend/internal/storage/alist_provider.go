package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// AListProvider implements StorageProvider for AList v3 REST API.
type AListProvider struct {
	baseURL    string
	username   string
	password   string
	token      string
	basePath   string // AList user's base_path (e.g. /115/影视), fetched from /api/me
	httpClient *http.Client
}

// NewAListProvider creates a new instance of AListProvider.
func NewAListProvider(baseURL, username, password, token string) *AListProvider {
	// Ensure base URL doesn't have trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")
	p := &AListProvider{
		baseURL:  baseURL,
		username: username,
		password: password,
		token:    token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	if token != "" {
		p.fetchBasePath()
	}
	return p
}

func (a *AListProvider) Type() string { return "alist" }

// Response structures for AList API
type alistResponse struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type alistFileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
}

type alistListResult struct {
	Content []alistFileInfo `json:"content"`
	Total   int64           `json:"total"`
}

type alistGetResult struct {
	Name   string            `json:"name"`
	RawURL string            `json:"raw_url"`
	Sign   string            `json:"sign"`
	Header json.RawMessage   `json:"header"`
}

func (a *AListProvider) login() error {
	reqBody := map[string]string{
		"username": a.username,
		"password": a.password,
	}
	
	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal login body failed: %w", err)
	}

	url := fmt.Sprintf("%s/api/auth/login", a.baseURL)
	resp, err := a.httpClient.Post(url, "application/json", bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("login http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read login response failed: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login HTTP error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	var alistResp alistResponse
	if err := json.Unmarshal(respBody, &alistResp); err != nil {
		return fmt.Errorf("unmarshal login response wrapper failed: %w", err)
	}

	if alistResp.Code != 200 {
		return fmt.Errorf("alist login API error: code %d, message: %s", alistResp.Code, alistResp.Message)
	}

	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(alistResp.Data, &result); err != nil {
		return fmt.Errorf("unmarshal login token failed: %w", err)
	}

	a.token = result.Token

	// Fetch user's base_path from /api/me
	a.fetchBasePath()

	return nil
}

// fetchBasePath retrieves the authenticated user's base_path from AList /api/me.
// AList users may have a base_path like "/115/影视" which means /api/fs paths are
// relative to that base, but /d/ download URLs require the absolute path.
func (a *AListProvider) fetchBasePath() {
	if a.token == "" {
		return
	}

	url := fmt.Sprintf("%s/api/me", a.baseURL)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", a.token)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	var alistResp alistResponse
	if err := json.Unmarshal(respBody, &alistResp); err != nil {
		return
	}
	if alistResp.Code != 200 {
		return
	}

	var meResult struct {
		BasePath string `json:"base_path"`
	}
	if err := json.Unmarshal(alistResp.Data, &meResult); err != nil {
		return
	}

	a.basePath = strings.TrimSuffix(meResult.BasePath, "/")
}

func (a *AListProvider) request(method, path string, body interface{}, out interface{}) error {
	return a.requestWithUA(method, path, body, out, "")
}

func (a *AListProvider) requestWithUA(method, path string, body interface{}, out interface{}, userAgent string) error {
	// If token is missing, but username and password are provided, attempt dynamic login
	if a.token == "" && a.username != "" && a.password != "" {
		if err := a.login(); err != nil {
			return fmt.Errorf("auto-login failed: %w", err)
		}
	} else if a.basePath == "" && a.token != "" {
		a.fetchBasePath()
	}

	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body failed: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	url := fmt.Sprintf("%s%s", a.baseURL, path)
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("new request failed: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if a.token != "" {
		req.Header.Set("Authorization", a.token)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body failed: %w", err)
	}

	// Handle token expiration/invalid token (Code 401)
	var alistResp alistResponse
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(respBody, &alistResp); err == nil && alistResp.Code == 401 && a.username != "" && a.password != "" {
			// Try login again
			if err := a.login(); err == nil {
				// Re-execute request once
				return a.requestWithUA(method, path, body, out, userAgent)
			}
		}
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http error: status %d, body: %s", resp.StatusCode, string(respBody))
	}

	if err := json.Unmarshal(respBody, &alistResp); err != nil {
		return fmt.Errorf("unmarshal response wrapper failed: %w", err)
	}

	if alistResp.Code != 200 {
		return fmt.Errorf("alist API error: code %d, message: %s", alistResp.Code, alistResp.Message)
	}

	if out != nil {
		if err := json.Unmarshal(alistResp.Data, out); err != nil {
			return fmt.Errorf("unmarshal response data failed: %w", err)
		}
	}

	return nil
}

func (a *AListProvider) ListDir(path string) ([]FileInfo, error) {
	apiPath := a.getRelativePath(path)
	reqBody := map[string]interface{}{
		"path":     apiPath,
		"password": "",
		"page":     1,
		"per_page": 0, // 0 usually fetches all or default limit
		"refresh":  false,
	}

	var result alistListResult
	if err := a.request("POST", "/api/fs/list", reqBody, &result); err != nil {
		return nil, err
	}

	files := make([]FileInfo, 0, len(result.Content))
	for _, f := range result.Content {
		// Construct path for items
		itemPath := strings.TrimSuffix(path, "/") + "/" + f.Name
		if path == "/" {
			itemPath = "/" + f.Name
		}

		files = append(files, FileInfo{
			Name:     f.Name,
			Path:     itemPath,
			Size:     f.Size,
			IsDir:    f.IsDir,
			Modified: f.Modified,
		})
	}
	return files, nil
}

func (a *AListProvider) GetFileInfo(path string) (*FileInfo, error) {
	apiPath := a.getRelativePath(path)
	reqBody := map[string]interface{}{
		"path":     apiPath,
		"password": "",
	}

	var f alistFileInfo
	if err := a.request("POST", "/api/fs/get", reqBody, &f); err != nil {
		return nil, err
	}

	return &FileInfo{
		Name:     f.Name,
		Path:     path,
		Size:     f.Size,
		IsDir:    f.IsDir,
		Modified: f.Modified,
	}, nil
}

func (a *AListProvider) GetDownloadURL(path string, userAgent string) (*DownloadLink, error) {
	apiPath := a.getRelativePath(path)
	reqBody := map[string]interface{}{
		"path":     apiPath,
		"password": "",
	}

	var result alistGetResult
	if err := a.requestWithUA("POST", "/api/fs/get", reqBody, &result, userAgent); err != nil {
		return nil, err
	}

	absolutePath := a.getAbsolutePath(path)

	escapedPath := url.PathEscape(absolutePath)
	escapedPath = strings.ReplaceAll(escapedPath, "%2F", "/")
	if !strings.HasPrefix(escapedPath, "/") {
		escapedPath = "/" + escapedPath
	}

	signedURL := fmt.Sprintf("%s/d%s", a.baseURL, escapedPath)
	if result.Sign != "" {
		signedURL = fmt.Sprintf("%s?sign=%s", signedURL, result.Sign)
	}

	headers := make(map[string]string)
	if len(result.Header) > 0 {
		var parsed map[string]string
		if err := json.Unmarshal(result.Header, &parsed); err == nil && parsed != nil {
			headers = parsed
		}
	}

	return &DownloadLink{
		URL:    signedURL,
		Header: headers,
	}, nil
}

func (a *AListProvider) getRelativePath(path string) string {
	if a.basePath != "" && strings.HasPrefix(path, a.basePath) {
		rel := strings.TrimPrefix(path, a.basePath)
		if !strings.HasPrefix(rel, "/") {
			rel = "/" + rel
		}
		return rel
	}
	return path
}

func (a *AListProvider) getAbsolutePath(path string) string {
	if a.basePath != "" && !strings.HasPrefix(path, a.basePath) {
		return a.basePath + "/" + strings.TrimPrefix(path, "/")
	}
	return path
}

func (a *AListProvider) Ping() error {
	// Ping by listing the root directory
	_, err := a.ListDir("/")
	return err
}
