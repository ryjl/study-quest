package service

import (
	"errors"
	"log"
	"strings"
	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"

	"gorm.io/gorm"
)

// SubjectService handles Subject CRUD, key-rename cascade, and default seeding.
type SubjectService interface {
	List() ([]model.Subject, error)
	FindByID(id uint) (*model.Subject, error)
	// Create 新建学科。aiConfig 是学科级默认 AI 提示(5 字段),序列化进
	// Subject.AIConfigJSON;课程级对应字段为空时回退到这里。handler 层从
	// *aiConfigRequest 解析得到,这里走 model.AIConfig 类型更安全。
	Create(key, label, color string, sortOrder int, aiConfig model.AIConfig) (*model.Subject, error)
	// Update applies the (already-loaded) subject's new fields. If the Key
	// changed, badges.rule_target is cascaded in the same transaction.
	Update(s *model.Subject, oldKey string) error
	Delete(id uint) error
	SeedDefaultSubjects() error
}

type subjectService struct {
	db           *gorm.DB
	repo         repository.SubjectRepository
	badgeRepo    repository.BadgeRepository
	badgeService BadgeService // auto-generate/clean up subject_count badges
}

// NewSubjectService creates an instance of SubjectService. badgeService is
// needed to auto-generate a multi-tier subject badge when a subject is created
// and clean it up on delete; badgeRepo cascades rule_target on rename.
func NewSubjectService(db *gorm.DB, repo repository.SubjectRepository, br repository.BadgeRepository, bs BadgeService) SubjectService {
	return &subjectService{db: db, repo: repo, badgeRepo: br, badgeService: bs}
}

func (s *subjectService) List() ([]model.Subject, error) {
	return s.repo.List()
}

func (s *subjectService) FindByID(id uint) (*model.Subject, error) {
	return s.repo.FindByID(id)
}

func (s *subjectService) Create(key, label, color string, sortOrder int, aiConfig model.AIConfig) (*model.Subject, error) {
	key = strings.TrimSpace(strings.ToLower(key))
	if key == "" {
		return nil, errors.New("subject key is required")
	}
	if label == "" {
		return nil, errors.New("subject label is required")
	}
	subj := &model.Subject{
		Key:       key,
		Label:     label,
		Color:     color,
		SortOrder: sortOrder,
	}
	// 学科级默认 AI 提示。空 AIConfig → SetAIConfig 写入空串(等同没配)。
	subj.SetAIConfig(aiConfig)
	if err := s.repo.Create(subj); err != nil {
		return nil, err
	}
	// Auto-generate the subject's multi-tier subject_count badge.
	if s.badgeService != nil {
		if err := s.badgeService.SeedSubjectBadge(subj.ID, subj.Key, subj.Label); err != nil {
			log.Printf("Warning: failed to auto-generate badge for subject %s: %v", subj.Key, err)
		}
	}
	return subj, nil
}

// Update saves the subject. If its Key differs from oldKey, every badge rule
// that targeted the old key is rewritten so subject_count rules keep matching,
// and the auto-generated subject badge's code + title are updated to match.
// All writes run in one transaction so a failure partway through can't leave
// the subject renamed but its badge cascade undone (or vice versa).
func (s *subjectService) Update(subj *model.Subject, oldKey string) error {
	newKey := strings.TrimSpace(strings.ToLower(subj.Key))
	if newKey == "" {
		return errors.New("subject key is required")
	}
	subj.Key = newKey

	return s.db.Transaction(func(tx *gorm.DB) error {
		// Save the subject itself first.
		if err := tx.Save(subj).Error; err != nil {
			return err
		}
		if oldKey == "" || newKey == oldKey {
			// Key unchanged — just refresh the auto badge's title/desc if any.
			if subj.Label != "" {
				var b model.Badge
				if err := tx.Where("code = ?", SubjectBadgeCode(newKey)).First(&b).Error; err == nil {
					b.Title = subj.Label + "达人"
					b.Description = "完成的 " + subj.Label + " 视频课时数"
					return tx.Save(&b).Error
				}
			}
			return nil
		}
		// Key changed: cascade rule_target on ALL badges referencing oldKey.
		if err := tx.Model(&model.Badge{}).
			Where("rule_target = ?", oldKey).
			Update("rule_target", newKey).Error; err != nil {
			return err
		}
		// Re-key the auto-generated subject badge (code + title) to the new key.
		var oldBadge model.Badge
		if err := tx.Where("code = ?", SubjectBadgeCode(oldKey)).First(&oldBadge).Error; err == nil {
			oldBadge.Code = SubjectBadgeCode(newKey)
			oldBadge.Title = subj.Label + "达人"
			oldBadge.Description = "完成的 " + subj.Label + " 视频课时数"
			if err := tx.Save(&oldBadge).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete refuses system-seeded subjects first (IsSystem check), then refuses
// when HAND-AUTHORED badge rules still reference the subject's key
// (ErrSubjectHasBadges — rule_target has no FK, so this service-level count is
// the only guard against orphaning those rules). It then falls through to the
// DB-level FK RESTRICT guard (ErrSubjectInUse) when a course still references
// the subject. On success it also removes the subject's auto-generated badge
// (and its user_badges). The auto-badge does NOT trigger ErrSubjectHasBadges
// (it's excluded by code from the rule_target count, and cleaned up here).
func (s *subjectService) Delete(id uint) error {
	subj, err := s.repo.FindByID(id)
	if err != nil {
		return err
	}
	if subj == nil {
		return nil
	}
	if subj.IsSystem {
		return ErrSystemProtected
	}
	// Refuse if any HAND-AUTHORED badge still targets this subject's key.
	// Pass the auto-badge's code as the exclude so the subject's own generated
	// badge (which we delete below) doesn't trip the guard.
	handAuthored, err := s.badgeRepo.CountByRuleTargetExcludingCode(subj.Key, SubjectBadgeCode(subj.Key))
	if err != nil {
		return err
	}
	if handAuthored > 0 {
		return ErrSubjectHasBadges
	}
	err = s.repo.Delete(id)
	if err != nil {
		// SQLite emits "FOREIGN KEY constraint failed"; other drivers phrase it
		// differently, so we match loosely on "foreign key" / "constraint".
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "foreign key") || strings.Contains(msg, "constraint") {
			return ErrSubjectInUse
		}
		return err
	}
	// Clean up the auto-generated subject badge (best-effort).
	if delErr := s.badgeRepo.DeleteByCode(SubjectBadgeCode(subj.Key)); delErr != nil {
		log.Printf("Warning: failed to remove subject badge for %s: %v", subj.Key, delErr)
	}
	return nil
}

// SeedDefaultSubjects populates the canonical subject set on first run and
// incrementally backfills any defaults added later (e.g. the junior-high
// subjects added after launch). Keys MUST stay aligned with badge_service.go's
// subject_count default RuleTarget values ("math", "english") or those badge
// rules silently stop matching. All seeded rows are marked IsSystem so they
// can be renamed/recolored but never deleted.
//
// Idempotent + incremental: each default is inserted by key; a key that
// already exists (unique index collision) is skipped. So an existing install
// picks up newly-added defaults on the next boot without re-seeding the ones
// it already has, and a fresh install gets the full set.
//
// AI seed 回填:对已存在的 math/english 系统学科,如果 AIConfigJSON 为空(老 install
// 在引入学科级 AIConfig 之前就建过这两科),回填 docs/prompt-seed-content.md 起草的
// 5 字段默认值,让 Effective*Hint 在课程级没配时也能拿到合理默认。**不覆盖 admin
// 已经配过的**(AIConfigJSON 非空就不动)。象棋是自定义学科,不在这里 seed(保留前端
// aiHintTemplates.ts 路径)。其余 8 个系统学科只建记录,AIConfigJSON 留空。
func (s *subjectService) SeedDefaultSubjects() error {
	defaults := []model.Subject{
		{Key: "chinese", Label: "语文", Color: "#60a5fa", SortOrder: 1, IsSystem: true},
		{Key: "math", Label: "数学", Color: "#f59e0b", SortOrder: 2, IsSystem: true},
		{Key: "english", Label: "英语", Color: "#34d399", SortOrder: 3, IsSystem: true},
		{Key: "physics", Label: "物理/科学", Color: "#a78bfa", SortOrder: 4, IsSystem: true},
		// 初中分科（对齐全学段学校课程）
		{Key: "chemistry", Label: "化学", Color: "#22d3ee", SortOrder: 5, IsSystem: true},
		{Key: "biology", Label: "生物", Color: "#84cc16", SortOrder: 6, IsSystem: true},
		{Key: "history", Label: "历史", Color: "#d97706", SortOrder: 7, IsSystem: true},
		{Key: "geography", Label: "地理", Color: "#0ea5e9", SortOrder: 8, IsSystem: true},
		{Key: "politics", Label: "道德与法治", Color: "#ef4444", SortOrder: 9, IsSystem: true},
		{Key: "extra", Label: "课外百科", Color: "#f43f5e", SortOrder: 10, IsSystem: true},
		// Entertainment: the implicit subject for fun videos (no learning stats,
		// no badge). Entertainment courses point SubjectID here to satisfy the
		// NOT NULL constraint. SortOrder 99 keeps it at the end of any list.
		{Key: "entertainment", Label: "娱乐", Color: "#8b5cf6", SortOrder: 99, IsSystem: true},
	}

	// subjectAISeed 是 math/english 两科的默认 AIConfig seed。来源:
	// docs/prompt-seed-content.md(主会话起草)。回填到 Subject.AIConfigJSON,让这俩
	// 学科的课程在课程级没配 hint 时也有合理默认。其余系统学科本轮只建空记录。
	//
	// 象棋是自定义学科(不是系统学科),admin 自己建,它的 seed 保留在前端
	// aiHintTemplates.ts,不走后端 Subject.AIConfig 路径——所以这里没有 xiangqi。
	subjectAISeed := map[string]model.AIConfig{
		"math": {
			WhisperHint: "这是一节数学课。常见术语：通分、约分、公分母、异分母、倒数、绝对值、方程、函数、整数、分数、小数、乘除、积、商、倍。",
			SummaryHint: "侧重讲解的解题思路和方法推导过程。如果讲了例题,把例题的解题步骤拆解清楚(第一步做什么、为什么这么做)。公式/定理要写明适用条件和易错前提。",
			QuizHint:    "题型倾向：计算题、事实性知识点（公式、定义、定理）≥50% 出填空（答案必须唯一，如 [\"12\",\"十二\"]）；辨析、应用、证明题出选择或多选。\n难度：中等偏难。干扰项要基于学生真实的错算理（如通分时分子没乘、符号搞反、小数点错位）。",
			AdviceHint:  "侧重计算类知识点的巩固建议。如果学生在计算类知识点弱,建议回到视频重看推导过程 + 做同类计算题巩固;概念类弱点建议结合具体例题理解。",
			TermDict:    "通分 → 勿作\"同分\"\n约分 → 勿作\"月分\"\n公分母 → 勿作\"工分母\"\n倒数 → 勿作\"道数\"\n绝对值 → 勿作\"决对值\"",
		},
		"english": {
			WhisperHint: "这是一节英语课，老师用中文讲解，音频里会夹带英文单词和句子。常见语法术语：时态、语态、从句、分词、动名词、虚拟语气。",
			SummaryHint: "侧重语法规则和用法辨析。如果讲了语法点,把规则、适用场景、和易混语法的区别讲清楚。例句要保留(英中对照)。",
			QuizHint:    "题型倾向：语法辨析、词义选择、完形填空为主（选择）；单词拼写、动词变形用填空。\n难度：中等。干扰项要是真实的语法错误（时态混用、单复数错误、介词搭配错误）。",
			AdviceHint:  "侧重语法应用和词汇积累建议。语法弱点建议结合例句记忆 + 造句练习;词汇弱点建议在语境中记单词而非死背。",
			TermDict:    "英文学科术语按需纠正;中文讲解部分的同音错字按常识纠正。",
		},
	}

	for i := range defaults {
		created := true
		if err := s.repo.Create(&defaults[i]); err != nil {
			// uniqueIndex collision → already present (old install); skip but
			// still make sure its badge exists below.
			if errors.Is(err, gorm.ErrDuplicatedKey) {
				created = false
			} else {
				log.Printf("Failed to seed subject %s: %v", defaults[i].Key, err)
				continue
			}
		}
		// Ensure each default subject has its auto-generated badge (idempotent),
		// EXCEPT entertainment (fun videos carry no badge). Run for both
		// newly-created and already-present subjects so an old install converges.
		if defaults[i].Key != "entertainment" && s.badgeService != nil {
			if err := s.badgeService.SeedSubjectBadge(defaults[i].ID, defaults[i].Key, defaults[i].Label); err != nil {
				log.Printf("Warning: failed to seed badge for subject %s: %v", defaults[i].Key, err)
			}
		}

		// AI seed 回填:对 math/english 系统学科,如果 AIConfigJSON 为空(新建的或老
		// install 残留的),回填 seed 内容。**不覆盖** admin 已配的(AIConfigJSON 非空
		// 就跳过)。新建时 Create 已经把行写进去了,AIConfigJSON 也是空——这里同样要
		// 回填,所以不区分 created。fresh install 的 math/english 也能拿到默认 seed。
		if seed, ok := subjectAISeed[defaults[i].Key]; ok {
			// 重新加载一次:Create 后 defaults[i].AIConfigJSON 已经是空,但 collision
			// 分支下 defaults[i] 是入参副本(不是 DB 里的实际行),需要查 DB 才知道 admin
			// 是否已经配过。Find by key 拿到真实行。
			existing, ferr := s.repo.FindByKey(defaults[i].Key)
			if ferr != nil {
				log.Printf("Warning: failed to reload subject %s for AI seed: %v", defaults[i].Key, ferr)
			} else if existing != nil && strings.TrimSpace(existing.AIConfigJSON) == "" {
				// AIConfigJSON 为空 → admin 没配过(或新建的),回填 seed。
				existing.SetAIConfig(seed)
				if uErr := s.repo.Update(existing); uErr != nil {
					log.Printf("Warning: failed to backfill AI seed for subject %s: %v", defaults[i].Key, uErr)
				}
			}
			// AIConfigJSON 非空 → admin 配过了,跳过(不覆盖)。
		}
		_ = created
	}
	return nil
}
