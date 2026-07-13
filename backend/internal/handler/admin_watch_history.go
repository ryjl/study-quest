package handler

import (
	"net/http"
	"time"

	"studyquest/backend/internal/appclock"

	"github.com/gin-gonic/gin"
)

// watchHistoryDayDTO is one cell of the month heatmap: a business-calendar day
// and the total watch duration (seconds) the user accrued that day.
type watchHistoryDayDTO struct {
	Date    string `json:"date"`    // YYYY-MM-DD (business zone)
	Seconds int64  `json:"seconds"` // sum of duration_seconds across that day's events
}

// watchEventDTO is one row of the selected-day detail timeline.
type watchEventDTO struct {
	ID              uint      `json:"id"`
	EpisodeID       uint      `json:"episode_id"`
	EpisodeTitle    string    `json:"episode_title"`
	CourseID        uint      `json:"course_id"`
	CourseTitle     string    `json:"course_title"`
	ContentType     string    `json:"content_type"` // learning | entertainment
	StartedAt       time.Time `json:"started_at"`
	EndedAt         time.Time `json:"ended_at"`
	DurationSeconds int       `json:"duration_seconds"`
}

// GetUserWatchHistory GET /admin/api/users/:id/watch-history?from=YYYY-MM-DD&to=YYYY-MM-DD
// Returns the per-day watch-duration totals for a user across [from, to), used
// to color the month heatmap. `from`/`to` are business-calendar day strings;
// they default to the first/last day of the current business-zone month.
func (h *adminHandler) GetUserWatchHistory(c *gin.Context) {
	if h.watchEventRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "watch history not configured"})
		return
	}
	userID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	now := appclock.Now()
	fromStr := c.DefaultQuery("from", "")
	toStr := c.DefaultQuery("to", "")

	var from, to time.Time
	if fromStr != "" {
		if t, perr := time.ParseInLocation("2006-01-02", fromStr, appclock.Zone()); perr == nil {
			from = t
		}
	}
	if toStr != "" {
		if t, perr := time.ParseInLocation("2006-01-02", toStr, appclock.Zone()); perr == nil {
			// `to` is exclusive: caller passes the day AFTER the last wanted.
			// If they pass the last day inclusively, advance one so it's covered.
			to = t.AddDate(0, 0, 1)
		}
	}
	// Default to current business-zone month if either bound is missing.
	if from.IsZero() {
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, appclock.Zone())
	}
	if to.IsZero() {
		to = from.AddDate(0, 1, 0) // first day of next month
	}

	durMap, err := h.watchEventRepo.DailyDurationsInRange(userID, from, to)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load watch history"})
		return
	}

	// Emit a row for every day in [from, to), zero-filled, so the heatmap grid
	// (which expects a contiguous month) doesn't have to fill gaps client-side.
	out := make([]watchHistoryDayDTO, 0, 31)
	for d := from; d.Before(to); d = d.AddDate(0, 0, 1) {
		ds := d.Format("2006-01-02")
		out = append(out, watchHistoryDayDTO{Date: ds, Seconds: durMap[ds]})
	}
	c.JSON(http.StatusOK, out)
}

// GetUserWatchEvents GET /admin/api/users/:id/watch-events?day=YYYY-MM-DD
// Returns the user's watch events for a single business-calendar day, ascending
// by StartedAt, with episode + course titles denormalized for display.
func (h *adminHandler) GetUserWatchEvents(c *gin.Context) {
	if h.watchEventRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "watch history not configured"})
		return
	}
	userID, err := parseUintParam(c, "id")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}
	day := c.Query("day")
	if day == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing day (YYYY-MM-DD)"})
		return
	}

	events, err := h.watchEventRepo.ListByUserAndDay(userID, day)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to load watch events"})
		return
	}

	// Batch-load titles: collect distinct episode + course ids, fetch them in
	// one pass each, then map onto the events. Avoids N+1 queries for a busy
	// day with many episodes.
	episodeIDs := make(map[uint]struct{})
	courseIDs := make(map[uint]struct{})
	for _, e := range events {
		episodeIDs[e.EpisodeID] = struct{}{}
		courseIDs[e.CourseID] = struct{}{}
	}
	episodeTitles := make(map[uint]string, len(episodeIDs))
	for id := range episodeIDs {
		if ep, eerr := h.episodeRepo.FindByID(id); eerr == nil && ep != nil {
			episodeTitles[id] = ep.Title
		}
	}
	courseTitles := make(map[uint]string, len(courseIDs))
	for id := range courseIDs {
		if co, cerr := h.courseRepo.FindByID(id); cerr == nil && co != nil {
			courseTitles[id] = co.Title
		}
	}

	out := make([]watchEventDTO, 0, len(events))
	for _, e := range events {
		out = append(out, watchEventDTO{
			ID:              e.ID,
			EpisodeID:       e.EpisodeID,
			EpisodeTitle:    episodeTitles[e.EpisodeID],
			CourseID:        e.CourseID,
			CourseTitle:     courseTitles[e.CourseID],
			ContentType:     e.ContentType,
			StartedAt:       e.StartedAt,
			EndedAt:         e.EndedAt,
			DurationSeconds: e.DurationSeconds,
		})
	}
	c.JSON(http.StatusOK, out)
}
