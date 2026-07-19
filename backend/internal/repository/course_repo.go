package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// CourseRepository handles SQL operations for Courses entity.
type CourseRepository interface {
	// WithTx returns a copy bound to an in-progress transaction.
	WithTx(tx *gorm.DB) CourseRepository
	List(grade string, subjectID uint, contentType model.ContentType, allowedIDs []uint) ([]model.Course, error)
	FindByID(id uint) (*model.Course, error)
	// FindByIDWithSubject 同 FindByID 但额外 Preload Subject。**只读路径用**
	// (agent 工具取 course 上下文、Effective*Hint 学科级回退);写路径(UpdateCourse
	// 的 db.Save)禁用 —— Save 遇到预加载的 belongsTo 关联会 FullSaveAssociations
	// 误改 course.SubjectID(回归见 TestCourseUpdate)。
	FindByIDWithSubject(id uint) (*model.Course, error)
	Create(course *model.Course) error
	Update(course *model.Course) error
	Delete(id uint) error
	// SetTags replaces a course's tags with the given tag IDs (syncs the
	// course_tags join table). Pass nil/empty to clear all tags.
	SetTags(courseID uint, tagIDs []uint) error
	// SetGrades replaces a course's applicable-grade set (syncs the
	// course_grades join table). Pass nil/empty to clear all grades.
	SetGrades(courseID uint, grades []model.Grade) error
}

type courseRepo struct {
	db *gorm.DB
}

// NewCourseRepository creates an instance of CourseRepository.
func NewCourseRepository(db *gorm.DB) CourseRepository {
	return &courseRepo{db: db}
}

func (r *courseRepo) WithTx(tx *gorm.DB) CourseRepository {
	return &courseRepo{db: tx}
}

func (r *courseRepo) List(grade string, subjectID uint, contentType model.ContentType, allowedIDs []uint) ([]model.Course, error) {
	var courses []model.Course
	query := r.db.Model(&model.Course{})

	// Filter by user course access permissions
	if allowedIDs != nil {
		if len(allowedIDs) == 0 {
			return []model.Course{}, nil
		}
		query = query.Where("id IN ?", allowedIDs)
	}

	// Grade filter via the course_grades join table (exact match, plus
	// universal courses match any grade). Replaces the old LIKE-based query
	// that suffered from substring false-positives (e.g. grade "1" matching "10").
	if grade != "" {
		query = query.Where(
			"id IN (SELECT course_id FROM course_grades WHERE grade = ? OR grade = ?)",
			grade, string(model.GradeUniversal),
		)
	}
	if subjectID != 0 {
		query = query.Where("subject_id = ?", subjectID)
	}
	// Content type filter: default to learning so entertainment courses don't
	// leak into the Study Hall listing. Entertainment tab explicitly passes
	// ContentEntertainment. "all" skips this filter to return everything (useful for admin).
	if contentType != "all" {
		if contentType == "" {
			contentType = model.ContentLearning
		}
		query = query.Where("content_type = ?", contentType)
	}

	err := query.Preload("Tags").Preload("Grades").Find(&courses).Error
	return courses, err
}

func (r *courseRepo) FindByID(id uint) (*model.Course, error) {
	var course model.Course
	// 注意:这里**不** Preload("Subject")。原因:UpdateCourse 调 FindByID 拿到 c
	// 后用 db.Save(c) 写回,若 c.Subject 被预加载(非零值),GORM 的 FullSaveAssociations
	// 会错误地改写 subjects 表 / course.SubjectID 关系(回归:TestCourseUpdate 改
	// subject 后回读仍是旧值)。EffectiveXxxHint 需要学科级回退的调用方,应单独
	// subjectRepo.FindByID(course.SubjectID) 取 subject 再传入(见 ai_service*.go)。
	if err := r.db.Preload("Tags").Preload("Grades").First(&course, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &course, nil
}

func (r *courseRepo) FindByIDWithSubject(id uint) (*model.Course, error) {
	var course model.Course
	// 只读路径专用:带 Subject 预加载,供 Effective*Hint 学科级回退 + prompt 显示科目名。
	// 写路径禁用(见 FindByID 注释)。
	if err := r.db.Preload("Tags").Preload("Grades").Preload("Subject").First(&course, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &course, nil
}

func (r *courseRepo) Create(course *model.Course) error {
	return r.db.Create(course).Error
}

func (r *courseRepo) Update(course *model.Course) error {
	return r.db.Save(course).Error
}

func (r *courseRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Clean up related episodes and assets.
		//
		// GORM's declared OnDelete:CASCADE relations (chapters, quizzes, tags,
		// course_grades via Course, and the cascade chain course→episode→
		// subtitle/user_progress/entertainment_progress) now fire automatically
		// because _foreign_keys=on lives in the DSN. The per-episode deletes
		// below are kept as explicit defense-in-depth and to cover rows that
		// predate the constraint.
		//
		// HOWEVER several AI/observability tables carry a bare course_id /
		// episode_id column with NO GORM foreignKey relation, so the CASCADE
		// never reaches them. They MUST be cleaned here by hand:
		//   - watch_events        (episode_id, no FK)
		//   - ai_jobs             (course_id + episode_id, no FK)
		//   - ai_summaries        (episode_id, no FK)
		//   - content_chunks      (episode_id, no FK)
		//   - knowledge_memory    (episode_id, no FK)
		//   - quizzes             (episode_id, no FK)
		//   - study_advice scope=course  (polymorphic scope_id, no FK)
		//   - ai_course_summaries (course_id, no FK)
		var episodes []model.Episode
		tx.Where("course_id = ?", id).Find(&episodes)
		for _, ep := range episodes {
			tx.Delete(&model.Subtitle{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.UserProgress{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.EntertainmentProgress{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.WatchEvent{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.AISummary{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.ContentChunk{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.KnowledgeMemory{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.Quiz{}, "episode_id = ?", ep.ID)
			tx.Delete(&model.StudyAdvice{}, "scope = ? AND scope_id = ?", "episode", ep.ID)
		}
		tx.Delete(&model.Episode{}, "course_id = ?", id)
		tx.Delete(&model.UserCourseAccess{}, "course_id = ?", id)
		tx.Delete(&model.CourseGrade{}, "course_id = ?", id)
		tx.Delete(&model.AIJob{}, "course_id = ?", id)
		tx.Delete(&model.AICourseSummary{}, "course_id = ?", id)
		tx.Delete(&model.StudyAdvice{}, "scope = ? AND scope_id = ?", "course", id)
		return tx.Delete(&model.Course{}, id).Error
	})
}

// SetTags replaces the course's tag associations. It uses GORM's association
// Replace which diffs the join table — rows no longer in tagIDs are removed,
// new ones are inserted, unchanged ones stay.
func (r *courseRepo) SetTags(courseID uint, tagIDs []uint) error {
	var course model.Course
	if err := r.db.First(&course, courseID).Error; err != nil {
		return err
	}
	tagIDs = dedupUint(tagIDs)
	var tags []model.Tag
	if len(tagIDs) > 0 {
		if err := r.db.Where("id IN ?", tagIDs).Find(&tags).Error; err != nil {
			return err
		}
	}
	return r.db.Model(&course).Association("Tags").Replace(&tags)
}

// SetGrades replaces the course's applicable-grade set. It clears the
// course_grades join table for this course and inserts one row per grade.
func (r *courseRepo) SetGrades(courseID uint, grades []model.Grade) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("course_id = ?", courseID).Delete(&model.CourseGrade{}).Error; err != nil {
			return err
		}
		seen := make(map[model.Grade]bool, len(grades))
		for _, g := range grades {
			if seen[g] || !g.Valid() {
				continue
			}
			seen[g] = true
			if err := tx.Create(&model.CourseGrade{CourseID: courseID, Grade: g}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// dedupUint returns ids with duplicates removed, preserving order.
func dedupUint(ids []uint) []uint {
	seen := make(map[uint]bool, len(ids))
	out := make([]uint, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}
