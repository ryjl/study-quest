package service

import (
	"time"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// Code split from ai_service.go for navigability.
// Job/run listing, enrichment, stats, reset/retry/skip and summary-status queries.

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
