package handler

// DTOs for /admin/api/*. The GORM models in internal/model have no json tags,
// so we project them through these structs to emit clean snake_case JSON to
// the SPA. Keeping this here (not on the models) avoids touching the /api/v1/*
// contract that the Flutter client depends on.

import (
	"time"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

type courseDTO struct {
	ID                  uint     `json:"id"`
	Title               string   `json:"title"`
	Grade               string   `json:"grade"`
	Subject             string   `json:"subject"`     // subject key, e.g. "math" (resolved from SubjectID)
	SubjectID           uint     `json:"subject_id"`  // FK → subjects.id
	CoverURL            string   `json:"cover_url"`
	// CoverFallbackURL is a derived thumbnail used only when CoverURL is empty:
	// the first episode's cover (by sort_order). Lets newly-imported courses
	// show a real frame instead of an emoji placeholder before an admin picks
	// a dedicated cover. Not persisted — computed per-listing.
	CoverFallbackURL    string   `json:"cover_fallback_url"`
	Tags                string   `json:"tags"`        // comma-joined labels (back-compat for old clients)
	TagsList            []string `json:"tags_list"`   // tag labels in sort order
	TagIDs              []uint   `json:"tag_ids"`     // tag ids (for admin edit forms)
	GradeDisplay        string   `json:"grade_display"`
	AttachmentJSON      string   `json:"attachment_json"`
	EpisodeCount        int64    `json:"episode_count"`
	ChapterCount        int64    `json:"chapter_count"`
	TotalDurationSeconds int64   `json:"total_duration_seconds"`
	CreatedAt           string   `json:"created_at"`
	UpdatedAt           string   `json:"updated_at"`
}

func (h *adminHandler) toCourseDTO(c model.Course) courseDTO {
	eps, _ := h.episodeRepo.CountByCourse(c.ID)
	chs, _ := h.chapterRepo.CountByCourse(c.ID)
	dur, _ := h.episodeRepo.SumDurationByCourse(c.ID)
	// Resolve the subject key for the frontend (which still expects a string
	// key, not the raw FK id). A missing subject falls back to "" — this
	// shouldn't happen under RESTRICT, but keeps listings resilient.
	subjectKey := ""
	if subj, _ := h.subjectRepo.FindByID(c.SubjectID); subj != nil {
		subjectKey = subj.Key
	}
	// Cover fallback: when the course has no dedicated cover, borrow the first
	// episode's cover (by sort_order) so the card shows a real frame instead of
	// an emoji. Only computed when CoverURL is empty, and never persisted.
	coverFallback := ""
	if c.CoverURL == "" {
		if epList, _ := h.episodeRepo.ListByCourse(c.ID); len(epList) > 0 {
			for _, e := range epList {
				if e.CoverURL != "" {
					coverFallback = e.CoverURL
					break
				}
			}
		}
	}

	return courseDTO{
		ID:                   c.ID,
		Title:                c.Title,
		Grade:                string(c.Grade),
		Subject:              subjectKey,
		SubjectID:            c.SubjectID,
		CoverURL:             c.CoverURL,
		CoverFallbackURL:     coverFallback,
		Tags:                 c.TagsJoined(),   // comma-joined labels (legacy)
		TagsList:             c.TagsList(),     // []string labels
		TagIDs:               tagIDsOf(c.Tags), // []uint tag ids
		GradeDisplay:         c.GradeDisplay(),
		AttachmentJSON:       c.AttachmentJSON,
		EpisodeCount:         eps,
		ChapterCount:         chs,
		TotalDurationSeconds: dur,
		CreatedAt:            formatTime(c.CreatedAt),
		UpdatedAt:            formatTime(c.UpdatedAt),
	}
}

type episodeDTO struct {
	ID                   uint   `json:"id"`
	CourseID             uint   `json:"course_id"`
	ChapterID            uint   `json:"chapter_id"`
	SortOrder            int    `json:"sort_order"`
	Title                string `json:"title"`
	VideoRelativePath    string `json:"video_relative_path"`
	CoverURL             string `json:"cover_url"`
	AttachmentJSON       string `json:"attachment_json"`
	FileHash             string `json:"file_hash"`
	OriginalRelativePath string `json:"original_relative_path"`
	FileSize             *int64 `json:"file_size"`
	DurationSeconds      *int   `json:"duration_seconds"`
	MediaMetaJSON        string `json:"media_meta_json"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

func toEpisodeDTO(e model.Episode) episodeDTO {
	return episodeDTO{
		ID:                   e.ID,
		CourseID:             e.CourseID,
		ChapterID:            e.ChapterID,
		SortOrder:            e.SortOrder,
		Title:                e.Title,
		VideoRelativePath:    e.VideoRelativePath,
		CoverURL:             e.CoverURL,
		AttachmentJSON:       e.AttachmentJSON,
		FileHash:             e.FileHash,
		OriginalRelativePath: e.OriginalRelativePath,
		FileSize:             e.FileSize,
		DurationSeconds:      e.DurationSeconds,
		MediaMetaJSON:        e.MediaMetaJSON,
		CreatedAt:            formatTime(e.CreatedAt),
		UpdatedAt:            formatTime(e.UpdatedAt),
	}
}

type chapterDTO struct {
	ID             uint   `json:"id"`
	CourseID       uint   `json:"course_id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	CoverURL       string `json:"cover_url"`
	AttachmentJSON string `json:"attachment_json"`
	SortOrder      int    `json:"sort_order"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

func toChapterDTO(c model.Chapter) chapterDTO {
	return chapterDTO{
		ID:             c.ID,
		CourseID:       c.CourseID,
		Title:          c.Title,
		Description:    c.Description,
		CoverURL:       c.CoverURL,
		AttachmentJSON: c.AttachmentJSON,
		SortOrder:      c.SortOrder,
		CreatedAt:      formatTime(c.CreatedAt),
		UpdatedAt:      formatTime(c.UpdatedAt),
	}
}

type userDTO struct {
	ID                 uint     `json:"id"`
	Nickname           string   `json:"nickname"`
	AvatarURL          string   `json:"avatar_url"`
	Role               string   `json:"role"`
	CurrentPoints      int      `json:"current_points"`
	TotalEarnedPoints  int      `json:"total_earned_points"`
	CourseAccess       []uint   `json:"course_access"`
	// Learning stats (populated from batch aggregates in ListUsers so the
	// user list avoids N+1). All default to 0 when the user has no data.
	CompletedEpisodes   int    `json:"completed_episodes"`   // 完成课时数
	AccessibleEpisodes  int    `json:"accessible_episodes"`  // 已授权课程总课时数
	WatchSeconds        int64  `json:"watch_seconds"`        // 累计学习秒（前端按此显示，避免 <60s 误显示 0）
	WatchMinutes        int    `json:"watch_minutes"`        // 累计学习分钟（= watch_seconds/60，保留兼容）
	UnlockedBadges      int    `json:"unlocked_badges"`      // 已解锁徽章
	TotalBadges         int    `json:"total_badges"`         // 全局徽章总数
	LastActiveAt        string `json:"last_active_at"`       // 最近一次上报进度（空=从未学习）
	CreatedAt          string   `json:"created_at"`
	UpdatedAt          string   `json:"updated_at"`
}

// userStatsBatch holds the batch-aggregated maps that ListUsers computes
// once and feeds into toUserDTO, so per-user projection is O(1) map lookups.
type userStatsBatch struct {
	points      map[uint]model.UserPoint
	access      map[uint][]uint
	progress    map[uint]repository.UserProgressSummary
	accessible  map[uint]int64
	badges      map[uint]int64
	totalBadges int64
}

func (h *adminHandler) toUserDTO(u model.User, b userStatsBatch) userDTO {
	var cp, tp int
	if pt, ok := b.points[u.ID]; ok {
		cp = pt.CurrentPoints
		tp = pt.TotalEarnedPoints
	}
	access := b.access[u.ID]
	if access == nil {
		access = []uint{}
	}
	prog := b.progress[u.ID]
	lastActive := ""
	if prog.LastActiveAt != nil {
		lastActive = formatTime(*prog.LastActiveAt)
	}
	return userDTO{
		ID:                 u.ID,
		Nickname:           u.Nickname,
		AvatarURL:          u.AvatarURL,
		Role:               u.Role,
		CurrentPoints:      cp,
		TotalEarnedPoints:  tp,
		CourseAccess:       access,
		CompletedEpisodes:  int(prog.CompletedEpisodes),
		AccessibleEpisodes: int(b.accessible[u.ID]),
		WatchSeconds:       prog.TotalWatchSeconds,
		WatchMinutes:       int(prog.TotalWatchSeconds / 60),
		UnlockedBadges:     int(b.badges[u.ID]),
		TotalBadges:        int(b.totalBadges),
		LastActiveAt:       lastActive,
		CreatedAt:          formatTime(u.CreatedAt),
		UpdatedAt:          formatTime(u.UpdatedAt),
	}
}

type badgeDTO struct {
	ID          uint   `json:"id"`
	Code        string `json:"code"`
	Title       string `json:"title"`
	Description string `json:"description"`
	IconName    string `json:"icon_name"`
	RuleType    string `json:"rule_type"`
	RuleTarget  string `json:"rule_target"`
	Threshold   int    `json:"threshold"`
	RuleJSON    string `json:"rule_json"` // composite rule tree; empty = single rule (RuleType/Target/Threshold)
	IsSystem    bool   `json:"is_system"` // true = seeded default, protected from deletion
	Unlocked    bool   `json:"unlocked"`
	UnlockedAt  string `json:"unlocked_at"`
}

func toBadgeDTO(b model.Badge, unlocked bool, unlockedAt string) badgeDTO {
	return badgeDTO{
		ID:          b.ID,
		Code:        b.Code,
		Title:       b.Title,
		Description: b.Description,
		IconName:    b.IconName,
		RuleType:    b.RuleType,
		RuleTarget:  b.RuleTarget,
		Threshold:   b.Threshold,
		RuleJSON:    b.RuleJSON,
		IsSystem:    b.IsSystem,
		Unlocked:    unlocked,
		UnlockedAt:  unlockedAt,
	}
}

type subjectDTO struct {
	ID        uint   `json:"id"`
	Key       string `json:"key"`
	Label     string `json:"label"`
	Emoji     string `json:"emoji"`
	Color     string `json:"color"`
	SortOrder int    `json:"sort_order"`
	IsSystem  bool   `json:"is_system"` // true = seeded default, protected from deletion
}

func toSubjectDTO(s model.Subject) subjectDTO {
	return subjectDTO{
		ID:        s.ID,
		Key:       s.Key,
		Label:     s.Label,
		Emoji:     s.Emoji,
		Color:     s.Color,
		SortOrder: s.SortOrder,
		IsSystem:  s.IsSystem,
	}
}

type tagDTO struct {
	ID          uint   `json:"id"`
	Key         string `json:"key"`
	Label       string `json:"label"`
	Color       string `json:"color"`
	SortOrder   int    `json:"sort_order"`
	IsSystem    bool   `json:"is_system"`    // true = seeded default, protected from deletion
	CourseCount int64  `json:"course_count"` // how many courses use this tag (blast radius for delete-confirm)
}

func toTagDTO(t model.Tag) tagDTO {
	return tagDTO{
		ID:        t.ID,
		Key:       t.Key,
		Label:     t.Label,
		Color:     t.Color,
		SortOrder: t.SortOrder,
		IsSystem:  t.IsSystem,
	}
}

// toTagDTOWithCount is the list-path variant that also fills CourseCount from
// a precomputed tag_id → count map (so the tag table can show "used by N
// courses" without an N+1 lookup per row).
func toTagDTOWithCount(t model.Tag, counts map[uint]int64) tagDTO {
	dto := toTagDTO(t)
	if counts != nil {
		dto.CourseCount = counts[t.ID]
	}
	return dto
}

// tagIDsOf extracts the IDs from a course's loaded Tags relation.
func tagIDsOf(tags []model.Tag) []uint {
	if len(tags) == 0 {
		return []uint{}
	}
	out := make([]uint, len(tags))
	for i, t := range tags {
		out[i] = t.ID
	}
	return out
}

type subtitleDTO struct {
	ID        uint   `json:"id"`
	EpisodeID uint   `json:"episode_id"`
	Language  string `json:"language"`
	Label     string `json:"label"`
	CreatedAt string `json:"created_at"`
}

func toSubtitleDTO(s model.Subtitle) subtitleDTO {
	return subtitleDTO{
		ID:        s.ID,
		EpisodeID: s.EpisodeID,
		Language:  s.Language,
		Label:     s.Label,
		CreatedAt: formatTime(s.CreatedAt),
	}
}

type ledgerDTO struct {
	ID           uint   `json:"id"`
	UserID       uint   `json:"user_id"`
	ChangeAmount int    `json:"change_amount"`
	ReasonType   string `json:"reason_type"`
	Description  string `json:"description"`
	CreatedAt    string `json:"created_at"`
}

func toLedgerDTO(l model.PointsLedger) ledgerDTO {
	return ledgerDTO{
		ID:           l.ID,
		UserID:       l.UserID,
		ChangeAmount: l.ChangeAmount,
		ReasonType:   l.ReasonType,
		Description:  l.Description,
		CreatedAt:    formatTime(l.CreatedAt),
	}
}

type dashboardStatsDTO struct {
	UserCount            int64                       `json:"user_count"`
	CourseCount          int64                       `json:"course_count"`
	EpisodeCount         int64                       `json:"episode_count"`
	TotalDurationSeconds int64                       `json:"total_duration_seconds"`
	PendingProbeCount    int64                       `json:"pending_probe_count"`
	SubjectDistribution  []subjectCountDTO           `json:"subject_distribution"`
	RecentDailyEpisodes  []repositoryDailyCountAlias `json:"recent_daily_episodes"`

	// Learning-activity aggregates (new).
	TotalWatchSeconds    int64                       `json:"total_watch_seconds"`    // platform-wide accumulated learning time
	CompletedEpisodes    int64                       `json:"completed_episodes"`     // total completed progress rows
	ActiveUsersToday     int64                       `json:"active_users_today"`     // distinct users active since 00:00 today
	UnlockedBadgeCount   int64                       `json:"unlocked_badge_count"`   // total badge unlocks across all users
	RecentDailyWatch     []repositoryDailyWatchAlias `json:"recent_daily_watch"`     // last-7-days learning-time trend
	TopUsers             []dashboardLeaderRow        `json:"top_users"`              // most active users by watch_seconds
	TopCourses           []dashboardLeaderRow        `json:"top_courses"`            // popular courses by completions
}

type subjectCountDTO struct {
	Subject string `json:"subject"`
	Count   int    `json:"count"`
}

// dashboardLeaderRow is a generic {id, label, value} leaderboard entry,
// reused for both top-users (id=user_id, value=watch_seconds) and top-courses
// (id=course_id, value=completed_episodes). Label carries the friendly name
// (nickname / course title) resolved in the handler.
type dashboardLeaderRow struct {
	ID    uint   `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

// repositoryDailyCountAlias lets us emit the repository.DailyCount type
// without importing repository directly into the DTO file's surface (the JSON
// shape is already {date,count} via that type's tags).
type repositoryDailyCountAlias = struct {
	Date  string `json:"date"`
	Count int    `json:"count"`
}

// repositoryDailyWatchAlias mirrors repository.DailyWatch ({date,seconds}) for
// the dashboard learning-trend chart.
type repositoryDailyWatchAlias = struct {
	Date    string `json:"date"`
	Seconds int64  `json:"seconds"`
}

// formatTime normalizes a time.Time into an RFC3339 string. Empty times become
// "" so the frontend's Date parsing gets a predictable falsy value.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
