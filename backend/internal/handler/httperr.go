package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ──────────────────────────────────────────────────────────────────────────────
// Centralized service-error → HTTP mapping.
//
// Before this existed, every handler did its own `errors.Is` chain and most
// service/repo errors were blindly turned into `500 {error: err.Error()}` —
// which leaked internal detail (SQL fragments, driver messages) to clients and
// collapsed "not found", "conflict", "validation", and genuine failures all
// into the same status. respondError is the single funnel.
//
// Mapping rules:
//   • ErrSystemProtected → 403 (delete of a system-seeded row is refused)
//   • ErrSubjectInUse    → 409 (FK RESTRICT: still referenced)
//   • gorm.ErrRecordNotFound → 404
//   • anything else      → 500, with the raw error logged server-side but a
//     generic message returned to the client (no internal leak)
//
// The service-layer sentinels are registered here via the httpErrRegistry so
// the mapping table stays in ONE place — handlers no longer repeat the same
// `if errors.Is(...) { c.JSON(403, "...") }` block. New domain errors get
// registered by the package that owns them via registerAppError (see init in
// this file and errors.go adapter).
// ──────────────────────────────────────────────────────────────────────────────

// appError maps a known sentinel error to an HTTP status + client message.
type appError struct {
	status  int
	message string
}

// httpErrRegistry holds sentinel→appError mappings. Looked up via errors.Is so
// it matches wrapped sentinels too.
var httpErrRegistry = map[error]appError{}

// registerAppError associates a sentinel error with an HTTP status and a
// client-facing message. Called from init() blocks; the mapping is fixed at
// startup.
func registerAppError(sentinel error, status int, message string) {
	httpErrRegistry[sentinel] = appError{status: status, message: message}
}

// respondError maps a service/repository error to an HTTP response and writes
// it. Use it as the single error-exit point from a handler when the error came
// from the service or repository layer. For input-validation errors (bad IDs,
// missing fields) keep using a direct c.JSON(400, ...) — those aren't service
// errors and have handler-specific messages.
func respondError(c *gin.Context, err error) {
	if err == nil {
		return
	}

	// Check registered domain sentinels first.
	for sentinel, ae := range httpErrRegistry {
		if errors.Is(err, sentinel) {
			c.JSON(ae.status, gin.H{"error": ae.message})
			return
		}
	}

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "资源不存在"})
	default:
		// Genuine failure: log the detail server-side, return a generic
		// message so we never leak SQL/driver internals to the client.
		log.Printf("handler error: %s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "服务器内部错误"})
	}
}

func init() {
	// Register the known domain sentinels. These mirror the service-package
	// sentinels; the service package is imported by handler (no cycle), so we
	// reference them directly below — see service_errors_init.go for the
	// registrations that depend on the service package.
}

// bindJSON unifies the c.ShouldBindJSON + 400 pattern that was duplicated 60+
// times across handlers with 6 different (sometimes internal-detail-leaking)
// error messages. Returns true on success; on failure it has already written
// the 400 response, so callers just `if !bindJSON(c, &req) { return }`.
//
// The error message is deliberately generic ("Invalid payload format") and
// does NOT include err.Error() — leaking binding/driver internals to the
// client was a recurring red line violation (see the header comment above).
func bindJSON(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload format"})
		return false
	}
	return true
}

// parseUintParam reads a path parameter as a uint32. Returns an error
// (without writing a response) so the caller can pick the right status /
// message for its context. Most callers follow up with respondError or a
// direct 400.
func parseUintParam(c *gin.Context, name string) (uint, error) {
	id, err := strconv.ParseUint(c.Param(name), 10, 32)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

// parseLimit extracts a ?limit=N query parameter clamped to [1, max],
// falling back to def when absent or invalid. Replaces 4 hand-rolled
// variants that disagreed on max, error handling, and default.
func parseLimit(c *gin.Context, def, max int) int {
	raw := c.Query("limit")
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

