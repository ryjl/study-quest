package repository

import (
	"errors"
	"studyquest/backend/internal/model"

	"gorm.io/gorm"
)

// TagRepository handles SQL operations for the Tag entity and its many-to-many
// relation to courses. Mirrors the SubjectRepository pattern.
type TagRepository interface {
	List() ([]model.Tag, error)                  // ORDER BY sort_order, id
	FindByKey(key string) (*model.Tag, error)    // nil, nil if absent
	FindByID(id uint) (*model.Tag, error)        // nil, nil if absent
	FindByIDs(ids []uint) ([]model.Tag, error)   // batch lookup
	Create(t *model.Tag) error
	Update(t *model.Tag) error                   // db.Save
	Delete(id uint) error                        // course_tags join auto-cleared by CASCADE

	// CountCoursesByTag returns how many courses currently use a tag. Used by
	// the delete-confirm prompt so the admin sees the blast radius before
	// cascading the removal.
	CountCoursesByTag(tagID uint) (int64, error)
	// BatchCourseCountsByTag returns tag_id → course count in one query, so the
	// tag list can show a "used by N courses" column without N+1 lookups.
	BatchCourseCountsByTag() (map[uint]int64, error)
}

type tagRepo struct {
	db *gorm.DB
}

// NewTagRepository creates an instance of TagRepository.
func NewTagRepository(db *gorm.DB) TagRepository {
	return &tagRepo{db: db}
}

func (r *tagRepo) List() ([]model.Tag, error) {
	var tags []model.Tag
	err := r.db.Order("sort_order ASC, id ASC").Find(&tags).Error
	return tags, err
}

func (r *tagRepo) FindByKey(key string) (*model.Tag, error) {
	var tag model.Tag
	if err := r.db.Where("key = ?", key).First(&tag).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &tag, nil
}

func (r *tagRepo) FindByID(id uint) (*model.Tag, error) {
	var tag model.Tag
	if err := r.db.First(&tag, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &tag, nil
}

func (r *tagRepo) FindByIDs(ids []uint) ([]model.Tag, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var tags []model.Tag
	err := r.db.Where("id IN ?", ids).Order("sort_order ASC, id ASC").Find(&tags).Error
	return tags, err
}

func (r *tagRepo) Create(t *model.Tag) error {
	return r.db.Create(t).Error
}

func (r *tagRepo) Update(t *model.Tag) error {
	return r.db.Save(t).Error
}

// Delete removes the tag; the course_tags join rows are cleared automatically
// by the ON DELETE CASCADE constraint, so no application-level cleanup needed.
func (r *tagRepo) Delete(id uint) error {
	return r.db.Delete(&model.Tag{}, id).Error
}

// CountCoursesByTag counts rows in the course_tags join table for one tag.
func (r *tagRepo) CountCoursesByTag(tagID uint) (int64, error) {
	var count int64
	err := r.db.Table("course_tags").Where("tag_id = ?", tagID).Count(&count).Error
	return count, err
}

// BatchCourseCountsByTag returns tag_id → course count in a single grouped
// query over the course_tags join table. Tags with no courses are absent from
// the map (callers treat missing as 0).
func (r *tagRepo) BatchCourseCountsByTag() (map[uint]int64, error) {
	type row struct {
		TagID uint
		Count int64
	}
	var rows []row
	err := r.db.Table("course_tags").
		Select("tag_id, COUNT(*) AS count").
		Group("tag_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]int64, len(rows))
	for _, r := range rows {
		out[r.TagID] = r.Count
	}
	return out, nil
}
