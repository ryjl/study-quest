// Package spa embeds the built admin SPA so the Go server can serve the
// entire admin panel from a single binary without any external static files.
//
// Embedding strategy: `all:dist` matches the Vite build output
// (index.html + assets/*). dist/ is NOT committed (it's gitignored) and is
// empty on a fresh clone, so we also embed `_keep.go`-style sentinel files
// (`notbuilt.html`) that live in the package directory and ARE committed.
// This guarantees the embed directive always matches at least one file, even
// before `make build` has run — otherwise `go build` would fail with
// "no matching files found" on a fresh checkout.
//
// After a real build, dist/ contains index.html + assets/, and the server
// serves those. The notbuilt.html fallback is only shown when dist/index.html
// is missing.
package spa

import "embed"

// notbuilt.html is shown when the SPA hasn't been built yet (see router.go).
//
//go:embed notbuilt.html
var NotBuiltHTML []byte

// Assets holds the built SPA. May be empty on a fresh clone.
//
//go:embed all:dist
var Assets embed.FS
