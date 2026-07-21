package repository

import (
	"errors"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
)

// ErrGlossaryNotFound is returned by AIService accept/reject when no candidate
// matches the id (admin pointed at a stale row, or it was deleted). The handler
// surfaces 404.
var ErrGlossaryNotFound = errors.New("glossary candidate not found")

// GlossaryRepository persists term-correction candidates mined by the polish
// pipeline (see docs/subtitle-system-overhaul.md §三). The interesting method
// is UpsertCandidate — the same (course, original, corrected) rule surfaces
// every time an episode of that course is polished, so we must NOT create a
// fresh pending row on each polish run. Instead we accumulate evidence and
// keep the highest confidence, while leaving admin-decided rows (accepted /
// rejected) untouched so a later polish run can't silently undo a decision.
//
// Listing / accepting / rejecting candidates is the PR2.5 admin UI's job and
// lives on this interface.
type GlossaryRepository interface {
	// UpsertCandidate inserts c if (course_id, original, corrected) is new;
	// otherwise updates an existing PENDING row (bumps EvidenceCount, takes
	// the higher Confidence). Accepted/rejected rows are left as-is. The
	// CourseID on c is authoritative — callers must set it.
	UpsertCandidate(c *model.GlossaryCandidate) error
	// UpsertCandidates is the batched form, looping UpsertCandidate. A single
	// error aborts the batch (the caller is the polish job; partial writes
	// would leave an inconsistent pending pool).
	UpsertCandidates(cs []model.GlossaryCandidate) error

	// ListByCourse returns the course's candidates, optionally filtered by
	// status ("" = all). Ordered by confidence desc then id asc so the admin
	// review UI surfaces the highest-signal rules first. Used by the PR2.5
	// "术语候选" admin tab.
	ListByCourse(courseID uint, status string) ([]model.GlossaryCandidate, error)
	// FindByID returns one candidate by id, or (nil, nil) when not found. Used
	// by accept/reject handlers to load + verify the row before mutating it.
	FindByID(id uint) (*model.GlossaryCandidate, error)
	// Update writes the candidate back. Used by accept/reject after the service
	// layer stamps Status/AcceptedAt/Corrected/Context on it. Caller is
	// responsible for the status-machine transition rules (e.g. don't accept an
	// already-accepted row) — this is a dumb write.
	Update(c *model.GlossaryCandidate) error
}

type glossaryRepo struct {
	db *gorm.DB
}

// NewGlossaryRepository creates a GlossaryRepository bound to db.
func NewGlossaryRepository(db *gorm.DB) GlossaryRepository {
	return &glossaryRepo{db: db}
}

func (r *glossaryRepo) UpsertCandidate(c *model.GlossaryCandidate) error {
	var existing model.GlossaryCandidate
	err := r.db.Where("course_id = ? AND original = ? AND corrected = ?",
		c.CourseID, c.Original, c.Corrected).First(&existing).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		// New rule for this course. Seed EvidenceCount from the first batch
		// if the caller didn't set it (polish pipeline passes the size of
		// EvidenceSample).
		if c.EvidenceCount == 0 && c.EvidenceSample != "" {
			c.EvidenceCount = 1
		}
		if c.Status == "" {
			c.Status = "pending"
		}
		return r.db.Create(c).Error
	}
	if err != nil {
		return err
	}

	// Existing row. Admin decisions are sticky: once accepted/rejected, a
	// future polish run must not flip it back to pending or rewrite its
	// context/confidence — the admin has spoken. Only pending rows absorb
	// new evidence.
	if existing.Status != "pending" {
		return nil
	}
	if c.Confidence > existing.Confidence {
		existing.Confidence = c.Confidence
	}
	if c.Context != "" && existing.Context == "" {
		existing.Context = c.Context
	}
	// Each polish run contributes at least one observation; if the caller
	// passed an explicit count, add it, otherwise bump by one.
	bump := 1
	if c.EvidenceCount > 0 {
		bump = c.EvidenceCount
	}
	existing.EvidenceCount += bump
	// Replace the evidence sample with the latest (keeps it fresh + bounded
	// to 5 by the caller; cheaper than merging JSON arrays on every run).
	if c.EvidenceSample != "" {
		existing.EvidenceSample = c.EvidenceSample
	}
	return r.db.Save(&existing).Error
}

func (r *glossaryRepo) UpsertCandidates(cs []model.GlossaryCandidate) error {
	for i := range cs {
		if err := r.UpsertCandidate(&cs[i]); err != nil {
			return err
		}
	}
	return nil
}

// ListByCourse returns the course's candidates filtered by status ("" = all).
// Ordered by confidence desc then id asc — the admin review UI wants the
// highest-signal rules first, and the tiebreaker keeps the order stable across
// pages/reloads.
func (r *glossaryRepo) ListByCourse(courseID uint, status string) ([]model.GlossaryCandidate, error) {
	var out []model.GlossaryCandidate
	q := r.db.Where("course_id = ?", courseID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	err := q.Order("confidence desc, id asc").Find(&out).Error
	return out, err
}

func (r *glossaryRepo) FindByID(id uint) (*model.GlossaryCandidate, error) {
	var c model.GlossaryCandidate
	if err := r.db.First(&c, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *glossaryRepo) Update(c *model.GlossaryCandidate) error {
	return r.db.Save(c).Error
}
