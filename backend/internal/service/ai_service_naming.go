package service

import (
	"studyquest/backend/internal/model"
)

// Code split from ai_service.go for navigability.
// jobNameCache + resolveJobNames (human-readable names for the admin jobs table).

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

// (The old `sleep` test-seam var is gone — runWorker now uses a real
// context.Context + time.Ticker, which is itself cleanly cancellable, so
// the indirection was no longer earning its keep.)
