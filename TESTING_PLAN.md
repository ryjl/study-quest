# 后端测试补强方案（handoff）

> 本会话已完成的提交：分支 `feat/subjects-tags-theme-polish`（3 commits）已 push。
> 下一会话从本文件接着做 UT 补测。

## 当前覆盖率（实测）

```
handler       0.4%   ← 最大缺口，HTTP 集成测试几乎空白
repository   20.3%
service      27.1%
router/middleware/config/model/storage  0%
```

handler 层几乎所有函数 cov=0%（`go tool cover -func` 确认）：所有 `Admin*`/`ListUsers`/`CreateCourse`/`UpdateCourse`/`resolveSubjectID`/`toCourseDTO`/`toUserDTO`/`toClientDTO`/`GetCourses`/badge handler 全空。tag/subject 的 repo/service 虽然单测覆盖，但 handler 层（HTTP 入口）完全没测。

## 建议方案：HTTP 集成测试（handler 层）

性价比最高——一条测试覆盖"请求 → middleware → handler → service → repo → DB → 响应"全链路，且能锁住这一串会话改过的关键契约（subject FK 409、tag CASCADE、DTO 双契约、batch 聚合）。

### Step 1：建测试 helper `backend/internal/handler/testhelper_test.go`

仿 `main.go` 的 DI wiring，起一个 in-memory SQLite + RegisterRoutes 的 gin engine：

```go
package handler

import (
    "bytes"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"

    "github.com/gin-gonic/gin"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    "studyquest/backend/internal/model"
    "studyquest/backend/internal/repository"
    "studyquest/backend/internal/service"
    "studyquest/backend/internal/router"
)

// testEnv bundles a fully-wired gin engine + the underlying DB for direct
// fixture setup. Mirrors main.go's DI but with in-memory SQLite + no probe
// worker goroutine (pass a no-op enqueue).
type testEnv struct {
    engine *gin.Engine
    db     *gorm.DB
    adminCookie *http.Cookie // pre-authenticated admin session cookie
}

// newTestEnv builds a fresh server with seeded subjects/tags/badges + a logged-
// in admin session cookie ready to use.
func newTestEnv(t *testing.T) *testEnv {
    t.Helper()
    gin.SetMode(gin.TestMode)
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
    if err != nil { t.Fatalf("open db: %v", err) }
    db.Exec("PRAGMA foreign_keys=ON") // 让 FK RESTRICT/CASCADE 真实生效
    if err := model.AutoMigrate(db); err != nil { t.Fatalf("migrate: %v", err) }

    // repos
    settingsRepo := repository.NewSettingsRepository(db)
    userRepo := repository.NewUserRepository(db)
    courseRepo := repository.NewCourseRepository(db)
    episodeRepo := repository.NewEpisodeRepository(db)
    progressRepo := repository.NewProgressRepository(db)
    chapterRepo := repository.NewChapterRepository(db)
    badgeRepo := repository.NewBadgeRepository(db)
    subjectRepo := repository.NewSubjectRepository(db)
    tagRepo := repository.NewTagRepository(db)

    // services
    userService := service.NewUserService(userRepo)
    courseService := service.NewCourseService(courseRepo, userRepo)
    episodeService := service.NewEpisodeService(episodeRepo, settingsRepo)
    badgeService := service.NewBadgeService(badgeRepo, progressRepo)
    progressService := service.NewProgressService(progressRepo, episodeRepo, badgeService)
    subjectService := service.NewSubjectService(subjectRepo, badgeRepo)
    tagService := service.NewTagService(tagRepo)
    probeWorker := service.NewProbeWorker(episodeService, episodeRepo)
    importService := service.NewImportService(episodeRepo, courseRepo, settingsRepo, chapterRepo, subjectRepo, probeWorker.Enqueue)
    chapterService := service.NewChapterService(chapterRepo)

    // seed (顺序: subjects → tags → badges, 同 main.go)
    if err := subjectService.SeedDefaultSubjects(); err != nil { t.Fatalf("seed subjects: %v", err) }
    if err := tagService.SeedDefaultTags(); err != nil { t.Fatalf("seed tags: %v", err) }
    if err := badgeService.SeedDefaultBadges(); err != nil { t.Fatalf("seed badges: %v", err) }

    // handlers
    healthH := handler.NewHealthHandler()
    userH := handler.NewUserHandler(userService)
    courseH := handler.NewCourseHandler(courseService, episodeService, chapterService, subjectRepo)
    episodeH := handler.NewEpisodeHandler(episodeService, progressService, settingsRepo)
    progressH := handler.NewProgressHandler(progressService)
    ingestH := handler.NewIngestHandler(episodeRepo, episodeService, probeWorker.Enqueue)
    adminH := handler.NewAdminHandler(settingsRepo, userRepo, courseRepo, episodeRepo, chapterRepo, progressRepo, subjectRepo, badgeRepo, userService, courseService, importService, episodeService, chapterService, badgeService, probeWorker)
    badgeH := handler.NewBadgeHandler(badgeService)
    subjectH := handler.NewSubjectHandler(subjectService)
    tagH := handler.NewTagHandler(tagService)

    r := gin.New()
    router.RegisterRoutes(r, healthH, userH, courseH, episodeH, progressH, ingestH, adminH, badgeH, subjectH, tagH, userRepo, settingsRepo)

    // 登录拿 admin cookie
    env := &testEnv{engine: r, db: db}
    env.adminCookie = env.loginAdmin(t)
    return env
}

// loginAdmin 走 /admin/api/login（默认密码 "admin"），返回 session cookie。
func (e *testEnv) loginAdmin(t *testing.T) *http.Cookie { ... }

// do / doJSON — 发请求，自动带 admin cookie；返回 *httptest.ResponseRecorder。
func (e *testEnv) do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder { ... }
```

> ⚠️ 注意：handler 包测试要 import router 包，会有循环依赖风险——`router` import `handler`，反过来 `handler` 测试 import `router` 会 cycle。**解法**：把 testhelper 放 `backend/internal/router/router_test.go`（router 包内测，能引用 handler），或单独建 `backend/cmd/server/server_test.go`（main 包，可引用两者）。**推荐后者**——放 cmd/server 下做集成测试，最干净。

### Step 2：关键集成测试用例（按优先级）

放到 `backend/cmd/server/api_integration_test.go`，`package main`。

#### A. Subject（锁住 FK RESTRICT + key 级联）
```go
func TestSubjectDeleteWithCourse(t *testing.T) {
    env := newTestEnv(t)
    // 建课程绑 math
    resp := env.do(t, "POST", "/admin/api/courses", map[string]any{"title":"数学","grade":"3","subject":"math"})
    // 删 math 应 409
    resp = env.do(t, "DELETE", "/admin/api/subjects/2", nil)
    assert(resp.Code == 409, "应被 FK 拦截")
}

func TestSubjectRenameKeyCascadesBadge(t *testing.T) {
    env := newTestEnv(t)
    // math_expert badge 的 rule_target 应是 "math"
    // PUT 改 math → mathematics
    env.do(t, "PUT", "/admin/api/subjects/2", map[string]any{"key":"mathematics","label":"数学","emoji":"📐","color":"#f59e0b"})
    // 验证 math_expert 的 rule_target 变 mathematics
}
```

#### B. Tag（锁住 many2many + CASCADE）
```go
func TestTagDeleteDetachesFromCourse(t *testing.T) {
    env := newTestEnv(t)
    // 建课程绑 tag_id=1,2
    cid := createCourse(env, "t", "math", []uint{1,2})
    // 删 tag 1
    env.do(t, "DELETE", "/admin/api/tags/1", nil) // 200
    // 课程 tag_ids 应只剩 [2]
    c := getCourses(env)[0]
    assert(reflect.DeepEqual(c.TagIDs, []uint{2}))
}
```

#### C. Course DTO 双契约（admin snake_case + client PascalCase）
```go
func TestCourseDTOContracts(t *testing.T) {
    env := newTestEnv(t)
    createCourse(env, "测试", "math", []uint{1})
    // admin /admin/api/courses → snake_case: tag_ids, subject_id
    // client /api/v1/courses → PascalCase: Tags, TagsList, TagIDs, Subject
}
```

#### D. User batch 聚合（完课/时长/徽章/活跃）
```go
func TestUserListBatchAggregates(t *testing.T) {
    env := newTestEnv(t)
    // 建学生 + 授权课程 + 报进度 (completed, watch_seconds)
    // GET /admin/api/users → 验证 completed_episodes/watch_minutes/unlocked_badges 字段存在且正确
}
```

#### E. import 空 grade fallback
```go
func TestImportEmptyGradeFallsBackToUniversal(t *testing.T) {
    // grade="" 应成功 (fallback universal), 不报 invalid course grade
}
```

### Step 3：填空 repo/service 单测（次要）

cov 工具显示这些 0%：
- `tag_repo` 全部（Create/FindByKey/SetTags 等）—— 其实 `SetTags` 在 course_repo 里很关键，应单测绑/解/清空
- `tag_service.SeedDefaultTags` —— 验证幂等
- `import_service.ExecuteTreeImport` —— 端到端树导入（建课+章节+课时）
- `course_repo.SetTags` + `dedupUint` —— 去重 + Replace 行为

## 下会话执行 checklist

1. `git checkout feat/subjects-tags-theme-polish && git pull`
2. 建 `backend/cmd/server/server_integration_test.go`（package main，避开循环依赖）
3. 写 testhelper（newTestEnv/do/doJSON/loginAdmin）
4. 按上面 A-E 顺序写测试，每写完一个跑 `go test ./cmd/server/`
5. 最后 `go test -cover ./...` 看覆盖率从 0.4% → 目标 handler 到 30%+
6. 提交：`test: add HTTP integration tests for subject/tag/course/user flows`

## 已知坑

- **循环依赖**：handler 测试不能 import router（router import handler）。解法放 `package main`（cmd/server）。
- **SQLite FK 默认关**：测试 DB 必须 `db.Exec("PRAGMA foreign_keys=ON")`，否则 RESTRICT/CASCADE 不生效（之前 subject_repo_test 踩过）。
- **gin test mode**：`gin.SetMode(gin.TestMode)` 减少日志噪音。
- **AdminAuthMiddleware**：测试要真的走 login 拿 cookie，不能跳过 middleware（否则没测到 auth 链路）。
