package repository

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"studyquest/backend/internal/model"
)

// AIPolishChunkRepository persists the per-chunk checkpoint rows for the polish
// pipeline (断点续润). The interesting bits:
//
//   - SeedChunksForJob is IDEMPOTENT (INSERT ... ON CONFLICT DO NOTHING on the
//     (job_id, chunk_index) unique index): runPolishJob calls it at the start of
//     every attempt. A first run seeds all chunks queued; a retry call is a
//     no-op for chunks that already exist (it must not clobber done/failed
//     rows written by a prior attempt).
//   - MarkChunkDone / MarkChunkFailed flip one chunk's status + stats. These
//     are called from inside polish.Polish's OnChunkDone callback, so they run
//     on the per-chunk goroutine — they must be cheap (single-row UPDATE on the
//     composite key) and not error out the pipeline. The service layer treats
//     checkpoint write failures as non-fatal (logs + continues).
type AIPolishChunkRepository interface {
	// SeedChunksForJob upserts the chunk SKELETONS for a job. Idempotent: rows
	// that already exist (a retry of a prior attempt) are left untouched so we
	// don't reset done chunks back to queued. Callers seed exactly one row per
	// chunk index, in order; the slice's ChunkIndex values are authoritative.
	SeedChunksForJob(jobID uint, chunks []model.AIPolishChunk) error
	// ListChunksForJob returns all chunk rows for a job, ordered by ChunkIndex
	// ascending — so runPolishJob can scan for done chunks to feed back as
	// PriorOutcomes.
	ListChunksForJob(jobID uint) ([]model.AIPolishChunk, error)
	// MarkChunkDone flips chunk (jobID, chunkIndex) to done with its token/retry
	// stats + the serialized {changes,glossary} payload (for resume rebuild).
	MarkChunkDone(jobID uint, chunkIndex int, promptTokens, completionTokens, retries, highEditDist, changedCues int, polishedJSON string) error
	// MarkChunkFailed flips chunk (jobID, chunkIndex) to failed with the last
	// retry's error (truncated by caller to fit FirstErr's size:256).
	MarkChunkFailed(jobID uint, chunkIndex int, promptTokens, completionTokens, retries int, errStr string) error
}

type aiPolishChunkRepo struct {
	db *gorm.DB
}

// NewAIPolishChunkRepository creates an AIPolishChunkRepository bound to db.
func NewAIPolishChunkRepository(db *gorm.DB) AIPolishChunkRepository {
	return &aiPolishChunkRepo{db: db}
}

// SeedChunksForJob uses ON CONFLICT DO NOTHING against the
// idx_polish_chunk_job_idx composite unique index, so a retry re-seeding the
// same job's chunks is a no-op for rows that already exist (crucial: a retry
// must not clobber done/failed rows a prior attempt wrote). Only rows for chunk
// indices not yet present get inserted, in queued status.
func (r *aiPolishChunkRepo) SeedChunksForJob(jobID uint, chunks []model.AIPolishChunk) error {
	if len(chunks) == 0 {
		return nil
	}
	for i := range chunks {
		chunks[i].JobID = jobID
		if chunks[i].Status == "" {
			chunks[i].Status = "queued"
		}
	}
	// clause.OnConflict DoNothing maps to INSERT ... ON CONFLICT (...) DO NOTHING
	// on SQLite/Postgres. We rely on the composite unique index
	// idx_polish_chunk_job_idx (job_id, chunk_index) to detect the conflict.
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).
		Create(&chunks).Error
}

func (r *aiPolishChunkRepo) ListChunksForJob(jobID uint) ([]model.AIPolishChunk, error) {
	var out []model.AIPolishChunk
	err := r.db.Where("job_id = ?", jobID).Order("chunk_index ASC").Find(&out).Error
	return out, err
}

// MarkChunkDone updates the chunk row by (jobID, chunkIndex) to done with all
// its telemetry. Uses a map-based Updates so zero-value fields (e.g. 0 retries)
// still write — a struct-based Updates would skip them.
func (r *aiPolishChunkRepo) MarkChunkDone(jobID uint, chunkIndex int, promptTokens, completionTokens, retries, highEditDist, changedCues int, polishedJSON string) error {
	return r.db.Model(&model.AIPolishChunk{}).
		Where("job_id = ? AND chunk_index = ?", jobID, chunkIndex).
		Updates(map[string]interface{}{
			"status":                  "done",
			"prompt_tokens":           promptTokens,
			"completion_tokens":       completionTokens,
			"retries":                 retries,
			"high_edit_distance_count": highEditDist,
			"changed_cues":            changedCues,
			"polished_chunk_json":     polishedJSON,
			"first_err":               "",
		}).Error
}

// MarkChunkFailed updates the chunk row to failed with the last retry's error
// string. Token counts are still recorded — a failed chunk still spent tokens
// (HTTP-successful-but-validation-failed attempts bill the relay), and the
// admin needs to see that to judge cost.
func (r *aiPolishChunkRepo) MarkChunkFailed(jobID uint, chunkIndex int, promptTokens, completionTokens, retries int, errStr string) error {
	return r.db.Model(&model.AIPolishChunk{}).
		Where("job_id = ? AND chunk_index = ?", jobID, chunkIndex).
		Updates(map[string]interface{}{
			"status":            "failed",
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"retries":           retries,
			"first_err":         errStr,
		}).Error
}
