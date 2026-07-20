package service

import (
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// CourseService handles Course business operations.
type CourseService interface {
	GetCourses(userID uint, userRole string, grade string, subjectID uint, contentType model.ContentType) ([]model.Course, error)
	GetCourseByID(id uint) (*model.Course, error)
	// CreateCourse/UpdateCourse 末尾的 aiSummaryEnabled/aiQuizEnabled 透传 admin
	// 的两个 AI 开关。放在签名尾部是为了让其它调用方(多为 admin handler 与测试)
	// 只需追加两个 bool 参数即可适配,不必改动已有参数顺序。
	//
	// aiConfig 是 admin 表单的 5 个 AI 提示 textarea(whisper/summary/quiz/advice/
	// term_dict),service 层直接 SetAIConfig 序列化进 Course.AIConfigJSON 单列
	// (前向兼容:加新配置项不必改 schema)。老 AIHint 列不再写,旧数据走
	// Effective* 回退。详见 model.Course.AIConfigJSON / AIConfig / SetAIConfig。
	CreateCourse(title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiConfig model.AIConfig, aiSummaryEnabled, aiQuizEnabled bool) (*model.Course, error)
	UpdateCourse(id uint, title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiConfig model.AIConfig, aiSummaryEnabled, aiQuizEnabled bool) (*model.Course, error)
	DeleteCourse(id uint) error
}

type courseService struct {
	courseRepo repository.CourseRepository
	userRepo   repository.UserRepository
}

// NewCourseService creates an instance of CourseService.
func NewCourseService(cr repository.CourseRepository, ur repository.UserRepository) CourseService {
	return &courseService{
		courseRepo: cr,
		userRepo:   ur,
	}
}

func (s *courseService) GetCourses(userID uint, userRole string, grade string, subjectID uint, contentType model.ContentType) ([]model.Course, error) {
	// Admin or Parent can view all courses
	if userRole == model.RoleAdmin || userRole == model.RoleParent {
		return s.courseRepo.List(grade, subjectID, contentType, nil)
	}

	// Students/Teens are restricted to granted courses only
	allowedIDs, err := s.userRepo.GetAccessList(userID)
	if err != nil {
		return nil, err
	}

	return s.courseRepo.List(grade, subjectID, contentType, allowedIDs)
}

func (s *courseService) GetCourseByID(id uint) (*model.Course, error) {
	return s.courseRepo.FindByID(id)
}

func (s *courseService) CreateCourse(title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiConfig model.AIConfig, aiSummaryEnabled, aiQuizEnabled bool) (*model.Course, error) {
	if !contentType.Valid() {
		contentType = model.ContentLearning
	}

	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}

	c := &model.Course{
		Title:            title,
		SubjectID:        subjectID,
		ContentType:      contentType,
		CoverURL:         coverURL,
		AttachmentJSON:   attachmentJSON,
		AISummaryEnabled: aiSummaryEnabled,
		AIQuizEnabled:    aiQuizEnabled,
	}
	c.SetAIConfig(aiConfig)
	if err := s.courseRepo.Create(c); err != nil {
		return nil, err
	}
	// Sync the many2many tag association after the course row exists.
	if len(tagIDs) > 0 {
		if err := s.courseRepo.SetTags(c.ID, tagIDs); err != nil {
			return nil, err
		}
	}
	// Sync the grade set (course_grades join table).
	if err := s.courseRepo.SetGrades(c.ID, grades); err != nil {
		return nil, err
	}
	// Reload so the returned object carries Tags + Grades for DTO projection.
	reloaded, err := s.courseRepo.FindByID(c.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return c, nil
}

func (s *courseService) UpdateCourse(id uint, title string, grades []model.Grade, subjectID uint, contentType model.ContentType, coverURL string, tagIDs []uint, attachmentJSON string, aiConfig model.AIConfig, aiSummaryEnabled, aiQuizEnabled bool) (*model.Course, error) {
	if !contentType.Valid() {
		contentType = model.ContentLearning
	}

	c, err := s.courseRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, nil
	}

	if attachmentJSON == "" {
		attachmentJSON = "[]"
	}

	c.Title = title
	c.SubjectID = subjectID
	c.ContentType = contentType
	c.CoverURL = coverURL
	c.AttachmentJSON = attachmentJSON
	// AI 配置走单一 JSON 列(AIConfigJSON)。service 层把 admin 表单的 5 个
	// textarea 组装成 AIConfig 再序列化。加新配置项时只需扩 AIConfig struct +
	// 表单,不用改 DB schema。老 AIHint 列清空,让 Effective* 不再回退到旧
	// blob(避免陈旧提示残留)。
	c.SetAIConfig(aiConfig)
	c.AIHint = ""
	// AI 开关同样透传:之前这两行漏掉,导致 admin 即便勾选了也会被丢弃。
	c.AISummaryEnabled = aiSummaryEnabled
	c.AIQuizEnabled = aiQuizEnabled

	if err := s.courseRepo.Update(c); err != nil {
		return nil, err
	}
	// Replace the tag set (handles add/remove/clear atomically).
	if err := s.courseRepo.SetTags(c.ID, tagIDs); err != nil {
		return nil, err
	}
	// Replace the grade set.
	if err := s.courseRepo.SetGrades(c.ID, grades); err != nil {
		return nil, err
	}
	// Reload to reflect the new Tags + Grades associations in the returned object.
	reloaded, err := s.courseRepo.FindByID(c.ID)
	if err != nil {
		return nil, err
	}
	if reloaded != nil {
		return reloaded, nil
	}
	return c, nil
}

func (s *courseService) DeleteCourse(id uint) error {
	return s.courseRepo.Delete(id)
}
