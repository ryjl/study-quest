package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
	"studyquest/backend/internal/ai/polish"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/subtitle"
)

// AIService is the facade for the AI subsystem's business logic: enqueueing and
// running jobs (segment / summary), and reading results (summaries, jobs, runs
// for the admin observability page). It owns the in-process job worker.
//
// It is the ONLY place that ties together the provider resolver (ai.ProviderResolver),
// the segmenter, the embedder, the summarizer, and the content repo — keeping
// that wiring out of the handlers (which stay thin) and out of the agent package
// (which stays focused on decision logic). Handlers depend on this interface.
//
// Nil-safe semantics: when AI isn't wired (no resolver / no providers), the
// enqueue methods still record jobs (so the admin sees what WOULD have run) but
// processing reports "AI not configured" and marks the job skipped rather than
// crashing. This is the "add-on layer" guarantee in action.
type AIService interface {
	// ── job enqueue (admin-triggered) ──
	EnqueueSegment(episodeIDs []uint) (enqueued []uint, skipped map[uint]string, err error)
	EnqueueSummary(episodeIDs []uint) (enqueued []uint, skipped map[uint]string, err error)
	// EnqueuePolish re-runs the subtitle polish pipeline on episodes that have a
	// whisper-sourced primary subtitle. Use case: admin accepts glossary
	// candidates → Course.TermDict grows → they want the new terminology applied
	// to already-polished episodes. Polish reads from RawVttContent (not the
	// current VttContent) so re-runs don't compound LLM drift. Episodes without
	// a whisper subtitle, or with an in-flight polish job, are skipped.
	EnqueuePolish(episodeIDs []uint) (enqueued []uint, skipped map[uint]string, err error)
	// EnqueueSegmentForCourse enqueues segment jobs for every episode of a
	// course that already has a subtitle (the agent needs source material). Used
	// when an admin flips a course's AI switch from off→on: previously-arrived
	// subtitles never triggered AI work (OnSubtitleCompleted early-returns when AI
	// is off), so we batch-backfill here. Returns the number of jobs enqueued.
	EnqueueSegmentForCourse(courseID uint) (int, error)

	// ── subtitle completion hook ──
	// OnSubtitleCompleted is called by the subtitle job service when a transcript
	// lands. It enqueues a segment job so the new transcript is chunked+embedded
	// automatically, but ONLY if the episode's course has AI enabled. This is the
	// seam that connects Step 2 (subtitles) to Step 3 (AI) without either knowing
	// the other's internals.
	OnSubtitleCompleted(episodeID uint)

	// ── quiz: client-driven lazy generation + answering (Phase C) ──
	// GetOrEnqueueQuiz is the lazy-generation entry point. The client GETs the
	// quiz; if none exists yet, one is enqueued for this user and the status is
	// "generating" (client polls). Returns "ready" with the existing quiz, or
	// "unavailable" when AI/quiz is off or the episode isn't ready (no chunks).
	GetOrEnqueueQuiz(userID, episodeID uint) (status string, quiz *model.Quiz, err error)
	// GetQuizForClient returns the quiz + questions in a client-safe shape: the
	// question list NEVER exposes the correct answer/answer_text (that's revealed
	// only after submit, via SubmitQuizAnswer). Includes per-question answered
	// state so the client can show progress on a redo.
	GetQuizForClient(userID, episodeID uint) (*QuizView, error)
	// SubmitQuizAnswer grades one answer, persists it, updates memory, and
	// returns the verdict + explanation + jump-to-video timestamp. answerIndex is
	// used for choice, answerText for fill (exactly one is set per question type).
	SubmitQuizAnswer(userID, questionID uint, answerIndex *int, answerText *string) (*AnswerResult, error)
	// SubmitAllQuizAnswers 是 Phase B 的"统一交卷":一次性判分整张卷子,逐题返回
	// 结果 + 更新 memory,然后给 quiz 盖 SubmittedAt 锁定。对已交卷的 quiz 返回
	// ErrQuizAlreadySubmitted。answers 里缺的题视为漏答(计错误但仍 reveal 正确答案)。
	SubmitAllQuizAnswers(userID, episodeID uint, answers []QuizAnswerInput) ([]AnswerResult, error)
	// RegenerateQuiz drops the user's current quiz and re-runs the agent against
	// their latest memory (换题). Returns the new status ("generating").
	RegenerateQuiz(userID, episodeID uint) (status string, err error)
	// ListQuizHistory returns a user's archived (superseded) quizzes for an
	// episode as fully read-only views (correct answers revealed, no submit
	// path). Powers the Phase 3 history panel. Newest-archive first.
	ListQuizHistory(userID, episodeID uint) ([]QuizHistoryView, error)

	// ── Phase C: agent 驱动的学习建议(advice)──
	// GetOrEnqueueAdvice 是建议的 lazy 生成入口(同 GetOrEnqueueQuiz 的模式):
	// 已有 advice → "ready" + advice;无 + AI 配置好 → 入队低优先级 advice job,返回
	// "generating"(客户端轮询);AI off → "unavailable"。scope ∈ {episode,course,
	// subject},scopeID 是对应实体 id。访问控制由 handler 在调用前做。
	GetOrEnqueueAdvice(userID uint, scope string, scopeID uint) (status string, advice *model.StudyAdvice, err error)
	// EnqueueAdviceForEpisode 是 submit-all 成功后的链式触发:异步入队 episode 级
	// advice job(低优先级),让学生交卷后过一会能看到"这节课的复习建议"。幂等:已有
	// 在途 advice job 不重复入队。
	EnqueueAdviceForEpisode(userID, episodeID uint) error

	// ── Phase D: 课程级总结(course-unique 纯内容总结,agent 驱动)──
	// EnqueueCourseSummary 是 admin 手动触发"为某课程生成课程级总结"入口。入队低优先级
	// course_summary job,返回 "generating"(无在途 job 时)或 "unavailable"(AI off /
	// 课程不存在)。在途 job 不重复入队(避免堆 job)。和 advice 的 lazy 生成不同:
	// course summary 纯 admin 触发(客户端只读已生成的总结,不触发生成——因为总结是
	// course-unique 共享的,不应让任一学生触发)。courseID 必须指向存在的课程。
	EnqueueCourseSummary(courseID uint) (status string, err error)
	// GetCourseSummary 取某课程的最新课程总结(unique on course_id,所以最多一条)。无总结
	// 返回 nil。客户端 GET /courses/:id/ai-summary 调此方法;nil → handler 返回 404
	// ("暂无课程总结")。课程总结是 course-unique 的纯内容总结(不含个人维度)。
	GetCourseSummary(courseID uint) (*model.AICourseSummary, error)
	// HasPendingCourseSummaryJob 报告该课程是否有在途 course_summary job,handler/admin
	// 据此区分"正在生成"(generating)vs"无总结未生成"(显示生成按钮)。
	HasPendingCourseSummaryJob(courseID uint) bool

	// ── quiz: admin observability (Phase C) ──
	ListQuizzesForUser(userID uint) ([]QuizDetailQuiz, error)
	GetQuizDetail(quizID uint) (*QuizDetail, error)

	// ── Phase E: admin 用户学习报告(agent 驱动,跨课程画像)──
	// EnqueueUserReport 是 admin 触发的"为某用户生成学习报告"入口。入队低优先级
	// user_report job,返回 "generating"(无在途 job 时)或 "unavailable"(AI off)。
	// 在途 job 不重复入队。和 advice 的 lazy 生成不同:user_report 纯 admin 触发。
	EnqueueUserReport(userID uint) (status string, err error)
	// GetUserStudyReport 取某用户的最新学习报告(unique on user_id)。无报告返回 nil。
	// handler 结合 HasPendingUserReportJob 决定响应是 ready / generating / 空。
	GetUserStudyReport(userID uint) (*model.UserStudyReport, error)
	// HasPendingUserReportJob 报告该用户是否有在途 user_report job,handler 据此区分
	// "正在生成"(generating)vs"无报告未生成"(显示生成按钮)。
	HasPendingUserReportJob(userID uint) bool

	// ── admin: 重新生成 + 删除(2026-07-19 这轮加)──
	// 这组方法是 admin 控制台"重新生成中枢"和"删除 AI 产物"按钮的后端。
	// 重新生成走"覆盖式":UpsertAdvice/UpsertSummary/UpsertCourseSummary 都覆盖,
	// quiz 走 archive + 插新 active,和客户端换题语义一致。所有方法都去重在途 job。
	//
	// RegenerateAdvice 强制重生成某 (user, scope, scopeID) 的 advice。和 lazy 生成
	// (GetOrEnqueueAdvice)的差异:跳过 mastery gate —— admin 强制重跑应能跑,即使学
	// 生没做题(advice 会给出"建议先做题"的默认建议)。三档 scope 都支持,course/
	// subject 级首次触发也走这里(以前无任何途径刷新它们)。
	RegenerateAdvice(userID uint, scope string, scopeID uint) (status string, err error)
	// RegenerateQuizForUser 是 admin 端"给某学生重出题"的入口,照抄客户端 RegenerateQuiz
	// 但不校验 unlock(admin 不受 drip schedule 限制)。
	RegenerateQuizForUser(userID, episodeID uint) (status string, err error)

	// Delete 系列物理删除 AI 产物(不走 archive)。Quiz 的删除会级联清 Question + Answer
	// (FK CASCADE)。语义幂等:删一个不存在的 id 不报错(DELETE WHERE id=? 匹配 0 行
	// 也是成功),handler 统一返回 200 {ok: true}。如果将来需要"不存在则 404"语义,
	// 再扩成 (rowsAffected, error) 让 handler 分辨 —— 当前所有调用方都接受幂等语义。
	DeleteSummary(episodeID uint) error
	DeleteQuiz(quizID uint) error
	DeleteAdvice(userID uint, scope string, scopeID uint) error
	DeleteCourseSummary(courseID uint) error
	DeleteUserReport(userID uint) error

	// ListUserAdvice 列出某用户的所有 advice(三档 scope,所有 scope_id),给 admin
	// 控制台显示"这个学生有哪些 advice + 删除按钮"用。按 generated_at DESC 排序。
	ListUserAdvice(userID uint) ([]model.StudyAdvice, error)

	// ── results (read) ──
	GetSummary(episodeID uint) (*model.AISummary, error)
	// ListJobs/GetJob return AIJobView (job row + resolved human-readable names
	// for episode/course/user) so the admin UI renders titles instead of bare
	// ids. Name resolution lives in the service layer, mirroring the subtitle
	// queue's SubtitleJobWithEpisode join pattern (see subtitle_service.Claim).
	ListJobs(jobType, status string, limit int) ([]AIJobView, error)
	GetJob(id uint) (*AIJobView, error)
	ListRunsForJob(jobID uint) ([]model.AIRun, error)
	ListRecentRuns(limit int) ([]model.AIRun, error)
	// ListRecentRunsEnriched / ListRunsForJobEnriched return AIRunView (run +
	// resolved episode/course/user titles) for the admin UI. The plain variants
	// above stay for internal callers (e.g. quiz history) that want raw runs.
	ListRecentRunsEnriched(limit int) ([]AIRunView, error)
	ListRunsForJobEnriched(jobID uint) ([]AIRunView, error)
	GetRun(id uint) (*model.AIRun, error)
	JobStats() (map[string]int, error)

	// ── status / staleness helpers (内容管理 tab + 课程总览陈旧检测) ──
	// ListEpisodeSummaryStatus 返回某课程下"已有 summary"的 episode id 列表,
	// 给 admin 内容管理 tab gate 每集"删除"按钮用。
	ListEpisodeSummaryStatus(courseID uint) ([]uint, error)
	// CountEpisodesWithSummary 返回某课程下已有 summary 的 episode 数量。
	// 课程总览的陈旧检测用:生成时快照,读时对比。
	CountEpisodesWithSummary(courseID uint) (int64, error)

	// ── maintenance ──
	// ReapStaleJobs resets 'processing' AI jobs whose claimed_at is older than a
	// fixed threshold back to 'queued', so a worker that crashed mid-LLM-call
	// doesn't leave a job stuck forever. Mirrors the subtitle reaper; the AI
	// worker is in-process so this is mainly insurance against a hard kill.
	ReapStaleJobs() (int64, error)
	// ResetJob is the single-job, admin-triggered counterpart of ReapStaleJobs:
	// it resets one 'processing' job the admin has judged stuck back to 'queued'
	// (clearing claimed_at + error). Returns repository.ErrJobNotProcessing if
	// the job isn't currently processing (so the handler can 409 cleanly).
	ResetJob(jobID uint) error
	// RetryJob is the admin-triggered way to revive a terminal 'failed' job back
	// to 'queued' so the worker re-runs it — the only such path, since failJob
	// marks jobs failed without auto-retry. Use case: a job failed because the
	// embedding/chat provider was misconfigured, the admin fixed the config, now
	// they want to re-run. Returns repository.ErrJobNotFailed if the job isn't
	// currently failed (so the handler can 409 cleanly).
	RetryJob(jobID uint) error
	// SkipPolish is the polish-specific escape hatch: when a polish job is
	// failed and the admin decides the raw subtitle is good enough (or the
	// provider issue can't be fixed), they skip polish entirely. The job is
	// marked done ("admin skipped"), the subtitle stays at its current state
	// (raw whisper text), and segment is chained so downstream AI proceeds.
	// Only valid on a FAILED POLISH job — other states/types return a 409-mapped
	// error (ErrJobNotFailed / ErrJobNotPolish).
	SkipPolish(jobID uint) error

	// ── glossary candidate review (PR2.5) ──
	// The polish job mines term-correction rules (军→车 in a xiangqi course) and
	// leaves them as pending GlossaryCandidate rows. These methods are the admin
	// review surface: list, accept (which promotes the rule into the course's
	// TermDict so future polish runs apply it), and reject.

	// ListGlossaryCandidates returns the course's candidates, optionally
	// filtered by status ("" = all). Ordered by confidence desc.
	ListGlossaryCandidates(courseID uint, status string) ([]model.GlossaryCandidate, error)
	// AcceptGlossaryCandidate promotes one pending candidate into the course's
	// TermDict. corrected/context are optional admin edits (the admin can fix a
	// wrong LLM suggestion before accepting). If applyToSubjectSiblings is true,
	// the same rule is appended to every other course under the same subject,
	// sparing the admin from repeating the review for each course. Returns
	// ErrGlossaryNotPending if the candidate isn't reviewable.
	AcceptGlossaryCandidate(id uint, corrected, context string, applyToSubjectSiblings bool) error
	// RejectGlossaryCandidate marks one candidate rejected so it stops surfacing
	// in the default review list. The row is kept (not deleted) so the polish
	// job's UpsertCandidate won't re-create it next time. Returns
	// ErrGlossaryNotPending if the candidate isn't reviewable.
	RejectGlossaryCandidate(id uint) error
}

// ErrGlossaryNotPending is returned by Accept/RejectGlossaryCandidate when the
// target row is already accepted or rejected (the admin double-clicked, or two
// admins reviewed concurrently). Non-fatal: the handler surfaces 409.
var ErrGlossaryNotPending = errors.New("glossary candidate is not pending")

type aiService struct {
	db            *gorm.DB
	contentRepo   repository.AIContentRepository
	episodeRepo   repository.EpisodeRepository
	courseRepo    repository.CourseRepository
	subtitleRepo  repository.EpisodeRepository // same repo, GetSubtitle lives here
	resolver      *ai.ProviderResolver
	unlockService UnlockService // gates client quiz access (IsEpisodeVisible); nil in tests
	// userRepo feeds advice agent 的 list_user_courses 工具(查学生被授权的课程 id)。
	// nil 时该工具回退返回空(advice agent 据此降级)。quiz 路径不用它。
	userRepo aiUserCourseLister
	// glossaryRepo stores term-correction candidates mined by the polish job.
	// nil-safe: when nil, polish still runs but skips persisting candidates
	// (PR2.5 admin UI reads this; tests that don't care about glossary pass nil).
	glossaryRepo repository.GlossaryRepository
	// subjectRepo reads Subject rows for the polish job's TermDict lookup
	// (Course.EffectiveTermDict takes a Subject). nil in tests that don't run polish.
	subjectRepo repository.SubjectRepository
}

// aiUserCourseLister 是 aiService 对 userRepo 的窄依赖:只暴露 advice 工具需要的
// GetAccessList(查学生被授权的课程 id)。和 agentEpisodeLoader / agentCourseLoader
// 同思路——不把整个 repository.UserRepository 拉进 service 包,保持可测试性 +
// 依赖最小化。nil 时 advice 的 list_user_courses 工具回退返回空。
type aiUserCourseLister interface {
	GetAccessList(userID uint) ([]uint, error)
}

// AIJobView is one admin-facing job row WITH the human-readable names resolved
// from the episode/course/user repos. The job field carries the raw model row
// (so existing code that reads Status/Error/Attempt keeps working); the three
// title fields are best-effort lookups (empty when the referenced row was
// deleted). Mirrors repository.SubtitleJobWithEpisode — name resolution lives
// in the service, not the handler, so handlers stay thin.
type AIJobView struct {
	Job           model.AIJob
	EpisodeTitle  string
	CourseTitle   string
	UserNickname  string
}

// AIRunView is one admin-facing run row WITH the episode/course/user titles
// resolved from the run's job. The Run field embeds model.AIRun (so the JSON
// shape is unchanged — every existing field stays where the frontend expects
// it), and the three title fields are added alongside. The frontend's AiRun
// type already declares episode_title?/course_title? — they were aspirational
// until this type was added.
//
// Why a separate type instead of enriching model.AIRun: AIRun is a GORM model
// shared with internal callers (e.g. ai_service_quiz.go's history builder);
// adding title columns to it would imply a schema change and bleed display
// concerns into the model. A view type keeps the model clean.
type AIRunView struct {
	model.AIRun
	EpisodeTitle string `json:"episode_title,omitempty"`
	CourseTitle  string `json:"course_title,omitempty"`
	UserNickname string `json:"user_nickname,omitempty"`
}

// NewAIService constructs an AIService. resolver may be nil in degenerate
// builds (AI disabled); the service degrades gracefully. unlockService gates
// client-facing quiz access (IsEpisodeVisible); the existing unlock service
// satisfies this interface. userRepo feeds the advice agent's list_user_courses
// tool (查询学生被授权的课程);nil 时该工具回退返回空(advice 据此降级),传
// repository.UserRepository 即可满足 aiUserCourseLister 接口。
func NewAIService(
	db *gorm.DB,
	contentRepo repository.AIContentRepository,
	episodeRepo repository.EpisodeRepository,
	courseRepo repository.CourseRepository,
	resolver *ai.ProviderResolver,
	unlockService UnlockService,
	userRepo aiUserCourseLister,
	glossaryRepo repository.GlossaryRepository,
	subjectRepo repository.SubjectRepository,
) AIService {
	s := &aiService{
		db:            db,
		contentRepo:   contentRepo,
		episodeRepo:   episodeRepo,
		courseRepo:    courseRepo,
		resolver:      resolver,
		unlockService: unlockService,
		userRepo:      userRepo,
		glossaryRepo:  glossaryRepo,
		subjectRepo:   subjectRepo,
	}
	go s.runWorker() // single in-process worker goroutine; see runWorker
	return s
}

// --- job enqueue ---

func (s *aiService) EnqueueSegment(episodeIDs []uint) ([]uint, map[uint]string, error) {
	// segment 优先级 2:高于 summary(派生数据,可慢慢来),低于 quiz(学生正在等)。
	return s.enqueue(episodeIDs, "segment", prioritySegment)
}

func (s *aiService) EnqueueSummary(episodeIDs []uint) ([]uint, map[uint]string, error) {
	// summary 优先级 1:它是 segment 的下游产物,不阻塞任何用户交互,最低即可。
	//
	// 去重门(2026-07-19 加):已有在途 summary job(queued/processing)的 episode 跳过,
	// 进 skipped map。没这道门 admin 连点会堆多条 summary job —— worker 单线程串行跑,
	// 堆多条只是浪费 token + 污染 job 列表(结果幂等,因 UpsertSummary 覆盖)。照抄
	// EnqueueSegmentForCourse / runSegmentJob 链式入队用的 hasPendingJob 模式。
	//
	// 注意:已存在的 AISummary 行(done job 的产物)不算"在途",admin 仍可强制重跑覆盖。
	enqueued := make([]uint, 0, len(episodeIDs))
	skipped := make(map[uint]string)
	for _, epID := range episodeIDs {
		if s.hasPendingJob("summary", epID) {
			skipped[epID] = "已有在途 summary 作业"
			continue
		}
		ep, err := s.episodeRepo.FindByID(epID)
		if err != nil {
			skipped[epID] = "查询课时失败: " + err.Error()
			continue
		}
		if ep == nil {
			skipped[epID] = "课时不存在"
			continue
		}
		epIDCopy, courseIDCopy := epID, ep.CourseID
		job := &model.AIJob{
			JobType:   "summary",
			EpisodeID: &epIDCopy,
			CourseID:  &courseIDCopy,
			Status:    "queued",
			Priority:  prioritySummary,
		}
		if err := s.contentRepo.CreateJob(job); err != nil {
			skipped[epID] = "入队失败: " + err.Error()
			continue
		}
		enqueued = append(enqueued, epID)
	}
	return enqueued, skipped, nil
}

// 作业优先级(高 = ClaimNextQueuedJob 先捞)。设计意图:
//   - quiz(10):学生正盯着屏幕等出题,响应延迟最刺眼,必须最先跑。
//   - segment(2):summary/quiz 的上游,但属于后台批量,不需要抢在 quiz 前。
//   - summary(1):纯派生展示数据,无人在等,放到最低。
//   - advice(1):和 summary 同级——advice 是"打开建议页"或"交卷后"异步入队的,
//     学生不在屏幕前干等(页面会显示 generating),低优先级不饿死 quiz(10)。
//   - course_summary(1):课程级总结,admin 手动触发(无学生在屏幕前干等),后台慢慢跑即可。
//   - user_report(1):admin 用户报告,同 course_summary——admin 触发,后台跑。
const (
	priorityQuiz    = 10
	prioritySegment = 2
	prioritySummary = 1
	priorityAdvice  = 1
	// priorityPolish: polish 是 segment 的上游(字幕先润色再切片),所以略高于
	// segment(2)。但低于 quiz(10)——学生在屏幕前等出题永远最优先。后台批量任务,
	// 不在屏幕前干等。
	priorityPolish = 3
	// priorityCourseSummary:和 advice/summary 同级——admin 手动触发,客户端轮询显示
	// generating,不在屏幕前干等,低优先级不饿死 quiz。
	priorityCourseSummary = 1
	// priorityUserReport:和 advice/summary 同级——admin 触发,页面轮询显示 generating,
	// 不在屏幕前干等,低优先级不饿死 quiz(学生正等的最高优先级)。
	priorityUserReport = 1
)

// enqueue creates one job per episode. It resolves each episode's course_id and
// skips episodes that can't be resolved (deleted / missing). Returns the list of
// enqueued ids and a per-episode skip-reason map (not an error — partial success
// is normal when bulk-enqueuing from the course tree).
func (s *aiService) enqueue(episodeIDs []uint, jobType string, priority int) ([]uint, map[uint]string, error) {
	enqueued := make([]uint, 0, len(episodeIDs))
	skipped := make(map[uint]string)
	for _, epID := range episodeIDs {
		ep, err := s.episodeRepo.FindByID(epID)
		if err != nil {
			skipped[epID] = "查询课时失败: " + err.Error()
			continue
		}
		if ep == nil {
			skipped[epID] = "课时不存在"
			continue
		}
		epID := epID       // capture for pointer (loop var reuse safety)
		courseID := ep.CourseID
		job := &model.AIJob{
			JobType:   jobType,
			EpisodeID: &epID,
			CourseID:  &courseID,
			Status:    "queued",
			Priority:  priority,
		}
		if err := s.contentRepo.CreateJob(job); err != nil {
			skipped[epID] = "入队失败: " + err.Error()
			continue
		}
		enqueued = append(enqueued, epID)
	}
	return enqueued, skipped, nil
}

// EnqueuePolish re-runs the polish pipeline on the given episodes. Unlike
// EnqueueSegment/Summary (which are "first time" triggers), polish has extra
// preconditions:
//   - The episode must have a PRIMARY subtitle whose source is "whisper" — the
//     raw transcript is what polish corrects. source=embedded/manual rows are
//     human-corrected and polish skips them (runPolishJob enforces this too).
//     source=llm_optimized (already polished) IS allowed here — that's the
//     whole point: re-polish with an updated TermDict.
//   - No in-flight polish job (dedup, mirrors the summary chain's hasPendingJob).
//
// Re-polish is drift-safe because runPolishJob reads RawVttContent (the
// immutable whisper snapshot), not the current (possibly already-polished)
// VttContent. See model.Subtitle.RawVttContent doc.
func (s *aiService) EnqueuePolish(episodeIDs []uint) ([]uint, map[uint]string, error) {
	enqueued := make([]uint, 0, len(episodeIDs))
	skipped := make(map[uint]string)
	for _, epID := range episodeIDs {
		ep, err := s.episodeRepo.FindByID(epID)
		if err != nil {
			skipped[epID] = "查询课时失败: " + err.Error()
			continue
		}
		if ep == nil {
			skipped[epID] = "课时不存在"
			continue
		}
		// Must have a whisper-sourced primary subtitle to polish. embedded/
		// manual tracks are human-corrected; polishing them is a no-op at best
		// and confusing at worst (the admin didn't ask whisper to re-transcribe).
		// llm_optimized IS allowed (re-polish with richer TermDict).
		sub, _ := s.episodeRepo.GetSubtitle(epID)
		if sub == nil {
			skipped[epID] = "无字幕"
			continue
		}
		if sub.Source != "whisper" && sub.Source != "llm_optimized" {
			skipped[epID] = "字幕来源为 " + sub.Source + "（仅 whisper 转录可润色）"
			continue
		}
		if s.hasPendingJob("polish", epID) {
			skipped[epID] = "已有在途 polish 作业"
			continue
		}
		epIDCopy, courseIDCopy := epID, ep.CourseID
		job := &model.AIJob{
			JobType:   "polish",
			EpisodeID: &epIDCopy,
			CourseID:  &courseIDCopy,
			Status:    "queued",
			Priority:  priorityPolish,
		}
		if err := s.contentRepo.CreateJob(job); err != nil {
			skipped[epID] = "入队失败: " + err.Error()
			continue
		}
		enqueued = append(enqueued, epID)
	}
	return enqueued, skipped, nil
}

// EnqueueSegmentForCourse 批量给一个课程下所有"已有字幕"的 episode 入队
// segment job。用于 admin 把 AI 开关从 off→on 的回填场景:开关关着时到达的
// 字幕在 OnSubtitleCompleted 里被早返回了,不会产生任何 AI 工作,所以历史
// 字幕需要在这个时机补一次。只挑有字幕的 episode,因为 segment job 没有
// 字幕会直接失败(无源材料)。返回实际入队的作业数。
func (s *aiService) EnqueueSegmentForCourse(courseID uint) (int, error) {
	episodes, err := s.episodeRepo.ListByCourse(courseID)
	if err != nil {
		return 0, err
	}
	if len(episodes) == 0 {
		return 0, nil
	}
	// 收集 episode id,批量查"是否有字幕",避免逐条 N+1。
	ids := make([]uint, 0, len(episodes))
	for _, ep := range episodes {
		ids = append(ids, ep.ID)
	}
	subCounts, err := s.episodeRepo.CountSubtitlesByEpisodes(ids)
	if err != nil {
		return 0, err
	}
	// 收集目标:有字幕,且当前没有在途 segment job(queued/processing)。这道去重
	// 门对齐 quiz 路径的 hasPendingQuizJob —— 没有它的话,admin 反复切开关(off→on→
	// off→on)或开关 on 时恰好 OnSubtitleCompleted 已入过 job,会堆出多条 segment
	// job 重复跑 embedding。结果幂等(ReplaceChunksForEpisode 是 DELETE+INSERT),
	// 但白白浪费 LLM/embedding 配额,也污染 admin 的 job 列表。注意:只在这个批量
	// 回填路径去重;admin 手动 EnqueueSegment 走通用 enqueue,保留强制重跑能力。
	targetIDs := make([]uint, 0, len(ids))
	for _, id := range ids {
		if subCounts[id] > 0 && !s.hasPendingJob("segment", id) {
			targetIDs = append(targetIDs, id)
		}
	}
	if len(targetIDs) == 0 {
		return 0, nil
	}
	enqueued, _, err := s.EnqueueSegment(targetIDs)
	if err != nil {
		return 0, err
	}
	return len(enqueued), nil
}

// --- subtitle completion hook ---

func (s *aiService) OnSubtitleCompleted(episodeID uint) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil || ep == nil {
		return // can't do anything without the episode
	}
	// Only auto-segment if the course has AI enabled. This is the gate that keeps
	// AI a pure add-on: a course with AI off never triggers AI work, even when
	// subtitles arrive.
	course, err := s.courseRepo.FindByID(ep.CourseID)
	if err != nil || course == nil {
		return
	}
	if !course.AISummaryEnabled && !course.AIQuizEnabled {
		return // AI off for this course — leave it as a plain subtitled video.
	}
	// Branch on the subtitle's source:
	//   whisper → enqueue polish first; polish's chain will enqueue segment
	//            once the transcript is corrected. This avoids segmenting
	//            (and embedding) a raw transcript full of homophones.
	//   embedded / manual → already human-corrected, skip polish and go
	//            straight to segment.
	// We also gate polish on a resolver being configured: if AI isn't set up
	// at all, polishing would just fail-and-block, so we skip straight to
	// segment (which will itself skip cleanly on no-embedder) rather than
	// leave the chain stuck on a job that can't succeed yet.
	sub, _ := s.episodeRepo.GetSubtitle(episodeID)
	shouldPolish := sub != nil && sub.Source == "whisper" && s.resolver != nil
	// Dedup guard target depends on the branch: polish branch dedups on polish
	// jobs (we're about to enqueue one), segment branch on segment jobs.
	// Either way, if the relevant next step is already queued/processing we
	// don't stack another. The chain (polish→segment, or direct segment) is
	// idempotent in result, so a duplicate only wastes budget.
	if shouldPolish {
		if s.hasPendingJob("polish", episodeID) {
			return
		}
		epID, courseID := episodeID, ep.CourseID
		job := &model.AIJob{
			JobType:   "polish",
			EpisodeID: &epID,
			CourseID:  &courseID,
			Status:    "queued",
			Priority:  priorityPolish,
		}
		if err := s.contentRepo.CreateJob(job); err != nil {
			log.Printf("AI: failed to enqueue polish job for episode %d: %v", episodeID, err)
		}
		return
	}
	s.enqueueSegmentForPolish(episodeID, ep.CourseID)
}

// hasPendingJob reports whether a queued/processing job of jobType exists for
// the episode. The generic sibling of hasPendingQuizJob: used to suppress
// duplicate auto-enqueue (e.g. the summary chain firing while a prior summary
// job is still in flight). done/failed/skipped jobs don't count — those are
// finished states and a new trigger means we genuinely want a fresh attempt.
func (s *aiService) hasPendingJob(jobType string, episodeID uint) bool {
	var count int64
	s.db.Model(&model.AIJob{}).
		Where("job_type = ? AND episode_id = ? AND status IN ?", jobType, episodeID, []string{"queued", "processing"}).
		Count(&count)
	return count > 0
}

// --- in-process worker ---

// runWorker is the single goroutine that drains AI jobs. It polls every few
// seconds for queued segment/summary jobs and processes them sequentially.
// Sequential is fine for MVP (jobs are LLM-bound, ~5-30s each; parallelism would
// just rate-limit at the provider). The poll interval is a tradeoff: short =
// jobs start quickly, long = less DB churn when idle.
//
// It runs for the process lifetime; there's no shutdown handshake beyond the
// process exiting (an in-flight LLM call may be interrupted, which is fine —
// the job stays "processing" and a future reaper would reset it; for now, a
// killed job is just lost, acceptable for a generation task that the admin can
// re-trigger).
func (s *aiService) runWorker() {
	jobTypes := []string{"segment", "summary", "quiz", "advice", "course_summary", "user_report", "polish"}
	for {
		// recover guard: a panic in any job handler (nil deref on a deleted
		// row, a bug in a new job type, etc.) MUST NOT kill the worker
		// goroutine — that would silently halt ALL background AI processing
		// (segment/summary/quiz/polish/...) until the process is restarted.
		// We mark the panicking job failed (so it surfaces in the admin UI
		// instead of vanishing) and keep draining the queue. The panic's stack
		// is logged so it's still debuggable.
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("AI worker: PANIC recovered (worker stays alive): %v", r)
					// Best-effort failJob on whatever job was in flight; we don't
					// know which one here (processOneJob claimed it internally),
					// so we can't stamp a specific error. The job stays
					// 'processing' and the reaper will reset it after 30min.
					// That's acceptable — the point is keeping the worker alive.
				}
			}()
			s.processOneJob(jobTypes)
		}()
		// Poll every 3s. A real impl might use a channel signaled on enqueue,
		// but a poll is simpler and the 3s latency is invisible to the admin
		// (jobs take 5-30s to run anyway).
		sleep(3)
	}
}

// processOneJob claims and runs at most one queued job. Split from runWorker for
// testability (a test can drive single jobs without the poll loop).
func (s *aiService) processOneJob(jobTypes []string) {
	job, err := s.contentRepo.ClaimNextQueuedJob(jobTypes)
	if err != nil {
		log.Printf("AI worker: claim error: %v", err)
		return
	}
	if job == nil {
		return // nothing queued
	}
	switch job.JobType {
	case "segment":
		s.runSegmentJob(job)
	case "summary":
		s.runSummaryJob(job)
	case "quiz":
		s.runQuizJob(job)
	case "advice":
		s.runAdviceJob(job)
	case "course_summary":
		s.runCourseSummaryJob(job)
	case "user_report":
		s.runUserReportJob(job)
	case "polish":
		s.runPolishJob(job)
	default:
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "unknown job_type: "+job.JobType, nil)
	}
}

// runSegmentJob segments an episode's subtitle into chunks and embeds them.
// Steps: load subtitle → parse SRT → segment → embed each chunk → persist.
// On any failure the job is marked failed with the error message (visible in the
// admin UI) so the operator can diagnose without server logs.
func (s *aiService) runSegmentJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	// segment job 必须有真实 episode/course(EpisodeID/CourseID 是 *uint,subject 级
	// advice job 才会留 nil,segment job 永远有值)。这里 deref + 守卫,后续逻辑用
	// 本地 uint 变量,避免到处 ptrVal。
	if job.EpisodeID == nil || job.CourseID == nil {
		s.failJob(job, "segment job missing episode_id/course_id")
		return
	}
	episodeID, courseID := *job.EpisodeID, *job.CourseID
	// 1. Load the episode's subtitle.
	sub, err := s.episodeRepo.GetSubtitle(episodeID)
	if err != nil {
		s.failJob(job, "load subtitle: "+err.Error())
		return
	}
	if sub == nil {
		s.failJob(job, "no subtitle for this episode")
		return
	}
	// 2. Parse + segment. The subtitle is stored as VTT; convert to SRT for
	// the existing parser (VTT styling/settings stripped — AI doesn't need them).
	cues, err := ai.ParseSRT(subtitle.VttToSrt(sub.VttContent))
	if err != nil {
		s.failJob(job, "parse SRT: "+err.Error())
		return
	}
	drafts := ai.SegmentChunks(cues, ai.DefaultSegmentConfig())
	if len(drafts) == 0 {
		s.failJob(job, "segmentation produced no chunks")
		return
	}
	// 3. Embed each chunk's text. Batched for efficiency (one embedder call per
	// chunk would be wasteful whether local or API).
	embedder, err := s.resolver.ResolveEmbedder()
	if err != nil {
		s.failJob(job, "resolve embedder: "+err.Error())
		return
	}
	texts := make([]string, len(drafts))
	for i, d := range drafts {
		texts[i] = d.Text
	}
	vectors, err := embedder.Embed(ctx, texts)
	if err != nil {
		s.failJob(job, "embed chunks: "+err.Error())
		return
	}
	// 4. Persist. Convert drafts + vectors into ContentChunk rows.
	chunks := make([]model.ContentChunk, len(drafts))
	for i, d := range drafts {
		embJSON, _ := json.Marshal(vectors[i])
		chunks[i] = model.ContentChunk{
			ChunkIndex: d.ChunkIndex,
			StartTime:  &d.StartTime,
			EndTime:    &d.EndTime,
			Text:       d.Text,
			Embedding:  string(embJSON),
			SourceRef:  fmt.Sprintf("%d", sub.ID),
		}
	}
		if err := s.contentRepo.ReplaceChunksForEpisode(episodeID, courseID, "subtitle", chunks); err != nil {
			s.failJob(job, "persist chunks: "+err.Error())
			return
		}
		s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)

		// 链式触发 summary:segment 是 summary 的上游,既然刚把源材料(chunk)写好,
		// 紧接着入队 summary 就能让一节字幕落地后自动走到结构化总结,无需 admin 再
		// 手动点按钮(那个 admin UI 其实一直没接,导致 summary 永远没人触发)。
		// 三道门:课程开关、尚无 summary、尚无在途 summary 作业。最后一道防止
		// 重复入队(例如重新分段时旧 summary job 还在跑)。
		// 注意:GetSummary==nil 这道门意味着"重新分段(re-segment)不会自动刷新已有
		// summary" —— 这是预期:re-segment 通常只是修切片,内容没变;若确实想刷新
		// summary,admin 应走手动 EnqueueSummary(强制重跑,不经此链式门)。
		if course, cerr := s.courseRepo.FindByID(courseID); cerr == nil && course != nil && course.AISummaryEnabled {
			if existing, serr := s.contentRepo.GetSummary(episodeID); serr == nil && existing == nil {
				if !s.hasPendingJob("summary", episodeID) {
					sjob := &model.AIJob{
						JobType:   "summary",
						EpisodeID: &episodeID,
						CourseID:  &courseID,
						Status:    "queued",
						Priority:  prioritySummary,
					}
					if err := s.contentRepo.CreateJob(sjob); err != nil {
						log.Printf("AI: failed to chain-enqueue summary job for episode %d: %v", episodeID, err)
					}
				}
			}
		}
}

// runPolishJob runs the subtitle-homophone-correction pipeline on an episode's
// primary whisper subtitle. This is the first link of the post-Complete chain
// (polish → segment → summary): a whisper transcript lands raw and full of
// homophone errors (军/局→车, 金→进 in xiangqi), so before the segmenter cuts
// it into chunks we let an LLM fix the obvious ones using a course-specific
// term dictionary.
//
// Failure semantics are deliberately "block, don't skip": a failed polish job
// HALTS the chain — segment is NOT auto-enqueued. The admin sees the failed
// job in the AI Console and chooses: Retry (re-run polish with the same or a
// fixed provider) or SkipPolish (give up on polish, fall back to the raw
// subtitle, and let segment/summary proceed off the un-corrected text). This
// matches the user's explicit requirement that polish problems surface for
// human judgement rather than silently degrading downstream quality.
//
// Only whisper-sourced primary subtitles are polished. Embedded/manual tracks
// are already human-corrected; OnSubtitleCompleted routes them straight to
// segment, never enqueueing a polish job in the first place. The source check
// here is defense-in-depth (a misrouted job skips cleanly instead of polishing
// a track that shouldn't be).
func (s *aiService) runPolishJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		// No resolver = AI entirely off. Block (failed) so the admin notices
		// once they configure a provider; they can retry or skip then.
		s.failJob(job, "AI not configured (no resolver)")
		return
	}
	if job.EpisodeID == nil || job.CourseID == nil {
		s.failJob(job, "polish job missing episode_id/course_id")
		return
	}
	episodeID, courseID := *job.EpisodeID, *job.CourseID

	sub, err := s.episodeRepo.GetSubtitle(episodeID)
	if err != nil {
		s.failJob(job, "load subtitle: "+err.Error())
		return
	}
	if sub == nil {
		s.failJob(job, "no primary subtitle for this episode")
		return
	}
	if sub.Source != "whisper" {
		// Shouldn't happen (OnSubtitleCompleted gates on source), but if a
		// misrouted job slips through, skip it cleanly rather than polishing
		// a track that's already human-corrected. This is NOT a failure —
		// chain to segment so downstream proceeds.
		s.contentRepo.UpdateJobStatus(job.ID, "skipped",
			"source="+sub.Source+" not eligible for polish", nil)
		s.enqueueSegmentForPolish(episodeID, courseID)
		return
	}

	llm, err := s.resolver.ResolveChatByPurpose("polish")
	if err != nil {
		// Provider not configured / misconfigured. Block — admin fixes the
		// provider config and retries. We do NOT fall through to segment,
		// because the whole point of polish is to fix the raw transcript
		// before AI consumes it.
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("polish")

	// Build the polish request: TermDict comes from Course + Subject merge
	// (Course.EffectiveTermDict). Subject is also passed to the LLM as domain
	// context ("xiangqi" vs "math" primes it toward the right terminology).
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		s.failJob(job, "load course: "+err.Error())
		return
	}
	if course == nil {
		// courseRepo.FindByID returns (nil, nil) when the row was deleted
		// between enqueue and run. Split from the err branch above so we don't
		// dereference a nil err — that panic would kill the AI worker goroutine
		// (runWorker has no recover).
		s.failJob(job, fmt.Sprintf("course %d not found (deleted after polish enqueue?)", courseID))
		return
	}
	var subject model.Subject
	if s.subjectRepo != nil {
		if subj, serr := s.subjectRepo.FindByID(course.SubjectID); serr == nil && subj != nil {
			subject = *subj
		}
	}
	termDict := course.EffectiveTermDict(subject)
	subjectLabel := subject.Label
	if subjectLabel == "" {
		// Fall back to the key (e.g. "math") when Label is empty — the
		// polish prompt just needs SOME domain hint.
		subjectLabel = subject.Key
	}

	// Polish deadline: the PoC ran 7m13s for a 157k-char episode at concurrency 3.
	// 20 min is a generous ceiling that still catches a stuck relay.
	polishCtx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()

	// Source the polish input from RawVttContent (the immutable pre-polish
	// snapshot) when it exists, falling back to VttContent for legacy rows
	// that predate the RawVttContent column. This is the WHOLE POINT of
	// RawVttContent (see model.Subtitle doc): re-running polish must start
	// from the original whisper transcript each time, not from a prior polish
	// result — otherwise LLM drift compounds across re-polishes. The snapshot
	// is written by SaveSubtitleWithSource (Complete/upload/embedded extract
	// paths) and never overwritten by polish itself.
	polishInput := sub.RawVttContent
	if strings.TrimSpace(polishInput) == "" {
		polishInput = sub.VttContent
	}

	result, err := polish.Polish(polishCtx, llm, modelName, polish.PolishRequest{
		VttContent: polishInput,
		TermDict:   termDict,
		Subject:    subjectLabel,
	})
	if err != nil {
		s.failJob(job, "polish: "+err.Error())
		return
	}

	// Persist the polished subtitle. We write ONLY VttContent + Optimized +
	// Source — RawVttContent stays empty here so episodeRepo.SaveSubtitle's
	// "non-empty only" guard leaves the original snapshot untouched. IsPrimary
	// is echoed back from the loaded sub so the upsert doesn't accidentally
	// demote the primary track.
	if err := s.episodeRepo.SaveSubtitle(&model.Subtitle{
		ID:            sub.ID,
		EpisodeID:     sub.EpisodeID,
		Language:      sub.Language,
		Label:         sub.Label,
		VttContent:    result.PolishedVtt,
		Source:        "llm_optimized",
		Optimized:     true,
		IsPrimary:     sub.IsPrimary,
	}); err != nil {
		s.failJob(job, "persist polished subtitle: "+err.Error())
		return
	}

	// Mine term candidates for the admin review queue (PR2.5 UI). Best-effort:
	// a failure here doesn't unwind the polish itself (the subtitle is already
	// corrected and useful); we just log and move on. nil glossaryRepo in tests.
	if s.glossaryRepo != nil && len(result.Glossary) > 0 {
		candidates := polishGlossaryToModel(courseID, result.Glossary)
		if err := s.glossaryRepo.UpsertCandidates(candidates); err != nil {
			log.Printf("AI: polish job %d: glossary upsert failed (non-fatal): %v", job.ID, err)
		}
	}

	detail := fmt.Sprintf("polished: %d/%d cues changed, %d glossary candidates, cost≈%s",
		result.Stats.ChangedCues, result.Stats.TotalCues, len(result.Glossary),
		result.Stats.Duration.Truncate(time.Second))
	if result.Stats.PartialOptimized {
		// List which chunks failed + their last error, capped so a pathological
		// relay doesn't blow up the error column. The chunk index is 0-based
		// and maps to a contiguous cue range, so the admin can tell which part
		// of the subtitle wasn't polished.
		detail += fmt.Sprintf(" (partial: %d/%d chunks failed", result.Stats.FailedChunks, result.Stats.ChunkCount)
		// Collect + sort by chunk idx NUMERICALLY (not lexically — "chunk#10"
		// would sort before "chunk#2" under string sort). Cap each error at 120
		// runes so one verbose parse failure doesn't dominate the job detail.
		type failEntry struct {
			idx int
			err string
		}
		entries := make([]failEntry, 0, len(result.Stats.FailedChunkErrors))
		for idx, e := range result.Stats.FailedChunkErrors {
			// Truncate by RUNE count, not bytes — error strings carry Chinese
			// (ffmpeg/relay messages localized) and a byte cut would produce
			// invalid UTF-8 mid-character.
			if rs := []rune(e); len(rs) > 120 {
				e = string(rs[:120]) + "…"
			}
			entries = append(entries, failEntry{idx, e})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].idx < entries[j].idx })
		if len(entries) > 0 {
			parts := make([]string, len(entries))
			for i, e := range entries {
				parts[i] = fmt.Sprintf("chunk#%d: %s", e.idx, e.err)
			}
			detail += "; " + strings.Join(parts, "; ")
		}
		detail += ")"
	}
	s.contentRepo.UpdateJobStatus(job.ID, "done", detail, nil)

	// Chain to segment NOW that the polished subtitle is in place. Mirrors
	// runSegmentJob's chain-to-summary: same priority, same hasPendingJob guard.
	s.enqueueSegmentForPolish(episodeID, courseID)
}

// enqueueSegmentForPolish chains a segment job after a successful polish (or
// after a non-whisper subtitle completed, or after the admin skips polish).
// Centralized so runPolishJob, SkipPolish, and OnSubtitleCompleted's non-whisper
// branch all share the exact same gate logic. The hasPendingJob guard prevents
// stacking: if a segment job is already queued/processing (e.g. a previous run
// left one), this is a no-op.
func (s *aiService) enqueueSegmentForPolish(episodeID, courseID uint) {
	if s.hasPendingJob("segment", episodeID) {
		return
	}
	epID, cID := episodeID, courseID
	job := &model.AIJob{
		JobType:   "segment",
		EpisodeID: &epID,
		CourseID:  &cID,
		Status:    "queued",
		Priority:  prioritySegment,
	}
	if err := s.contentRepo.CreateJob(job); err != nil {
		log.Printf("AI: failed to chain-enqueue segment job for episode %d: %v", episodeID, err)
	}
}

// polishGlossaryToModel converts the polish package's mined candidates into
// model rows ready for UpsertCandidates. EvidenceCount is seeded from the
// number of cue ids the LLM cited, so the first sighting of a rule starts at
// the right count instead of 1. EvidenceSample is left empty for now — the
// polish package carries cue ids (not text), and PR2.5's accept UI will
// re-derive sample text from the persisted diff if the admin wants to see
// examples. The schema field is kept ready for that.
func polishGlossaryToModel(courseID uint, in []polish.GlossaryCandidate) []model.GlossaryCandidate {
	out := make([]model.GlossaryCandidate, 0, len(in))
	for _, g := range in {
		out = append(out, model.GlossaryCandidate{
			CourseID:      courseID,
			Original:      g.Original,
			Corrected:     g.Corrected,
			Context:       g.Context,
			Confidence:    g.Confidence,
			EvidenceCount: len(g.EvidenceIDs),
			Status:        "pending",
		})
	}
	return out
}


// runSummaryJob reads an episode's chunks and asks the summarizer to summarize.
// Requires chunks to exist (a segment job must have run first). If none exist,
// the job is marked failed with a clear message rather than silently producing
// an empty summary.
func (s *aiService) runSummaryJob(job *model.AIJob) {
	ctx := context.Background()
	if s.resolver == nil {
		s.contentRepo.UpdateJobStatus(job.ID, "skipped", "AI not configured (no resolver)", nil)
		return
	}
	if job.EpisodeID == nil || job.CourseID == nil {
		s.failJob(job, "summary job missing episode_id/course_id")
		return
	}
	episodeID, courseID := *job.EpisodeID, *job.CourseID
	// Chunks must exist. If they don't, the operator likely needs to run
	// segmentation first; surface that clearly.
	chunks, err := s.contentRepo.ListChunks(episodeID, "subtitle")
	if err != nil {
		s.failJob(job, "load chunks: "+err.Error())
		return
	}
	if len(chunks) == 0 {
		s.failJob(job, "no content chunks — run segmentation first")
		return
	}
	// Build course context for the prompt. EffectiveXxxHint(subject) 在课程 AIConfig
	// 空时回退到学科级 AIConfig(最后兜底 deprecated AIHint 列)。summary agent 吃的是
	// SummaryHint(风格/侧重点)+ TermDict(横切术语纠错),不再吃 quizHint。
	// 注意:courseRepo.FindByID 不 Preload Subject(避免 UpdateCourse 的 Save 误改关联),
	// 这里单独用 s.db 查一次 subject 供 Effective* 回退 + prompt 的"科目"显示。
	course, _ := s.courseRepo.FindByID(courseID)
	subject := ""
	summaryHint := ""
	termDict := ""
	if course != nil {
		var subj model.Subject
		if course.SubjectID != 0 {
			s.db.First(&subj, course.SubjectID) // 取不到 subj 保持零值,Effective 兜底
		}
		if subj.Label != "" {
			subject = subj.Label
		}
		summaryHint = course.EffectiveSummaryHint(subj)
		termDict = course.EffectiveTermDict(subj)
	}
	llm, err := s.resolver.ResolveChatByPurpose("summary")
	if err != nil {
		s.failJob(job, "resolve chat provider: "+err.Error())
		return
	}
	modelName := s.resolver.ChatModelNameByPurpose("summary")
	summarizer := agent.NewSummarizer(llm, s.contentRepo, modelName)
	_, err = summarizer.Summarize(ctx, agent.SummarizerRequest{
		EpisodeID:   episodeID,
		CourseID:    courseID,
		SummaryHint: summaryHint,
		TermDict:    termDict,
		Subject:     subject,
		Chunks:      chunks,
	}, job.ID)
	if err != nil {
		s.failJob(job, err.Error())
		return
	}
	s.contentRepo.UpdateJobStatus(job.ID, "done", "", nil)
}

// failJob marks a job failed with an error message and logs it. Centralized so
// every failure path records consistently (the admin UI shows the error string).
func (s *aiService) failJob(job *model.AIJob, msg string) {
	log.Printf("AI job %d (%s) episode %d failed: %s", job.ID, job.JobType, model.PtrVal(job.EpisodeID), msg)
	s.contentRepo.UpdateJobStatus(job.ID, "failed", msg, nil)
}

// --- reads ---

func (s *aiService) GetSummary(episodeID uint) (*model.AISummary, error) {
	return s.contentRepo.GetSummary(episodeID)
}

// --- admin regen + delete ---

func (s *aiService) DeleteSummary(episodeID uint) error {
	return s.contentRepo.DeleteSummary(episodeID)
}

func (s *aiService) DeleteQuiz(quizID uint) error {
	// 删 quiz:Fk CASCADE 会自动清 Question + Answer,所以这里只删 quiz 一行。
	return s.contentRepo.DeleteQuiz(quizID)
}

func (s *aiService) DeleteAdvice(userID uint, scope string, scopeID uint) error {
	return s.contentRepo.DeleteAdvice(userID, scope, scopeID)
}

func (s *aiService) DeleteCourseSummary(courseID uint) error {
	return s.contentRepo.DeleteCourseSummary(courseID)
}

func (s *aiService) DeleteUserReport(userID uint) error {
	return s.contentRepo.DeleteUserReport(userID)
}

func (s *aiService) ListUserAdvice(userID uint) ([]model.StudyAdvice, error) {
	return s.contentRepo.ListUserAdvice(userID)
}

func (s *aiService) ListJobs(jobType, status string, limit int) ([]AIJobView, error) {
	jobs, err := s.contentRepo.ListJobs(jobType, status, limit)
	if err != nil {
		return nil, err
	}
	names := s.resolveJobNames(jobs)
	out := make([]AIJobView, 0, len(jobs))
	for _, j := range jobs {
		v := AIJobView{Job: j}
		v.EpisodeTitle, v.CourseTitle, v.UserNickname = names.forJob(&j)
		out = append(out, v)
	}
	return out, nil
}

func (s *aiService) GetJob(id uint) (*AIJobView, error) {
	job, err := s.contentRepo.GetJob(id)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, nil
	}
	v := &AIJobView{Job: *job}
	names := s.resolveJobNames([]model.AIJob{*job})
	v.EpisodeTitle, v.CourseTitle, v.UserNickname = names.forJob(job)
	return v, nil
}

func (s *aiService) ListRunsForJob(jobID uint) ([]model.AIRun, error) {
	return s.contentRepo.ListRunsForJob(jobID)
}

func (s *aiService) ListRecentRuns(limit int) ([]model.AIRun, error) {
	return s.contentRepo.ListRecentRuns(limit)
}

// ListRecentRunsEnriched returns recent runs with episode/course/user titles
// resolved via the run's job. Powers the admin "决策痕迹(最近运行)" list and
// the Dashboard 最近活动 feed — both want to show WHAT (capability) plus WHERE
// (course/episode), not just the capability.
func (s *aiService) ListRecentRunsEnriched(limit int) ([]AIRunView, error) {
	runs, err := s.contentRepo.ListRecentRuns(limit)
	if err != nil {
		return nil, err
	}
	return s.enrichRuns(runs), nil
}

// ListRunsForJobEnriched is the per-job variant, used by GetAIJob so the job
// detail view's run list also shows episode/course context.
func (s *aiService) ListRunsForJobEnriched(jobID uint) ([]AIRunView, error) {
	runs, err := s.contentRepo.ListRunsForJob(jobID)
	if err != nil {
		return nil, err
	}
	return s.enrichRuns(runs), nil
}

// enrichRuns batch-resolves episode/course/user titles for a set of runs by
// joining through AIRun.JobID → AIJob → EpisodeID/CourseID/UserID. The job
// batch is loaded in one query (not per-run), then resolveJobNames does the
// id→title fanout. Best-effort: any lookup failure leaves the title empty.
func (s *aiService) enrichRuns(runs []model.AIRun) []AIRunView {
	if len(runs) == 0 {
		return []AIRunView{}
	}
	// Collect distinct job IDs the runs reference (JobID=0 means ad-hoc, skip).
	seen := map[uint]bool{}
	jobIDs := make([]uint, 0, len(runs))
	for _, r := range runs {
		if r.JobID != 0 && !seen[r.JobID] {
			seen[r.JobID] = true
			jobIDs = append(jobIDs, r.JobID)
		}
	}
	// Load all referenced jobs in one query, then reuse the existing job-name
	// resolver (it dedups episode/course/user id lookups internally).
	nameByJobID := map[uint]struct{ ep, course, user string }{}
	if len(jobIDs) > 0 {
		var jobs []model.AIJob
		if err := s.db.Where("id IN ?", jobIDs).Find(&jobs).Error; err == nil && len(jobs) > 0 {
			cache := s.resolveJobNames(jobs)
			for _, j := range jobs {
				ep, course, user := cache.forJob(&j)
				nameByJobID[j.ID] = struct{ ep, course, user string }{ep, course, user}
			}
		}
	}
	out := make([]AIRunView, len(runs))
	for i, r := range runs {
		v := AIRunView{AIRun: r}
		if r.JobID != 0 {
			if n, ok := nameByJobID[r.JobID]; ok {
				v.EpisodeTitle = n.ep
				v.CourseTitle = n.course
				v.UserNickname = n.user
			}
		}
		out[i] = v
	}
	return out
}

func (s *aiService) GetRun(id uint) (*model.AIRun, error) {
	return s.contentRepo.GetRun(id)
}

func (s *aiService) JobStats() (map[string]int, error) {
	return s.contentRepo.JobStats()
}

// ListEpisodeSummaryStatus 返回某课程下已有 summary 的 episode id 列表。
// 给 admin 内容管理 tab gate 每集"删除"按钮:无 summary 不显示删除。
func (s *aiService) ListEpisodeSummaryStatus(courseID uint) ([]uint, error) {
	return s.contentRepo.ListEpisodeIDsWithSummaryByCourse(courseID)
}

// CountEpisodesWithSummary 课程总览陈旧检测用:跟 AICourseSummary.EpisodeCountAtGen
// 对比,差值 > 0 = 已新增了 summary 的课时,建议刷新。
func (s *aiService) CountEpisodesWithSummary(courseID uint) (int64, error) {
	return s.contentRepo.CountEpisodesWithSummaryByCourse(courseID)
}

// ReapStaleJobs 委托给 repo,固定 30 分钟阈值。一个 LLM 调用最多 ~30s,加上
// ReAct 多轮也就几分钟;claimed_at 超过半小时还停在 processing 几乎可以肯定
// 是 worker 挂了,重置回 queued 让下一轮 poll 重新认领。
func (s *aiService) ReapStaleJobs() (int64, error) {
	return s.contentRepo.ReapStaleJobs(30 * time.Minute)
}

// ResetJob 委托给 repo:把单条 processing 任务重置回 queued。repo 会校验当前
// 必须处于 processing,否则返回 ErrJobNotProcessing(非致命,handler 转 409)。
func (s *aiService) ResetJob(jobID uint) error {
	return s.contentRepo.ResetJob(jobID)
}

// RetryJob 委托给 repo:把单条 failed 任务复位回 queued,让 worker 重跑。repo 校验
// 当前必须处于 failed,否则返回 ErrJobNotFailed(非致命,handler 转 409)。
func (s *aiService) RetryJob(jobID uint) error {
	return s.contentRepo.RetryJob(jobID)
}

// SkipPolish is the admin escape hatch for a stuck (failed) polish job. It:
//  1. validates the job is a polish job AND currently failed — anything else
//     is a misuse (409 to the admin, not a silent success).
//  2. marks the job done with "admin skipped polish" so it leaves the failed
//     queue and stops showing as an error.
//  3. chains a segment job so downstream AI proceeds off the raw subtitle.
//
// The subtitle itself is left untouched (still raw whisper text, optimized=
// false). If the admin later wants polish after all, they enqueue a fresh
// polish job via EnqueueSegmentForCourse-style batch (or the future regen UI).
// There's no "un-skip" — re-running polish is just a new polish job.
func (s *aiService) SkipPolish(jobID uint) error {
	job, err := s.contentRepo.GetJob(jobID)
	if err != nil {
		return err
	}
	if job == nil {
		return repository.ErrJobNotFound
	}
	if job.JobType != "polish" {
		return repository.ErrJobNotPolish
	}
	if job.Status != "failed" {
		return repository.ErrJobNotFailed
	}
	s.contentRepo.UpdateJobStatus(jobID, "done", "admin skipped polish", nil)
	if job.EpisodeID != nil && job.CourseID != nil {
		s.enqueueSegmentForPolish(*job.EpisodeID, *job.CourseID)
	}
	return nil
}

// --- glossary candidate review (PR2.5) ---
//
// The polish job mines term-correction rules and leaves them as pending
// candidates. The admin reviews them in the AI Console: accept promotes a rule
// into the course TermDict (so future polish runs apply it automatically),
// reject hides it from the default review list. Accepted/rejected rows are
// kept so UpsertCandidate won't re-create them next polish run.

// formatTermDictEntry renders one candidate as the TermDict format the polish
// prompt understands: "original→corrected（context）". Parens + context are
// omitted when context is empty (the prompt tolerates both forms). This is the
// exact shape Course.EffectiveTermDict returns to the polish job, so what the
// admin accepts is byte-identical to what the next polish run receives.
func formatTermDictEntry(original, corrected, context string) string {
	context = strings.TrimSpace(context)
	if context == "" {
		return original + "→" + corrected
	}
	return original + "→" + corrected + "（" + context + "）"
}

// appendTermDict appends one entry to a course's TermDict string, respecting
// the ';' separator. Handles the empty-existing case (no leading separator)
// and dedup: if the exact entry is already present (admin re-accepting after a
// context edit), it's a no-op.
func appendTermDict(existing, entry string) string {
	existing = strings.TrimSpace(existing)
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return existing
	}
	// Dedup: scan existing semicolon-separated entries for an exact match.
	for _, e := range strings.Split(existing, ";") {
		if strings.TrimSpace(e) == entry {
			return existing
		}
	}
	if existing == "" {
		return entry
	}
	return existing + ";" + entry
}

// applyGlossaryToCourse mutates one course's AIConfig.TermDict in place by
// appending the entry, then persists the course. Centralized so the accept and
// the apply-to-siblings paths share the exact same write logic.
func (s *aiService) applyGlossaryToCourse(courseID uint, entry string) error {
	course, err := s.courseRepo.FindByID(courseID)
	if err != nil {
		return fmt.Errorf("load course %d: %w", courseID, err)
	}
	if course == nil {
		return nil // course deleted between polish and review — skip silently
	}
	cfg := course.AIConfig()
	cfg.TermDict = appendTermDict(cfg.TermDict, entry)
	course.SetAIConfig(cfg)
	return s.courseRepo.Update(course)
}

// ListGlossaryCandidates delegates to the repo. The handler passes "" for
// status to show all, or "pending" for the default review list.
func (s *aiService) ListGlossaryCandidates(courseID uint, status string) ([]model.GlossaryCandidate, error) {
	if s.glossaryRepo == nil {
		return nil, errors.New("glossary subsystem not configured")
	}
	return s.glossaryRepo.ListByCourse(courseID, status)
}

// AcceptGlossaryCandidate promotes one pending candidate into TermDict. The
// admin may override corrected/context (e.g. the LLM suggested 居 but the admin
// knows it should be 車) — the overrides are applied both to the candidate row
// (so the record reflects what was actually accepted) and to the TermDict entry.
// applyToSubjectSiblings repeats the TermDict append on every other course
// under the same subject, sparing the admin from per-course review.
func (s *aiService) AcceptGlossaryCandidate(id uint, correctedOverride, contextOverride string, applyToSubjectSiblings bool) error {
	if s.glossaryRepo == nil {
		return errors.New("glossary subsystem not configured")
	}
	c, err := s.glossaryRepo.FindByID(id)
	if err != nil {
		return err
	}
	if c == nil {
		return repository.ErrGlossaryNotFound
	}
	if c.Status != "pending" {
		return ErrGlossaryNotPending
	}
	// Apply admin overrides (empty = keep the LLM's values).
	corrected := strings.TrimSpace(correctedOverride)
	if corrected == "" {
		corrected = c.Corrected
	}
	context := strings.TrimSpace(contextOverride)
	if context == "" {
		context = c.Context
	}
	// Stamp the row as accepted with the FINAL (possibly admin-edited) values.
	c.Corrected = corrected
	c.Context = context
	c.Status = "accepted"
	now := time.Now()
	c.AcceptedAt = &now
	if err := s.glossaryRepo.Update(c); err != nil {
		return err
	}

	// Promote to the originating course's TermDict.
	entry := formatTermDictEntry(c.Original, corrected, context)
	if err := s.applyGlossaryToCourse(c.CourseID, entry); err != nil {
		return err
	}

	// Optional cross-course推广: same subject, every other course gets the rule
	// too. Best-effort — a failure on one sibling course doesn't unwind the
	// accept itself (the candidate is already marked accepted on the origin
	// course); we log and continue so one bad course doesn't block the rest.
	if applyToSubjectSiblings {
		origin, err := s.courseRepo.FindByID(c.CourseID)
		// Guard against SubjectID==0: courseRepo.List("", 0, ...) skips the
		// subject filter entirely (0 means "no filter" in that query), so
		// without this check we'd推广 the rule to EVERY course in the DB — a
		// xiangqi term would land in math/english/... TermDicts. A course with
		// no subject has no siblings by definition; skip the推广 silently.
		if err == nil && origin != nil && origin.SubjectID != 0 {
		// contentType=ContentLearning: 推广只覆盖同学科的学习课程。
		// entertainment 课程（动画片/电影）即使共享学科 key，其术语需求和
		// 学习课也不同——象棋术语不该被强加给一部电影。接受的术语进的是
		// 每门课独立的 TermDict（admin 在 Prompt 配置 tab 可随时改/删），
		// 所以如果某门娱乐课确实需要该术语，admin 可手动去那门课加。
		// 用具体的 ContentLearning 而非 ""，因为后者也会 fallback 到 learning
		// 但语义不显式；显式更清晰且未来想放开时只改这一处。
		siblings, lerr := s.courseRepo.List("", origin.SubjectID, model.ContentLearning, nil)
			if lerr != nil {
				log.Printf("glossary accept: list subject %d siblings failed (non-fatal): %v", origin.SubjectID, lerr)
			}
			for i := range siblings {
				sib := &siblings[i]
				if sib.ID == origin.ID {
					continue
				}
				if err := s.applyGlossaryToCourse(sib.ID, entry); err != nil {
					log.Printf("glossary accept: apply to sibling course %d failed (non-fatal): %v", sib.ID, err)
				}
			}
		} else if err == nil && origin != nil && origin.SubjectID == 0 {
			log.Printf("glossary accept: course %d has no subject; skipping sibling推广", origin.ID)
		}
	}
	return nil
}

// RejectGlossaryCandidate marks one candidate rejected. The row stays (it's
// the dedup anchor that stops UpsertCandidate re-creating it), it just leaves
// the default review list (which filters status=pending).
func (s *aiService) RejectGlossaryCandidate(id uint) error {
	if s.glossaryRepo == nil {
		return errors.New("glossary subsystem not configured")
	}
	c, err := s.glossaryRepo.FindByID(id)
	if err != nil {
		return err
	}
	if c == nil {
		return repository.ErrGlossaryNotFound
	}
	if c.Status != "pending" {
		return ErrGlossaryNotPending
	}
	c.Status = "rejected"
	return s.glossaryRepo.Update(c)
}


// view can render names without an N+1 (one query per distinct episode/course/
// user, not one per job). Titles are best-effort: a missing id (deleted row)
// simply isn't in the map, and forJob returns "" for it.
type jobNameCache struct {
	episodeTitles map[uint]string
	courseTitles  map[uint]string
	userNicknames map[uint]string
}

func (c jobNameCache) forJob(j *model.AIJob) (string, string, string) {
	ep, course, user := "", "", ""
	// Episode lookup is by job.EpisodeID via the course chain: the episode row
	// gives us the title AND its CourseID (which we trust over job.CourseID for
	// title resolution, since the episode is the source of truth for course
	// membership). job.CourseID is denormalized at enqueue time.
	// EpisodeID/CourseID 现在是 *uint,subject 级 advice job 是 nil → ptrVal 返回 0,
	// map 查 0 拿不到标题(正常,subject job 没 episode/course 可显示)。
	if t, ok := c.episodeTitles[model.PtrVal(j.EpisodeID)]; ok {
		ep = t
	}
	if t, ok := c.courseTitles[model.PtrVal(j.CourseID)]; ok {
		course = t
	}
	if j.UserID != nil {
		if t, ok := c.userNicknames[*j.UserID]; ok {
			user = t
		}
	}
	return ep, course, user
}

// resolveJobNames batch-loads episode/course/user titles for a job set. It
// collects the distinct ids referenced, then issues one Find per type (the
// repos expose single-id FindByID only, so we loop — counts are small: a list
// page is capped at 100 jobs, and most share a handful of episodes/courses).
// Lookups are best-effort: any error degrades to an empty title for that id.
func (s *aiService) resolveJobNames(jobs []model.AIJob) jobNameCache {
	c := jobNameCache{
		episodeTitles: make(map[uint]string, len(jobs)),
		courseTitles:  make(map[uint]string, len(jobs)),
		userNicknames: make(map[uint]string, len(jobs)),
	}
	seenEp, seenCourse, seenUser := map[uint]bool{}, map[uint]bool{}, map[uint]bool{}
	for _, j := range jobs {
		// EpisodeID/CourseID 是 *uint:subject 级 advice job 为 nil,跳过 title 解析
		// (没对应实体,无标题可解析)。ptrVal nil → 0,seenEp[0] 防止重复空查询。
		epID := model.PtrVal(j.EpisodeID)
		if j.EpisodeID != nil && !seenEp[epID] {
			seenEp[epID] = true
			if ep, err := s.episodeRepo.FindByID(epID); err == nil && ep != nil {
				c.episodeTitles[epID] = ep.Title
			}
		}
		courseID := model.PtrVal(j.CourseID)
		if j.CourseID != nil && !seenCourse[courseID] {
			seenCourse[courseID] = true
			if course, err := s.courseRepo.FindByID(courseID); err == nil && course != nil {
				c.courseTitles[courseID] = course.Title
			}
		}
		if j.UserID != nil && !seenUser[*j.UserID] {
			seenUser[*j.UserID] = true
			// Resolve nickname via db directly: aiService doesn't carry a
			// UserRepository (its constructor predates this need), and a single
			// column read is cheap. A real userRepo dependency would be cleaner
			// but would ripple into NewAIService + main.go + tests for one field.
			var nick string
			if err := s.db.Model(&model.User{}).Select("nickname").Where("id = ?", *j.UserID).Take(&nick).Error; err == nil {
				c.userNicknames[*j.UserID] = nick
			}
		}
	}
	return c
}

// sleep is a thin wrapper around time.Sleep used by the worker poll loop. Kept
// as a helper so it's swappable in tests (a test can replace it with a no-op or
// a channel signal).
var sleep = func(seconds int) { time.Sleep(time.Duration(seconds) * time.Second) }
