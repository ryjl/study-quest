# CLAUDE.md

Guidance for AI assistants (and humans) working in this repo. Start here;
drill into the linked docs when you need depth.

## What this is

StudyQuest ("学途奇旅") — a video-learning platform for young students. Three
parts in one repo:

| Part | Path | Stack |
|------|------|-------|
| Backend | `backend/` | Go + Gin + GORM, SQLite, `go:embed`s the admin SPA |
| Admin panel | `frontend-admin/` | React 18 + TS + Vite + TanStack Query + Tailwind |
| Student client | `frontend/` | Flutter (Android/iOS/desktop) |

Single binary at runtime: the Go process serves `/admin` (SPA), `/admin/api/*`
(admin JSON), and `/api/v1/*` (Flutter client) on one port.

## Build / test / run

```bash
make build          # build backend (embeds a fresh admin SPA build)
make build-admin    # build just the admin SPA → backend/internal/admin/spa/dist
make run            # build + run the server
make test           # backend Go tests (./...)
cd frontend-admin && npm test     # admin SPA tests (vitest)
cd frontend-admin && npx tsc --noEmit   # type-check admin without emitting
cd frontend && flutter analyze    # student client
```

Always run `make test` before declaring backend work done. The admin SPA has
a pre-existing failing suite (`TagInput.test.tsx` — missing
`QueryClientProvider`) that fails on a clean tree too; it's not your
regression unless you touched `TagInput`.

## Hard-won rules (each exists because a bug violated it)

### Frontend: after any mutation, invalidate the caches it changes

React Query is the *immediate* source of truth for the UI, not the server.
Login, logout, CRUD, reorder, probe, settings save — every write must
`invalidateQueries` (or `setQueryData`) for the keys it affects, or the UI
keeps reading stale data. This rule lives in detail at
`frontend-admin/README.md §Conventions` with the login-bounce bug it came from.

```ts
const mut = useMutation({
  mutationFn: api.someWrite,
  onSuccess: () => qc.invalidateQueries({ queryKey: ['the-data-it-changed'] }),
})
```

### Backend: business-timezone math goes through `appclock`

"Today / yesterday / late-night hour / consecutive days" are computed in ONE
fixed business timezone (Asia/Shanghai), never via `time.Now()` directly or
SQLite's `'localtime'` modifier. Those two diverged inside containers and
silently zeroed streaks. Storage stays UTC; only human-date semantics convert
via `appclock` (which is injectable for tests). See
`backend/internal/appclock/clock.go`. The SQL offset trick
(`datetime(t, '+08:00')`) is documented in `badge_repo.go`.

### Backend: watch_seconds accumulation must be atomic

`progress_service.ReportProgress` writes watch time via a single
`INSERT ... ON CONFLICT DO UPDATE` (`UpsertAndAccumulateWatch`), NOT a
read-modify-write. The old path lost deltas under concurrent reports (player
timer vs quiz ping) and the admin "learning time" column sat at 0. If you
touch progress reporting, preserve the atomic-increment invariant.

### Backend: system-seeded rows (IsSystem=true) are not deletable

Subjects, Tags, and Badges seeded by `SeedDefault*` carry `IsSystem=true` and
the service `Delete` returns `ErrSystemProtected` (→ HTTP 403). They may be
renamed/recolored but never deleted. `main.go:markSystemDefaults` backfills
the flag on pre-existing installs. Never add a delete path that bypasses this.

## Where things live (quick map)

- **Time/timezone:** `backend/internal/appclock/` (the one business zone) + its
  uses in `badge_repo.go`, `admin_handler.go` (today cutoff),
  `episode_repo.go`/`progress_repo.go` (recent-N-day windows).
- **Badge rules:** single-rule (`RuleType/Target/Threshold`) and composite
  (`RuleJSON`, an AND/OR tree) both evaluated in
  `service/badge_service.go EvaluateRules`. Composite fails closed on bad JSON.
- **Admin DTOs:** `backend/internal/handler/admin_dto.go`. snake_case JSON for
  the SPA; the Flutter client uses camelCase via separate handlers.
- **Admin SPA auth:** `AuthGuard` in `App.tsx` reads the `['me']` query;
  `Login.tsx` invalidates it on success (see the frontend rule above).
- **Embedded SPA:** `backend/internal/admin/spa/embed.go` (`//go:embed dist/*`).
  If `/admin` shows "SPA 尚未构建", run `make build` or `make build-admin`.

## Deeper docs

- `design.md` — overall architecture & design tokens
- `docs/architecture.md` — backend layering
- `docs/adr.md` — architecture decision records
- `TESTING_PLAN.md` — coverage gaps & integration-test design
- `frontend-admin/README.md` — SPA conventions (mutate→invalidate etc.)
