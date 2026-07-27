package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
	"gorm.io/gorm"
	"studyquest/backend/internal/ai"
	"studyquest/backend/internal/ai/agent"
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
	// EnqueueHomework enqueues homework jobs for the given episodes (v2: the
	// checkbox-style batch entry). Frontend POSTs /admin/api/ai/jobs with
	// job_type=homework, handler dispatches here. Episodes without subtitle
	// chunks (no material) or with an in-flight homework job are skipped with
	// a reason in the skipped map. The legacy course-level entry
	// (EnqueueHomeworkForCourse below) is retained as deprecated fallback.
	EnqueueHomework(episodeIDs []uint) (enqueued []uint, skipped map[uint]string, err error)
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

	// ── 错题本 (TODO.md P0) ──
	// GetWrongBook 列错题本。courseID=0 表全局;mastered 非空则按掌握状态过滤。
	// nil-safe(wrongBookRepo 未注入时返回空),守 AI 附加层降级。
	GetWrongBook(userID, courseID uint, mastered *bool) ([]WrongBookItemView, error)
	// MarkWrongBookMastered 手动/重做正确后标记掌握。nil-safe。
	MarkWrongBookMastered(userID, questionID uint, mastered bool) error
	// RedoWrongBookQuiz 取一批未掌握错题当"重做卷"(复用 QuizViewQuestion 渲染)。
	// limit<=0 默认 10。nil-safe。
	RedoWrongBookQuiz(userID, courseID uint, limit int) ([]QuizViewQuestion, error)
	// SubmitWrongBookRedo 错题本重做交卷。逐题判分 + 更新 curation 状态,不落 Answer
	// 行、不改 quiz-side mastery(和正式 quiz 交卷隔离)。nil-safe。
	SubmitWrongBookRedo(userID uint, answers []QuizAnswerInput) ([]WrongBookRedoResult, error)
	// UnmasteredCount 返回某用户未掌握错题总数(给 tab 角标用)。nil-safe 返回 0。
	UnmasteredCount(userID uint) (int64, error)

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

	// ── structured logs (TODO.md P1 — /admin/logs page) ──
	// ListLogEntries returns recent log entries with level/source/job filters,
	// enriched with episode/course titles (resolved via the entry's own
	// EpisodeID/CourseID, falling back to JobID). Newest first.
	ListLogEntries(level, source string, jobID *uint, limit int) ([]LogEntryView, error)

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
	// AcknowledgeJob marks a failed job as 'skipped' WITHOUT re-running it or
	// chaining downstream (unlike SkipPolish). Use case: a job failed for an
	// unrecoverable reason the admin can't fix (typical: episode has no
	// subtitle, so summary/quiz can't run). Retry is pointless (no subtitle
	// appeared), but the job lingering in 'failed' pollutes the failure signal.
	// Acknowledge lets the admin dismiss it — it leaves the failed list and
	// shows as 'skipped' in history. Re-running is still possible by enqueuing
	// a fresh job from the workbench (no "un-acknowledge" needed). Only valid
	// on a FAILED job; returns repository.ErrJobNotFailed otherwise.
	AcknowledgeJob(jobID uint) error

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

	// Stop halts the background worker goroutine. Production never calls this
	// (process exit reclaims it); tests call it via t.Cleanup to avoid leaking a
	// poller per NewAIService across the test binary.
	Stop()

	// ── 课程考试 (TODO.md P0) ──
	// StartExam 为 (user, course) 开考组卷。题库不足返回 ErrExamInsufficientPool。
	// nil-safe(examRepo 未注入返回错误,不 panic)。
	StartExam(userID, courseID uint) (*ExamView, error)
	// GetActiveExamView 取 (user, course) 的 active exam 视图。无返回 (nil,nil)。
	GetActiveExamView(userID, courseID uint) (*ExamView, error)
	// SubmitExam 交卷。已交卷返回 ErrExamAlreadySubmitted。nil-safe。
	SubmitExam(userID, examID uint, answers []QuizAnswerInput) (*ExamSubmitReport, error)
	// GetExamStatus gate:课程题库够不够开考。
	GetExamStatus(courseID uint) (ExamStatus, error)

	// ── 课程考试 admin 观测(每个失败返回零值,handler log + 降级) ──
	ExamStats() (repository.ExamStats, error)
	ExamSourceQuality() ([]repository.ExamSourceQualityRow, error)

	// ── 课后作业卷 (TODO.md P0,episode 级通用卷,纯打印) ──
	// EnqueueHomeworkForCourse 为某课程所有有素材(chunks)的 episode 批量入队 homework
	// job。去重:已有在途 homework job 的 episode 跳过。返回实际入队数。nil-safe
	// (homeworkRepo 未注入返回错误)。admin 手动触发,低优先级(=1,不饿死 quiz)。
	EnqueueHomeworkForCourse(courseID uint) (int, error)
	// GetHomeworkViewByID 取某 homework 完整内容(sections+questions),admin 预览/打印用。
	// 无返回 (nil,nil)。nil-safe。
	GetHomeworkViewByID(id uint) (*HomeworkView, error)
	// ListHomeworksByCourse 列某课程所有 homework(admin 列表)。nil-safe。
	ListHomeworksByCourse(courseID uint) ([]model.Homework, error)
	// HasPendingHomeworkJob 报告某 episode 是否有在途 homework job,admin 据此区分
	// "正在生成" vs "未生成"。
	HasPendingHomeworkJob(episodeID uint) bool
	// ── Homework prompt 配置(per-subject 完整 system prompt,admin 可编辑) ──
	// GetHomeworkPromptConfig 取某 subject 的 prompt(无则 lazy 创建灌默认)。nil-safe。
	GetHomeworkPromptConfig(subjectID uint, subjectKey string) (model.HomeworkPromptConfig, error)
	// SaveHomeworkPromptConfig 覆盖某 subject 的 system prompt(admin 编辑)。nil-safe。
	SaveHomeworkPromptConfig(subjectID uint, subjectKey string, prompt string) error
	// ResetHomeworkPromptConfig 重置回默认(admin 恢复默认)。nil-safe。
	ResetHomeworkPromptConfig(subjectID uint, subjectKey string) error

	// ── 错题本 admin 观测(每个失败返回零值,handler log + 降级) ──
	// WrongBookStats 返回错题本全局统计(nil-safe:wrongBookRepo 未注入返回零值)。
	WrongBookStats() (repository.WrongBookStats, error)
	// WrongBookTopFrequent 返回高频错题榜(nil-safe 返回空)。
	WrongBookTopFrequent(limit int) ([]repository.FrequentWrongRow, error)
	// WrongBookSubjectDistribution 返回按科目分组的错题量(nil-safe 返回空)。
	WrongBookSubjectDistribution() ([]repository.SubjectWrongCount, error)
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
	// cancel stops the runWorker goroutine. Replacing the old "worker polls
	// forever, leaks one goroutine per NewAIService call" pattern that caused
	// intermittent test failures when many service tests spawned workers
	// concurrently. Production never calls Stop (process exit reclaims it);
	// tests register t.Cleanup(svc.Stop).
	cancel context.CancelFunc
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
	// polishChunkRepo stores the per-chunk checkpoint rows for 断点续润 — when a
	// polish job is retried after a partial failure, runPolishJob reads back the
	// done chunks and feeds them to polish.Polish as PriorOutcomes, so only the
	// FAILED chunks re-call the LLM. nil-safe: when nil, polish still runs but
	// does no checkpointing (a retry re-burns all chunks — the pre-断点续润行为).
	polishChunkRepo repository.AIPolishChunkRepository
	// logRepo stores LogEntry rows for the lightweight structured-log layer
	// (TODO.md P1). appendLog is nil-safe, so tests that don't assert on logs
	// pass nil. Production wires NewLogRepository(db).
	logRepo repository.LogRepository
	// wrongBookRepo stores 错题本 curation 状态(交卷时对做错的题 upsert)。
	// nil-safe: when nil, submit 不写错题本(降级,不阻断交卷主流程),quiz 闭环照常。
	// 这保持 AI 附加层 + 零回归:老测试不传它也能跑,生产 NewAIService 注入。
	wrongBookRepo repository.WrongBookRepository
	// examRepo stores 课程考试(Exam/ExamQuestion/ExamAnswer)。nil-safe: when nil,
	// StartExam/SubmitExam 返回 "考试功能未启用",其它功能照常。生产 NewAIService 注入。
	examRepo repository.ExamRepository
	// homeworkRepo stores 课后作业卷(Homework/HomeworkSection/HomeworkQuestion/
	// HomeworkPromptConfig)。nil-safe: when nil, 作业相关方法返回 "作业功能未启用",
	// 其它功能照常。生产 NewAIService 注入。
	homeworkRepo repository.HomeworkRepository
	// polishLLMOverride is a TEST-ONLY seam: when non-nil, runPolishJob uses
	// this provider directly instead of resolving through `resolver`. This lets
	// service-level tests drive the full polish→writeback→chain path with a
	// fake LLM (the real resolver only builds openai_compat HTTP clients, so it
	// can't be stubbed without a live relay). Production leaves this nil and
	// the resolver path runs unchanged. See ai_service_polish_test.go.
	polishLLMOverride ai.LLMProvider
	// homeworkLLMOverride is a TEST-ONLY seam (mirrors polishLLMOverride): when
	// non-nil, runHomeworkJob uses this provider directly instead of resolving
	// through `resolver`. Lets service tests drive the full RAG→LLM→parse→persist
	// path with a fake LLM. Production leaves this nil. See ai_service_homework_test.go.
	homeworkLLMOverride ai.LLMProvider
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

// LogEntryView is one admin-facing log row WITH episode/course titles resolved.
// LogEntry carries EpisodeID/CourseID directly (it's an event row, not a
// per-LLM-call row), so enrichment joins on those — no job fanout needed unless
// they're nil, in which case we fall back through JobID (failJob entries set
// JobID but not the episode/course ids).
type LogEntryView struct {
	model.LogEntry
	EpisodeTitle string `json:"episode_title,omitempty"`
	CourseTitle  string `json:"course_title,omitempty"`
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
	polishChunkRepo repository.AIPolishChunkRepository,
	logRepo repository.LogRepository,
	wrongBookRepo repository.WrongBookRepository,
	examRepo repository.ExamRepository,
	homeworkRepo repository.HomeworkRepository,
) AIService {
	s := &aiService{
		db:              db,
		contentRepo:     contentRepo,
		episodeRepo:     episodeRepo,
		courseRepo:      courseRepo,
		resolver:        resolver,
		unlockService:   unlockService,
		userRepo:        userRepo,
		glossaryRepo:    glossaryRepo,
		subjectRepo:     subjectRepo,
		polishChunkRepo: polishChunkRepo,
		logRepo:         logRepo,
		wrongBookRepo:   wrongBookRepo,
		examRepo:        examRepo,
		homeworkRepo:    homeworkRepo,
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.runWorker(ctx) // single in-process worker goroutine; see runWorker
	return s
}

// Stop halts the background worker goroutine. Production code never needs to
// call this (the worker dies with the process); it exists so tests can release
// the worker instead of leaking it for the duration of `go test ./...`.
func (s *aiService) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
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
	// priorityHomework:作业卷,admin 手动批量触发,后台跑。和 summary/advice 同级——
	// 不在屏幕前干等,低优先级不饿死 quiz(学生正等的最高优先级)。单 goroutine worker
	// 共享:作业 job 跑时会阻塞期间到达的 quiz(最多几十秒),但 quiz 优先级更高,队里
	// 有 quiz 会先认领,只在"正好作业在跑且新来 quiz"的窗口有短暂阻塞,可接受。
	priorityHomework = 1
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
		// Must have a polishable-source primary subtitle. The isPolishableSource
		// helper is shared with runPolishJob so enqueue and execution agree on
		// exactly which sources are admissible — the prior inline check here
		// admitted llm_optimized while runPolishJob's separate check rejected
		// it, leaving re-polish jobs stuck in queued→skipped loops.
		sub, _ := s.episodeRepo.GetSubtitle(epID)
		if sub == nil {
			skipped[epID] = "无字幕"
			continue
		}
		if !isPolishableSource(sub.Source) {
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
		log.Printf("AI: OnSubtitleCompleted ep %d: episode lookup failed, aborting chain", episodeID)
		return // can't do anything without the episode
	}
	// Only auto-segment if the course has AI enabled. This is the gate that keeps
	// AI a pure add-on: a course with AI off never triggers AI work, even when
	// subtitles arrive.
	course, err := s.courseRepo.FindByID(ep.CourseID)
	if err != nil || course == nil {
		log.Printf("AI: OnSubtitleCompleted ep %d: course %d lookup failed, aborting chain", episodeID, ep.CourseID)
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
	// shouldPolish: only polish whisper-sourced subtitles here. This is the
	// "fresh transcript just landed" entry point, so sub.Source is normally
	// "whisper" anyway — but using isPolishableSource would be wrong because
	// it also admits "llm_optimized", and a re-polish on a just-completed
	// subtitle makes no sense (Complete writes source=whisper, never
	// llm_optimized). Keep the explicit "whisper" check; the helper is for
	// the EnqueuePolish/runPolishJob pair where re-polish IS the point.
	shouldPolish := sub != nil && sub.Source == "whisper" && s.resolver != nil
	// Decision log: every input that could flip shouldPolish is named on one line.
	// This hook had ZERO observability before — the 2026-07-22 reaper-timezone bug
	// (jobs reaped every 5 min) took hours to diagnose precisely because there was
	// no log saying "I decided to enqueue polish" vs "I fell through to segment".
	log.Printf("AI: OnSubtitleCompleted ep %d: source=%q resolverNil=%v → shouldPolish=%v",
		episodeID, subSourceForLog(sub), s.resolver == nil, shouldPolish)
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

// subSourceForLog returns the subtitle's source for logging, or "<nil>" when
// the subtitle pointer is nil. Kept as a helper so the OnSubtitleCompleted log
// line stays readable (a bare sub.Source on a nil sub would panic).
func subSourceForLog(sub *model.Subtitle) string {
	if sub == nil {
		return "<nil>"
	}
	return sub.Source
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

// maxConsecutiveFailures 是同一任务连续失败后「熔断」的阈值。客户端轮询触发的 lazy
// 入队(GetOrEnqueueQuiz / GetOrEnqueueAdvice / EnqueueAdviceForEpisode)在入队前会
// 调 consecutiveFailures 检查:连续失败达到这个数就拒绝自动入队、返回 cooling 状态,
// 避免像 episode 31 那样客户端反复入队 9 次烧 50 万 token 的惨剧。admin 手动
// RetryJob / Regenerate* 不受此限(那是 escape hatch)。
//
// 取 3 次:LLM 失败有随机性(中转站偶发 502、模型偶发截断),给 3 次机会足够区分
// 「偶发」和「确定性失败」;确定性失败(长课截断、配置错)在第 3 次后就该停下来让
// admin 介入,而不是继续烧。
const maxConsecutiveFailures = 3

// consecutiveFailures 数该 (jobType, episode) 最近连续失败了多少次。返回最近
// maxConsecutiveFailures 条 job(按时间倒序)里,从最新一条往回数的连续 failed
// 条数——一旦遇到非 failed(done/skipped/queued/processing)就停。
//
// 「连续」是关键:不是「历史总失败数」,而是「最近一次成功之后的失败连击」。这样
// admin RetryJob 成功一次后,失败计数自然清零(最近一条是 done,返回 0);反之只要
// 最近 3 条全是 failed 就触发熔断,不管历史上成功过多少次(老的成功不抵消最近的问题)。
//
// 用途:summary/segment/polish 这种 episode 级批作业的熔断(目标实体 = episode_id,
// 与 user 无关——这些作业的产物是全 episode 共享的)。
//
// limit 取 maxConsecutiveFailures:只需判断是否到阈值,多查无益(判定逻辑只看最近 N 条)。
func (s *aiService) consecutiveFailures(jobType string, episodeID uint) int {
	var jobs []model.AIJob
	s.db.Where("job_type = ? AND episode_id = ?", jobType, episodeID).
		Order("created_at DESC").Limit(maxConsecutiveFailures).Find(&jobs)
	count := 0
	for _, j := range jobs {
		if j.Status == "failed" {
			count++
		} else {
			break // 遇到非 failed 中断连击
		}
	}
	return count
}

// consecutiveQuizFailures 是 consecutiveFailures 的 per-user quiz 版。quiz 是每个
// 学生在每节课独立一套(A 学生失败不影响 B 学生),必须按 (job_type=quiz, user_id,
// episode_id) 三元组计数——不能只用 episode_id(否则同节课别的学生失败会误熔断
// 当前学生)。
func (s *aiService) consecutiveQuizFailures(userID, episodeID uint) int {
	var jobs []model.AIJob
	s.db.Where("job_type = ? AND user_id = ? AND episode_id = ?", "quiz", userID, episodeID).
		Order("created_at DESC").Limit(maxConsecutiveFailures).Find(&jobs)
	count := 0
	for _, j := range jobs {
		if j.Status == "failed" {
			count++
		} else {
			break
		}
	}
	return count
}

// consecutiveAdviceFailures 是 consecutiveFailures 的 advice 专用版。advice 的目标
// 实体是 (user, scope, scope_id),scope_id 存在 PayloadJSON 里(不是 SQL 列),无法
// 用 SQL WHERE 精确过滤——复用 hasPendingAdviceJob 的查询+Go 层解码模式:按
// (job_type=advice, user_id) 拉最近 maxConsecutiveFailures 条 advice job,Go 层
// 匹配 scope/scope_id 后数连击。
//
// 注意 Limit 取的是「所有 scope 混在一起的最近 N 条」,Go 层 adviceJobMatchesScope
// 过滤后实际同 scope 的可能 < N 条。这对「判定是否到阈值」是保守安全的:同 scope 的
// 连击被别的 scope job 夹断时,函数返回的值偏小(少熔断,不会误熔断)。advice 无客户端
// 高频轮询(submit-all 后链式触发一次),少熔断的代价可接受;真要精确就得查全量再过滤,
// 但 advice job 每用户量级很小,这里 Limit 已够用。
func (s *aiService) consecutiveAdviceFailures(userID uint, scope string, scopeID uint) int {
	var jobs []model.AIJob
	s.db.Where("job_type = ? AND user_id = ?", "advice", userID).
		Order("created_at DESC").Limit(maxConsecutiveFailures).Find(&jobs)
	count := 0
	for _, j := range jobs {
		if j.Status != "failed" {
			break
		}
		if !adviceJobMatchesScope(j, scope, scopeID) {
			break // 连击里混入了别的 scope 的失败,不算同任务的连续失败
		}
		count++
	}
	return count
}

// adviceJobMatchesScope 判断一条 advice job 是否属于 (scope, scope_id)。和
// hasPendingAdviceJob 用的是同一套匹配规则(无 PayloadJSON 默认 episode 级,
// 用 EpisodeID 比较;有 PayloadJSON 解码比较 scope/scope_id)。抽出来供两处复用。
func adviceJobMatchesScope(j model.AIJob, scope string, scopeID uint) bool {
	if j.PayloadJSON == "" {
		return scope == agent.ScopeEpisode && j.EpisodeID != nil && *j.EpisodeID == scopeID
	}
	var p struct {
		Scope   string `json:"scope"`
		ScopeID uint   `json:"scope_id"`
	}
	if json.Unmarshal([]byte(j.PayloadJSON), &p) != nil {
		return false
	}
	return p.Scope == scope && p.ScopeID == scopeID
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
func (s *aiService) runWorker(ctx context.Context) {
	jobTypes := []string{"segment", "summary", "quiz", "advice", "course_summary", "user_report", "polish", "homework"}
	// Use a 3s ticker for polling; on ctx cancellation the worker exits
	// promptly (within the select, not after a full sleep).
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
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
						s.appendLog("error", "ai_worker", fmt.Sprintf("worker panic recovered: %v", r), "", nil)
						// Best-effort failJob on whatever job was in flight; we don't
						// know which one here (processOneJob claimed it internally),
						// so we can't stamp a specific error. The job stays
					// 'processing' and the reaper will reset it after 30min.
					// That's acceptable — the point is keeping the worker alive.
				}
			}()
			s.processOneJob(jobTypes)
		}()
		// Wait for either the next poll tick or cancellation. The old sleep(3)
		// was uninterruptible, so canceled workers stuck around for up to 3s
		// AND leaked entirely if the test forgot to call stop (none did, since
		// stop didn't exist).
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
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
	case "homework":
		s.runHomeworkJob(job)
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
				// 熔断门:summary 连续失败 ≥ 阈值就不再链式入队。这是「白烧 token」的
				// 第二条潜在路径(第一条是 quiz 客户端轮询,已在 GetOrEnqueueQuiz 拦住):
				// 若某节课的 summary 反复失败(内容超长截断等),admin 每次重新 segment
				// 都会触发这里再入队一次 summary 烧 token。admin 仍可走手动
				// EnqueueSummary 强制重跑(那条路径不查熔断,是 escape hatch)。
				if !s.hasPendingJob("summary", episodeID) && s.consecutiveFailures("summary", episodeID) < maxConsecutiveFailures {
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
// Also appends a structured log_entries row (level=error) so the failure shows
// on /admin/logs without SSH — see appendLog (nil-safe).
func (s *aiService) failJob(job *model.AIJob, msg string) {
	log.Printf("AI job %d (%s) episode %d failed: %s", job.ID, job.JobType, model.PtrVal(job.EpisodeID), msg)
	s.appendLog("error", "ai_worker", msg, "", job)
	s.contentRepo.UpdateJobStatus(job.ID, "failed", msg, nil)
}

// appendLog writes one structured log entry. Nil-safe (no-op when s.logRepo is
// nil, e.g. tests) and error-safe (a logging failure MUST NOT derail the caller
// — the whole point of best-effort logging is that business logic never depends
// on it). job is optional: when non-nil, its JobID/EpisodeID/CourseID are
// stamped so the /admin/logs row can be filtered + enriched with titles.
//
// fieldsJSON is optional structured context (already-JSON-encoded string, or
// "" for none). Callers that want a few key/values pass a json.Marshal'd map.
func (s *aiService) appendLog(level, source, message, fieldsJSON string, job *model.AIJob) {
	if s.logRepo == nil {
		return
	}
	entry := &model.LogEntry{
		Level:      level,
		Source:     source,
		Message:    message,
		FieldsJSON: fieldsJSON,
	}
	if job != nil {
		jid := job.ID
		entry.JobID = &jid
		entry.EpisodeID = job.EpisodeID
		entry.CourseID = job.CourseID
	}
	if err := s.logRepo.Append(entry); err != nil {
		// Fall back to stderr so we at least see it somewhere; never return.
		log.Printf("AI: appendLog failed (non-fatal, level=%s source=%s): %v", level, source, err)
	}
}

// --- reads ---

// Cohesive blocks extracted to siblings (same package):
//   ai_service_polish.go  — runPolishJob + glossary workflow
//   ai_service_jobs.go    — job/run listing + reset/retry/skip
//   ai_service_naming.go  — jobNameCache + resolveJobNames + sleep seam
// The interface, struct, constructor, enqueue logic, and the worker loop
// (incl. runSegmentJob / runSummaryJob / failJob) remain here.
