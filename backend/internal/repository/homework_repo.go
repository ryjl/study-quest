package repository

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// homework_repo.go — 课后作业卷存取(见 model.homework.go)。和 exam_repo.go 平行:
//   - CreateHomework: archive 旧 active homework + insert 新的(单事务,守 partial unique index)
//   - GetActiveHomework: 取 episode 的 active homework(nil = 没有)
//   - admin 列表 / 历史归档
//   - HomeworkPromptConfig: 按 subject 存完整 system prompt,lazy 创建 + 编辑 + 恢复默认
//
// 独立于 AIContentRepository——作业是平行功能,独立 repo 让边界更清晰(AIContentRepository
// 已经很大,不继续往里塞)。

// HomeworkWithContent 是 GetActiveHomework/GetHomeworkByID 的返回:卷子本体 + sections
// + questions 三层全展开。sections 按 Seq 排,questions 按 SectionID 分组后各自按 Seq 排,
// 由调用方(service)组装成视图。
type HomeworkWithContent struct {
	Homework model.Homework
	Sections []model.HomeworkSection
	// Questions 所有 section 的题混在一起,按 SectionID ASC, Seq ASC 排序。调用方按
	// SectionID 分组渲染(一个 section 的题连续)。不返回 map[uint][]Question 是为了
	// 保持顺序确定 + 序列化友好。
	Questions []model.HomeworkQuestion
}

// HomeworkRepository 课后作业存取接口。
type HomeworkRepository interface {
	// CreateHomework 在单事务里 archive 该 episode 的旧 active homework(转 archived),
	// 再 insert 新 active homework + 它的 sections + questions。返回新 homework id。
	// 同 exam_repo.CreateExam 的 archive-then-insert 范式:partial unique index 是 DB 级
	// 守卫,事务里 archive-then-insert 保证不违反唯一约束。调用方负责算好 Version
	// (无旧卷=1,有旧卷=旧+1)——repo 保持无状态,不自增。
	CreateHomework(hw *model.Homework, sections []model.HomeworkSection, questions []model.HomeworkQuestion) (uint, error)
	// GetActiveHomework 取某 episode 的 active homework 完整内容(sections+questions),
	// 无返回 (nil, nil)。
	GetActiveHomework(episodeID uint) (*HomeworkWithContent, error)
	// GetHomeworkByID 按 id 取一条 homework 完整内容(admin 预览/打印用)。无返回 (nil,nil)。
	GetHomeworkByID(id uint) (*HomeworkWithContent, error)
	// ListHomeworksByCourse 列某课程下所有 homework(admin 列表,按 created_at DESC)。
	// 只返回卷子本体不含 sections/questions(列表轻量;点开某条再 GetHomeworkByID 拉详情)。
	ListHomeworksByCourse(courseID uint) ([]model.Homework, error)
	// ListArchivedHomeworks 列某 episode 的历史版本(Status='archived'),newest-first。
	// 不含 active(active 走 GetActiveHomework)。
	ListArchivedHomeworks(episodeID uint) ([]model.Homework, error)

	// ── HomeworkPromptConfig ──
	// GetOrCreatePromptConfig 取某 subject 的 prompt 配置;无则用 defaultPrompt 创建一条
	// 返回(lazy 创建,NOT NULL 保证有内容)。defaultPrompt 由 service 层用
	// defaultHomeworkPrompt(subject) 算好传入(repo 不依赖 ai/agent 包,保持分层干净)。
	GetOrCreatePromptConfig(subjectID uint, defaultPrompt string) (model.HomeworkPromptConfig, error)
	// UpdatePromptConfig 覆盖某 subject 的 system prompt(admin 编辑)。
	UpdatePromptConfig(subjectID uint, prompt string) error
	// ResetPromptConfig 把某 subject 的 system prompt 重置回 defaultPrompt(admin 恢复默认)。
	ResetPromptConfig(subjectID uint, defaultPrompt string) error
}

type homeworkRepo struct {
	db *gorm.DB
}

// NewHomeworkRepository creates a HomeworkRepository bound to db.
func NewHomeworkRepository(db *gorm.DB) HomeworkRepository {
	return &homeworkRepo{db: db}
}

func (r *homeworkRepo) CreateHomework(hw *model.Homework, sections []model.HomeworkSection, questions []model.HomeworkQuestion) (uint, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// Archive 旧 active homework(同 episode)。只翻 status,保留行做历史。
		var old model.Homework
		findErr := tx.Where("episode_id = ? AND status = 'active'", hw.EpisodeID).First(&old).Error
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
		// Insert 新 active homework。
		hw.ID = 0
		hw.Status = "" // 让列默认 'active' 生效
		if err := tx.Create(hw).Error; err != nil {
			return err
		}
		for i := range sections {
			sections[i].ID = 0
			sections[i].HomeworkID = hw.ID
		}
		if len(sections) > 0 {
			if err := tx.Create(&sections).Error; err != nil {
				return err
			}
		}
		// questions 的 SectionID 在调用方算好(指向上面刚 insert 的 sections 里的对应项)。
		// 调用方构造 sections/questions 时要保证 SectionID 已对齐(见 service 层)。
		for i := range questions {
			questions[i].ID = 0
			questions[i].HomeworkID = hw.ID
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
	return hw.ID, nil
}

// loadContent 加载 homework 的 sections + questions,组装成 HomeworkWithContent。
// GetActiveHomework / GetHomeworkByID 共用。
func (r *homeworkRepo) loadContent(hw *model.Homework) (*HomeworkWithContent, error) {
	var sections []model.HomeworkSection
	if err := r.db.Where("homework_id = ?", hw.ID).Order("seq ASC").Find(&sections).Error; err != nil {
		return nil, err
	}
	var questions []model.HomeworkQuestion
	if err := r.db.Where("homework_id = ?", hw.ID).Order("section_id ASC, seq ASC").Find(&questions).Error; err != nil {
		return nil, err
	}
	return &HomeworkWithContent{Homework: *hw, Sections: sections, Questions: questions}, nil
}

func (r *homeworkRepo) GetActiveHomework(episodeID uint) (*HomeworkWithContent, error) {
	var hw model.Homework
	if err := r.db.Where("episode_id = ? AND status = 'active'", episodeID).First(&hw).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.loadContent(&hw)
}

func (r *homeworkRepo) GetHomeworkByID(id uint) (*HomeworkWithContent, error) {
	var hw model.Homework
	if err := r.db.First(&hw, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.loadContent(&hw)
}

func (r *homeworkRepo) ListHomeworksByCourse(courseID uint) ([]model.Homework, error) {
	var hws []model.Homework
	if err := r.db.Where("course_id = ?", courseID).Order("created_at DESC").Find(&hws).Error; err != nil {
		return nil, err
	}
	return hws, nil
}

func (r *homeworkRepo) ListArchivedHomeworks(episodeID uint) ([]model.Homework, error) {
	var hws []model.Homework
	if err := r.db.Where("episode_id = ? AND status = 'archived'", episodeID).
		Order("archived_at DESC").Find(&hws).Error; err != nil {
		return nil, err
	}
	return hws, nil
}

// ── HomeworkPromptConfig ──

func (r *homeworkRepo) GetOrCreatePromptConfig(subjectID uint, defaultPrompt string) (model.HomeworkPromptConfig, error) {
	var cfg model.HomeworkPromptConfig
	err := r.db.Transaction(func(tx *gorm.DB) error {
		// 先查:有就直接返回。
		if err := tx.Where("subject_id = ?", subjectID).First(&cfg).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// 无则 lazy 创建(NOT NULL,一次性灌满默认 prompt)。
		cfg = model.HomeworkPromptConfig{
			SubjectID:    subjectID,
			SystemPrompt: defaultPrompt,
		}
		if err := tx.Create(&cfg).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return model.HomeworkPromptConfig{}, err
	}
	return cfg, nil
}

func (r *homeworkRepo) UpdatePromptConfig(subjectID uint, prompt string) error {
	return r.db.Model(&model.HomeworkPromptConfig{}).
		Where("subject_id = ?", subjectID).
		Update("system_prompt", prompt).Error
}

func (r *homeworkRepo) ResetPromptConfig(subjectID uint, defaultPrompt string) error {
	return r.db.Model(&model.HomeworkPromptConfig{}).
		Where("subject_id = ?", subjectID).
		Update("system_prompt", defaultPrompt).Error
}
