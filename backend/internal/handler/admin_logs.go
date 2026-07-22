package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// admin_logs.go — structured-log admin read API (TODO.md P1). Mirrors
// admin_ai_jobs.go's ListAIRuns: thin handler over aiService.ListLogEntries,
// which returns enriched views (episode/course titles) for the /admin/logs page.

// ListLogs returns recent log_entries, optionally filtered by ?level= and/or
// ?source= and/or ?job_id=. Newest first. The page is a tail view of operational
// events (job failures, reaper runs, polish telemetry, provider errors, worker
// panics) — see service.appendLog for the write sites.
//
// GET /admin/api/logs?level=error&source=ai_worker&job_id=42&limit=100
func (h *adminHandler) ListLogs(c *gin.Context) {
	if h.aiService == nil {
		// AI subsystem off — render an empty list so the page doesn't error.
		c.JSON(http.StatusOK, []any{})
		return
	}
	level := c.Query("level")   // "" = all (info|warn|error)
	source := c.Query("source") // "" = all (ai_worker|reaper|polish|provider|...)
	limit := parseLimit(c, 100, 500)

	var jobID *uint
	if jidStr := c.Query("job_id"); jidStr != "" {
		jid, err := strconv.ParseUint(jidStr, 10, 64)
		if err == nil && jid > 0 {
			u := uint(jid)
			jobID = &u
		}
	}

	views, err := h.aiService.ListLogEntries(level, source, jobID, limit)
	if err != nil {
		respondError(c, err)
		return
	}
	c.JSON(http.StatusOK, views)
}
