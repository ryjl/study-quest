# StudyQuest Admin SPA

The modern admin panel for StudyQuest — a React 18 + TypeScript + Vite + Tailwind
CSS single-page application that is **embedded into the Go backend binary** via
`go:embed`. There is no separate web server: at runtime you visit
`http://<server-ip>:8080/admin` and the Go process serves the bundled SPA, the
`/admin/api/*` JSON endpoints, and the Flutter client's `/api/v1/*` endpoints
all from one binary on one port.

## Tech stack

- **React 18** + **React Router 6** — SPA routing with an `AuthGuard` that
  redirects unauthenticated users to `/admin/login`.
- **TanStack Query 5** — server state, optimistic updates, background refetch.
  No more `window.location.reload()` anywhere.
- **Tailwind CSS 3** — design tokens mirror `design.md §3` and the Flutter
  client's `theme.dart` (same dark palette + blue primary `#3B82F6`).
- **Recharts** — dashboard charts.
- **Vitest + Testing Library** — unit/component tests.

## Project layout

```
frontend-admin/
├── src/
│   ├── lib/
│   │   ├── api/            # endpoint client split by domain (auth.ts, courses.ts,
│   │   │                   # ai.ts, ...) aggregated as `export const api = {...}`.
│   │   │                   # Call sites use `api.foo()`; the split is navigability-only.
│   │   ├── types.ts        # snake_case API types + subject/grade metadata
│   │   ├── grades.ts       # grade presets + labels (open tag system)
│   │   ├── caches.ts       # runtime subject/tag caches
│   │   ├── format.ts       # duration / filesize / codec / date formatting
│   │   └── toast.tsx       # toast + confirm() providers (replaces alert/confirm)
│   ├── components/         # Layout, Modal, Drawer, TagInput, ImageUpload, ...
│   │   └── homework/       # homework sheet print view + preview modal + print CSS
│   └── pages/
│       ├── courses/        # the big one: CourseTree, CourseModal, editors, subtitles
│       ├── ai/             # AI workbench: object-as-navigation (course/student)
│       ├── ai-console/     # legacy AI console (regen columns, prompt config)
│       ├── users/          # user CRUD + per-user detail
│       ├── reading-room/   # reading resources
│       ├── Dashboard.tsx   # stats + charts
│       └── ...
└── vite.config.ts          # outDir → ../backend/internal/admin/spa/dist
```

## Development

```bash
# Install deps (one time)
cd frontend-admin && npm install

# Build the SPA → writes to backend/internal/admin/spa/dist
npm run build

# Run the Go backend (serves the freshly built SPA at /admin)
cd ../backend && go run cmd/server/main.go

# (Optional) Hot-reload dev server on :5171 that proxies API calls to :8080
npm run dev
```

> Production never runs `vite dev` — the dev server is an optional convenience
> for hot-reload during development. `make build` always produces a fully
> self-contained binary.

## Tests

```bash
npm test            # run once (112 tests across 10 files)
npm run test:watch  # watch mode
```

Go-side tests (`cd backend && go test ./...`) cover the snake_case DTOs, the
new episode aggregation queries, and the PATCH-style `UpdateEpisodeAdmin` that
preserves ffprobe media metadata.

## How it gets into the binary

```
frontend-admin/ ──npm run build──▶ backend/internal/admin/spa/dist/
                                            │
                      backend/internal/admin/spa/embed.go
                                            │  //go:embed all:dist
                                            ▼
                              gin serves /admin + /admin/assets/*
                              (deep links fall back to index.html)
```

If the SPA hasn't been built yet, visiting `/admin` returns a friendly
"Admin SPA 尚未构建" page instead of crashing — run `make build` (or
`cd frontend-admin && npm run build`) to fix it.

## Conventions

A short list of hard-won rules. Each exists because a real bug violated it.

### 1. After any mutation, invalidate the caches it changes

This is the single most important rule in this codebase. React Query is the
**immediate** source of truth for the UI — not cookies, not the DB, not "what
the server said last request." When an action changes server state, the
in-memory cache for that state is now *stale* and must be invalidated, or the
UI will keep reading the old value and behave as if the action never happened.

**The bug this comes from:** logging in set the `admin_session` cookie on the
server, but the SPA's `['me']` query was still cached as `authenticated:false`
(within the 10s `staleTime`). `navigate('/admin/')` re-mounted `AuthGuard`,
which read the stale `false` and bounced the user back to `/admin/login`.
Symptom: "I click login and nothing happens; after 4-5 tries I get in." The
fix was `qc.setQueryData(['me'], {authenticated:true})` + `invalidateQueries`
in `Login.tsx` before navigating.

**Rule:**
```ts
const mut = useMutation({
  mutationFn: api.someWrite,
  onSuccess: () => qc.invalidateQueries({ queryKey: ['the-data-it-changed'] }),
})
```

Applies to **every** write: login/logout (invalidate `['me']`), CRUD on
courses/tags/subjects/badges/users (invalidate the matching list key),
reorder, probe, settings save. When in doubt, invalidate more, not less.

### 2. SPA navigation ≠ page refresh

`navigate('/x')` swaps components within the same JS runtime; it does **not**
clear caches or re-establish "the world." Operations that change the
*identity* of the session (login, logout, switching admin account) should
either invalidate all derived caches or use a hard `window.location` reload.
Plain page navigation can stay a soft `navigate`.

### 3. Test the operation chain, not just the operation

A unit test asserting "clicking login calls `api.login`" passes even when
login is broken (the bug above did). Tests must cover the *chain*:
`login success → navigate → AuthGuard reads ['me'] → should be authenticated`.
For auth/stateful flows, render the whole routing tree with a real
`QueryClient` and assert the end state the user observes, not just which API
was called.

### 4. Module-level caches are not reactive — subscribe in render paths

`subjectMeta(key)` / `tagMeta` in `lib/types.ts` read a module-level cache that
`Layout` warms via `useSubjects()`/`useTags()`. That cache is fine for
non-React code paths, but **reading it during render is a race**: if your
component paints before the catalog query resolves, it gets the raw key
(`"english"`) + grey fallback, and filling the cache later does NOT re-render
your component (the cache isn't React state). The dashboard's subject
distribution chart had exactly this bug.

When you need catalog metadata in render, either:
- resolve from the reactive query (`useSubjects().data.find(...)`), so the
  component re-renders when the data lands; or
- for shared components, call the hook inside the component
  (`SubjectBadge` does this) so it's correct on every page.

`subjectMeta(key)` is acceptable only as a **fallback** (when the reactive list
hasn't matched) or outside React entirely.

