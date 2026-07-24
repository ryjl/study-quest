package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// exam_repo.go — 课程考试的存取(见 model.exam.go)。和 ai_quiz_repo.go 平行:
//   - CreateExam: archive 旧 active exam + insert 新的(单事务,守 partial unique index)
//   - GetActiveExam: 取 (user, course) 的 active exam(nil = 没有)
//   - TryMarkExamSubmitted: 条件 UPDATE 抢占交卷锁(复用 quiz 范式,消除 TOCTOU)
//   - SubmitAll 时逐题写 ExamAnswer + 读 ExamQuestion
//   - admin 聚合:Stats / ScoreDistribution / SourceQuality(对比 pool vs generated)

// ExamRepository 课程考试存取接口。
type ExamRepository interface {
	// CreateExam 在单事务里 archive 该 (user, course) 的旧 active exam(转 archived),
	// 再 insert 新 active exam + 它的 exam_questions。返回新 exam id。
	// 和 ai_quiz_repo.CreateQuiz 同范式:partial unique index 是 DB 级守卫,事务里
	// archive-then-insert 保证不违反唯一约束。
	CreateExam(exam *model.Exam, questions []model.ExamQuestion) (uint, error)
	// GetActiveExam 取 (user, course) 的 active exam,无返回 (nil, nil)。
	GetActiveExam(userID, courseID uint) (*model.Exam, error)
	// GetExamByID 按 id 取一条 exam(admin / 交卷回放用)。
	GetExamByID(examID uint) (*model.Exam, error)
	// GetExamQuestions 取某 exam 的题目,按 OrderIdx 排序。
	GetExamQuestions(examID uint) ([]model.ExamQuestion, error)
	// CreateExamAnswer 落一条交卷作答(append-only)。
	CreateExamAnswer(a *model.ExamAnswer) error
	// TryMarkExamSubmitted 条件 UPDATE 抢占交卷锁:仅 submitted_at IS NULL 时盖戳。
	// 返回 (claimed, error):claimed=true 本请求抢到,false 已被别人抢(并发重复交卷)。
	TryMarkExamSubmitted(examID uint, at time.Time) (bool, error)
	// ListExamAnswers 取某 exam 的全部交卷作答(交卷报告/回放用)。
	ListExamAnswers(examID uint) ([]model.ExamAnswer, error)
	// ListExamsForCourse 列某课程下所有 exam(admin 观测,按 created_at DESC)。
	ListExamsForCourse(courseID uint, limit int) ([]model.Exam, error)

	// ── admin 观测聚合(每个失败返回零值,handler log + 降级) ──
	ExamStats() (ExamStats, error)
	// ExamSourceQuality 对比 pool(题库抽)vs generated(agent 新出)题的正确率,
	// 验证迁移题难度是否合理。返回每类的 {total, correct, rate}。
	ExamSourceQuality() ([]ExamSourceQualityRow, error)
}

// ExamStats 课程考试全局统计(admin 观测页 StatCard 用)。
type ExamStats struct {
	Total      int64   // 累计考试次数(所有 exam 行数,含 archived)
	TotalScore float64 // 所有已交卷 exam 的得分率之和(算平均用)
	Submitted  int64   // 已交卷数(Score 有效)
	AvgScore   float64 // 平均得分率 = TotalScore/Submitted;Submitted=0 时为 0
	ThisWeek   int64   // 本周新开的考试数
}

// ExamSourceQualityRow 是题源质量对比的一行(pool vs generated)。
type ExamSourceQualityRow struct {
	Source  string  // pool | generated
	Total   int64   // 该类题总数(跨所有考试)
	Correct int64   // 该类题答对数
	Rate    float64 // 正确率 = Correct/Total;Total=0 时为 0
}

type examRepo struct {
	db *gorm.DB
}

// NewExamRepository creates an ExamRepository bound to db.
func NewExamRepository(db *gorm.DB) ExamRepository {
	return &examRepo{db: db}
}

func (r *examRepo) CreateExam(exam *model.Exam, questions []model.ExamQuestion) (uint, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Archive 旧 active exam(同 quiz 范式:只翻 status,保留行做历史)。
		var old model.Exam
		findErr := tx.Where("user_id = ? AND course_id = ? AND status = 'active'", exam.UserID, exam.CourseID).
			First(&old).Error
		if findErr == nil {
			now := time.Now().UTC()
			if err := tx.Model(&old).Updates(map[string]interface{}{
				"status":      "archived",
				"archived_at": now,
			}).Error; err != nil {
				return err
			}
		} else if !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return findErr
		}
		// Insert 新 active exam。
		exam.ID = 0
		exam.Status = "" // 让列默认 'active' 生效
		if err := tx.Create(exam).Error; err != nil {
			return err
		}
		for i := range questions {
			questions[i].ID = 0      // 确保是 insert 不是 update(caller 可能复用 slice)
			questions[i].ExamID = exam.ID
		}
		if len(questions) > 0 {
			if err := tx.Create(&questions).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return exam.ID, nil
}

func (r *examRepo) GetActiveExam(userID, courseID uint) (*model.Exam, error) {
	var e model.Exam
	if err := r.db.Where("user_id = ? AND course_id = ? AND status = 'active'", userID, courseID).
		First(&e).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

func (r *examRepo) GetExamByID(examID uint) (*model.Exam, error) {
	var e model.Exam
	if err := r.db.First(&e, examID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &e, nil
}

func (r *examRepo) GetExamQuestions(examID uint) ([]model.ExamQuestion, error) {
	var qs []model.ExamQuestion
	if err := r.db.Where("exam_id = ?", examID).Order("order_idx ASC").Find(&qs).Error; err != nil {
		return nil, err
	}
	return qs, nil
}

func (r *examRepo) CreateExamAnswer(a *model.ExamAnswer) error {
	return r.db.Create(a).Error
}

// TryMarkExamSubmitted 复用 quiz 交卷锁范式:条件 UPDATE 消除 TOCTOU。
// 两个并发 submit 都可能过 service 层的 nil 检查,但只有一个能抢到这把锁——
// SQLite 的 UPDATE 自动行锁,败者 RowsAffected=0 直接拒。
func (r *examRepo) TryMarkExamSubmitted(examID uint, at time.Time) (bool, error) {
	res := r.db.Model(&model.Exam{}).
		Where("id = ? AND submitted_at IS NULL", examID).
		Update("submitted_at", at)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *examRepo) ListExamAnswers(examID uint) ([]model.ExamAnswer, error) {
	var answers []model.ExamAnswer
	if err := r.db.Where("exam_id = ?", examID).Order("exam_question_id ASC").Find(&answers).Error; err != nil {
		return nil, err
	}
	return answers, nil
}

func (r *examRepo) ListExamsForCourse(courseID uint, limit int) ([]model.Exam, error) {
	if limit <= 0 {
		limit = 50
	}
	var exams []model.Exam
	if err := r.db.Where("course_id = ?", courseID).Order("created_at DESC").Limit(limit).Find(&exams).Error; err != nil {
		return nil, err
	}
	return exams, nil
}

// ── admin 聚合 ──

func (r *examRepo) ExamStats() (ExamStats, error) {
	var stats ExamStats
	if err := r.db.Model(&model.Exam{}).Count(&stats.Total).Error; err != nil {
		return stats, err
	}
	// 已交卷(submitted_at IS NOT NULL)的:计数 + 得分率求和。分两次简单查询,
	// 不靠 Scan 别名(不同驱动/版本对别名识别不一致,简单查询更稳)。
	if err := r.db.Model(&model.Exam{}).Where("submitted_at IS NOT NULL").Count(&stats.Submitted).Error; err != nil {
		return stats, err
	}
	var scoreSum float64
	row := r.db.Model(&model.Exam{}).Where("submitted_at IS NOT NULL").
		Select("COALESCE(SUM(score), 0)")
	if err := row.Scan(&scoreSum).Error; err != nil {
		return stats, err
	}
	stats.TotalScore = scoreSum
	if stats.Submitted > 0 {
		stats.AvgScore = scoreSum / float64(stats.Submitted)
	}
	weekAgo := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if err := r.db.Model(&model.Exam{}).Where("created_at >= ?", weekAgo).Count(&stats.ThisWeek).Error; err != nil {
		return stats, err
	}
	return stats, nil
}

// ExamSourceQuality 按 Source(pool/generated)分组聚合 exam_answers 的正确率。
// JOIN exam_questions 取 source;LEFT JOIN 容忍题被删。
func (r *examRepo) ExamSourceQuality() ([]ExamSourceQualityRow, error) {
	var rows []ExamSourceQualityRow
	err := r.db.Table("exam_answers").
		Select(`COALESCE(exam_questions.source, 'pool') AS source,
			COUNT(*) AS total,
			SUM(CASE WHEN exam_answers.correct = 1 THEN 1 ELSE 0 END) AS correct`).
		Joins("LEFT JOIN exam_questions ON exam_questions.id = exam_answers.exam_question_id").
		Group("source").
		Order("source ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		if rows[i].Total > 0 {
			rows[i].Rate = float64(rows[i].Correct) / float64(rows[i].Total)
		}
	}
	return rows, nil
}
