package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"sync"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"gorm.io/gorm"
)

// BadgeService handles badge CRUD, seeding, and event-based rule evaluation.
type BadgeService interface {
	List() ([]model.Badge, error)
	FindByID(id uint) (*model.Badge, error)
	Create(badge *model.Badge) error
	Update(badge *model.Badge) error
	Delete(id uint) error

	ListUserBadges(userID uint) ([]model.Badge, error)
	EvaluateRules(userID uint) ([]model.Badge, error) // Returns newly unlocked badges
	// UserBadgeStatuses returns every badge with the user's current progress:
	// unlocked tier (0-based, -1 if none), total tier count, raw progress
	// value, and the next tier's threshold (0 if already maxed). Powers the
	// honor-wall UI (tier dots + progress bar).
	UserBadgeStatuses(userID uint) ([]BadgeStatus, error)
	SeedDefaultBadges() error
	// SeedSubjectBadge creates the auto-generated multi-tier subject_count
	// badge for a subject (idempotent). Called when a subject is created.
	SeedSubjectBadge(subjectID uint, key, label string) error
	// RemoveDeprecatedDefaults is retained for interface compat; the multi-tier
	// rebuild handles cleanup now.
	RemoveDeprecatedDefaults() error
}

type badgeService struct {
	db           *gorm.DB
	badgeRepo    repository.BadgeRepository
	progressRepo repository.ProgressRepository
	// userLocks serializes EvaluateRules per user. The player sends a 5s
	// heartbeat that may overlap with a quiz-triggered report; without this,
	// two concurrent EvaluateRules calls can both read "not yet unlocked"
	// and both try to award tier-up points. The atomic ON CONFLICT in
	// UnlockBadgeTier catches most of it, but SQLite's rows_affected for a
	// WHERE-rejected conflict is driver-dependent, so we mutex at the service
	// layer to make it deterministic. One child's heartbeat is not a hot path.
	userLocks sync.Map // map[uint]*sync.Mutex
}

// lockUser returns (and lazily creates) the mutex for one user, then locks it.
// The caller must unlock it.
func (s *badgeService) lockUser(userID uint) *sync.Mutex {
	v, _ := s.userLocks.LoadOrStore(userID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// NewBadgeService creates an instance of BadgeService.
func NewBadgeService(db *gorm.DB, br repository.BadgeRepository, pr repository.ProgressRepository) BadgeService {
	return &badgeService{
		db:           db,
		badgeRepo:    br,
		progressRepo: pr,
	}
}

func (s *badgeService) List() ([]model.Badge, error) {
	return s.badgeRepo.List()
}

func (s *badgeService) FindByID(id uint) (*model.Badge, error) {
	return s.badgeRepo.FindByID(id)
}

func (s *badgeService) Create(badge *model.Badge) error {
	return s.badgeRepo.Create(badge)
}

func (s *badgeService) Update(badge *model.Badge) error {
	return s.badgeRepo.Update(badge)
}

// Delete refuses system-seeded badges (IsSystem=true) so the curated default
// set survives; user-created badges are freely deletable.
func (s *badgeService) Delete(id uint) error {
	b, err := s.badgeRepo.FindByID(id)
	if err != nil {
		return err
	}
	if b == nil {
		return nil
	}
	if b.IsSystem {
		return ErrSystemProtected
	}
	return s.badgeRepo.Delete(id)
}

func (s *badgeService) ListUserBadges(userID uint) ([]model.Badge, error) {
	return s.badgeRepo.ListUserBadges(userID)
}

// EvaluateRules walks every badge, evaluates its rule(s) against the user's
// current stats, and unlocks/advances any newly-satisfied badge.
//
// Three badge shapes:
//  1. Multi-tier (Tiers non-empty): compute the user's raw progress, find the
//     highest tier whose threshold the progress meets, and advance the user to
//     that tier if they haven't reached it yet — awarding that tier's reward
//     points. A user keeps progressing up the tiers over time, so this NEVER
//     skips a badge once it has a tier; it checks for tier ups instead.
//  2. Composite (RuleJSON non-empty): AND/OR tree of leaf rules — single
//     unlock, no tiers.
//  3. Single-tier legacy (RuleType + Threshold): unlock once when met.
//
// Each unlock/tier-up + its points ledger entry is written in one transaction
// so a crash between them can't leave a partial state.
func (s *badgeService) EvaluateRules(userID uint) ([]model.Badge, error) {
	// Serialize per-user so two overlapping heartbeats can't both award the
	// same tier-up. See userLocks field doc.
	mu := s.lockUser(userID)
	mu.Lock()
	defer mu.Unlock()

	badges, err := s.badgeRepo.List()
	if err != nil {
		return nil, err
	}

	var newlyUnlocked []model.Badge

	for _, badge := range badges {
		if badge.Tiers != "" {
			// Multi-tier progression.
			_, tierIdx, tiers, ok := s.evalMultiTier(userID, &badge)
			if !ok {
				continue
			}
			if tierIdx < 0 {
				continue // hasn't cleared even tier 0 yet
			}
			existing, err := s.badgeRepo.FindUserBadge(userID, badge.ID)
			if err != nil {
				continue
			}
			currentTier := -1
			if existing != nil {
				currentTier = existing.Tier
			}
			if tierIdx <= currentTier {
				continue // already at or past this tier
			}
			reward := tiers[tierIdx].R
			desc := fmt.Sprintf("解锁荣誉「%s」第 %d/%d 层 (%s)", badge.Title, tierIdx+1, len(tiers), badge.Description)
			ledger := &model.PointsLedger{
				UserID:       userID,
				ChangeAmount: reward,
				ReasonType:   "badge_unlocked",
				Description:  desc,
			}
			err = s.db.Transaction(func(tx *gorm.DB) error {
				br := s.badgeRepo.WithTx(tx)
				if _, err := br.UnlockBadgeTier(userID, badge.ID, tierIdx); err != nil {
					return err
				}
				return s.progressRepo.WithTx(tx).AddPoints(ledger)
			})
			if err == nil {
				newlyUnlocked = append(newlyUnlocked, badge)
			}
			continue
		}

		// Single-tier path (composite or legacy). Skip if already unlocked.
		unlocked, err := s.badgeRepo.HasUnlocked(userID, badge.ID)
		if err != nil || unlocked {
			continue
		}
		shouldUnlock := false
		if badge.RuleJSON != "" {
			shouldUnlock = s.evalComposite(userID, badge.RuleJSON)
		} else {
			shouldUnlock = s.evalLeaf(userID, badge.RuleType, badge.RuleTarget, badge.Threshold)
		}
		if shouldUnlock {
			ledger := &model.PointsLedger{
				UserID:       userID,
				ChangeAmount: 0, // single-tier unlock awards no points
				ReasonType:   "badge_unlocked",
				Description:  fmt.Sprintf("解锁荣誉徽章：%s (%s)", badge.Title, badge.Description),
			}
			err := s.db.Transaction(func(tx *gorm.DB) error {
				if err := s.badgeRepo.WithTx(tx).UnlockBadge(userID, badge.ID); err != nil {
					return err
				}
				return s.progressRepo.WithTx(tx).AddPoints(ledger)
			})
			if err == nil {
				newlyUnlocked = append(newlyUnlocked, badge)
			}
		}
	}

	return newlyUnlocked, nil
}

// evalMultiTier computes the user's raw progress for a multi-tier badge and
// returns: the progress value, the highest cleared tier index (0-based, -1 if
// below tier 0), the parsed tiers, and ok=false on parse/query error.
func (s *badgeService) evalMultiTier(userID uint, badge *model.Badge) (progress int, tierIdx int, tiers []model.TierDef, ok bool) {
	var tiersParsed []model.TierDef
	if err := json.Unmarshal([]byte(badge.Tiers), &tiersParsed); err != nil || len(tiersParsed) == 0 {
		return 0, -1, nil, false
	}
	// Defensive: ensure ascending order by threshold. The curated defaults are
	// already sorted, but an admin-entered Tiers JSON might not be — without
	// this, the break-below loop and NextTier logic misbehave on unsorted input.
	sort.Slice(tiersParsed, func(i, j int) bool { return tiersParsed[i].T < tiersParsed[j].T })
	progress, err := s.evalLeafProgress(userID, badge.RuleType, badge.RuleTarget)
	if err != nil {
		return 0, -1, nil, false
	}
	// Find the highest tier whose threshold the progress meets.
	idx := -1
	for i, td := range tiersParsed {
		if progress >= td.T {
			idx = i
		} else {
			break // tiers are ascending (sorted above); stop at first miss
		}
	}
	return progress, idx, tiersParsed, true
}

// evalLeafProgress returns the user's RAW progress value for a rule type
// (minutes / days / count / points), without comparing to any threshold. It's
// the multi-tier companion to evalLeaf: evalLeaf returns a bool (met?), this
// returns the underlying number so callers can pick the right tier. Returns an
// error for composite rule types (those can't be reduced to one number).
func (s *badgeService) evalLeafProgress(userID uint, ruleType, ruleTarget string) (int, error) {
	switch ruleType {
	case "watch_duration":
		return s.badgeRepo.GetTotalWatchDurationMinutes(userID)
	case "consecutive_days":
		return s.badgeRepo.GetConsecutiveActiveDays(userID)
	case "subject_count":
		return s.badgeRepo.GetCompletedEpisodesCountBySubject(userID, ruleTarget)
	case "episode_completed_count":
		return s.badgeRepo.GetCompletedEpisodesCount(userID)
	case "distinct_subject_count":
		return s.badgeRepo.GetDistinctSubjectCompletedCount(userID)
	case "course_completion":
		return s.badgeRepo.GetCompletedCoursesCount(userID)
	case "weekly_all_present":
		return s.badgeRepo.GetActiveDaysInLastWeek(userID)
	case "points_earned":
		pt, err := s.progressRepo.GetPoints(userID)
		if err != nil || pt == nil {
			return 0, err
		}
		return pt.TotalEarnedPoints, nil
	}
	return 0, fmt.Errorf("unsupported rule type for progress: %s", ruleType)
}

// evalLeaf evaluates a single (non-composite) rule against the user's stats.
// Returns false on any aggregate error so a broken query never falsely unlocks.
func (s *badgeService) evalLeaf(userID uint, ruleType, ruleTarget string, threshold int) bool {
	switch ruleType {
	case "watch_duration":
		minutes, err := s.badgeRepo.GetTotalWatchDurationMinutes(userID)
		return err == nil && minutes >= threshold
	case "consecutive_days":
		days, err := s.badgeRepo.GetConsecutiveActiveDays(userID)
		return err == nil && days >= threshold
	case "subject_count":
		count, err := s.badgeRepo.GetCompletedEpisodesCountBySubject(userID, ruleTarget)
		return err == nil && count >= threshold
	case "episode_completed_count":
		count, err := s.badgeRepo.GetCompletedEpisodesCount(userID)
		return err == nil && count >= threshold
	case "distinct_subject_count":
		count, err := s.badgeRepo.GetDistinctSubjectCompletedCount(userID)
		return err == nil && count >= threshold
	case "course_completion":
		count, err := s.badgeRepo.GetCompletedCoursesCount(userID)
		return err == nil && count >= threshold
	case "weekly_all_present":
		days, err := s.badgeRepo.GetActiveDaysInLastWeek(userID)
		return err == nil && days >= threshold
	case "points_earned":
		pt, err := s.progressRepo.GetPoints(userID)
		return err == nil && pt != nil && pt.TotalEarnedPoints >= threshold
	}
	return false
}

// BadgeStatus is one badge paired with the user's current progress on it.
// Designed for the honor-wall UI: Tier dots + a progress bar toward the next
// tier.
type BadgeStatus struct {
	model.Badge
	Unlocked  bool `json:"unlocked"`   // a UserBadge row exists
	Tier      int  `json:"tier"`       // highest cleared tier (0-based); -1 if none
	TierCount int  `json:"tier_count"` // total tiers (1 for single-tier)
	Progress  int  `json:"progress"`   // raw progress value (minutes/days/count/points)
	NextTier  int  `json:"next_tier"`  // next tier threshold; 0 if maxed or single-tier-unlocked
}

// UserBadgeStatuses returns every badge with the user's progress. For
// multi-tier badges it computes the raw progress and resolved tier so the UI
// can render a progress bar and tier dots. For single-tier / composite badges
// only the unlocked flag is meaningful (progress/next_tier are 0).
func (s *badgeService) UserBadgeStatuses(userID uint) ([]BadgeStatus, error) {
	badges, err := s.badgeRepo.List()
	if err != nil {
		return nil, err
	}

	out := make([]BadgeStatus, 0, len(badges))
	for _, b := range badges {
		st := BadgeStatus{Badge: b}
		ub, _ := s.badgeRepo.FindUserBadge(userID, b.ID)
		if ub != nil {
			st.Unlocked = true
			st.Tier = ub.Tier
		} else {
			st.Tier = -1
		}

		if b.Tiers != "" {
				var tiers []model.TierDef
				if err := json.Unmarshal([]byte(b.Tiers), &tiers); err == nil && len(tiers) > 0 {
					sort.Slice(tiers, func(i, j int) bool { return tiers[i].T < tiers[j].T })
					st.TierCount = len(tiers)
					progress, perr := s.evalLeafProgress(userID, b.RuleType, b.RuleTarget)
					if perr == nil {
						st.Progress = progress
					}
					// NextTier = the first tier threshold strictly above the
					// user's CURRENT progress (not their persisted tier). This
					// keeps the progress bar accurate even in the short window
					// between a ReportProgress that crossed a threshold and the
					// EvaluateRules that persists the tier-up.
					next := 0
					for _, td := range tiers {
						if td.T > progress {
							next = td.T
							break
						}
					}
					st.NextTier = next
				}
		} else {
			// Single-tier badge.
			st.TierCount = 1
			if st.Unlocked {
				st.Tier = 0
			} else {
				st.Tier = -1
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// evalComposite parses a CompositeRule JSON tree and evaluates it. A group
// node combines its children with AND (all pass) or OR (any pass); a leaf
// node (no SubRules) delegates to evalLeaf. Malformed JSON or an unknown
// logic defaults to AND-fail (returns false) so a corrupt rule can't unlock.
func (s *badgeService) evalComposite(userID uint, ruleJSON string) bool {
	var root model.CompositeRule
	if err := json.Unmarshal([]byte(ruleJSON), &root); err != nil {
		return false
	}
	return s.evalNode(userID, root)
}

func (s *badgeService) evalNode(userID uint, node model.CompositeRule) bool {
	// Leaf: evaluate directly. A node is a leaf when it has no sub-rules.
	if len(node.SubRules) == 0 {
		return s.evalLeaf(userID, node.Type, node.Target, node.Threshold)
	}
	// Group: combine children.
	switch node.Logic {
	case "or":
		for _, child := range node.SubRules {
			if s.evalNode(userID, child) {
				return true
			}
		}
		return false
	default: // "and" (and any unrecognized logic — fail-closed)
		for _, child := range node.SubRules {
			if !s.evalNode(userID, child) {
				return false
			}
		}
		return true
	}
}

// SeedDefaultBadges populates a curated multi-tier default badge set on first
// run. Subject badges are NOT seeded here — they're generated dynamically by
// SubjectService when a subject is created/seeded (see SeedSubjectBadge).
//
// Multi-tier badges (Tiers JSON) let a user progress through several tiers on
// ONE badge, each with its own threshold and reward points. The curve is tuned
// so the first 1-2 tiers are reachable in days (hook the child's enthusiasm),
// later tiers take weeks/months, and the top tier is a multi-year goal. New
// tiers can be added later by appending to a Tiers array — no migration.
//
// Defaults are marked IsSystem so they survive deletion but stay editable.
func (s *badgeService) SeedDefaultBadges() error {
	// Idempotent + incremental: each default is inserted by code; a code that
	// already exists (unique index collision) is skipped.
	defaults := defaultBadges()
	for i := range defaults {
		if err := s.badgeRepo.Create(&defaults[i]); err != nil {
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				continue
			}
			log.Printf("Failed to seed badge %s: %v", defaults[i].Code, err)
		}
	}
	return nil
}

// defaultBadges returns the curated multi-tier default badge set. Kept as a
// function (not a package var) so the Tiers JSON is built fresh each call via
// tiers().
func defaultBadges() []model.Badge {
	return []model.Badge{
		// 首战告捷 — single-tier, instant first-win to hook enthusiasm.
		{
			Code: "first_blood", Title: "首战告捷", IconName: "badge_first_blood",
			Description: "累计视频学习时长达到 1 分钟",
			RuleType:    "watch_duration", Threshold: 1, IsSystem: true,
		},
		// 连续学习 — 7 tiers: 3/7/14/30/60/100/200 days.
		{
			Code: "streak", Title: "连续学习", IconName: "badge_streak_7",
			Description: "保持每天学习的连胜记录",
			RuleType: "consecutive_days", Tiers: tiers(
				3, 10, 7, 20, 14, 30, 30, 50, 60, 80, 100, 120, 200, 200,
			), IsSystem: true,
		},
		// 累计课时 — 7 tiers: 3/10/30/60/120/300/600 episodes.
		{
			Code: "episode_master", Title: "课时大师", IconName: "badge_first_blood",
			Description: "累计完成的视频课时数",
			RuleType: "episode_completed_count", Tiers: tiers(
				3, 10, 10, 20, 30, 30, 60, 50, 120, 80, 300, 120, 600, 200,
			), IsSystem: true,
		},
		// 累计时长 — 7 tiers: 1/15/60/300/1500/5000/15000 minutes. The 15-min
		// tier fills the early gap so a child gets a tier-up within the first
		// session instead of waiting until 60 accumulated minutes.
		{
			Code: "time_master", Title: "专注时长", IconName: "badge_first_blood",
			Description: "累计视频学习时长（分钟）",
			RuleType: "watch_duration", Tiers: tiers(
				1, 10, 15, 10, 60, 15, 300, 25, 1500, 50, 5000, 100, 15000, 250,
			), IsSystem: true,
		},
		// 积分成就 — 6 tiers: 50/200/500/1000/3000/8000 points.
		{
			Code: "points_hero", Title: "星币成就", IconName: "badge_gold",
			Description: "累计获得的积分里程碑",
			RuleType: "points_earned", Tiers: tiers(
				50, 10, 200, 20, 500, 30, 1000, 50, 3000, 80, 8000, 200,
			), IsSystem: true,
		},
		// 博学多闻 — 4 tiers: 2/3/5/8 distinct subjects.
		{
			Code: "explorer", Title: "博学多闻", IconName: "badge_english",
			Description: "涉猎的不同学科数量",
			RuleType: "distinct_subject_count", Tiers: tiers(
				2, 10, 3, 20, 5, 40, 8, 80,
			), IsSystem: true,
		},
		// 课程通关 — 5 tiers: 1/3/5/10/20 fully-completed courses.
		{
			Code: "course_master", Title: "课程通关", IconName: "badge_gold",
			Description: "完整学完的课程数（学完所有视频）",
			RuleType: "course_completion", Tiers: tiers(
				1, 10, 3, 20, 5, 30, 10, 50, 20, 100,
			), IsSystem: true,
		},
		// 周全勤 — 3 tiers: 3/5/7 active days in the last 7 days.
		{
			Code: "weekly_dedication", Title: "周全勤", IconName: "badge_streak_7",
			Description: "最近 7 天内有学习活动的天数",
			RuleType: "weekly_all_present", Tiers: tiers(
				3, 10, 5, 20, 7, 40,
			), IsSystem: true,
		},
	}
}

// tiers builds the Tiers JSON from a flat threshold/reward/threshold/reward/...
// sequence. Odd-length input panics (programmer error). Kept short so the
// default set above reads as a compact curve.
func tiers(vals ...int) string {
	if len(vals)%2 != 0 {
		panic("tiers: odd number of args (need threshold,reward pairs)")
	}
	td := make([]model.TierDef, 0, len(vals)/2)
	for i := 0; i < len(vals); i += 2 {
		td = append(td, model.TierDef{T: vals[i], R: vals[i+1]})
	}
	b, _ := json.Marshal(td)
	return string(b)
}

// SubjectBadgeCode returns the badge code used for a subject's auto-generated
// subject_count badge. Shared by SubjectService (create/delete/rename) and
// SeedSubjectBadge so they agree on the convention.
func SubjectBadgeCode(subjectKey string) string {
	return "subject_" + subjectKey
}

// SeedSubjectBadge creates (idempotently) the auto-generated multi-tier
// subject_count badge for one subject. Called by SubjectService on subject
// create and by SeedDefaultSubjects for each default subject. The subjectID
// links the badge to its subject via FK (replacing the old string-only coupling).
func (s *badgeService) SeedSubjectBadge(subjectID uint, key, label string) error {
	code := SubjectBadgeCode(key)
	if existing, _ := s.badgeRepo.FindByCode(code); existing != nil {
		return nil // already exists
	}
	return s.badgeRepo.Create(&model.Badge{
		Code: code, Title: label + "达人", IconName: "badge_english",
		Description: "完成的 " + label + " 视频课时数",
		RuleType: model.RuleSubjectCount, RuleTarget: key,
		SubjectID: &subjectID,
		Tiers: tiers(
			1, 5, 5, 15, 20, 30, 50, 60, 150, 120,
		),
		IsSystem: true,
	})
}

// RemoveDeprecatedDefaults is retained for interface compat; the multi-tier
// rebuild now handles cleanup. No-op.
func (s *badgeService) RemoveDeprecatedDefaults() error {
	return nil
}
