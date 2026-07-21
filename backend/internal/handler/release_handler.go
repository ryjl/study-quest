package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"github.com/gin-gonic/gin"
)

// ──────────────────────────────────────────────────────────────────────────────
// FROZEN CLIENT CONTRACT — do not change these endpoints' paths, query params,
// or response field names/types once shipped. Old APKs depend on them forever.
//
//   GET /api/v1/app/latest?abi=<abi>&version_code=<n>   [public, no auth]
//   GET /api/v1/app/download?version_code=<n>&abi=<abi> [public, no auth]
//
// Evolve the contract by ADDING fields only. Never rename, retype, or remove
// an existing field, and never key a lookup off the DB primary key.
// ──────────────────────────────────────────────────────────────────────────────

// SupportedABIs is the closed set of Android ABIs we distribute. The download /
// latest endpoints reject anything outside it (defends against path traversal:
// the ABI becomes part of the on-disk filename, so it must be a known literal).
var SupportedABIs = map[string]bool{
	"arm64-v8a":   true,
	"armeabi-v7a": true,
	"x86_64":      true,
}

// releasesDir is the on-disk root for APK files, relative to the server CWD.
// Each build lives at releasesDir/<version_code>/<abi>.apk — a deterministic
// path so files survive a database rebuild (the contract keys on version_code
// + abi, not on the DB row).
const releasesDir = "./data/releases"

// maxApkSize caps an uploaded APK at 500 MB. Real StudyQuest APKs are tens of
// MB; anything larger is either a mistake or abuse. Guards the server against
// disk exhaustion from a runaway upload.
const maxApkSize = 500 * 1024 * 1024

// ReleaseHandler serves the OTA client contract (public) and the admin release
// management API (auth-protected).
type ReleaseHandler interface {
	// ── Frozen client contract (public) ──
	GetLatest(c *gin.Context)
	Download(c *gin.Context)

	// ── Admin management ──
	List(c *gin.Context)
	Upload(c *gin.Context)
	Update(c *gin.Context)
	Delete(c *gin.Context)
}

type releaseHandler struct {
	repo repository.ReleaseRepository
}

// NewReleaseHandler creates an instance of ReleaseHandler.
func NewReleaseHandler(repo repository.ReleaseRepository) ReleaseHandler {
	return &releaseHandler{repo: repo}
}

// GetLatest GET /api/v1/app/latest?abi=<abi>&version_code=<n>
//
// Returns the newest active build for the requested ABI. The version_code query
// param is OPTIONAL — it is accepted for forward-compat (e.g. "only tell me
// about builds newer than what I have"); today the server always returns the
// absolute latest active build and the client decides whether to upgrade.
//
// Response (FROZEN — add fields, don't change existing ones):
//   {version_code, version_name, abi, force_update, download_url,
//    download_size, release_notes, created_at}
//
// 404 when no active build exists for the ABI — clients treat this as "no
// update" and must not surface an error to the user.
func (h *releaseHandler) GetLatest(c *gin.Context) {
	abi := strings.TrimSpace(c.Query("abi"))
	if !SupportedABIs[abi] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported or missing abi"})
		return
	}

	rel, err := h.repo.FindLatest(abi)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query latest release"})
		return
	}
	if rel == nil {
		// No active build for this ABI. Per the contract this is 404, which the
		// client silently interprets as "up to date".
		c.JSON(http.StatusNotFound, gin.H{"error": "no release available for this abi"})
		return
	}

	c.JSON(http.StatusOK, h.toClientDTO(rel))
}

// Download GET /api/v1/app/download?version_code=<n>&abi=<abi>
//
// Streams the APK bytes. Keyed on version_code + abi (semantic identifiers),
// never on the DB id. Withdrawn (is_active=false) builds return 404 so a
// retracted version becomes un-downloadable, while already-downloaded copies
// are unaffected.
func (h *releaseHandler) Download(c *gin.Context) {
	vc, abi, ok := parseVersionABI(c)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or missing version_code/abi"})
		return
	}

	rel, err := h.repo.FindByVersionAndABI(vc, abi)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query release"})
		return
	}
	if rel == nil || !rel.IsActive {
		c.JSON(http.StatusNotFound, gin.H{"error": "release not found"})
		return
	}

	fullPath := filepath.Join(releasesDir, strconv.Itoa(vc), abi+".apk")
	if _, err := os.Stat(fullPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "release file missing"})
		return
	}

	// Content-Disposition carries a friendly filename derived from the version.
	c.FileAttachment(fullPath, fmt.Sprintf("study-quest-%s-%s.apk", rel.VersionName, abi))
}

// ── Admin management ──────────────────────────────────────────────────────────

// List GET /admin/api/releases — all builds, newest version_code first.
func (h *releaseHandler) List(c *gin.Context) {
	rels, err := h.repo.FindAll()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list releases"})
		return
	}
	out := make([]adminReleaseDTO, 0, len(rels))
	for _, r := range rels {
		out = append(out, h.toAdminDTO(&r))
	}
	c.JSON(http.StatusOK, out)
}

// Upload POST /admin/api/releases/upload (multipart/form-data)
//
// Form fields: file (apk), version_name, version_code, abi, force_update,
// release_notes. If (version_code, abi) already exists, the file is replaced
// and the DB row updated — this is the "re-upload a fixed build for the same
// version+abi" path. SHA256 + file size are computed server-side (never trust
// client-supplied values for integrity data).
func (h *releaseHandler) Upload(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no apk file uploaded"})
		return
	}
	// Reject oversized uploads before touching disk.
	if file.Size > maxApkSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": fmt.Sprintf("APK 太大 (%d 字节)，上限 500 MB", file.Size),
		})
		return
	}

	versionCode, err := strconv.Atoi(strings.TrimSpace(c.PostForm("version_code")))
	if err != nil || versionCode <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_code must be a positive integer"})
		return
	}
	abi := strings.TrimSpace(c.PostForm("abi"))
	if !SupportedABIs[abi] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported abi; must be one of arm64-v8a, armeabi-v7a, x86_64"})
		return
	}
	versionName := strings.TrimSpace(c.PostForm("version_name"))
	if versionName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "version_name is required"})
		return
	}
	forceUpdate := strings.ToLower(strings.TrimSpace(c.PostForm("force_update"))) == "true"
	releaseNotes := c.PostForm("release_notes")

	// Deterministic path: data/releases/<version_code>/<abi>.apk
	dir := filepath.Join(releasesDir, strconv.Itoa(versionCode))
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create release dir"})
		return
	}
	dst := filepath.Join(dir, abi+".apk")

	// Save to a temp file first, then compute hash, then promote. Avoids a
	// half-written file being served if hashing fails mid-way.
	tmp, err := os.CreateTemp(dir, ".upload-*.tmp")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create temp file"})
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op if we renamed it
	if err := tmp.Close(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to stage upload"})
		return
	}
	if err := c.SaveUploadedFile(file, tmpPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save apk"})
		return
	}

	sum, size, err := hashAndSize(tmpPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash apk"})
		return
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to finalize apk"})
		return
	}

	relPath := fmt.Sprintf("releases/%d/%s.apk", versionCode, abi)

	// Upsert: replace an existing (version_code, abi) row, or create a new one.
	existing, err := h.repo.FindByVersionAndABI(versionCode, abi)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to check existing release"})
		return
	}
	if existing != nil {
		existing.VersionName = versionName
		existing.Filepath = relPath
		existing.FileSize = size
		existing.SHA256 = sum
		existing.ReleaseNotes = releaseNotes
		existing.ForceUpdate = forceUpdate
		existing.IsActive = true
		if err := h.repo.Update(existing); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update release"})
			return
		}
		c.JSON(http.StatusOK, h.toAdminDTO(existing))
		return
	}

	rel := &model.AppRelease{
		VersionCode:  versionCode,
		VersionName:  versionName,
		ABI:          abi,
		Filepath:     relPath,
		FileSize:     size,
		SHA256:       sum,
		ReleaseNotes: releaseNotes,
		ForceUpdate:  forceUpdate,
		IsActive:     true,
	}
	if err := h.repo.Create(rel); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create release"})
		return
	}
	c.JSON(http.StatusOK, h.toAdminDTO(rel))
}

// Update PUT /admin/api/releases/:id — edit metadata or toggle active/force.
// Body may include any of: release_notes, force_update, is_active. version_code
// and abi are immutable (they are the identity of a released build; clients may
// already have cached a reference to them).
func (h *releaseHandler) Update(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid release id"})
		return
	}
	rel, err := h.repo.FindByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query release"})
		return
	}
	if rel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "release not found"})
		return
	}

	var req struct {
		ReleaseNotes *string `json:"release_notes"`
		ForceUpdate  *bool   `json:"force_update"`
		IsActive     *bool   `json:"is_active"`
	}
	if !bindJSON(c, &req) { return }
	if req.ReleaseNotes != nil {
		rel.ReleaseNotes = *req.ReleaseNotes
	}
	if req.ForceUpdate != nil {
		rel.ForceUpdate = *req.ForceUpdate
	}
	if req.IsActive != nil {
		rel.IsActive = *req.IsActive
	}
	if err := h.repo.Update(rel); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update release"})
		return
	}
	c.JSON(http.StatusOK, h.toAdminDTO(rel))
}

// Delete DELETE /admin/api/releases/:id — removes the DB row AND the apk file.
func (h *releaseHandler) Delete(c *gin.Context) {
	id, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid release id"})
		return
	}
	rel, err := h.repo.FindByID(uint(id))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to query release"})
		return
	}
	if rel == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "release not found"})
		return
	}

	// Remove the apk file. Best-effort — a missing file shouldn't block the
	// DB cleanup. We recompute the path from (version_code, abi) rather than
	// trusting rel.Filepath, so delete stays correct even if the stored path is
	// stale (the contract guarantees the on-disk location is deterministic).
	onDisk := filepath.Join(releasesDir, strconv.Itoa(rel.VersionCode), rel.ABI+".apk")
	_ = os.Remove(onDisk)
	if err := h.repo.Delete(uint(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete release"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "deleted"})
}

// ── DTOs ──────────────────────────────────────────────────────────────────────

// latestReleaseDTO is the FROZEN /api/v1/app/latest response. Every field's
// json key is part of the public client contract — add fields only, never
// rename/retype/remove. The struct (vs the old gin.H map) makes the field set
// explicit and gives compile-time visibility of the contract surface.
type latestReleaseDTO struct {
	VersionCode  int       `json:"version_code"`
	VersionName  string    `json:"version_name"`
	ABI          string    `json:"abi"`
	ForceUpdate  bool      `json:"force_update"`
	DownloadURL  string    `json:"download_url"`
	DownloadSize int64     `json:"download_size"`
	ReleaseNotes string    `json:"release_notes"`
	CreatedAt    time.Time `json:"created_at"`
}

// adminReleaseDTO is the /admin/api/releases list/upload/update shape.
type adminReleaseDTO struct {
	ID           uint      `json:"id"`
	VersionCode  int       `json:"version_code"`
	VersionName  string    `json:"version_name"`
	ABI          string    `json:"abi"`
	FileSize     int64     `json:"file_size"`
	SHA256       string    `json:"sha256"`
	ReleaseNotes string    `json:"release_notes"`
	ForceUpdate  bool      `json:"force_update"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
}

// toClientDTO builds the FROZEN client-contract response. Field names are part
// of the public API — do not rename. New fields may be appended.
func (h *releaseHandler) toClientDTO(m *model.AppRelease) latestReleaseDTO {
	return latestReleaseDTO{
		VersionCode:  m.VersionCode,
		VersionName:  m.VersionName,
		ABI:          m.ABI,
		ForceUpdate:  m.ForceUpdate,
		DownloadURL:  fmt.Sprintf("/api/v1/app/download?version_code=%d&abi=%s", m.VersionCode, m.ABI),
		DownloadSize: m.FileSize,
		ReleaseNotes: m.ReleaseNotes,
		CreatedAt:    m.CreatedAt,
	}
}

func (h *releaseHandler) toAdminDTO(m *model.AppRelease) adminReleaseDTO {
	return adminReleaseDTO{
		ID:           m.ID,
		VersionCode:  m.VersionCode,
		VersionName:  m.VersionName,
		ABI:          m.ABI,
		FileSize:     m.FileSize,
		SHA256:       m.SHA256,
		ReleaseNotes: m.ReleaseNotes,
		ForceUpdate:  m.ForceUpdate,
		IsActive:     m.IsActive,
		CreatedAt:    m.CreatedAt,
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// parseVersionABI extracts & validates version_code (positive int) + abi (known
// literal) from the query string. Returns ok=false on any failure.
func parseVersionABI(c *gin.Context) (int, string, bool) {
	vc, err := strconv.Atoi(strings.TrimSpace(c.Query("version_code")))
	if err != nil || vc <= 0 {
		return 0, "", false
	}
	abi := strings.TrimSpace(c.Query("abi"))
	if !SupportedABIs[abi] {
		return 0, "", false
	}
	return vc, abi, true
}

// hashAndSize returns the SHA256 hex digest and byte length of a file.
func hashAndSize(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
