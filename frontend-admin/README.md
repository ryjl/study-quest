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
  client's `theme.dart` (same dark palette + purple primary).
- **Recharts** — dashboard charts.
- **Vitest + Testing Library** — unit/component tests.

## Project layout

```
frontend-admin/
├── src/
│   ├── lib/
│   │   ├── api.ts          # typed fetch wrapper + all /admin/api/* calls
│   │   ├── types.ts        # snake_case API types + subject/grade metadata
│   │   ├── format.ts       # duration / filesize / codec / date formatting
│   │   └── toast.tsx       # toast + confirm() providers (replaces alert/confirm)
│   ├── components/         # Layout, Modal, Drawer, TagInput, ImageUpload, ...
│   └── pages/
│       ├── courses/        # the big one: CourseTree, CourseModal, editors, subtitles
│       ├── Dashboard.tsx   # stats + charts
│       ├── Users.tsx       # CRUD + per-user detail drawer (ledger + badges + access)
│       ├── Badges.tsx      # badge wall + rule-engine editor
│       ├── Import.tsx      # 3-step netdisk import wizard
│       └── Settings.tsx    # storage config + connection test + probe trigger
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
npm test            # run once (format, api, TagInput — 41 tests)
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
                                            │  //go:embed dist/*
                                            ▼
                              gin serves /admin + /admin/assets/*
                              (deep links fall back to index.html)
```

If the SPA hasn't been built yet, visiting `/admin` returns a friendly
"Admin SPA 尚未构建" page instead of crashing — run `make build` (or
`cd frontend-admin && npm run build`) to fix it.
