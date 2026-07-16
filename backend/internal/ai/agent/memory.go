package agent

import (
	"context"

	"studyquest/backend/internal/model"
)

// Memory is the feedback-loop carrier: it records, per (user, knowledge-point
// chunk), how well the student has mastered that point, and it is READ by the
// quiz agent on the next generation so questions adapt to weak points. This is
// what makes the system an agent (state-driven, self-adapting) rather than a
// stateless quiz generator.
//
// Two tables back it (defined in internal/model):
//   - KnowledgeMemory: the CURRENT aggregated state (mastery 0-1, correct/wrong
//     counts, last reviewed). One row per (user, chunk). Updated on every answer.
//   - Answer: the append-only per-question answer log. The detail behind the
//     aggregate. Kept even when a quiz is regenerated (regeneration only deletes
//     Quiz+Question rows, NOT memory) so a student's long-term mastery survives
//     "换题".
//
// MemoryStore abstracts the read/write so the agent and grading paths don't
// touch GORM directly, and so this can be unit-tested with a fake.

// MemoryRepo is the slice of the content repository that memory needs. Kept
// minimal so the agent package depends only on what it uses (and a test can
// substitute a fake without implementing the whole AIContentRepository).
type MemoryRepo interface {
	GetMasteries(userID, episodeID uint) ([]model.KnowledgeMemory, error)
	// UpsertMemoryOnAnswer applies the feedback update atomically: correct
	// nudges mastery up, wrong nudges it down, counts tick, last_reviewed = now.
	UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID uint, correct bool) error
}

// MemoryStore reads and updates per-user learning state. It is the read-side
// backing for the get_user_mastery tool (quiz agent) and the write-side backing
// for answer grading (SubmitQuizAnswer → mastery update → next-gen adaptation).
type MemoryStore struct {
	repo MemoryRepo
}

// NewMemoryStore builds a MemoryStore over repo.
func NewMemoryStore(repo MemoryRepo) *MemoryStore {
	return &MemoryStore{repo: repo}
}

// Masteries returns the user's per-chunk mastery for an episode, ordered worst-
// first (lowest mastery first). The quiz agent reads this to decide which weak
// points to target. A student with no history yet (new to the lesson) returns
// an empty slice — the agent then falls back to coverage-based question choice.
func (m *MemoryStore) Masteries(ctx context.Context, userID, episodeID uint) ([]model.KnowledgeMemory, error) {
	// ctx reserved for future repo-level cancellation; current repo is sync.
	_ = ctx
	rows, err := m.repo.GetMasteries(userID, episodeID)
	if err != nil {
		return nil, err
	}
	// Sort worst-first (ascending mastery) so the agent's prompt lists the most
	// urgent weaknesses at the top. Insertion sort — per-episode chunk counts
	// are small (tens).
	for i := 1; i < len(rows); i++ {
		j := i
		for j > 0 && rows[j].Mastery < rows[j-1].Mastery {
			rows[j], rows[j-1] = rows[j-1], rows[j]
			j--
		}
	}
	return rows, nil
}

// RecordAnswer updates mastery after a student answers. correct=true ⇒ mastery
// +0.1, correct_count++; correct=false ⇒ mastery -0.2, wrong_count++. Mastery
// is clamped to [0,1]. last_reviewed = now.
//
// Why asymmetric (+0.1 / -0.2): a wrong answer is a stronger signal than a
// correct one — guessing right is possible, but a demonstrated misconception
// should pull mastery down faster than it recovers, so weaknesses surface
// quickly and the agent prioritizes them. The exact numbers are tunable; the
// shape (wrong costs more than right gains) is the design.
//
// chunkID == 0 means the question was synthetic (not tied to a chunk) — there's
// no knowledge point to update, so this is a no-op. (A decay curve over time is
// a planned Phase D addition; for now mastery only moves on answers.)
func (m *MemoryStore) RecordAnswer(ctx context.Context, userID, chunkID, episodeID, courseID uint, correct bool) error {
	_ = ctx
	if chunkID == 0 {
		return nil
	}
	return m.repo.UpsertMemoryOnAnswer(userID, chunkID, episodeID, courseID, correct)
}

// ApplyMastery is the pure mastery-update rule, extracted from the DB path so
// it can be unit-tested without a database. Given the current mastery, it
// returns the new mastery + which counter to increment. Clamped to [0,1].
//
// Exported so the repo's upsert can reuse the identical rule (single source of
// truth — the DB UPDATE must apply the same math as this function).
func ApplyMastery(current float64, correct bool) (newMastery float64, field string) {
	if correct {
		newMastery = current + 0.1
		field = "correct_count"
	} else {
		newMastery = current - 0.2
		field = "wrong_count"
	}
	if newMastery > 1.0 {
		newMastery = 1.0
	}
	if newMastery < 0.0 {
		newMastery = 0.0
	}
	return newMastery, field
}
