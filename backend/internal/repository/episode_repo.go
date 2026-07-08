package repository

import (
	"errors"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// EpisodeRepository implements core GORM functions and double-protection search.
type EpisodeRepository interface {
	ListByCourse(courseID uint) ([]model.Episode, error)
	ListByNullDuration() ([]model.Episode, error)
	FindByID(id uint) (*model.Episode, error)
	FindByHash(hash string) (*model.Episode, error)
	FindByPathAndSize(path string, size int64) (*model.Episode, error)
	Create(episode *model.Episode) error
	Update(episode *model.Episode) error
	Delete(id uint) error
	FindByCriteria(basename string, size *int64, pathHint string) ([]model.Episode, error)

	// Aggregation queries used by the admin dashboard.
	CountAll() (int64, error)
	CountByNullDuration() (int64, error)
	SumTotalDurationSeconds() (int64, error)
	CountBySubject() (map[string]int, error)
	CountByCourse(courseID uint) (int64, error)
	SumDurationByCourse(courseID uint) (int64, error)
	RecentDailyCount(days int) ([]DailyCount, error)

	// Subtitle operations
	GetSubtitle(episodeID uint) (*model.Subtitle, error)
	ListSubtitles(episodeID uint) ([]model.Subtitle, error)
	GetSubtitleByID(id uint) (*model.Subtitle, error)
	SaveSubtitle(subtitle *model.Subtitle) error
	DeleteSubtitle(id uint) error

	// AI Lesson Content operations
	GetAIContent(episodeID uint) (*model.AILessonContent, error)
	SaveAIContent(content *model.AILessonContent) error

	// BatchAccessibleEpisodeCounts returns user_id → count of episodes across
	// that user's granted courses (one query, JOIN user_course_accesses ↔
	// episodes). Used by the admin user list to render "X/Y 课时".
	BatchAccessibleEpisodeCounts() (map[uint]int64, error)
}

// DailyCount is a per-day aggregate row used by dashboard charts.
type DailyCount struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

type episodeRepo struct {
	db *gorm.DB
}

// NewEpisodeRepository creates an instance of EpisodeRepository.
func NewEpisodeRepository(db *gorm.DB) EpisodeRepository {
	return &episodeRepo{db: db}
}

func (r *episodeRepo) ListByCourse(courseID uint) ([]model.Episode, error) {
	var episodes []model.Episode
	// Order by sort_order ascending
	err := r.db.Where("course_id = ?", courseID).Order("sort_order asc").Find(&episodes).Error
	return episodes, err
}

// ListByNullDuration returns every episode whose duration_seconds is NULL —
// i.e. ones that still need an ffprobe backfill. Used by the admin
// "scan missing durations" action.
func (r *episodeRepo) ListByNullDuration() ([]model.Episode, error) {
	var episodes []model.Episode
	err := r.db.Where("duration_seconds IS NULL OR cover_url IS NULL OR cover_url = ''").Order("id asc").Find(&episodes).Error
	return episodes, err
}

func (r *episodeRepo) FindByID(id uint) (*model.Episode, error) {
	var ep model.Episode
	if err := r.db.First(&ep, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ep, nil
}

func (r *episodeRepo) FindByHash(hash string) (*model.Episode, error) {
	var ep model.Episode
	if err := r.db.Where("file_hash = ?", hash).First(&ep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ep, nil
}

func (r *episodeRepo) FindByPathAndSize(path string, size int64) (*model.Episode, error) {
	var ep model.Episode
	if err := r.db.Where("video_relative_path = ? AND file_size = ?", path, size).First(&ep).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ep, nil
}

func (r *episodeRepo) Create(episode *model.Episode) error {
	return r.db.Create(episode).Error
}

func (r *episodeRepo) Update(episode *model.Episode) error {
	return r.db.Save(episode).Error
}

func (r *episodeRepo) Delete(id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		tx.Delete(&model.Subtitle{}, "episode_id = ?", id)
		tx.Delete(&model.AILessonContent{}, "episode_id = ?", id)
		tx.Delete(&model.UserProgress{}, "episode_id = ?", id)
		return tx.Delete(&model.Episode{}, id).Error
	})
}

func (r *episodeRepo) GetSubtitle(episodeID uint) (*model.Subtitle, error) {
	var sub model.Subtitle
	if err := r.db.Where("episode_id = ?", episodeID).Order("id asc").First(&sub).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *episodeRepo) ListSubtitles(episodeID uint) ([]model.Subtitle, error) {
	var subs []model.Subtitle
	err := r.db.Where("episode_id = ?", episodeID).Order("id asc").Find(&subs).Error
	return subs, err
}

func (r *episodeRepo) GetSubtitleByID(id uint) (*model.Subtitle, error) {
	var sub model.Subtitle
	if err := r.db.First(&sub, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *episodeRepo) SaveSubtitle(subtitle *model.Subtitle) error {
	var sub model.Subtitle
	err := r.db.Where("episode_id = ? AND language = ?", subtitle.EpisodeID, subtitle.Language).First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(subtitle).Error
		}
		return err
	}
	sub.SrtContent = subtitle.SrtContent
	sub.Label = subtitle.Label
	return r.db.Save(&sub).Error
}

func (r *episodeRepo) DeleteSubtitle(id uint) error {
	return r.db.Delete(&model.Subtitle{}, id).Error
}

func (r *episodeRepo) GetAIContent(episodeID uint) (*model.AILessonContent, error) {
	var ai model.AILessonContent
	if err := r.db.First(&ai, "episode_id = ?", episodeID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &ai, nil
}

func (r *episodeRepo) SaveAIContent(content *model.AILessonContent) error {
	var ai model.AILessonContent
	err := r.db.First(&ai, "episode_id = ?", content.EpisodeID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(content).Error
		}
		return err
	}
	ai.PreAdventureJSON = content.PreAdventureJSON
	ai.PostReviewJSON = content.PostReviewJSON
	return r.db.Save(&ai).Error
}

// BatchAccessibleEpisodeCounts counts episodes each user can access, by
// JOINing user_course_accesses with episodes on course_id and grouping by
// user. Users with access to courses that have zero episodes still get a row
// (count 0) only if the JOIN is INNER — we use a LEFT-like count via
// COUNT(episodes.id) so course-only-access still counts as 0.
func (r *episodeRepo) BatchAccessibleEpisodeCounts() (map[uint]int64, error) {
	type row struct {
		UserID uint
		Count  int64
	}
	var rows []row
	err := r.db.Table("user_course_accesses").
		Select("user_course_accesses.user_id AS user_id, COUNT(episodes.id) AS count").
		Joins("LEFT JOIN episodes ON episodes.course_id = user_course_accesses.course_id").
		Group("user_course_accesses.user_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.UserID] = r.Count
	}
	return out, nil
}

func (r *episodeRepo) FindByCriteria(basename string, size *int64, pathHint string) ([]model.Episode, error) {
	var episodes []model.Episode
	query := r.db.Model(&model.Episode{})

	// Match basename at the end of path (e.g., /01.mp4 or /01.mkv)
	basenameLike := "%/" + basename + ".%"
	query = query.Where("(original_relative_path LIKE ? OR video_relative_path LIKE ?)", basenameLike, basenameLike)

	if size != nil {
		query = query.Where("file_size = ?", *size)
	}

	if pathHint != "" {
		pathHintLike := "%" + pathHint + "%"
		query = query.Where("(original_relative_path LIKE ? OR video_relative_path LIKE ?)", pathHintLike, pathHintLike)
	}

	err := query.Find(&episodes).Error
	return episodes, err
}

// CountAll returns the total number of episodes across all courses.
func (r *episodeRepo) CountAll() (int64, error) {
	var count int64
	err := r.db.Model(&model.Episode{}).Count(&count).Error
	return count, err
}

// CountByNullDuration counts episodes still missing a probed duration (or cover),
// mirroring the ListByNullDuration predicate.
func (r *episodeRepo) CountByNullDuration() (int64, error) {
	var count int64
	err := r.db.Model(&model.Episode{}).
		Where("duration_seconds IS NULL OR cover_url IS NULL OR cover_url = ''").
		Count(&count).Error
	return count, err
}

// SumTotalDurationSeconds sums every probed episode duration. NULLs are ignored
// by SQLite, so this only counts episodes that have been ffprobed.
func (r *episodeRepo) SumTotalDurationSeconds() (int64, error) {
	var sum int64
	err := r.db.Model(&model.Episode{}).
		Where("duration_seconds IS NOT NULL").
		Select("COALESCE(SUM(duration_seconds), 0)").Scan(&sum).Error
	return sum, err
}

// CountBySubject joins episodes → courses → subjects and groups by the subject
// key. Returns a subject-key→episode-count map for the dashboard distribution chart.
func (r *episodeRepo) CountBySubject() (map[string]int, error) {
	type row struct {
		Subject string
		Count   int
	}
	var rows []row
	err := r.db.Table("episodes").
		Select("subjects.key AS subject, COUNT(*) AS count").
		Joins("LEFT JOIN courses ON courses.id = episodes.course_id").
		Joins("LEFT JOIN subjects ON subjects.id = courses.subject_id").
		Group("subjects.key").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int, len(rows))
	for _, r := range rows {
		out[r.Subject] = r.Count
	}
	return out, nil
}

// CountByCourse returns the number of episodes belonging to a single course.
func (r *episodeRepo) CountByCourse(courseID uint) (int64, error) {
	var count int64
	err := r.db.Model(&model.Episode{}).Where("course_id = ?", courseID).Count(&count).Error
	return count, err
}

// SumDurationByCourse sums the probed durations of all episodes in one course.
func (r *episodeRepo) SumDurationByCourse(courseID uint) (int64, error) {
	var sum int64
	err := r.db.Model(&model.Episode{}).
		Where("course_id = ? AND duration_seconds IS NOT NULL", courseID).
		Select("COALESCE(SUM(duration_seconds), 0)").Scan(&sum).Error
	return sum, err
}

// RecentDailyCount returns episode counts grouped by creation day for the last
// `days` days, oldest first. Uses SQLite's date() on the created_at column.
func (r *episodeRepo) RecentDailyCount(days int) ([]DailyCount, error) {
	type row struct {
		Date  string
		Count int
	}
	// Window and day-bucketing in the BUSINESS timezone so "today" lines up
	// with the Beijing calendar (and with the streak/today cutoffs elsewhere).
	// We compute the cutoff as a Go instant and bucket via the business offset
	// instead of SQLite's date('now'), which is UTC-only.
	mod := sqliteOffsetModifier(businessZoneOffsetMinutes())
	since := appclock.Now().AddDate(0, 0, -days+1)
	sinceMidnight := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location())
	var rows []row
	err := r.db.Table("episodes").
		Select("strftime('%Y-%m-%d', datetime(created_at, ?)) AS date, COUNT(*) AS count", mod).
		Where("created_at >= ?", sinceMidnight.UTC()).
		Group("date").
		Order("date asc").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]DailyCount, len(rows))
	for i, r := range rows {
		out[i] = DailyCount{Date: r.Date, Count: r.Count}
	}
	return out, err
}

// itoa is a stdlib-free local int→string to avoid pulling in strconv here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
