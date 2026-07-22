package repository

import (
	"errors"
	"time"
	"gorm.io/gorm"
	"studyquest/backend/internal/model"
)

// Code split from ai_content_repo.go for navigability. The interface
// and constructor remain in ai_content_repo.go.

func (r *aiContentRepo) CreateJob(job *model.AIJob) error {
	return r.db.Create(job).Error
}

func (r *aiContentRepo) GetJob(id uint) (*model.AIJob, error) {
	var job model.AIJob
	if err := r.db.First(&job, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

func (r *aiContentRepo) UpdateJobStatus(id uint, status string, errMsg string, progress *float64) error {
	updates := map[string]interface{}{
		"status": status,
		"error":  errMsg,
	}
	query := r.db.Model(&model.AIJob{}).Where("id = ?", id)

	isTerminal := status == "done" || status == "failed" || status == "skipped"
	if isTerminal {
		// 终态写入(done/failed/skipped)加 status='processing' 守卫:worker 是进程内
		// 单 goroutine,正常情况一个 job 只有一个 writer,本不需要守卫。但 admin 可以
		// 手动 ResetJob(或 reaper 自动复位)把 processing 改回 queued —— 如果此时 worker
		// 正好在一个长 LLM 调用里(30s+),调用返回时的终态写入若不加守卫,会把已被
		// 复位的 queued job 又改回 done,让 reset/reap 静默失效。守卫保证:只有仍在
		// processing 的 job 才接受终态;已被复位的 job 此处 RowsAffected=0,本次 LLM
		// 结果自然不落盘(它会在下一轮 poll 被重新 claim 重跑)。
		updates["completed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
		query = query.Where("status = ?", "processing")
		// 终态时把 progress 钉死:done=1.0(圆满完成),failed/skipped=0(没有意义的中间
		// 进度可显示,且避免 processing 阶段残留的高 progress 在失败页闪一下满进度条)。
		// 之前终态写 progress=nil(不更新列),导致 failed job 带着 processing 末期写入的
		// progress≈0.8/1.0 残留,前端同时读 status=failed + progress=0.9 会自相矛盾。
		if status == "done" {
			p := 1.0
			updates["progress"] = &p
		} else {
			updates["progress"] = gorm.Expr("NULL")
		}
	} else if progress != nil {
		// 非终态(processing 的中间进度更新)加单调守卫:progress 只增不减。
		// polish 的 chunk 回调是 3-way 并发的,每个 goroutine Add(1) 后算出的 done 值本身
		// 单调递增,但随后各自的 DB UPDATE 提交顺序不定 —— goroutine B(done=0.8)可能先
		// commit、A(done=0.6)后 commit,无守卫的话 DB 会停在 0.6,进度条倒退/抖动。
		// WHERE progress IS NULL OR progress < ? 保证只有更大的值能写入。
		updates["progress"] = *progress
		query = query.Where("progress IS NULL OR progress < ?", *progress)
	}
	return query.Updates(updates).Error
}

// ClaimNextQueuedJob atomically claims the oldest queued job of one of the given
// types. Returns (nil, nil) when none are queued. Single in-process worker means
// contention is low, but the WHERE status='queued' guard still prevents double
// processing if we later add worker parallelism.
func (r *aiContentRepo) ClaimNextQueuedJob(jobTypes []string) (*model.AIJob, error) {
	if len(jobTypes) == 0 {
		return nil, nil
	}
	var job model.AIJob
	err := r.db.Raw(`UPDATE ai_jobs
		SET status = ?, claimed_at = CURRENT_TIMESTAMP, attempt = attempt + 1, updated_at = CURRENT_TIMESTAMP
		WHERE id = (
			SELECT id FROM ai_jobs
			WHERE status = ? AND job_type IN ?
			ORDER BY priority DESC, created_at ASC
			LIMIT 1
		)
		RETURNING *`,
		"processing", "queued", jobTypes).Scan(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if job.ID == 0 {
		return nil, nil
	}
	return &job, nil
}

func (r *aiContentRepo) ListJobs(jobType string, status string, limit int) ([]model.AIJob, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := r.db.Model(&model.AIJob{})
	if jobType != "" {
		q = q.Where("job_type = ?", jobType)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var jobs []model.AIJob
	if err := q.Order("created_at DESC").Limit(limit).Find(&jobs).Error; err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *aiContentRepo) JobStats() (map[string]int, error) {
	type row struct {
		Status string
		Count  int
	}
	var rows []row
	if err := r.db.Model(&model.AIJob{}).
		Select("status, count(*) as count").
		Group("status").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Status] = r.Count
	}
	return out, nil
}

// ReapStaleJobs 把长时间停在 'processing' 的作业(claimed_at 早于 cutoff)重置
// 回 'queued',并清空 claimed_at/error。AI worker 是进程内单 goroutine,正常情
// 况不会滞留,但进程被 hard-kill(SIGKILL/断电)时会留下这种僵尸行——没有 reaper
// 的话它们就永远卡在 processing,既占统计又永不重跑。参照 subtitle reaper。
//
// claimed_at 由 ClaimNextQueuedJob 用 SQLite CURRENT_TIMESTAMP 写入,是 UTC。cutoff
// 必须也用 UTC 算——以前用 time.Now()(本地时间)导致差了本地 UTC offset(生产 +8h),
// 刚 claim 的 job 5 分钟内就被误 reap,polish 这种慢 job(2-7 分钟)永远跑不完。
// 这是 appclock 类 bug(CLAUDE.md 规则 #3 警告的同类),只是出在 reaper 而非 streak。
func (r *aiContentRepo) ReapStaleJobs(staleTimeout time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-staleTimeout)
	res := r.db.Exec(`UPDATE ai_jobs
		SET status = 'queued', claimed_at = NULL, error = '', updated_at = CURRENT_TIMESTAMP
		WHERE status = 'processing' AND claimed_at IS NOT NULL AND claimed_at < ?`,
		cutoff)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ResetJob 是 ReapStaleJobs 的"单行手动版":admin 判定某条 processing 任务卡死
// (worker 还活着但 LLM 调用挂住,没崩)后,把它重置回 queued,清掉 claimed_at +
// error,让下一轮 worker poll 干净地重新认领。WHERE status='processing' 防止把
// 已完成/已失败的任务误重置(那会复活一条终态记录)。非 processing 返回
// ErrJobNotProcessing,handler 据此返回 409 而不是静默成功。
func (r *aiContentRepo) ResetJob(jobID uint) error {
	res := r.db.Exec(`UPDATE ai_jobs
		SET status = 'queued', claimed_at = NULL, error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = 'processing'`, jobID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrJobNotProcessing
	}
	return nil
}

// RetryJob 把一条 failed 的 job 复位回 queued,让 worker 重新跑。和 ResetJob 的
// 区别:ResetJob 针对 processing(卡住但 worker 还活着),RetryJob 针对 failed(终态,
// 唯一复活途径——failJob 不自动重试)。WHERE status='failed' 防止误重置其他状态;
// 非 failed 返回 ErrJobNotFailed,handler 据此返回 409 而非静默成功。
// 不重置 attempt(让它累加,便于观察"这条重试过几次");清 claimed_at + error。
func (r *aiContentRepo) RetryJob(jobID uint) error {
	res := r.db.Exec(`UPDATE ai_jobs
		SET status = 'queued', claimed_at = NULL, error = '', updated_at = CURRENT_TIMESTAMP
		WHERE id = ? AND status = 'failed'`, jobID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrJobNotFailed
	}
	return nil
}

// --- ai_runs ---

func (r *aiContentRepo) CreateRun(run *model.AIRun) error {
	return r.db.Create(run).Error
}

func (r *aiContentRepo) GetRun(id uint) (*model.AIRun, error) {
	var run model.AIRun
	if err := r.db.First(&run, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (r *aiContentRepo) ListRunsForJob(jobID uint) ([]model.AIRun, error) {
	var runs []model.AIRun
	if err := r.db.Where("job_id = ?", jobID).Order("created_at ASC").Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

func (r *aiContentRepo) ListRecentRuns(limit int) ([]model.AIRun, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	var runs []model.AIRun
	if err := r.db.Order("created_at DESC").Limit(limit).Find(&runs).Error; err != nil {
		return nil, err
	}
	return runs, nil
}

// --- quizzes / questions / answers (Phase C) ---
