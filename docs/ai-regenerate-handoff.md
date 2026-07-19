# Handoff:AI 内容「删除 + 重新生成」闭环

> 给下一个会话的起步文档。用户钦点需求,价值已被确认。TODO.md 里列为 P0 第一项。
> 本文档由 2026-07-19 会话结束时整理,所有现状数据已核实(代码 commit `bb53dac`)。

## 一句话需求

让 admin 和用户能对每类 AI 产物做「重新生成」(覆盖式重跑),必要时能「删除」。
**核心痛点不是删,是重新生成的入口分散/缺失** —— 机制大半都在(Upsert 覆盖式),缺入口和去重。

---

## 5 类 AI 产物的现状矩阵

| 产物 | 表 | 能删 | 重新生成 | 缺什么 |
|------|-----|:-:|:-:|------|
| **Episode Summary** | `AISummary`(uniqueIndex EpisodeID) | ❌ | △ 隐式 | `EnqueueSummary` 没去重(连点堆 job);admin SPA 无按钮;无删除端点 |
| **Episode Quiz** | `Quiz`(active/archived)+ Question + Answer | △ archive | ✅ 客户端换题 | admin 端零入口;客户端已完整 |
| **Episode Advice** | `StudyAdvice`(uniqueIndex UserID,Scope,ScopeID) | ❌ | △ 仅 episode 级 | course/subject 级**无任何途径**;客户端无刷新按钮 |
| **Course Summary** | `AICourseSummary`(uniqueIndex CourseID) | ❌ | ✅ 端点有 | admin SPA **没接 API**(`api.ts` 缺方法) |
| **User Study Report** | `UserStudyReport`(uniqueIndex UserID) | ❌ | ✅ 完整闭环 | 唯一完整的,**可作模板** |

**结论**:Quiz 客户端、User Study Report admin 端做得最完整,可以照抄。其余三类大半机制在,缺入口。

---

## 各产物的重新生成机制(机制都在,只差入口)

### 1. Episode Summary —— 缺入口 + 缺去重 + 缺删除

- **现有覆盖机制**:`UpsertSummary`(`repository/ai_content_repo.go:265-268`)走 GORM `db.Save`,靠 `uniqueIndex(episode_id)` 覆盖。
- **现有触发**:`POST /admin/api/ai/jobs` body `{"job_type":"summary","episode_ids":[...]}` → `EnqueueAIJobs`(`handler/admin_ai.go:371-402`)→ `aiService.EnqueueSummary(episodeIDs)`(`service/ai_service.go:221-224`)。
- **坑**:`EnqueueSummary` **没有去重门**(对比 `EnqueueSegmentForCourse` 的 `hasPendingJob` 检查,`ai_service.go:302-308`)。admin 连点会堆多条 summary job。
- **admin SPA**:AIWorkflow 页(`frontend-admin/src/pages/AIWorkflow.tsx`)只有队列观察 + reset/retry 按钮,没有"入队 summary"按钮(入队 UI 在 CourseTree/CoursesContent,不在 AIWorkflow)。
- **客户端**:完全只读(`fetchEpisodeSummary`),没有刷新按钮。

### 2. Episode Quiz —— 客户端完整,admin 零入口

- **现有完整闭环(客户端)**:`POST /api/v1/episodes/:id/ai-quiz/regenerate`(`router.go:94`)→ `aiHandler.RegenerateQuiz`(`handler/ai_handler.go:276-299`)→ `aiService.RegenerateQuiz(userID, episodeID)`(`service/ai_service_quiz.go:1028-1058`)。
- **机制**:不直接删旧 quiz,而是入一条新 quiz job(`Priority: priorityQuiz=10`),worker 跑 `CreateQuiz`(`ai_content_repo.go:510-557`)在事务里把旧 active 改 `status='archived'` + `ArchivedAt=now`,然后插新 active。旧 questions 物理保留(历史可读)。
- **去重门**:`hasPendingQuizJob` 在途直接返回 generating。
- **客户端 UI**:`ai_study_screen.dart:355-390` 的 `_regenerate()`,按钮文案"换题"。
- **缺什么**:**Admin 端完全没入口** —— `AIUserView.tsx:130-134` 的 quiz 列表只有"查看详情"按钮,没有"给这个学生重出一套题"。

### 3. Episode Advice —— 痛点最大

- **现状**:没有删除端点,没有显式重新生成端点。
- **episode 级的隐式刷新**:学生交卷(submit-all)后,`EnqueueAdviceForEpisode`(`ai_service_advice.go:327-333`)链式触发重算(强制覆盖,有在途去重)。
- **course/subject 级**:**完全没有刷新途径**。一旦生成就永远是那条,直到 SQL 手动清表。
- **GetOrEnqueueAdvice 的 lazy 路径不通**:`ai_service_advice.go:275-303` 的逻辑是"有 advice 就返回 ready(哪怕过期)"——所以删了再 GET 才会触发重跑,但你删不掉。

### 4. Course Summary —— 端点有,SPA 没接

- **端点齐全**:`POST /admin/api/ai/courses/:id/course-summary`(`router.go:257`)→ `TriggerCourseSummary`(`admin_ai.go:700-716`)→ `aiService.EnqueueCourseSummary`。
- 注释明确(`admin_ai.go:697-699`):**强制重生成语义,即使已有总结 POST 也会重跑(覆盖)**。
- 去重门:`HasPendingCourseSummaryJob`。
- **缺什么**:`api.ts` **没有 course-summary 的客户端方法**(grep 不到)。`AIWorkflow.tsx`/课程页都没按钮。只能 curl/Postman 触发。

### 5. User Study Report —— 完整模板,照抄

- **端点 + SPA 都有**:`POST /admin/api/ai/users/:id/study-report` + `AIUserView.tsx:158-228` 的 `UserStudyReportSection`,有"重新生成"按钮(`AIUserView.tsx:196-203`),tooltip"重新生成(覆盖当前报告)"。
- **API client**:`api.triggerUserReport` / `api.getUserReport`(`api.ts:612-617`)。
- **这是其它四类该长成的样子**。

---

## 建议的交付范围(下一轮)

### 必做(用户钦点的核心)

1. **Admin 端「重新生成」按钮**(5 类产物都加):
   - Summary:AIWorkflow 页或课程页加"重新生成 Summary"按钮,调 `EnqueueAIJobs`(要先给它加去重门)
   - Quiz:`AIUserView.tsx` 的 quiz 列表每行加"重出题"按钮,调一个新的 admin 端点(类似 client 的 RegenerateQuiz 但带 userID)
   - Advice:admin 加"重新生成建议"按钮(episode/course/subject scope 都要),需要一个新端点 `POST /admin/api/ai/.../advice/regenerate`
   - Course Summary:`api.ts` 加 `triggerCourseSummary` 方法 + 课程页或 AIWorkflow 加按钮
   - User Study Report:已有,不动

2. **EnqueueSummary 加去重门**:照抄 `hasPendingQuizJob` / `HasPendingCourseSummaryJob` 的模式,在 `ai_service.go` 加 `hasPendingSummaryJob`,避免连点堆 job。

3. **删除端点(选做)**:如果用户坚持要"删了重来"而非"覆盖重来",给 5 类产物加 DELETE 端点。但**优先做覆盖式重新生成**(更符合 Upsert 现有语义,quiz 的 archive 模式已经证明"不删只覆盖"是可行的)。

### 顺带做(技术债,和生命周期管理同源)

4. **删 episode/course/user 时 AI 数据级联**:见 TODO.md「已识别的技术债」最后一条。现在 AI 表都没声明 FK,删了变孤儿。和本轮一起做合理(都是 AI 内容生命周期)。

5. **客户端给 advice 加"刷新建议"按钮**(episode 级):`ai_study_screen.dart` 的 advice 卡片右上角加刷新按钮。

### 不做(明确边界)

- 不动 quiz 的 archive 语义(它是对的,保留历史)。
- 不做用户自助删除 summary/quiz(这是 admin 能力,用户端只给 advice 刷新就够了 —— 用户删 summary 没意义,反正会被覆盖)。
- 不加 prompt 版本管理(远期,TODO P2)。

---

## 关键文件索引(下个会话直接定位)

**Model(表结构)**:`backend/internal/model/models.go`
- `AISummary`(L974)、`AIJob`(L928)、`Quiz`(L1017)、`Question`/`Answer`(L1049/1102)、`StudyAdvice`(L1179)、`AICourseSummary`(L1200)、`UserStudyReport`(L1216)

**Admin AI handler**:`backend/internal/handler/admin_ai.go`
- `EnqueueAIJobs`(L371)、`GetAISummary`(L603)、`TriggerCourseSummary`(L700)、`TriggerUserStudyReport`(L774)、`ResetAIJob`(L539)、`RetryAIJob`(L571)
- **要在这里加**:admin 端的 regenerate 端点(quiz/advice/summary)

**Client AI handler**:`backend/internal/handler/ai_handler.go`
- `RegenerateQuiz`(L276)—— 唯一的客户端 regenerate 端点,admin 版可照抄

**Service 层**:
- `service/ai_service.go`:`EnqueueSummary`(L221,缺去重)、`hasPendingJob`(L302,去重模板)
- `service/ai_service_quiz.go`:`RegenerateQuiz`(L1028,客户端版,admin 版可照抄)、`hasPendingQuizJob`(去重模板)
- `service/ai_service_advice.go`:`GetOrEnqueueAdvice`(L275)、`EnqueueAdviceForEpisode`(L327,目前只在 submit-all 后调)
- `service/ai_service_course_summary.go`:`EnqueueCourseSummary`(L174)、`HasPendingCourseSummaryJob`(L214)
- `service/ai_service_user_report.go`:`EnqueueUserReport`(L237)

**Repository**:`backend/internal/repository/ai_content_repo.go`
- `UpsertSummary`(L265)、`CreateQuiz`(L510,含 archive 逻辑)、`UpsertAdvice`(L743)、`UpsertCourseSummary`(L773)、`UpsertUserStudyReport`(L798)
- **要在这里加**(如果做删除):`DeleteSummary`/`DeleteAdvice`/etc.

**路由**:`backend/internal/router/router.go`
- Admin AI 段:L237-265(新端点加这里)
- Client regenerate:L94

**Admin SPA**:
- `frontend-admin/src/pages/AIWorkflow.tsx`:队列 + RunDetail(L334),要加 summary/course-summary 重新生成按钮
- `frontend-admin/src/pages/AIUserView.tsx`:quiz 列表(L130,要加"重出题"按钮)+ UserStudyReportSection(L158,模板)
- `frontend-admin/src/lib/api.ts`:**缺 `triggerCourseSummary` 方法**,要加;regenerate 方法也要加

**Flutter 客户端**:
- `frontend/lib/ui/screen/ai_study_screen.dart`:advice 卡片(L706 `_buildAdviceCard`),要加刷新按钮
- `frontend/lib/service/api_service.dart`:目前零 DELETE 方法

---

## 实施建议

### 用 subagent 还是主会话串行?

根据 CLAUDE.md「Workflow: when to parallelize with subagents」的教训(上一轮 prompt 重构出 4 个 bug):
- 这轮是**中等耦合**:5 类产物各有 handler/service/repo 三层 + admin SPA,但每类内部改动小(加端点 + 加按钮)。
- 建议:**主会话串行做后端 + admin SPA 的 regenerate 端点和按钮**(契约清晰,跨层),**subagent 并行做客户端 advice 刷新按钮 + 文档**(独立、低耦合)。
- 不要 5 个 agent 各做一个产物 —— 容易跨产物契约不一致(比如去重门的命名、响应格式)。

### 建议的实施顺序

1. 先做去重门(`hasPendingSummaryJob`)+ User Study Report 模板提炼(看它怎么做的)
2. Admin 端 5 类 regenerate 端点 + 按钮(统一响应格式 `{status: "generating"}`)
3. `api.ts` 补 `triggerCourseSummary` + 各 regenerate 方法
4. (选做)删除端点
5. (顺带)episode/course/user 删除时的 AI 数据级联
6. 客户端 advice 刷新按钮(subagent 并行)

### 验证

- `go build` + `go test ./...`(全量,不能只 build —— 上一轮的教训)
- `tsc --noEmit` + `npm test`
- 手动:admin 给某学生重出 quiz → 看历史里多了一条 archived;重新生成 summary → AIWorkflow 看新 job;重新生成 advice → 看新文本覆盖旧文本

---

## 参考文档

- `CLAUDE.md`「Workflow: when to parallelize with subagents」—— 上一轮 4 个 bug 的教训
- `TODO.md` P0 第一项 —— 本需求的正式条目
- `docs/ai-agent-module.md` —— AI 模块整体设计
