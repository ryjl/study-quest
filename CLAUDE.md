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
> 7 rules live here.

1. **Cross-layer contract changes cannot be parallelized.** Go `json:` tags ↔
   TS interfaces ↔ Dart classes are an implicit hand-maintained 3-way contract
   (no codegen). Never spawn parallel agents on a refactor that touches the
   contract layer. When in doubt, the model/DTO layer is owned by the main
   session. **After any model change, run `make test`, not just `go build`.**

2. **Admin SPA: every mutation must `invalidateQueries`.** React Query is the
   *immediate* source of truth for the UI, not the server. Login, logout,
   CRUD, reorder, probe, settings save — every write must invalidate the keys
   it changes or the UI keeps reading stale data.

3. **时间处理铁律：存储永远 UTC，只有人类日期语义才走 `appclock`。**
   这个规则违反过太多次，每次都是隐性 bug + 定位极难。三条子规则：

   **(a) 数据库时间戳永远是 UTC。** 写库时用 `time.Now().UTC()` 或 SQLite
   `CURRENT_TIMESTAMP`（它本身就是 UTC），**绝不用裸 `time.Now()`**（它是
   Go 进程的本地时区，容器里通常 UTC、宿主机可能是 +08，不一致）。三层防护
   缺一不可，都已落地：① DSN 带 `_loc=UTC`（`cmd/server/main.go` + 所有测试
   DB 经 `testutil.GormConfig()`）；② GORM `NowFunc` 返回 `time.Now().UTC()`
   （auto-managed `CreatedAt`/`UpdatedAt` 由此走 UTC）；③ repository/service
   里**每一处**写库的 `time.Now()` 都显式 `.UTC()`。回归保护见
   `repository/reaper_timezone_test.go`（业务层：reaper cutoff vs UTC
   claimed_at）和 `repository/timezone_storage_test.go`（存储层 round-trip +
   同行两种写法一致）。**删任何一处 `.UTC()` 都要审——它不是冗余，是防线。**

   **(b) 业务日期语义（今天是周几、几点解锁、连续学习几天）走 `appclock`。**
   `appclock.Now()` / `appclock.In(t)` 把 UTC 存储时间转成业务时区
   (Asia/Shanghai) 再算 Weekday/Hour/calendar-day。`unlock_service.go` 是正例
   （L349-434 全用 `appclock.In`）。绝不在 service 层用 `time.Now()` 算"今天"。

   **(c) 任何 `time.Now()` 出现在 repository/service 层都要审。** 问自己：
   这是存储时间戳吗？（→ 用 `time.Now().UTC()`）还是业务日期语义？（→ 用
   `appclock`）。不确定时，宁可 `time.Now().UTC()`——至少和 `CURRENT_TIMESTAMP`
   一致，不会产生时区 mismatch。

   **血泪案例**：job reaper (`ai_job_repo.ReapStaleJobs`) 用 `time.Now().Add(-30min)`
   算 cutoff，但 `claimed_at` 是 `CURRENT_TIMESTAMP`（UTC）。+08 生产环境下
   cutoff 比 claimed_at 大 8 小时，导致刚 claim 的 job 每 5 分钟被误 reap，
   polish（2-7 分钟）永远跑不完。症状是"polish 没跑"，实际是 reaper 把它
   反复杀掉了。修复：cutoff 改 `time.Now().UTC().Add(-30min)`。该 bug 暴露后，
   全项目时区用法已系统性清理（DSN `_loc=UTC` + `NowFunc` UTC + 所有写库
   `time.Now().UTC()`），见本规则 (a) 的三层防护。

4. **APK OTA: `/api/v1/app/*` contract is FROZEN.** Already-shipped APKs
   depend on these endpoints forever. Never key off DB primary key (use
   `(version_code, abi)`); response fields are add-only; endpoints stay
   public. Regression-tested by `release_integration_test.go`.

5. **Bump `version_code` (the `+N` in `pubspec.yaml`) on every release**, or
   clients won't see the new version via OTA.

6. **AI is a pure附加层.** If no provider is configured or the course has AI
   disabled, system behavior is identical to pre-AI. Clients treat 404 as
   "no AI data" and hide the cards.

7. **Bug 当轮就修，不要因为"可能是小问题"就拖到下次。** review 标出来的问题
   （哪怕是 MAJOR 不是 BLOCKER、哪怕"终态正确"、哪怕看起来像 UX 瑕疵）都是真
   bug，用户看得到、会困惑。别用"小问题""可观测性层面"给自己找台阶。踩坑细节
   和具体修法进 `docs/pitfalls/`，不堆在 CLAUDE.md。

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
