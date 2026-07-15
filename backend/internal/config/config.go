package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the simple bootstrap configuration for the server.
type Config struct {
	ServerAddr string
	DBPath     string

	// SessionTTL is how long a login session stays valid. Sessions are fixed-TTL
	// (no sliding renewal); once expired the client must log in again. Default
	// 30 days matches a "log in once a month" cadence for a family device.
	SessionTTL time.Duration

	// IngestKey gates the /api/v1/ingest/* endpoints used by the local Python
	// toolchain. Empty = endpoints are public (LAN-only / backward compatible).
	// Set it before exposing the backend to a public network.
	IngestKey string

	// TrustedProxies is the comma-separated CIDR/host list Gin trusts for
	// X-Forwarded-For resolution. Defaults to loopback (a local caddy/nginx).
	// Affects rate-limiting IP isolation; use c.ClientIP(), never c.RemoteIP().
	TrustedProxies []string

	// WatchMergeWindow is how long a gap between two heartbeats is still
	// considered "the same continuous viewing session" and merged into one
	// WatchEvent row. Larger = fewer rows but pauses inside the window get
	// folded into the row's wall-clock span (DurationSeconds still excludes
	// them). 0 disables merging (every heartbeat = a new row). Default 60s.
	WatchMergeWindow time.Duration

	// AIModelsDir is the directory holding the local ONNX embedding artifacts
	// (libonnxruntime.so, bge model_quantized.onnx, vocab.txt). Fetched by
	// `make fetch-ai-models`. Boot-time only (changing it needs a restart), so
	// it lives in env rather than the ai_providers table. Default points at the
	// conventional backend/data/ai-models location. The AI subsystem degrades
	// gracefully if the dir is missing/empty: embedding is unavailable but the
	// server still starts and chat-only AI still works.
	AIModelsDir string
}

// LoadConfig reads configuration from environment variables with safe defaults.
func LoadConfig() *Config {
	serverAddr := os.Getenv("SERVER_ADDR")
	if serverAddr == "" {
		serverAddr = "0.0.0.0:8080"
	}

	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "./data/studyquest.db"
	}

	// Default 30 days. 0 or negative disables expiry (not recommended).
	ttlHours := 720
	if v := os.Getenv("SESSION_TTL_HOURS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ttlHours = n
		}
	}

	var proxies []string
	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		for _, p := range strings.Split(v, ",") {
			if s := strings.TrimSpace(p); s != "" {
				proxies = append(proxies, s)
			}
		}
	} else {
		// Default: trust only loopback (local reverse proxy).
		proxies = []string{"127.0.0.1", "::1"}
	}

	// Default 60s window for watch-event merging. 0 disables merging.
	mergeWindowSec := 60
	if v := os.Getenv("WATCH_MERGE_WINDOW"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			mergeWindowSec = n
		}
	}

	aiModelsDir := os.Getenv("AI_MODELS_DIR")
	if aiModelsDir == "" {
		aiModelsDir = "./data/ai-models"
	}

	return &Config{
		ServerAddr:      serverAddr,
		DBPath:          dbPath,
		SessionTTL:      time.Duration(ttlHours) * time.Hour,
		IngestKey:       os.Getenv("INGEST_KEY"),
		TrustedProxies:  proxies,
		WatchMergeWindow: time.Duration(mergeWindowSec) * time.Second,
		AIModelsDir:     aiModelsDir,
	}
}
