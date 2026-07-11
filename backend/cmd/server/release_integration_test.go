package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"studyquest/backend/internal/model"
)

// ──────────────────────────────────────────────────────────────────────────────
// These tests lock in the FROZEN client contract for APK OTA distribution.
// They exist so that a future refactor can't silently change the paths, query
// params, or response field names that already-shipped APKs depend on. If you
// legitimately must evolve the contract, do so by ADDING fields/params only.
// ──────────────────────────────────────────────────────────────────────────────

// TestOTALatestContract verifies the /api/v1/app/latest response shape and the
// "withdrawn build is hidden" rule, through the real router (no repo mocks).
func TestOTALatestContract(t *testing.T) {
	env := newTestEnv(t)

	// Seed: two active arm64-v8a builds (10, 12) and one withdrawn (13, higher).
	for _, r := range []model.AppRelease{
		{VersionCode: 10, VersionName: "1.0.0", ABI: "arm64-v8a", Filepath: "releases/10/arm64-v8a.apk", FileSize: 1000, IsActive: true, ReleaseNotes: "first"},
		{VersionCode: 12, VersionName: "1.2.0", ABI: "arm64-v8a", Filepath: "releases/12/arm64-v8a.apk", FileSize: 2000, ForceUpdate: true, IsActive: true, ReleaseNotes: "second"},
		{VersionCode: 13, VersionName: "1.3.0", ABI: "arm64-v8a", Filepath: "releases/13/arm64-v8a.apk", FileSize: 3000, IsActive: false, ReleaseNotes: "withdrawn"},
	} {
		if err := env.db.Create(&r).Error; err != nil {
			t.Fatalf("seed release vc=%d: %v", r.VersionCode, err)
		}
	}

	// Public request — no auth headers, no cookie.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/app/latest?abi=arm64-v8a&version_code=9", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("latest: expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Assert the EXACT frozen field set + values. These names are the public
	// contract — any change here breaks shipped clients.
	var got map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"version_code", "version_name", "abi", "force_update", "download_url", "download_size", "release_notes", "created_at"} {
		if _, ok := got[key]; !ok {
			t.Errorf("response missing frozen field %q", key)
		}
	}

	var resp struct {
		VersionCode  int    `json:"version_code"`
		VersionName  string `json:"version_name"`
		ABI          string `json:"abi"`
		ForceUpdate  bool   `json:"force_update"`
		DownloadURL  string `json:"download_url"`
		DownloadSize int64  `json:"download_size"`
		ReleaseNotes string `json:"release_notes"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	// Highest ACTIVE build is 12 — withdrawn 13 must be hidden.
	if resp.VersionCode != 12 {
		t.Errorf("version_code = %d, want 12 (13 is withdrawn)", resp.VersionCode)
	}
	if resp.ABI != "arm64-v8a" {
		t.Errorf("abi = %q, want arm64-v8a", resp.ABI)
	}
	if !resp.ForceUpdate {
		t.Error("force_update = false, want true")
	}
	// download_url is a RELATIVE path keyed on version_code+abi (not DB id),
	// so it survives a database rebuild.
	wantURL := "/api/v1/app/download?version_code=12&abi=arm64-v8a"
	if resp.DownloadURL != wantURL {
		t.Errorf("download_url = %q, want %q", resp.DownloadURL, wantURL)
	}
	if resp.DownloadSize != 2000 {
		t.Errorf("download_size = %d, want 2000", resp.DownloadSize)
	}
}

// TestOTALatestNoRelease: an ABI with no active build returns 404, which the
// client must treat as "up to date" (not an error).
func TestOTALatestNoRelease(t *testing.T) {
	env := newTestEnv(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/app/latest?abi=x86_64", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("latest for empty abi: expected 404, got %d", w.Code)
	}
}

// TestOTALatestRejectsBadABI guards against path traversal: abi becomes part of
// the on-disk filename, so anything outside the known set must be rejected.
func TestOTALatestRejectsBadABI(t *testing.T) {
	env := newTestEnv(t)
	for _, bad := range []string{"../../etc/passwd", "", "mips"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/app/latest?abi="+bad, nil)
		w := httptest.NewRecorder()
		env.engine.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Errorf("latest abi=%q: expected 400, got %d", bad, w.Code)
		}
	}
}

// TestOTADownloadStream verifies the download endpoint streams the actual APK
// bytes (not a JSON error) for an active build.
func TestOTADownloadStream(t *testing.T) {
	env := newTestEnv(t)

	// Seed record + a real file on disk at the deterministic path.
	if err := env.db.Create(&model.AppRelease{
		VersionCode: 5, VersionName: "1.0.5", ABI: "arm64-v8a",
		Filepath: "releases/5/arm64-v8a.apk", FileSize: 5, IsActive: true,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	dir := filepath.Join("./data/releases/5")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "arm64-v8a.apk"), []byte("APK!"), 0644); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll("./data/releases")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/app/download?version_code=5&abi=arm64-v8a", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("download: expected 200, got %d (body: %s)", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/vnd.android.package-archive" {
		t.Errorf("Content-Type = %q, want application/vnd.android.package-archive", ct)
	}
	if w.Body.String() != "APK!" {
		t.Errorf("download body = %q, want APK!", w.Body.String())
	}
}

// TestOTADownloadWithdrawnHidden: a withdrawn build (is_active=false) must not
// be downloadable — this is how a bad release is retracted in the field.
func TestOTADownloadWithdrawnHidden(t *testing.T) {
	env := newTestEnv(t)
	if err := env.db.Create(&model.AppRelease{
		VersionCode: 1, VersionName: "1.0.0", ABI: "arm64-v8a",
		Filepath: "releases/1/arm64-v8a.apk", IsActive: false,
	}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/app/download?version_code=1&abi=arm64-v8a", nil)
	w := httptest.NewRecorder()
	env.engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("download withdrawn: expected 404, got %d", w.Code)
	}
}
