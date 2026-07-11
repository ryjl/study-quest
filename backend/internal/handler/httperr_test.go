package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// TestRespondErrorMapping locks in the centralized error→status mapping so a
// future handler can't silently regress it (e.g. re-introduce a bare 500 for a
// not-found, or leak err.Error() internals).
func TestRespondErrorMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name       string
		err        error
		wantStatus int
	}{
		{"record-not-found → 404", gorm.ErrRecordNotFound, http.StatusNotFound},
		{"generic error → 500 (no leak)", errors.New("some internal SQL fragment"), http.StatusInternalServerError},
		{"nil → no-op (no write)", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
			respondError(c, tc.err)
			if tc.wantStatus == 0 {
				if w.Code != http.StatusOK {
					t.Errorf("nil error: expected no write (200 default), got %d", w.Code)
				}
				return
			}
			if w.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", w.Code, tc.wantStatus)
			}
			if w.Body.Len() == 0 {
				t.Error("expected non-empty error body")
			}
		})
	}
}

// TestRespondErrorNoLeak verifies a genuine internal error never surfaces its
// raw message (which could contain SQL/driver internals) to the client.
func TestRespondErrorNoLeak(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secret := "PG::connectdb: password authentication failed for user"
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)
	respondError(c, errors.New(secret))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status: got %d, want 500", w.Code)
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Errorf("internal error detail leaked to client: %s", w.Body.String())
	}
}
