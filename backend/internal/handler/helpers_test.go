package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestBindJSON locks in the bindJSON helper that replaced 60+ hand-rolled
// ShouldBindJSON + 400 blocks. Specifically guards the security contract:
// on binding failure the response is a generic "Invalid payload format" with
// NO echo of err.Error() (the previous hand-rolled variants leaked driver /
// binding internals on ~14 sites).
func TestBindJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("valid body binds and returns true", func(t *testing.T) {
		var req struct {
			Name string `json:"name"`
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/", jsonBody(`{"name":"ok"}`))

		if !bindJSON(c, &req) {
			t.Fatalf("bindJSON returned false on valid body")
		}
		if req.Name != "ok" {
			t.Fatalf("expected Name=ok, got %q", req.Name)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("expected no response written (200 default), got %d", w.Code)
		}
	})

	t.Run("malformed body returns 400 generic message, no err leak", func(t *testing.T) {
		var req struct {
			Name string `json:"name"`
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		// Invalid JSON — Gin's binding will produce an error whose .Error()
		// contains internal detail. bindJSON must NOT forward that to client.
		c.Request = httptest.NewRequest("POST", "/", jsonBody(`{not valid json`))

		if bindJSON(c, &req) {
			t.Fatalf("bindJSON returned true on invalid body")
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
		var resp map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response body not valid JSON: %v", err)
		}
		if resp["error"] != "Invalid payload format" {
			t.Fatalf("expected generic 'Invalid payload format', got %q (internal leak!)", resp["error"])
		}
	})

	t.Run("wrong types in body returns 400 generic message", func(t *testing.T) {
		var req struct {
			Count int `json:"count" binding:"required"`
		}
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		// 'count' is a string — binding fails on type mismatch.
		c.Request = httptest.NewRequest("POST", "/", jsonBody(`{"count":"not-a-number"}`))

		if bindJSON(c, &req) {
			t.Fatalf("bindJSON returned true on type-mismatched body")
		}
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", w.Code)
		}
	})
}

// TestParseLimit covers the ?limit=N parsing helper: defaults, clamping to
// [1, max], and graceful handling of garbage input.
func TestParseLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name    string
		query   string // full ?limit=... ("" = absent)
		def     int
		max     int
		want    int
	}{
		{"absent → default", "", 20, 200, 20},
		{"valid within range", "50", 20, 200, 50},
		{"at max boundary", "200", 20, 200, 200},
		{"over max → clamped to max", "500", 20, 200, 200},
		{"zero → default (not 0)", "0", 20, 200, 20},
		{"negative → default", "-5", 20, 200, 20},
		{"non-numeric → default", "abc", 20, 200, 20},
		{"empty string → default", "", 50, 100, 50},
		{"max=1 corner", "1", 20, 1, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			url := "/"
			if tc.query != "" {
				url += "?limit=" + tc.query
			}
			c.Request = httptest.NewRequest("GET", url, nil)
			got := parseLimit(c, tc.def, tc.max)
			if got != tc.want {
				t.Fatalf("parseLimit(def=%d, max=%d, q=%q) = %d, want %d",
					tc.def, tc.max, tc.query, got, tc.want)
			}
		})
	}
}

// jsonBody builds an *http.Request body from a raw JSON string.
func jsonBody(s string) *strings.Reader {
	return strings.NewReader(s)
}
