package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// dto_contract_test.go — cross-layer contract guard (P3).
//
// The Go admin/client DTOs, the TS interfaces (frontend-admin/src/lib/types.ts),
// and the Dart classes (frontend/lib/model/) are a HAND-MAINTAINED 3-way
// contract (no codegen). TestCourseDTOContracts (api_integration_test.go) locks
// the Go decode side but nothing catches it when the Go side RENAMES a field
// and the TS/Dart types drift.
//
// This file closes that gap for the highest-traffic endpoint (courses): it
// COMPARES the live admin + client /courses list responses against GOLDEN
// snapshots committed in testdata/. The admin SPA test (frontend-admin) loads
// the same snapshots and asserts its TS interfaces can deserialize them. So a
// Go DTO field rename breaks BOTH tests: this one (snapshot mismatch — review
// the diff) and the TS one (interface no longer matches the regenerated file
// unless the TS follows).
//
// To regenerate after an INTENTIONAL DTO change: run
//   go test ./cmd/server/ -run TestContract_CoursesGolden -update
// then commit the new testdata/*.json + update the TS interface + Dart model.

// goldenDir is where snapshots live (cmd/server/testdata/).
const goldenDir = "testdata"

// updateGolden is set via -update to refresh the committed snapshots. The
// default (compare) is the trap; -update is the escape hatch for intentional
// contract changes.
var updateGolden = flag.Bool("update", false, "regenerate golden DTO snapshots")

// compareOrUpdateGolden compares data to testdata/<name>; fails on mismatch
// unless -update, in which case it writes data and notes the regeneration.
// Both sides are normalized via normalizeForCompare first to scrub
// run-to-run-volatile values (timestamps) so the golden stays stable.
func compareOrUpdateGolden(t *testing.T, name string, data []byte) {
	t.Helper()
	path := filepath.Join(goldenDir, name)
	normalized := normalizeForCompare(data)
	if *updateGolden {
		if err := os.MkdirAll(goldenDir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", goldenDir, err)
		}
		if err := os.WriteFile(path, normalized, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		t.Logf("regenerated golden: %s (%d bytes)", path, len(normalized))
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (if intentional, re-run with -update)", path, err)
	}
	wantNorm := normalizeForCompare(want)
	if !bytesEqual(wantNorm, normalized) {
		t.Errorf("DTO contract drift for %s:\n--- committed golden ---\n%s\n--- live response ---\n%s\n"+
			"If this DTO change is intentional: re-run with -update, commit the new golden, "+
			"and update frontend-admin/src/lib/types.ts + frontend/lib/model/ to follow.",
			path, string(wantNorm), string(normalized))
	}
}

// tsPattern matches RFC3339-ish timestamps ("2026-08-02T11:14:12Z"). We scrub
// these to "<TS>" so the committed golden doesn't flip on every run (the
// created_at/updated_at columns are UTC-now at insert time, inherently volatile).
// The contract we're guarding is FIELD NAMES + presence, not the clock.
var tsPattern = regexp.MustCompile(`"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}[^"]*"`)
var tsPlaceholder = []byte(`"<TS>"`)

// normalizeForCompare scrubs volatile timestamp values to a fixed placeholder
// and compact-whitespace-trims the JSON so byte-compare is stable.
func normalizeForCompare(data []byte) []byte {
	scrubed := tsPattern.ReplaceAll(data, tsPlaceholder)
	// Re-format through a JSON round-trip for stable key ordering + spacing
	// (the server emits compact JSON, but a future gin change could add
	// whitespace; don't let that cause a false drift). Errors fall back to the
	// scrubbed bytes. We disable HTML escaping so the "<TS>" placeholder stays
	// readable (json.Marshal would emit "\u003cTS\u003e").
	var v any
	if json.Unmarshal(scrubed, &v) == nil {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err == nil {
			// Encode appends a trailing newline; trim it for a clean golden.
			out := bytes.TrimRight(buf.Bytes(), "\n")
			return out
		}
	}
	return scrubed
}

// bytesEqual is a plain compare (avoid pulling bytes/encoding for one call).
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestContract_CoursesGolden compares the admin + client /courses list
// responses against committed golden files. The seeded course ("契约快照") has
// a grade + tag so the snapshot exercises the full field set (grade_display,
// tags_list, tag_ids). A field rename or removal in the Go DTO breaks this test
// — the failure message explains the -update workflow.
func TestContract_CoursesGolden(t *testing.T) {
	env := newTestEnv(t)
	tag := env.findTagID(t, "required")
	cid := env.createCourse(t, "契约快照", "math", []uint{tag})

	// --- admin side: snake_case ---
	adminResp := env.do(t, http.MethodGet, "/admin/api/courses", nil)
	if adminResp.Code != 200 {
		t.Fatalf("admin list courses: %d %s", adminResp.Code, adminResp.Body.String())
	}
	compareOrUpdateGolden(t, "courses-admin.json", adminResp.Body.Bytes())

	// --- client side: PascalCase ---
	uid := env.createUser(t, "snapshot-student", "student")
	env.grantAccess(t, uid, cid)
	clientResp := env.doAsUser(t, uid, http.MethodGet, "/api/v1/courses", nil)
	if clientResp.Code != 200 {
		t.Fatalf("client list courses: %d %s", clientResp.Code, clientResp.Body.String())
	}
	compareOrUpdateGolden(t, "courses-client.json", clientResp.Body.Bytes())
}
