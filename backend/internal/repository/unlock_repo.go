package repository

import (
	"encoding/json"
	"errors"
	"studyquest/backend/internal/model"
	"time"

	"gorm.io/gorm"
)

// allowedEpisodeRepo helpers operate on the user_unlock_allowed_episodes join
// table, which replaced the old AllowedEpisodeIDsJSON blob on UserUnlockOverride.
// FK CASCADE on all three axes (user, course, episode) ensures no stale ids.

// EffectiveUnlock is the resolved unlock configuration for a (user, course):
// the strategy that actually applies (override wins over template, falling
// back to AllOpen), its parameters, the admin-curated allowlist, the manual
// unlock counter, and the GrantedAt anchor used for time-based strategies.
type EffectiveUnlock struct {
	Strategy          string
	IntervalSeconds   int
	WeeklyTimes       []model.WeeklyTime
	AllowedEpisodeIDs []uint
	ManualUnlockCount int
	GrantedAt         time.Time // zero if no access row exists
}

// UnlockRepository handles persistence for course unlock templates and
// per-(user, course) overrides, plus resolution of the effective strategy.
type UnlockRepository interface {
	// Template CRUD (course-level default strategy).
	GetTemplate(courseID uint) (*model.CourseUnlockTemplate, error)
	UpsertTemplate(t *model.CourseUnlockTemplate) error
	DeleteTemplate(courseID uint) error

	// Override CRUD (per user, course).
	GetOverride(userID, courseID uint) (*model.UserUnlockOverride, error)
	UpsertOverride(o *model.UserUnlockOverride) error
	DeleteOverride(userID, courseID uint) error
	ListOverridesByUser(userID uint) ([]model.UserUnlockOverride, error)

	// IncrementManualUnlock atomically bumps manual_unlock_count for a
	// (user, course) override, creating the row (inheriting AllOpen) if
	// missing. Uses a single-column increment so concurrent taps can't lose
	// counts (mirrors the atomic-increment invariant used for watch_seconds).
	IncrementManualUnlock(userID, courseID uint) error
	// DecrementManualUnlock atomically reduces manual_unlock_count by 1,
	// floored at 0 (the count never goes negative). No-op if the override row
	// doesn't exist. The floor is enforced in the UPDATE so a race between two
	// concurrent decrements can't push the count below zero.
	DecrementManualUnlock(userID, courseID uint) error

	// SetAllowedEpisodes replaces the allowlist on the override row (stored in
	// the user_unlock_allowed_episodes join table), creating the override
	// (inheriting AllOpen) if missing.
	SetAllowedEpisodes(userID, courseID uint, ids []uint) error
	// GetAllowedEpisodes returns the (user, course) allowlist from the join table.
	GetAllowedEpisodes(userID, courseID uint) ([]uint, error)

	// ResolveEffective returns the effective unlock config for a (user,
	// course): override wins over template, defaulting to AllOpen when
	// neither exists. GrantedAt comes from user_course_access (zero value
	// if the user has no access — callers should have already gated on
	// access before reaching here).
	ResolveEffective(userID, courseID uint) (EffectiveUnlock, error)
}

type unlockRepo struct {
	db *gorm.DB
}

// NewUnlockRepository creates an instance of UnlockRepository.
func NewUnlockRepository(db *gorm.DB) UnlockRepository {
	return &unlockRepo{db: db}
}

func (r *unlockRepo) GetTemplate(courseID uint) (*model.CourseUnlockTemplate, error) {
	var t model.CourseUnlockTemplate
	err := r.db.First(&t, "course_id = ?", courseID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

func (r *unlockRepo) UpsertTemplate(t *model.CourseUnlockTemplate) error {
	return r.db.Save(t).Error
}

func (r *unlockRepo) DeleteTemplate(courseID uint) error {
	return r.db.Delete(&model.CourseUnlockTemplate{}, "course_id = ?", courseID).Error
}

func (r *unlockRepo) GetOverride(userID, courseID uint) (*model.UserUnlockOverride, error) {
	var o model.UserUnlockOverride
	err := r.db.First(&o, "user_id = ? AND course_id = ?", userID, courseID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &o, nil
}

func (r *unlockRepo) UpsertOverride(o *model.UserUnlockOverride) error {
	return r.db.Save(o).Error
}

func (r *unlockRepo) DeleteOverride(userID, courseID uint) error {
	return r.db.Delete(&model.UserUnlockOverride{}, "user_id = ? AND course_id = ?", userID, courseID).Error
}

func (r *unlockRepo) ListOverridesByUser(userID uint) ([]model.UserUnlockOverride, error) {
	var list []model.UserUnlockOverride
	err := r.db.Where("user_id = ?", userID).Find(&list).Error
	return list, err
}

// IncrementManualUnlock bumps manual_unlock_count via a single-column UPDATE
// (INSERT ... ON CONFLICT to create the row first if missing). This mirrors
// the atomic-increment invariant used for watch_seconds: two concurrent taps
// must not lose a count to a read-modify-write race.
//
// When creating the override row for the first time, it inherits the
// currently-effective strategy (override → template → all_open) so that a
// manual bump doesn't accidentally clobber a manual/weekly template with
// all_open. On conflict (row already exists) only the count moves.
func (r *unlockRepo) IncrementManualUnlock(userID, courseID uint) error {
	// Determine the strategy to seed a brand-new row with. If a row already
	// exists, the ON CONFLICT branch ignores these values, so this only
	// matters for the insert path.
	eff, _ := r.ResolveEffective(userID, courseID)
	seedStrategy := eff.Strategy
	if seedStrategy == "" {
		seedStrategy = model.StrategyAllOpen
	}
	seedInterval := eff.IntervalSeconds
	seedWeekly := ""
	if len(eff.WeeklyTimes) > 0 {
		if buf, err := json.Marshal(eff.WeeklyTimes); err == nil {
			seedWeekly = string(buf)
		}
	}
	return r.db.Exec(`
		INSERT INTO user_unlock_overrides (user_id, course_id, strategy, interval_seconds, weekly_times_json, manual_unlock_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		ON CONFLICT(user_id, course_id) DO UPDATE SET
			manual_unlock_count = user_unlock_overrides.manual_unlock_count + 1,
			updated_at = excluded.updated_at
	`, userID, courseID, seedStrategy, seedInterval, seedWeekly, time.Now().UTC(), time.Now().UTC()).Error
}

// DecrementManualUnlock reduces manual_unlock_count by 1, floored at 0. Unlike
// Increment, it does NOT create a row if none exists (there's nothing to undo).
// The MAX(...,0) in the UPDATE makes the floor race-safe: two concurrent
// decrements of a count at 1 both resolve to 0 rather than one going to -1.
func (r *unlockRepo) DecrementManualUnlock(userID, courseID uint) error {
	return r.db.Exec(`
		UPDATE user_unlock_overrides
		SET manual_unlock_count = MAX(manual_unlock_count - 1, 0),
		    updated_at = ?
		WHERE user_id = ? AND course_id = ?
	`, time.Now().UTC(), userID, courseID).Error
}

func (r *unlockRepo) SetAllowedEpisodes(userID, courseID uint, ids []uint) error {
	// Ensure the override row exists (inheriting the effective strategy so
	// setting an allowlist doesn't silently switch a manual/weekly course to
	// all_open). Idempotent via the same inheritance mechanism as
	// IncrementManualUnlock.
	eff, _ := r.ResolveEffective(userID, courseID)
	seedStrategy := eff.Strategy
	if seedStrategy == "" {
		seedStrategy = model.StrategyAllOpen
	}
	seedInterval := eff.IntervalSeconds
	seedWeekly := ""
	if len(eff.WeeklyTimes) > 0 {
		if wb, err := json.Marshal(eff.WeeklyTimes); err == nil {
			seedWeekly = string(wb)
		}
	}
	// Create the override row if missing (ON CONFLICT keeps existing values).
	if err := r.db.Exec(`
		INSERT INTO user_unlock_overrides (user_id, course_id, strategy, interval_seconds, weekly_times_json, manual_unlock_count, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)
		ON CONFLICT(user_id, course_id) DO NOTHING
	`, userID, courseID, seedStrategy, seedInterval, seedWeekly, time.Now().UTC(), time.Now().UTC()).Error; err != nil {
		return err
	}
	// Replace the allowlist in the join table atomically.
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("user_id = ? AND course_id = ?", userID, courseID).
			Delete(&model.UserUnlockAllowedEpisode{}).Error; err != nil {
			return err
		}
		seen := make(map[uint]bool, len(ids))
		for _, eid := range ids {
			if seen[eid] {
				continue
			}
			seen[eid] = true
			if err := tx.Create(&model.UserUnlockAllowedEpisode{
				UserID: userID, CourseID: courseID, EpisodeID: eid,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetAllowedEpisodes returns the (user, course) allowlist from the join table.
func (r *unlockRepo) GetAllowedEpisodes(userID, courseID uint) ([]uint, error) {
	var rows []model.UserUnlockAllowedEpisode
	if err := r.db.Where("user_id = ? AND course_id = ?", userID, courseID).
		Order("episode_id asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]uint, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.EpisodeID)
	}
	return out, nil
}

// ResolveEffective returns the effective unlock config for a (user, course):
// override wins over template, defaulting to AllOpen when neither exists.
// GrantedAt comes from user_course_access (zero value if the user has no
// access — callers should have already gated on access before reaching here).
//
// NOT wrapped in a transaction: the three reads (override / template / access)
// are independent lookups and the resolution is read-only, so a torn read
// across them is benign for a single observer. The callers that write
// (IncrementManualUnlock / SetAllowedEpisodes) invoke this to seed a new row's
// strategy — an admin-initiated, low-frequency path where the inherent
// read-then-write window is acceptable. If unlock ever moves to high-frequency
// automated writes, wrap this + the write in a tx.
func (r *unlockRepo) ResolveEffective(userID, courseID uint) (EffectiveUnlock, error) {
	var eff EffectiveUnlock
	eff.Strategy = model.StrategyAllOpen
	eff.AllowedEpisodeIDs = []uint{}
	eff.WeeklyTimes = []model.WeeklyTime{}

	// 1. Load the override (per user-course) if present. It carries the
	//    allowlist + manual count, and (when set) the strategy/params.
	var override *model.UserUnlockOverride
	if o, err := r.GetOverride(userID, courseID); err != nil {
		return eff, err
	} else if o != nil {
		override = o
	}

	// 2. Load the template (course default) if present.
	var template *model.CourseUnlockTemplate
	if t, err := r.GetTemplate(courseID); err != nil {
		return eff, err
	} else if t != nil {
		template = t
	}

	// 3. Decide effective strategy + params. Override wins; absent override
	//    inherits the template; absent both stays AllOpen (the default).
	switch {
	case override != nil:
		eff.Strategy = override.Strategy
		if eff.Strategy == "" {
			eff.Strategy = model.StrategyAllOpen
		}
		eff.IntervalSeconds = override.IntervalSeconds
		eff.ManualUnlockCount = override.ManualUnlockCount
		// Weekly points: prefer the override's own; fall back to the template's
		// so an admin can change the global cadence without editing each user.
		weeklyJSON := override.WeeklyTimesJSON
		if weeklyJSON == "" && template != nil {
			weeklyJSON = template.WeeklyTimesJSON
		}
		if weeklyJSON != "" {
			var wts []model.WeeklyTime
			if err := json.Unmarshal([]byte(weeklyJSON), &wts); err == nil {
				eff.WeeklyTimes = wts
			}
		}
		if allowedIDs, err := r.GetAllowedEpisodes(userID, courseID); err == nil {
			eff.AllowedEpisodeIDs = allowedIDs
		}
	case template != nil:
		// No per-user override: inherit the template wholesale.
		eff.Strategy = template.Strategy
		if eff.Strategy == "" {
			eff.Strategy = model.StrategyAllOpen
		}
		eff.IntervalSeconds = template.IntervalSeconds
		if template.WeeklyTimesJSON != "" {
			var wts []model.WeeklyTime
			if err := json.Unmarshal([]byte(template.WeeklyTimesJSON), &wts); err == nil {
				eff.WeeklyTimes = wts
			}
		}
	}

	// 4. GrantedAt anchor from the access row.
	var access model.UserCourseAccess
	if err := r.db.First(&access, "user_id = ? AND course_id = ?", userID, courseID).Error; err == nil {
		eff.GrantedAt = access.GrantedAt
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return eff, err
	}

	return eff, nil
}
