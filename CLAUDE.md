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

Always run `make test` before declaring backend work done. The admin SPA's
`TagInput.test.tsx` mocks its data hook (`useTags`) so it runs without a
`QueryClientProvider` and passes on a clean tree — historical warnings to
the contrary are stale.

## Code layout (orient yourself here before editing)

**Backend (`backend/internal/`)** — Go package per concern:
- `model/` — every GORM model. Split by domain (`identity.go`, `grade.go`,
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
  `ai.ts`, ...) aggregated by `lib/api.ts` as `export const api = {...}`.
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

### APK OTA: the `/api/v1/app/*` client contract is FROZEN

`/api/v1/app/latest?abi=&version_code=` and `/api/v1/app/download?version_code=&abi=`
are the self-update channel. They are the most stability-sensitive endpoints in
the codebase because already-shipped APKs depend on them **forever** — if a path,
query param, or response field changes, those APKs can no longer find updates and
are stranded on their build. Rules:

- **Never** key a lookup off the DB primary key (`id`) — it changes if the DB is
  rebuilt. Use the semantic pair `(version_code, abi)`.
- `download_url` stays a **relative path** so a server IP/domain move doesn't
  break old clients (they resolve it against their configured baseUrl).
- Response fields are **add-only**: append new fields, never rename/retype/remove.
- The endpoints stay **public** (no auth) so a client that can't even reach login
  can still self-rescue via an update.
- Withdrawn builds (`is_active=false`) are hidden from `/latest` and return 404
  on `/download` — that's how a bad release is retracted in the field.
- `AppRelease.IsActive` has **no `default:true` GORM tag** on purpose: that tag +
  SQLite's column default silently persists a `false` value as `true`, leaking
  withdrawn builds to clients. The default is applied in code instead.
- `FindLatest` binds `is_active = ?` as the integer `1`, not the bool `true` —
  GORM inlines a bool literal into SQL and SQLite's `true` keyword isn't parsed
  consistently across driver versions (it matched inactive rows in tests).

The contract is regression-tested by `release_integration_test.go` (TestOTA*).

### Flutter: bump `version_code` on every release

`pubspec.yaml`'s version: X.Y.Z+N — the `+N` is the `version_code` (build
number). The OTA check compares `serverVersionCode > installedVersionCode`, so
**N must increment on every release** or clients won't see the new version.
When publishing via the admin Releases page, set version_code = N matching the
pubspec. Multi-ABI builds (`make build-apk --split-per-abi`) produce one APK
per ABI; upload each under the matching ABI so all device types get served.

### Frontend: cross-cutting features get their own page, not scattered across entity modals

AI 配置曾散落在三处 —— CourseModal 125 行、SubjectModal 62 行、Settings 的 Provider 卡片。集中到 `AIConsole` 后,实体 modal 缩到只管实体本身的属性。当一个功能的配置从 N 个不同实体编辑器里都能改,把它抽到独立页面 + 链接入口。这样实体 modal 只管实体本身,功能不和它不该绑的 CRUD 纠缠。本轮 AI 提示配置集中化后,CourseModal 从 320 行降到 ~200 行,125 行 AI 相关代码挪走;SubjectModal 减 62 行;Settings 的 Provider 卡片改成「前往 AI 控制台 →」链接。CourseModal/SubjectModal 各加一个「配置 →」深链到 `/admin/ai-console?tab=prompt&course=:id`(或 `&subject=:id`)预选实体。Course 级 AI 开关(`ai_summary_enabled`/`ai_quiz_enabled`)仍留在 CourseModal —— 那是 Course 实体本身的属性,不是 AI 配置。

### Workflow: when to parallelize with subagents vs. do it inline

Subagents (parallel `Agent` tool calls) are great for **low-coupling,
independently-verifiable** work: a self-contained widget, an isolated
endpoint, one screen's TV adaptation. They isolate the main context from
large file reads, which keeps the main session's attention focused. The
earlier "experience polish" round (markdown renderer + TV mode + subtitle
bug) used them well and shipped nearly bug-free.

Subagents are ** actively harmful for high-coupling refactors**. The prompt
rearchitecture round (5 agents touching model → prompts → services → tools →
DTO → frontend types → admin UI → seed in one go) shipped 4 bugs, none of
which any single agent could have caught because each agent's own files
compiled and tested green. The bugs lived in the **contracts between agents**
and the **completeness across branches** — exactly the seams no one owns.

Rules, learned from that round:

- **Before spawning agents, write the contract down.** Every field name,
  JSON shape, type signature that crosses agent boundaries goes in a shared
  block pasted into *each* agent's prompt. No "use an ai_config object"
  vagueness — spell out `{ai_config: {whisper_hint, summary_hint, ...}}` and
  require both the DTO writer and the type writer to match it verbatim. The
  DTO-flat-vs-frontend-nested bug existed because the backend agent and the
  main session each picked a reasonable shape with no coordination.
- **List every consumer when splitting a concept.** When QuizHint was split
  into QuizHint + TermDict, three call sites consumed it (summarizer
  direct, advice direct, quiz tools.go tool return). The agent was told
  about the first two; the third silently kept returning only the old
  QuizHint, so quiz lost all term correction. If a name is touched in N
  places, the agent's prompt must list all N and require confirmation.
- **Never parallelize repo/model-layer changes with their callers.**
  `courseRepo.FindByID` is called from dozens of sites; changing its behavior
  (e.g., adding `Preload("Subject")`) ripples everywhere. Worse,
  belongsTo + `db.Save` is a classic GORM trap: preloading Subject on the
  read path makes `Save` rewrite `course.SubjectID` via FullSaveAssociations.
  Read-path and write-path repo methods must be separate (`FindByID` vs
  `FindByIDWithSubject`). When in doubt, the model layer is owned by the
  main session, not a subagent.
- **After *any* model/repo change, run `go test ./...` immediately, not just
  `go build`.** Build-green is not behavior-green. The Preload regression
  was caught only because a full test run hit `TestCourseUpdate`; `go build`
  was silent. This is now a hard rule: model-layer change → full test,
  every time, no exceptions.
- **When adding a field, grep every constructor.** Adding `AdviceHint` to
  `AdviceRequest` means walking every `case agent.ScopeXxx:` branch that
  builds an `AdviceRequest` and confirming the field is set. The subject-
  scope branch was missed; subject-scoped advice silently lost its hint.
  Mechanical but effective: after the edit, `grep -n NewField` and check
  every hit.
- **For a refactor whose downstream reaches > 3 files and the changes
  depend on each other, do it inline in the main session.** The
  coordination cost of N agents exceeds the context cost of doing it
  yourself once N > ~3 and the files are coupled. Two agents doing
  genuinely-independent halves is fine; five agents doing one coupled
  pipeline is not.
- **Handoff docs can be wrong; verify with grep before planning.** The
  "AI regenerate" handoff said "入队 UI 在 CourseTree/CoursesContent"
  but `grep -r enqueueAiJobs frontend-admin/src` returned zero callers —
  the SPA surface existed but was never wired. Plans built on stale
  handoff descriptions over- or under-scope the work. Always re-verify
  file/line claims in handoff docs against the current code before
  treating them as ground truth.
- **"形式 not null" fields are the worst kind of tech debt.**
  `AIJob.EpisodeID uint gorm:"not null"` accepted 0 (SQLite treats 0
  as a valid integer), so subject-scope advice jobs silently wrote
  `episode_id=0` pointing at a non-existent episode. The constraint
  looked protective but wasn't. When we tried to add a FK, the bug
  surfaced immediately (FK would reject the 0 rows). Lesson: any `not
  null` column where the code path can produce a "no real entity"
  value should be `*uint` (nullable) from day one, with the nil case
  explicitly handled. Discovered during this round's FK cascade work.

The bias should be: **default to inline for anything that touches the
contract between layers; reach for subagents when the work is genuinely
parallel and each piece can be verified in isolation.**

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
- **APK OTA distribution:** backend `release_handler.go` (frozen client
  contract `/api/v1/app/latest` + `/api/v1/app/download`), `release_repo.go`,
  admin page `Releases.tsx`, Flutter `update_service.dart`. See the frozen-
  contract rule below.
- **Local UI prefs (字幕/AI 字号):** `frontend/lib/service/ui_prefs.dart`.
  `UiPrefs` singleton loaded once in `main.dart`; 字幕字号档位 + AI 页文字缩放
  存 SharedPreferences,改一次全局生效、下次进 App 沿用。播放器字幕菜单和 AI
  页右上角字号按钮都写回它。
- **Android TV 检测:** `frontend/lib/service/tv_mode.dart`. `TvMode.isActive`
  = 自动检测(systemFeatures 含 leanback)OR 设置页「预览 TV 模式」调试开关
  (给 MuMu 模拟器开发用)。各屏 `if (TvMode.instance.isActive)` 分支:隐藏 quiz、
  字号强制大、helper panel 默认展开、seek 步长 ±30s。
- **AI 富文本渲染:** `frontend/lib/ui/widget/markdown_view.dart`. `MarkdownView`
  渲染 GFM 表格/加粗/列表,并拦截 ```svg 代码块用 flutter_svg 渲染成图(失败降级
  显示源码)。AI 页 summary/advice/quiz 的长文本字段都走它。后端 prompts.go 的
  三个 system prompt 鼓励(非强制)输出 markdown + 约束式 SVG(只允许简单流程图/
  柱状图,禁止时序/ER/甘特等复杂图型)。
- **AI 配置(5 维度 + 学科默认):** `model.AIConfig`(WhisperHint/SummaryHint/
  QuizHint/AdviceHint/TermDict)存 `Course.AIConfigJSON` 和 `Subject.AIConfigJSON`
  两层。解析优先级:`Course.EffectiveXxxHint(subject)` Course 字段 > Subject 字段
  > deprecated AIHint 列(Whisper/Quiz 才有 legacy fallback);TermDict 特殊:课程级
  +学科级**合并**(`\n` 连接,学科在前),其余覆盖。admin 在 SubjectModal(学科默认)
  和 CourseModal(课程覆盖)各配 5 字段。**system prompt 不再硬编码学科术语**(车→居
  已清),改成 user prompt 里注入【术语字典】段(Summary/Advice 直传 Request;Quiz 走
  `get_episode_info` 工具;TermDict 横切给三个 agent)。数学/英语两科 seed 在
  `SeedDefaultSubjects` 回填,象棋等自定义学科走前端 `aiHintTemplates.ts` fallback。
- **AI Prompt 可观测性:** `model.AIRun.SystemPromptText/UserPromptText` 记录每次 LLM
  调用最终发出的完整 system+user prompt。`agent.Run`(quiz/advice/courseSummary/
  userStudy 共同入口)和 `summarizer.recordRun` 写入。admin AI Workflow 页"查看回放"
  展示。`POST /admin/api/ai/courses/:id/preview-prompt`(body `{agent}`)不调 LLM 拼
  出完整 prompt 供调优预览,`agent/preview.go` 的 `BuildXxxPromptForPreview` 保证"预览
  即真相"(调同一个 build*UserPrompt)。

## Deeper docs

- `design.md` — overall architecture & design tokens
- `docs/architecture.md` — backend layering
- `docs/adr.md` — architecture decision records
- `docs/ai-subtitle-queue.md` — subtitle generation queue (Step 1/2)
- `docs/ai-agent-module.md` — AI learning agent (Step 3: summary/quiz/chat)
- `TESTING_PLAN.md` — coverage gaps & integration-test design
- `frontend-admin/README.md` — SPA conventions (mutate→invalidate etc.)
