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
make build-apk      # Flutter fat APK (multi-ABI); see also build-apk-arm64/-arm/-x64

cd frontend-admin && npm test              # admin SPA tests (vitest)
cd frontend-admin && npx tsc --noEmit      # type-check admin without emitting
cd frontend && flutter analyze             # student client
cd frontend && flutter test                # dart tests
```

Always run `make test` before declaring backend work done.

## Code layout (orient yourself here before editing)

**Backend (`backend/internal/`)** — Go package per concern:
- `model/` — GORM models split by domain (`identity.go`, `grade.go`,
  `content.go`, `progress.go`, `unlock.go`, `reading.go`, `watch.go`,
  `ai.go`, `release.go`); `migrate.go` holds `AutoMigrate`. `models.go` is
  just the index/overview.
- `handler/` — Gin HTTP handlers. Big topics are sub-filed (`admin_ai_*.go`,
  `admin_reading_*.go`, `admin_content` → `admin_{course,episode,chapter,subtitle}.go`).
  Shared helpers (`bindJSON`, `parseUintParam`, `parseLimit`, `respondError`)
  live in `httperr.go`.
- `service/` — business logic. `ai_service*.go` is split into
  `{_advice,_quiz,_course_summary,_user_report,_polish,_jobs,_naming}.go` +
  the core `ai_service.go` (interface/struct/worker/segment+summary runners).
- `repository/` — GORM queries. `ai_content_repo.go` is the interface +
  constructor; method bodies live in `ai_{chunk,summary,job,quiz,memory,advice}_repo.go`.
- `media/` — ffmpeg/ffprobe wrappers (probe, screenshot, cover extraction,
  retry-on-transient-network-error).
- `ai/` — LLM provider plumbing and prompt construction (`agent/`, `polish/`).
- `appclock/` — the single business-timezone indirection (see rule below).

**Admin SPA (`frontend-admin/src/`):**
- `lib/api/` — endpoint client split by domain (`auth.ts`, `courses.ts`,
  `ai.ts`, ...) aggregated by `lib/api/` 域聚合 (拆分后) as `export const api = {...}`.
  Call sites always use `api.foo()`; the split is navigability-only.
- `lib/types.ts` is pure interfaces; runtime subject/tag caches live in
  `lib/caches.ts` and grade presets/labels in `lib/grades.ts`.
- Big pages are folder-per-screen (`pages/users/`, `pages/ai-console/regen/`,
  `pages/reading-room/`, `pages/courses/`).

**Flutter client (`frontend/lib/`):**
- `model/` / `service/` / `ui/{screen,widget,ai}/` layering.
- `service/api_service.dart` is the sole HTTP surface (plus deliberate
  bypasses in `update_service.dart` and `pdf_reader_screen.dart` for OTA /
  book-stream flows that run outside the auth envelope).
- `ui/widget/` holds reusable widgets; screen-specific extracted widgets
  live alongside the screen (e.g. `ui/widget/player/helper_panel.dart`).

## Hard-won rules (each exists because a bug violated it)

> Full details and other pitfalls in `docs/pitfalls/`. Only the most critical
> 6 rules live here.

1. **Cross-layer contract changes cannot be parallelized.** Go `json:` tags ↔
   TS interfaces ↔ Dart classes are an implicit hand-maintained 3-way contract
   (no codegen). Never spawn parallel agents on a refactor that touches the
   contract layer. When in doubt, the model/DTO layer is owned by the main
   session. **After any model change, run `make test`, not just `go build`.**

2. **Admin SPA: every mutation must `invalidateQueries`.** React Query is the
   *immediate* source of truth for the UI, not the server. Login, logout,
   CRUD, reorder, probe, settings save — every write must invalidate the keys
   it changes or the UI keeps reading stale data.

3. **Backend: business-timezone math goes through `appclock`.** "Today /
   yesterday / late-night hour / consecutive days" are computed in ONE fixed
   business timezone (Asia/Shanghai), never via `time.Now()` directly or
   SQLite's `'localtime'` modifier. Those two diverged inside containers and
   silently zeroed streaks.

4. **APK OTA: `/api/v1/app/*` contract is FROZEN.** Already-shipped APKs
   depend on these endpoints forever. Never key off DB primary key (use
   `(version_code, abi)`); response fields are add-only; endpoints stay
   public. Regression-tested by `release_integration_test.go`.

5. **Bump `version_code` (the `+N` in `pubspec.yaml`) on every release**, or
   clients won't see the new version via OTA.

6. **AI is a pure附加层.** If no provider is configured or the course has AI
   disabled, system behavior is identical to pre-AI. Clients treat 404 as
   "no AI data" and hide the cards.

## Where things live (quick map)

- **Time/timezone:** `backend/internal/appclock/` (the one business zone).
- **Badge rules:** `service/badge_service.go EvaluateRules`. Composite fails
  closed on bad JSON.
- **Admin DTOs:** `backend/internal/handler/admin_dto.go`. snake_case JSON
  for SPA; Flutter client uses PascalCase via separate handlers.
- **Admin SPA auth:** `AuthGuard` in `App.tsx` reads the `['me']` query.
- **Embedded SPA:** `backend/internal/admin/spa/embed.go` (`//go:embed all:dist`).
- **APK OTA:** `release_handler.go` + `release_repo.go` + admin `Releases.tsx`
  + Flutter `update_service.dart`.
- **AI 配置 (5 维度 + 学科默认):** `model.AIConfig` stored as
  `Course.AIConfigJSON` / `Subject.AIConfigJSON`. Priority:
  `Course.EffectiveXxxHint(subject)` Course > Subject > legacy `AIHint` column.
  admin edits at SubjectModal (subject default) and CourseModal (course override).
- **AI Prompt 可观测性:** `model.AIRun.SystemPromptText/UserPromptText` records
  every LLM call's final prompts. `POST /admin/api/ai/courses/:id/preview-prompt`
  builds (without calling LLM) the exact prompt for tuning.

## Deeper docs

- **整体架构** → `docs/architecture.md`
- **踩坑详情** → `docs/pitfalls/{backend,frontend,tools}.md`
- **模块深度** → `docs/modules/<module>/`（ai/storage/frontend）
- **开发环境 / 调试** → `docs/dev-setup.md`
- **待办 idea** → `TODO.md`

## 文档约定

- **Handoff/交接文档不进 git。** 它们是会话级产物，完成后失效。统一命名前缀
  `handoff-*.md`，`.gitignore` 已覆盖 `docs/**/handoff-*.md`。需要留交接给下个
  会话时，文件名以 `handoff-` 开头放任意位置都不会污染 repo。
- **设计文档反映"已落地"状态。** 未实现的部分标 ⏳。完成的功能不要留"未来计划"
  语气（容易误导读者以为还没做）。
- **改代码后同步对应 `docs/modules/` 文档。** 改 model 字段名、拆包、改端点路径
  时，PR review 时检查 `docs/` 里是否有引用过期了。
