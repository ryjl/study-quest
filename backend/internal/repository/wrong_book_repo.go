package repository

import (
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// wrongBookMasteredThreshold 是重做连对几次才算"掌握"的阈值。答错清零 streak,
// 连续答对到这个数才置 mastered=true。3 = 不会一次蒙对就清除,需反复答对确认真掌握。
const wrongBookMasteredThreshold = 3

// WrongBookRepository 错题本的 curation 状态存取。题面现查 Question 表(经
// AIContentRepository 的 ListWrongAnswersByUserCourse/ListWrongAnswersByUser),
// 本表只存"是否掌握 / 重做了几次"这类学生侧状态。见 model.WrongBookItem 注释。
type WrongBookRepository interface {
	// UpsertOnWrong 在学生做错一道题时调用:已存在则 AttemptCount++ + LastAttemptedAt
	// 刷新 + CorrectStreak 清零(答错打断连对),不存在则新建。单事务读-改-写
	// (先 First 再 Create/Update),交卷循环里逐题调。并发同题重复 upsert 概率极低
	// (一用户极少同时交两份含同题的卷),且 user_id+question_id unique index 兜底
	// (第二个 Create 会失败返回 error,不产生脏数据)。
	UpsertOnWrong(item model.WrongBookItem) error
	// IncrementCorrectStreak 重做答对时调用:CorrectStreak++。达 wrongBookMasteredThreshold
	// (3)则置 Mastered=true + MasteredAt + Streak 归 0(下轮重新累计)。返回是否新晋掌握。
	// 学生在错题本重做正确后调用。
	IncrementCorrectStreak(userID, questionID uint) (mastered bool, err error)
	// MarkMastered 标记掌握 / 取消掌握。mastered=true 时刷 MasteredAt=now。
	// 学生手动点"已掌握"调用(手动掌握不走 streak,直接置位 + 清零 streak)。
	MarkMastered(userID, questionID uint, mastered bool) error
	// GetItem 取单条错题状态(重做流用)。不存在返回 (nil, nil)。
	GetItem(userID, questionID uint) (*model.WrongBookItem, error)
	// ListByUser 列某用户的错题状态行(与 ListWrongAnswers join 出来的题面
	// 在 service 层合并)。可按 course/subject/chunk/mastered 过滤(0/nil=不过滤)。
	ListByUser(userID uint, filters WrongBookFilter) ([]model.WrongBookItem, error)

	// ── admin 观测聚合(每个失败返回零值,由 handler log + 降级,不拖垮整页) ──
	// Stats 返回错题本全局统计(总数、未掌握数、本周新增)。
	Stats() (WrongBookStats, error)
	// TopFrequentWrong 按 question_id 聚合,返回错得最多的 N 道题(全平台),
	// 带 attempt 次数和题面。帮 admin 发现"全员都错"的题(题面有问题或知识点难)。
	TopFrequentWrong(limit int) ([]FrequentWrongRow, error)
	// DistributionBySubject 按科目分组的错题量(给弱点分布图)。
	DistributionBySubject() ([]SubjectWrongCount, error)
}

// WrongBookStats 是错题本全局统计(admin 观测页顶栏 StatCard 用)。
type WrongBookStats struct {
	Total      int64 // 全部错题行数(不去重 question)
	Unmastered int64 // mastered=false 的行数
	ThisWeek   int64 // first_wrong_at 在最近 7 天内的行数
}

// FrequentWrongRow 是高频错题榜的一行:题面 + 出错人次 + 平均重做次数。
type FrequentWrongRow struct {
	QuestionID  uint
	Stem        string
	OccurCount  int64 // 多少个学生错这题(COUNT DISTINCT user_id)
	TotalAttempts int64 // 所有学生重做这题的总次数(SUM attempt_count)
}

// SubjectWrongCount 是按科目分组的错题量(弱点分布图用)。
type SubjectWrongCount struct {
	SubjectKey   string
	SubjectLabel string
	Count        int64
}

// WrongBookFilter 是 ListByUser 的过滤参数。指针字段 nil = 不过滤该维度。
type WrongBookFilter struct {
	CourseID  *uint
	SubjectID *uint
	ChunkID   *uint
	// Mastered 指针指向 bool: nil=不过滤(全要), &true=只要已掌握, &false=只要未掌握。
	Mastered  *bool
}

type wrongBookRepo struct {
	db *gorm.DB
}

// NewWrongBookRepository creates a WrongBookRepository bound to db.
func NewWrongBookRepository(db *gorm.DB) WrongBookRepository {
	return &wrongBookRepo{db: db}
}

func (r *wrongBookRepo) UpsertOnWrong(item model.WrongBookItem) error {
	// ON CONFLICT(user_id, question_id) DO UPDATE:已存在的题只 +1 attempt count +
	// 刷新 LastAttemptedAt,不动 FirstWrongAt(首次做错时间要保留)和 Mastered(学生
	// 可能标记过掌握但再次做错,这里不自动取消——让 curation 状态由学生主导)。
	now := time.Now().UTC()
	return r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.WrongBookItem
		err := tx.Where("user_id = ? AND question_id = ?", item.UserID, item.QuestionID).First(&existing).Error
		if err == nil {
			// 已存在:累加 + 刷新 + streak 清零(答错打断连对)。不覆盖 FirstWrongAt/Mastered。
			return tx.Model(&existing).Updates(map[string]interface{}{
				"attempt_count":     gorm.Expr("attempt_count + 1"),
				"last_attempted_at": now,
				"correct_streak":    0,
			}).Error
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		// 新建。
		item.FirstWrongAt = now
		item.LastAttemptedAt = &now
		if item.AttemptCount == 0 {
			item.AttemptCount = 1
		}
		return tx.Create(&item).Error
	})
}

// IncrementCorrectStreak 重做答对时 CorrectStreak++。达阈值(3)则置 mastered=true +
// MasteredAt + Streak 归 0(下轮重新累计),返回 mastered=true。单事务读-改-写避免
// 并发两次答对都过阈值。已 mastered 的题再答对幂等(streak 已 0,不会再触发)。
func (r *wrongBookRepo) IncrementCorrectStreak(userID, questionID uint) (bool, error) {
	var mastered bool
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var existing model.WrongBookItem
		if err := tx.Where("user_id = ? AND question_id = ?", userID, questionID).First(&existing).Error; err != nil {
			return err
		}
		// 已掌握的题不再累计(防御)。
		if existing.Mastered {
			mastered = false
			return nil
		}
		newStreak := existing.CorrectStreak + 1
		if newStreak >= wrongBookMasteredThreshold {
			now := time.Now().UTC()
			mastered = true
			return tx.Model(&existing).Updates(map[string]interface{}{
				"correct_streak": 0,
				"mastered":       true,
				"mastered_at":    now,
			}).Error
		}
		return tx.Model(&existing).Update("correct_streak", newStreak).Error
	})
	return mastered, err
}

func (r *wrongBookRepo) MarkMastered(userID, questionID uint, mastered bool) error {
	// mastered=true 时刷 MasteredAt=now + streak 归 0;false 时清空 mastered_at(用 map
	// 显式写 nil 才能置 NULL,GORM struct 更新会把零值 time 当"不改")+ streak 也归 0
	// (取消掌握后重新从 0 累计连对)。
	updates := map[string]interface{}{"mastered": mastered, "mastered_at": nil, "correct_streak": 0}
	if mastered {
		updates["mastered_at"] = time.Now().UTC()
	}
	return r.db.Model(&model.WrongBookItem{}).
		Where("user_id = ? AND question_id = ?", userID, questionID).
		Updates(updates).Error
}

func (r *wrongBookRepo) GetItem(userID, questionID uint) (*model.WrongBookItem, error) {
	var item model.WrongBookItem
	if err := r.db.Where("user_id = ? AND question_id = ?", userID, questionID).First(&item).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (r *wrongBookRepo) ListByUser(userID uint, filters WrongBookFilter) ([]model.WrongBookItem, error) {
	q := r.db.Where("user_id = ?", userID)
	if filters.CourseID != nil {
		q = q.Where("course_id = ?", *filters.CourseID)
	}
	if filters.SubjectID != nil {
		q = q.Where("subject_id = ?", *filters.SubjectID)
	}
	if filters.ChunkID != nil {
		q = q.Where("chunk_id = ?", *filters.ChunkID)
	}
	if filters.Mastered != nil {
		q = q.Where("mastered = ?", *filters.Mastered)
	}
	var items []model.WrongBookItem
	if err := q.Order("first_wrong_at DESC").Find(&items).Error; err != nil {
		return nil, err
	}
	return items, nil
}

// ── admin 观测聚合 ──

func (r *wrongBookRepo) Stats() (WrongBookStats, error) {
	var stats WrongBookStats
	if err := r.db.Model(&model.WrongBookItem{}).Count(&stats.Total).Error; err != nil {
		return stats, err
	}
	if err := r.db.Model(&model.WrongBookItem{}).Where("mastered = ?", false).Count(&stats.Unmastered).Error; err != nil {
		return stats, err
	}
	weekAgo := time.Now().UTC().Add(-7 * 24 * time.Hour)
	if err := r.db.Model(&model.WrongBookItem{}).Where("first_wrong_at >= ?", weekAgo).Count(&stats.ThisWeek).Error; err != nil {
		return stats, err
	}
	return stats, nil
}

// TopFrequentWrong 按题聚合:COUNT(DISTINCT user_id) 得出错这题的学生数,
// JOIN questions 取题面。ORDER BY occur_count DESC LIMIT N。
func (r *wrongBookRepo) TopFrequentWrong(limit int) ([]FrequentWrongRow, error) {
	if limit <= 0 {
		limit = 10
	}
	var rows []FrequentWrongRow
	// 注意:wrong_book_items 有 question_id 冗余,但题面要 join questions。
	// 用子查询聚合 + join 题面。LEFT JOIN 容忍题被删(题面空)。
	err := r.db.Table("wrong_book_items").
		Select(`wrong_book_items.question_id AS question_id,
			COALESCE(questions.stem, '(题目已删除)') AS stem,
			COUNT(DISTINCT wrong_book_items.user_id) AS occur_count,
			SUM(wrong_book_items.attempt_count) AS total_attempts`).
		Joins("LEFT JOIN questions ON questions.id = wrong_book_items.question_id").
		Group("wrong_book_items.question_id").
		Order("occur_count DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// DistributionBySubject 按 subject 分组错题量。wrong_book_items 冗余了 subject_id,
// 但 label 要 join subjects。
func (r *wrongBookRepo) DistributionBySubject() ([]SubjectWrongCount, error) {
	var rows []SubjectWrongCount
	err := r.db.Table("wrong_book_items").
		Select(`subjects.key AS subject_key,
			COALESCE(subjects.label, '未分类') AS subject_label,
			COUNT(*) AS count`).
		Joins("LEFT JOIN subjects ON subjects.id = wrong_book_items.subject_id").
		Group("subjects.id").
		Order("count DESC").
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	return rows, nil
}
