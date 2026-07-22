package repository

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

// Code split from the (hypothetical) grades module for navigability.
// Grade management repository: list distinct grade tags with usage counts
// across the four grade-bearing tables, plus rename / merge / delete ops.
//
// The four tables (course_grades, reading_series_grades, reading_book_grades,
// reading_article_grades) all share the same (entity_id, grade) composite-PK
// shape, so the rename/merge/delete operations are mechanically identical
// across them — centralized here so a future schema change (e.g. adding a new
// grade-bearing entity) only touches one place.

// GradeUsage is one grade tag with its total reference count across all four
// grade tables. Preset is true for the model.PresetGrades entries; the admin
// UI uses it to lock preset rows from deletion and to show a system-badge.
type GradeUsage struct {
	Grade    string
	Count    int64
	IsPreset bool
}

// GradeRepository is the admin-side CRUD surface for grade tags. The create
// path isn't here — grades get created implicitly when a course/reading item
// is saved with a new tag value (CourseGrade rows just store the string). This
// repo is for the LATER management operations: listing what's in use, renaming
// a typo'd tag, merging duplicates, deleting a now-unused one.
type GradeRepository interface {
	// ListAll returns every distinct grade tag across all four tables, each
	// with its total reference count. Preset tags that aren't currently used
	// by any row are still included with Count=0 (so the admin sees they exist
	// as options). Ordering: presets first (in model.PresetGrades order), then
	// custom tags alphabetically — stable + predictable for the admin UI.
	ListAll(presets []string) ([]GradeUsage, error)
	// Rename changes every occurrence of from→to across all four grade tables,
	// in a single transaction. Used for fixing typos in custom tags. Preset
	// tags are protected at the service layer (this method doesn't guard);
	// callers must check before calling. Composite-PK tables: a row already
	// at (entity_id, to) means the rename is a no-op for that row (the INSERT
	// would collide), so we delete-then-insert per table to handle the overlap.
	Rename(from, to string) error
	// Merge is like Rename but the "from" tag is expected to disappear
	// afterwards (its rows move to "to"). Implementation is identical to
	// Rename; the separate method exists for API clarity (the admin's intent
	// is "merge these two", not "rename this one"). After Merge, ListAll will
	// no longer show "from" (unless it's a preset, which we never remove).
	Merge(from, to string) error
	// Delete removes the tag from all four tables. Only valid when Count==0
	// (no rows reference it) — the service layer enforces this before calling.
	// In practice this is only useful for cleaning up preset tags the admin
	// wants gone, since custom tags with Count==0 don't appear in ListAll
	// anyway (they were never persisted).
	Delete(grade string) error
}

// gradeTable names one of the four (entity_id, grade) tables we operate on.
// Centralized so Rename/Merge/Delete iterate the same list.
var gradeTables = []struct {
	table    string
	idColumn string // the entity-id column name (course_id, series_id, ...)
}{
	{"course_grades", "course_id"},
	{"reading_series_grades", "series_id"},
	{"reading_book_grades", "book_id"},
	{"reading_article_grades", "article_id"},
}

type gradeRepo struct {
	db *gorm.DB
}

// NewGradeRepository constructs a GradeRepository. db is the same *gorm.DB
// every other repo gets.
func NewGradeRepository(db *gorm.DB) GradeRepository {
	return &gradeRepo{db: db}
}

// ListAll unions distinct grades from all four tables, then overlays preset
// membership. We could do this in one big UNION ALL query, but GORM's table
// abstraction doesn't love raw UNIONs and the four queries are cheap (these
// tables are small — at most a few hundred rows each). The per-table counts
// are summed in Go.
func (r *gradeRepo) ListAll(presets []string) ([]GradeUsage, error) {
	presetSet := make(map[string]bool, len(presets))
	for _, p := range presets {
		presetSet[p] = true
	}
	counts := make(map[string]int64)
	for _, t := range gradeTables {
		type row struct {
			Grade string
			Cnt   int64
		}
		var rows []row
		// COUNT(*) GROUP BY grade — one row per distinct grade in this table.
		if err := r.db.Table(t.table).
			Select("grade, COUNT(*) AS cnt").
			Group("grade").
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("count grades in %s: %w", t.table, err)
		}
		for _, gr := range rows {
			counts[gr.Grade] += gr.Cnt
		}
	}
	// Build the result: presets first (in the caller's order), then any custom
	// tags not in the preset set, alphabetically. Presets unused in the DB
	// still appear with Count=0.
	out := make([]GradeUsage, 0, len(counts)+len(presets))
	seen := make(map[string]bool, len(counts)+len(presets))
	for _, p := range presets {
		out = append(out, GradeUsage{Grade: p, Count: counts[p], IsPreset: true})
		seen[p] = true
	}
	// Collect custom tags, sort, append.
	var customs []string
	for g := range counts {
		if !seen[g] {
			customs = append(customs, g)
		}
	}
	// Sort customs alphabetically (Go's sort on []string is stable + lex).
	for i := 0; i < len(customs); i++ {
		for j := i + 1; j < len(customs); j++ {
			if customs[j] < customs[i] {
				customs[i], customs[j] = customs[j], customs[i]
			}
		}
	}
	for _, g := range customs {
		out = append(out, GradeUsage{Grade: g, Count: counts[g], IsPreset: false})
	}
	return out, nil
}

// Rename moves every (entity_id, from) row to (entity_id, to) across all four
// tables, in one transaction. The tricky case: if (entity_id, to) already
// exists (the entity was tagged with BOTH from and to), we must drop the
// "from" row rather than collide on the composite PK. We handle this with a
// DELETE WHERE grade=from AND entity_id IN (SELECT entity_id WHERE grade=to)
// first, then a plain UPDATE for the survivors. Doing it per-table keeps the
// SQL simple and the transaction tight.
func (r *gradeRepo) Rename(from, to string) error {
	if from == "" || to == "" {
		return errors.New("rename: from and to must both be non-empty")
	}
	if from == to {
		return nil // no-op
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, t := range gradeTables {
			// Step 1: drop "from" rows whose entity already has a "to" row
			// (would otherwise collide on the composite PK during UPDATE).
			delSQL := fmt.Sprintf(
				"DELETE FROM %s WHERE grade = ? AND %s IN (SELECT %s FROM %s WHERE grade = ?)",
				t.table, t.idColumn, t.idColumn, t.table,
			)
			if err := tx.Exec(delSQL, from, to).Error; err != nil {
				return fmt.Errorf("dedup %s for rename %s→%s: %w", t.table, from, to, err)
			}
			// Step 2: rename the surviving "from" rows to "to".
			updSQL := fmt.Sprintf("UPDATE %s SET grade = ? WHERE grade = ?", t.table)
			if err := tx.Exec(updSQL, to, from).Error; err != nil {
				return fmt.Errorf("update %s for rename %s→%s: %w", t.table, from, to, err)
			}
		}
		return nil
	})
}

// Merge is identical to Rename at the storage level — both move rows from one
// tag to another. The separate method exists because the admin-facing intent
// differs ("merge these two duplicates" vs "fix this typo"), and future
// divergence (e.g. logging, audit trail) is cleaner with two call sites.
func (r *gradeRepo) Merge(from, to string) error {
	return r.Rename(from, to)
}

// Delete removes every row with the given grade across all four tables. The
// service layer MUST guard this with a Count==0 check — once rows are gone,
// the entities that referenced the tag have silently lost a grade. We don't
// re-check here because the check is a TOCTOU race anyway (admin sees Count=0,
// someone saves a new course with that tag between the check and the delete);
// the service re-loads Count inside the same transaction would be the right
// fix if this ever becomes a real concern.
func (r *gradeRepo) Delete(grade string) error {
	if grade == "" {
		return errors.New("delete: grade must be non-empty")
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		for _, t := range gradeTables {
			if err := tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE grade = ?", t.table), grade).Error; err != nil {
				return fmt.Errorf("delete grade %s from %s: %w", grade, t.table, err)
			}
		}
		return nil
	})
}
