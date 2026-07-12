package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"studyquest/backend/internal/appclock"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"time"
)

// UnlockService manages course unlock templates, per-(user, course)
// overrides, and the resolution of which episodes a student may see. The
// "water level + allowlist" model is documented on the model types.
type UnlockService interface {
	// Template CRUD (admin).
	GetTemplate(courseID uint) (*model.CourseUnlockTemplate, error)
	SaveTemplate(courseID uint, strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime) (*model.CourseUnlockTemplate, error)
	DeleteTemplate(courseID uint) error

	// Override CRUD (admin). SaveOverride fully replaces the override config.
	GetOverride(userID, courseID uint) (*model.UserUnlockOverride, error)
	SaveOverride(userID, courseID uint, strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime, allowedIDs []uint) (*model.UserUnlockOverride, error)
	DeleteOverride(userID, courseID uint) error
	ListOverridesByUser(userID uint) ([]model.UserUnlockOverride, error)
	// GetAllowedEpisodes returns the admin-curated allowlist for a (user, course)
	// from the join table.
	GetAllowedEpisodes(userID, courseID uint) ([]uint, error)

	// IncrementManualUnlock bumps the water level by 1 for a (user, course).
	// No-op effect under StrategySelected (water level is always 0 there);
	// callers/UI should hide the button under Selected.
	IncrementManualUnlock(userID, courseID uint) error
	// DecrementManualUnlock reverses an accidental +1 (admin tapped too many
	// times). Floors the manual count at 0; never affects the automatic water
	// level from interval/weekly. No-op under Selected / when the count is
	// already 0.
	DecrementManualUnlock(userID, courseID uint) error

	// SetAllowedEpisodes replaces the allowlist (the sole visibility source
	// under StrategySelected, additive elsewhere).
	SetAllowedEpisodes(userID, courseID uint, ids []uint) error

	// ResolveVisibleEpisodes returns the ordered list of episode ids the user
	// may currently see, along with totals and a human label for display.
	ResolveVisibleEpisodes(userID, courseID uint) (vis UnlockVisibility, err error)

	// IsEpisodeVisible is the gate used by stream/play-info handlers to block
	// access to locked episodes (defense against URL guessing).
	IsEpisodeVisible(userID, episodeID uint) (bool, error)
}

// UnlockVisibility is the resolved view of one (user, course).
type UnlockVisibility struct {
	VisibleIDs   []uint // episode ids the user may see, in SortOrder
	Total        int    // total episodes in the course
	UnlockedN    int    // water level N (0 under Selected)
	Strategy     string // effective strategy
	StrategyLabel string // human-readable label for the client
	NextUnlockAt *time.Time // next automatic unlock instant (interval/weekly), business tz; nil if none
}

type unlockService struct {
	unlockRepo  repository.UnlockRepository
	episodeRepo repository.EpisodeRepository
}

// NewUnlockService creates an instance of UnlockService.
func NewUnlockService(ur repository.UnlockRepository, er repository.EpisodeRepository) UnlockService {
	return &unlockService{unlockRepo: ur, episodeRepo: er}
}

func (s *unlockService) GetTemplate(courseID uint) (*model.CourseUnlockTemplate, error) {
	return s.unlockRepo.GetTemplate(courseID)
}

func (s *unlockService) SaveTemplate(courseID uint, strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime) (*model.CourseUnlockTemplate, error) {
	if err := validateStrategy(strategy, intervalSeconds, weeklyTimes); err != nil {
		return nil, err
	}
	wj, err := marshalWeekly(weeklyTimes)
	if err != nil {
		return nil, err
	}
	t := &model.CourseUnlockTemplate{
		CourseID:        courseID,
		Strategy:        strategy,
		IntervalSeconds: intervalSeconds,
		WeeklyTimesJSON: wj,
	}
	if err := s.unlockRepo.UpsertTemplate(t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *unlockService) DeleteTemplate(courseID uint) error {
	return s.unlockRepo.DeleteTemplate(courseID)
}

func (s *unlockService) GetOverride(userID, courseID uint) (*model.UserUnlockOverride, error) {
	return s.unlockRepo.GetOverride(userID, courseID)
}

func (s *unlockService) SaveOverride(userID, courseID uint, strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime, allowedIDs []uint) (*model.UserUnlockOverride, error) {
	if err := validateStrategy(strategy, intervalSeconds, weeklyTimes); err != nil {
		return nil, err
	}
	wj, err := marshalWeekly(weeklyTimes)
	if err != nil {
		return nil, err
	}
	o := &model.UserUnlockOverride{
		UserID:          userID,
		CourseID:        courseID,
		Strategy:        strategy,
		IntervalSeconds: intervalSeconds,
		WeeklyTimesJSON: wj,
	}
	// Preserve the existing manual_unlock_count when overwriting config (don't
	// reset admin's accumulated manual bumps just because they tweaked the
	// strategy params).
	if existing, err := s.unlockRepo.GetOverride(userID, courseID); err == nil && existing != nil {
		o.ManualUnlockCount = existing.ManualUnlockCount
	}
	if err := s.unlockRepo.UpsertOverride(o); err != nil {
		return nil, err
	}
	// Store the allowlist in the join table (replaces the old JSON blob).
	if err := s.unlockRepo.SetAllowedEpisodes(userID, courseID, allowedIDs); err != nil {
		return nil, err
	}
	return o, nil
}

func (s *unlockService) DeleteOverride(userID, courseID uint) error {
	return s.unlockRepo.DeleteOverride(userID, courseID)
}

func (s *unlockService) ListOverridesByUser(userID uint) ([]model.UserUnlockOverride, error) {
	return s.unlockRepo.ListOverridesByUser(userID)
}

func (s *unlockService) GetAllowedEpisodes(userID, courseID uint) ([]uint, error) {
	return s.unlockRepo.GetAllowedEpisodes(userID, courseID)
}

func (s *unlockService) IncrementManualUnlock(userID, courseID uint) error {
	return s.unlockRepo.IncrementManualUnlock(userID, courseID)
}

func (s *unlockService) DecrementManualUnlock(userID, courseID uint) error {
	return s.unlockRepo.DecrementManualUnlock(userID, courseID)
}

func (s *unlockService) SetAllowedEpisodes(userID, courseID uint, ids []uint) error {
	return s.unlockRepo.SetAllowedEpisodes(userID, courseID, ids)
}

func (s *unlockService) ResolveVisibleEpisodes(userID, courseID uint) (UnlockVisibility, error) {
	eps, err := s.episodeRepo.ListByCourse(courseID)
	if err != nil {
		return UnlockVisibility{}, err
	}
	// SortOrder is the unlock rank. Sort defensively in case the repo ordering
	// changes; the (sort_order, id) tiebreak matches idx_course_sort.
	sort.SliceStable(eps, func(i, j int) bool {
		if eps[i].SortOrder != eps[j].SortOrder {
			return eps[i].SortOrder < eps[j].SortOrder
		}
		return eps[i].ID < eps[j].ID
	})
	total := len(eps)
	existingIDs := make(map[uint]int, total) // id → rank index
	orderedIDs := make([]uint, total)
	for i, ep := range eps {
		existingIDs[ep.ID] = i
		orderedIDs[i] = ep.ID
	}

	eff, err := s.unlockRepo.ResolveEffective(userID, courseID)
	if err != nil {
		return UnlockVisibility{}, err
	}
	now := appclock.Now()
	granted := eff.GrantedAt
	if granted.IsZero() {
		// No access row: nothing visible (caller should have gated already).
		return UnlockVisibility{
			VisibleIDs:    []uint{},
			Total:         total,
			UnlockedN:     0,
			Strategy:      eff.Strategy,
			StrategyLabel: strategyLabel(eff.Strategy),
		}, nil
	}

	n := computeUnlockedCount(eff.Strategy, eff.IntervalSeconds, eff.WeeklyTimes, granted, eff.ManualUnlockCount, now, total)

	// Union: first N by rank ∪ allowlist (∩ existing ids).
	seen := make(map[uint]struct{})
	visibleSet := make([]uint, 0)
	add := func(id uint) {
		if _, ok := existingIDs[id]; !ok {
			return // id no longer exists → auto-pruned
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		visibleSet = append(visibleSet, id)
	}
	limit := n
	if limit > total {
		limit = total
	}
	for i := 0; i < limit; i++ {
		add(orderedIDs[i])
	}
	for _, id := range eff.AllowedEpisodeIDs {
		add(id)
	}

	// Re-stable-sort the visible set into SortOrder for display.
	sort.SliceStable(visibleSet, func(i, j int) bool {
		return existingIDs[visibleSet[i]] < existingIDs[visibleSet[j]]
	})

	vis := UnlockVisibility{
		VisibleIDs:    visibleSet,
		Total:         total,
		UnlockedN:     n,
		Strategy:      eff.Strategy,
		StrategyLabel: strategyLabel(eff.Strategy),
	}
	vis.NextUnlockAt = nextUnlockAt(eff.Strategy, eff.IntervalSeconds, eff.WeeklyTimes, granted, now, total, n)
	return vis, nil
}

func (s *unlockService) IsEpisodeVisible(userID, episodeID uint) (bool, error) {
	ep, err := s.episodeRepo.FindByID(episodeID)
	if err != nil {
		return false, err
	}
	if ep == nil {
		return false, nil
	}
	vis, err := s.ResolveVisibleEpisodes(userID, ep.CourseID)
	if err != nil {
		return false, err
	}
	for _, id := range vis.VisibleIDs {
		if id == episodeID {
			return true, nil
		}
	}
	return false, nil
}

// ---- pure helpers ----

func validateStrategy(strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime) error {
	switch strategy {
	case model.StrategyAllOpen, model.StrategyManual, model.StrategyInterval, model.StrategyWeekly, model.StrategySelected:
	default:
		return fmt.Errorf("invalid unlock strategy: %q", strategy)
	}
	if strategy == model.StrategyInterval && intervalSeconds <= 0 {
		return errors.New("interval strategy requires interval_seconds > 0")
	}
	if strategy == model.StrategyWeekly && len(weeklyTimes) == 0 {
		return errors.New("weekly strategy requires at least one weekly time point")
	}
	for _, w := range weeklyTimes {
		if w.Weekday < 0 || w.Weekday > 6 || w.Hour < 0 || w.Hour > 23 || w.Minute < 0 || w.Minute > 59 {
			return fmt.Errorf("invalid weekly time: %+v", w)
		}
	}
	return nil
}

func marshalWeekly(wt []model.WeeklyTime) (string, error) {
	if len(wt) == 0 {
		return "", nil
	}
	buf, err := json.Marshal(wt)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

func strategyLabel(strategy string) string {
	switch strategy {
	case model.StrategyAllOpen:
		return "全部开放"
	case model.StrategyManual:
		return "手动解锁"
	case model.StrategyInterval:
		return "固定间隔解锁"
	case model.StrategyWeekly:
		return "每周定时解锁"
	case model.StrategySelected:
		return "自选课时"
	default:
		return strategy
	}
}

// computeUnlockedCount is the pure water-level function. It returns how many
// of the course's episodes (by SortOrder rank) are unlocked at `now`, given
// the effective strategy. All time math uses the BUSINESS timezone via
// appclock — per the project's appclock invariant, never time.Now() directly.
//
//	all_open  → total
//	manual    → clamp(1 + manualCount)
//	interval  → clamp(1 + floor((now-granted)/interval) + manualCount)
//	weekly    → clamp(1 + elapsedWeeklyPoints(granted, now) + manualCount)
//	selected  → 0  (visibility comes only from the allowlist)
func computeUnlockedCount(strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime, grantedAt time.Time, manualCount int, now time.Time, total int) int {
	if total < 0 {
		total = 0
	}
	// clamp bounds n into [0, total]. When total==0 (empty course) everything
	// collapses to 0 — there are no episodes to unlock, so even manual/weekly
	// base levels must not leak a phantom "1".
	clamp := func(n int) int {
		if n < 0 {
			n = 0
		}
		if n > total {
			n = total
		}
		return n
	}

	switch strategy {
	case model.StrategyAllOpen:
		return total
	case model.StrategyManual:
		return clamp(1 + manualCount)
	case model.StrategyInterval:
		if intervalSeconds <= 0 {
			return clamp(1 + manualCount)
		}
		// Operate in the business timezone so "elapsed" is wall-clock-correct.
		gt := appclock.In(grantedAt)
		nt := appclock.In(now)
		if !nt.After(gt) {
			return clamp(1 + manualCount)
		}
		dur := time.Duration(intervalSeconds) * time.Second
		elapsed := int(nt.Sub(gt) / dur)
		return clamp(1 + elapsed + manualCount)
	case model.StrategyWeekly:
		return clamp(1 + countElapsedWeekly(weeklyTimes, grantedAt, now) + manualCount)
	case model.StrategySelected:
		return 0
	default:
		return total
	}
}

// countElapsedWeekly counts how many configured weekly time points have
// occurred in the half-open interval (grantedAt, now]. Each time point fires
// once per week; we count the distinct weekly occurrences that fall strictly
// after grantedAt and at-or-before now. All comparisons are in the business
// timezone.
func countElapsedWeekly(times []model.WeeklyTime, grantedAt, now time.Time) int {
	if len(times) == 0 {
		return 0
	}
	gt := appclock.In(grantedAt)
	nt := appclock.In(now)
	if !nt.After(gt) {
		return 0
	}

	// For each configured point, walk week-by-week from the granted week and
	// count occurrences in (gt, nt]. We start from the week containing gt and
	// advance by 7 days until we pass nt. This is O(points * weeks_elapsed);
	// weeks_elapsed is small (a drip course runs over weeks/months).
	count := 0
	// Anchor to the Sunday 00:00 of the granted week (business tz).
	weekStart := time.Date(gt.Year(), gt.Month(), gt.Day(), 0, 0, 0, 0, gt.Location())
	weekStart = weekStart.AddDate(0, 0, -(int(gt.Weekday())))
	// Guard: never run more than ~10 years of weeks to bound worst case.
	maxIter := 52 * 10
	for iter := 0; iter < maxIter && !weekStart.After(nt); iter++ {
		for _, w := range times {
			t := weekStart.AddDate(0, 0, w.Weekday).Add(time.Duration(w.Hour)*time.Hour + time.Duration(w.Minute)*time.Minute)
			if t.After(gt) && !t.After(nt) {
				count++
			}
		}
		weekStart = weekStart.AddDate(0, 0, 7)
	}
	return count
}

// nextUnlockAt computes the next instant (business tz) at which the water
// level will automatically rise by one, for interval/weekly strategies.
// Returns nil when there's no scheduled next unlock (all_open/manual/selected,
// or the course is already fully unlocked).
func nextUnlockAt(strategy string, intervalSeconds int, weeklyTimes []model.WeeklyTime, grantedAt, now time.Time, total, currentN int) *time.Time {
	_ = currentN
	if total > 0 && currentN >= total {
		return nil
	}
	switch strategy {
	case model.StrategyInterval:
		if intervalSeconds <= 0 || grantedAt.IsZero() {
			return nil
		}
		gt := appclock.In(grantedAt)
		nt := appclock.In(now)
		dur := time.Duration(intervalSeconds) * time.Second
		// First boundary strictly after gt: gt + k*dur for the smallest k such
		// that the result is after nt.
		elapsed := nt.Sub(gt)
		var k int64 = 1
		if elapsed > 0 {
			k = int64(elapsed/dur) + 1
		}
		t := gt.Add(time.Duration(k) * dur)
		return &t
	case model.StrategyWeekly:
		if len(weeklyTimes) == 0 || grantedAt.IsZero() {
			return nil
		}
		nt := appclock.In(now)
		gt := appclock.In(grantedAt)
		// Scan weekly boundaries forward from the granted week until we find
		// the first one strictly after nt.
		weekStart := time.Date(gt.Year(), gt.Month(), gt.Day(), 0, 0, 0, 0, gt.Location())
		weekStart = weekStart.AddDate(0, 0, -(int(gt.Weekday())))
		for iter := 0; iter < 52*10; iter++ {
			for _, w := range weeklyTimes {
				t := weekStart.AddDate(0, 0, w.Weekday).Add(time.Duration(w.Hour)*time.Hour + time.Duration(w.Minute)*time.Minute)
				if t.After(nt) {
					return &t
				}
			}
			weekStart = weekStart.AddDate(0, 0, 7)
		}
		return nil
	default:
		return nil
	}
}
