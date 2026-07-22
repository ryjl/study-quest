package service

import (
	"errors"
	"fmt"
	"strings"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// Grade management service — the admin-side CRUD layer for grade tags.
//
// Grades get created implicitly (any new tag value a course/reading item is
// saved with just persists as a string in the composite-PK grade tables), so
// there's no Create here. What this service provides is the LATER management
// surface the admin needs once a pile of custom tags has accumulated:
//   - ListAll: see what's in use + how many entities reference each tag.
//   - Rename: fix a typo'd custom tag (cascades across all four grade tables).
//   - Merge: dedupe two tags that mean the same thing (考研 / 研究生).
//   - Delete: remove a now-unused tag (or clean up a deprecated preset).
//
// Preset tags (model.PresetGrades) are protected from rename/delete — they're
// the system defaults the GradePicker shows as checkboxes, and renaming one
// would silently change what every course form renders. The admin can still
// MERGE a preset INTO another tag (e.g. migrate historical "adult" rows to
// "college"), which doesn't rename the preset itself, just moves the rows.

// ErrGradeIsPreset is returned when the admin tries to rename or delete a
// preset tag. Non-fatal — handler maps to 409 so the UI can show "system tag,
// can't modify".
var ErrGradeIsPreset = errors.New("grade is a system preset and cannot be renamed or deleted (use merge to migrate its rows)")

// ErrGradeNotFound is returned when the admin tries to rename/merge/delete a
// tag that doesn't exist in any grade table. Mostly a defense against typos
// in the API call.
var ErrGradeNotFound = errors.New("grade tag not found in any grade table")

// ErrGradeInUse is returned when the admin tries to Delete a tag that still
// has rows referencing it. Delete is for cleanup only; for tags in use, the
// admin must Merge first (or re-tag every referencing entity manually).
var ErrGradeInUse = errors.New("grade tag is still in use; merge it into another tag before deleting")

// GradeService is the admin-facing grade management surface. Handlers depend
// on this interface; the concrete gradeService depends on GradeRepository.
type GradeService interface {
	ListAll() ([]repository.GradeUsage, error)
	Rename(from, to string) error
	Merge(from, to string) error
	Delete(grade string) error
}

type gradeService struct {
	gradeRepo repository.GradeRepository
}

// NewGradeService constructs a GradeService. gradeRepo may be nil in degenerate
// builds (no DB wired); methods return a friendly error in that case rather
// than nil-deref.
func NewGradeService(gradeRepo repository.GradeRepository) GradeService {
	return &gradeService{gradeRepo: gradeRepo}
}

func (s *gradeService) ListAll() ([]repository.GradeUsage, error) {
	if s.gradeRepo == nil {
		return nil, errors.New("grade subsystem not configured")
	}
	// model.PresetGrades → []string for the repo. The order matters: ListAll
	// returns presets first (in this order), then custom tags alphabetically.
	presets := make([]string, 0, len(model.PresetGrades))
	for _, g := range model.PresetGrades {
		presets = append(presets, string(g))
	}
	return s.gradeRepo.ListAll(presets)
}

// Rename changes a custom tag's value across all four grade tables. Preset
// tags are protected (ErrGradeIsPreset) — renaming "primary" would silently
// break the GradePicker checkbox mapping. Use Merge to migrate a preset's
// rows elsewhere.
func (s *gradeService) Rename(from, to string) error {
	if s.gradeRepo == nil {
		return errors.New("grade subsystem not configured")
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return errors.New("from and to must both be non-empty")
	}
	if isPresetGrade(from) {
		return ErrGradeIsPreset
	}
	// Confirm the source tag actually exists (rename of a non-existent tag is
	// almost certainly a typo or a stale UI).
	exists, err := s.gradeExists(from)
	if err != nil {
		return err
	}
	if !exists {
		return ErrGradeNotFound
	}
	return s.gradeRepo.Rename(from, to)
}

// Merge moves every row tagged `from` over to `to`, then `from` disappears
// from ListAll (unless it's a preset — presets stay listed with Count=0 after
// their rows migrate). Use case: the admin accumulated "考研" and "研究生"
// over time and wants to consolidate. Unlike Rename, `from` MAY be a preset
// — that's how the historical "adult" tag gets migrated to "college": merge
// adult→college moves the rows, and "adult" stops appearing (it was never a
// preset under the new scheme; see model/grade.go's 2026-07-21 note).
func (s *gradeService) Merge(from, to string) error {
	if s.gradeRepo == nil {
		return errors.New("grade subsystem not configured")
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return errors.New("from and to must both be non-empty")
	}
	if from == to {
		return errors.New("cannot merge a tag into itself")
	}
	// `to` must be a known tag OR a preset — merging into a non-existent tag
	// would orphan the rows (they'd land at a tag the admin can't see unless
	// they search). Allow `to` to be a preset even if it currently has 0 rows
	// (the common case when migrating adult→college and college is brand new).
	if !isPresetGrade(to) {
		exists, err := s.gradeExists(to)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("target tag %q does not exist; pick an existing tag or a preset", to)
		}
	}
	// `from` must exist somewhere (preset or in the DB). Merging a totally
	// phantom tag is a no-op at best and confusing at worst.
	if !isPresetGrade(from) {
		exists, err := s.gradeExists(from)
		if err != nil {
			return err
		}
		if !exists {
			return ErrGradeNotFound
		}
	}
	return s.gradeRepo.Merge(from, to)
}

// Delete removes every row tagged `grade` across all four tables. Refuses
// preset tags (use Merge to migrate their rows away first) and refuses tags
// still in use (use Merge to consolidate into another tag first). The only
// legitimate use is cleaning up a custom tag that ended up with Count==0
// because every entity referencing it was re-tagged or deleted.
func (s *gradeService) Delete(grade string) error {
	if s.gradeRepo == nil {
		return errors.New("grade subsystem not configured")
	}
	grade = strings.TrimSpace(grade)
	if grade == "" {
		return errors.New("grade must be non-empty")
	}
	if isPresetGrade(grade) {
		return ErrGradeIsPreset
	}
	all, err := s.ListAll()
	if err != nil {
		return err
	}
	for _, g := range all {
		if g.Grade == grade && g.Count > 0 {
			return ErrGradeInUse
		}
	}
	return s.gradeRepo.Delete(grade)
}

// gradeExists reports whether any of the four grade tables contains at least
// one row with this tag. Used to distinguish "rename a real tag" from "rename
// a typo".
func (s *gradeService) gradeExists(grade string) (bool, error) {
	all, err := s.ListAll()
	if err != nil {
		return false, err
	}
	for _, g := range all {
		if g.Grade == grade && g.Count > 0 {
			return true, nil
		}
	}
	return false, nil
}

// isPresetGrade reports whether `g` is one of model.PresetGrades.
func isPresetGrade(g string) bool {
	for _, p := range model.PresetGrades {
		if string(p) == g {
			return true
		}
	}
	return false
}
