package ai

import (
	"path/filepath"
	"strings"
)

// joinPath joins path elements into a clean slash-separated path. A thin
// wrapper around filepath.Join that also normalizes leading/trailing slashes,
// used so config-derived model paths stay consistent across the package without
// each call site repeating filepath.Join + TrimRight.
func joinPath(elem ...string) string {
	return filepath.Clean(strings.Join(elem, string(filepath.Separator)))
}
