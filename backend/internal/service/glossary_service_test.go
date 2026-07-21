package service

import (
	"strings"
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
	"studyquest/backend/internal/testutil"
)

// glossaryTestEnv builds a real aiService + repos against a file-backed test DB,
// wired up enough for the glossary accept/reject paths to run end-to-end (a
// course, a subject, a pending candidate). Mirrors aiServiceTestEnv but adds
// the glossary repo + a course we control.
func glossaryTestEnv(t *testing.T) (*aiService, repository.GlossaryRepository, repository.CourseRepository, repository.SubjectRepository) {
	t.Helper()
	db := testutil.NewFileDB(t)
	contentRepo := repository.NewAIContentRepository(db)
	episodeRepo := repository.NewEpisodeRepository(db)
	courseRepo := repository.NewCourseRepository(db)
	glossaryRepo := repository.NewGlossaryRepository(db)
	subjectRepo := repository.NewSubjectRepository(db)
	svc := NewAIService(
		db, contentRepo, episodeRepo, courseRepo,
		nil,    // no resolver — glossary paths don't need a provider
		nil, nil,
		glossaryRepo,
		subjectRepo,
	).(*aiService)
	return svc, glossaryRepo, courseRepo, subjectRepo
}

// seedCourseWithSubject makes one subject + one course under it, returns
// course id + subject id. The glossary accept path needs a real course row so
// it can load + mutate Course.AIConfigJSON.
func seedCourseWithSubject(t *testing.T, courseRepo repository.CourseRepository, subjectRepo repository.SubjectRepository, subjectKey, courseTitle string) (courseID, subjectID uint) {
	t.Helper()
	subj := model.Subject{Key: subjectKey, Label: courseTitle, SortOrder: 1}
	if err := subjectRepo.Create(&subj); err != nil {
		t.Fatalf("create subject: %v", err)
	}
	course := model.Course{Title: courseTitle, SubjectID: subj.ID, ContentType: model.ContentLearning}
	if err := courseRepo.Create(&course); err != nil {
		t.Fatalf("create course: %v", err)
	}
	return course.ID, subj.ID
}

// TestAcceptGlossaryCandidate_PromotesToTermDict: accept a pending candidate,
// assert (a) the candidate row flips to accepted, (b) the course's TermDict got
// the new entry appended in the documented format "original→corrected（context）".
func TestAcceptGlossaryCandidate_PromotesToTermDict(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)
	courseID, _ := seedCourseWithSubject(t, courseRepo, subjectRepo, "xiangqi", "象棋课")

	cand := model.GlossaryCandidate{
		CourseID:     courseID,
		Original:     "军",
		Corrected:    "车",
		Context:      "象棋术语,指棋子",
		Confidence:   0.95,
		Status:       "pending",
		EvidenceCount: 3,
	}
	if err := glossaryRepo.UpsertCandidate(&cand); err != nil {
		t.Fatalf("seed candidate: %v", err)
	}

	if err := svc.AcceptGlossaryCandidate(cand.ID, "", "", false); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// Candidate is now accepted.
	got, err := glossaryRepo.FindByID(cand.ID)
	if err != nil {
		t.Fatalf("reload candidate: %v", err)
	}
	if got.Status != "accepted" {
		t.Errorf("status = %q, want accepted", got.Status)
	}
	if got.AcceptedAt == nil {
		t.Errorf("AcceptedAt not stamped")
	}

	// Course TermDict got the entry appended.
	course, err := courseRepo.FindByID(courseID)
	if err != nil {
		t.Fatalf("reload course: %v", err)
	}
	td := course.AIConfig().TermDict
	want := "军→车（象棋术语,指棋子）"
	if !strings.Contains(td, want) {
		t.Errorf("TermDict = %q, want it to contain %q", td, want)
	}
}

// TestAcceptGlossaryCandidate_AdminOverrides: the admin can edit corrected +
// context at accept time. The override lands both on the candidate row AND in
// the TermDict entry.
func TestAcceptGlossaryCandidate_AdminOverrides(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)
	courseID, _ := seedCourseWithSubject(t, courseRepo, subjectRepo, "xiangqi", "象棋课2")

	cand := model.GlossaryCandidate{
		CourseID:   courseID,
		Original:   "军",
		Corrected:  "居", // LLM's wrong suggestion
		Context:    "llm context",
		Confidence: 0.9,
		Status:     "pending",
	}
	if err := glossaryRepo.UpsertCandidate(&cand); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Admin overrides both: corrected → 车, context → admin's better note.
	if err := svc.AcceptGlossaryCandidate(cand.ID, "车", "象棋术语:棋子", false); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	got, _ := glossaryRepo.FindByID(cand.ID)
	if got.Corrected != "车" {
		t.Errorf("Corrected = %q, want 车 (admin override)", got.Corrected)
	}
	if got.Context != "象棋术语:棋子" {
		t.Errorf("Context = %q, want admin override", got.Context)
	}

	course, _ := courseRepo.FindByID(courseID)
	td := course.AIConfig().TermDict
	if !strings.Contains(td, "军→车（象棋术语:棋子）") {
		t.Errorf("TermDict = %q, override didn't land", td)
	}
}

// TestAcceptGlossaryCandidate_AlreadyReviewed: accepting a non-pending row
// returns ErrGlossaryNotPending (the admin double-clicked or two admins
// reviewed concurrently).
func TestAcceptGlossaryCandidate_AlreadyReviewed(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)
	courseID, _ := seedCourseWithSubject(t, courseRepo, subjectRepo, "math", "数学课")

	cand := model.GlossaryCandidate{
		CourseID:   courseID,
		Original:   "通分",
		Corrected:  "同分",
		Status:     "accepted", // already reviewed
	}
	if err := glossaryRepo.UpsertCandidate(&cand); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := svc.AcceptGlossaryCandidate(cand.ID, "", "", false)
	if err != ErrGlossaryNotPending {
		t.Errorf("err = %v, want ErrGlossaryNotPending", err)
	}
}

// TestRejectGlossaryCandidate: reject marks status=rejected without touching
// TermDict. The row stays (so UpsertCandidate won't re-create it).
func TestRejectGlossaryCandidate(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)
	courseID, _ := seedCourseWithSubject(t, courseRepo, subjectRepo, "math", "数学课R")

	cand := model.GlossaryCandidate{
		CourseID:  courseID,
		Original:  "向",
		Corrected: "象",
		Status:    "pending",
	}
	if err := glossaryRepo.UpsertCandidate(&cand); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := svc.RejectGlossaryCandidate(cand.ID); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	got, _ := glossaryRepo.FindByID(cand.ID)
	if got.Status != "rejected" {
		t.Errorf("status = %q, want rejected", got.Status)
	}

	// TermDict untouched.
	course, _ := courseRepo.FindByID(courseID)
	if td := course.AIConfig().TermDict; td != "" {
		t.Errorf("TermDict = %q, want empty (reject shouldn't touch it)", td)
	}
}

// TestAcceptGlossaryCandidate_ApplyToSubjectSiblings: with the flag set, the
// rule is appended to EVERY course under the same subject, not just the origin.
// This is the "cross-course推广" convenience from docs §3.2 阶段 4.
func TestAcceptGlossaryCandidate_ApplyToSubjectSiblings(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)

	// One subject, THREE courses under it.
	subj := model.Subject{Key: "xiangqi", Label: "象棋", SortOrder: 1}
	if err := subjectRepo.Create(&subj); err != nil {
		t.Fatal(err)
	}
	mkCourse := func(title string) uint {
		c := model.Course{Title: title, SubjectID: subj.ID, ContentType: model.ContentLearning}
		if err := courseRepo.Create(&c); err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	c1 := mkCourse("象棋入门")
	c2 := mkCourse("象棋中局")
	c3 := mkCourse("象棋残局")

	// Candidate lives under c1 (the origin course).
	cand := model.GlossaryCandidate{
		CourseID:  c1,
		Original:  "军",
		Corrected: "车",
		Context:   "棋子",
		Status:    "pending",
	}
	if err := glossaryRepo.UpsertCandidate(&cand); err != nil {
		t.Fatal(err)
	}

	if err := svc.AcceptGlossaryCandidate(cand.ID, "", "", true); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	want := "军→车（棋子）"
	for _, cid := range []uint{c1, c2, c3} {
		course, _ := courseRepo.FindByID(cid)
		td := course.AIConfig().TermDict
		if !strings.Contains(td, want) {
			t.Errorf("course %d TermDict = %q, want it to contain %q (sibling推广 failed)", cid, td, want)
		}
	}
}

// TestAcceptGlossaryCandidate_SiblingsSkippedWhenNoSubject is the C1 regression
// test. When the origin course has SubjectID=0 (no subject assigned), the
// sibling推广 path MUST short-circuit instead of污染 every course in the DB.
// courseRepo.List("", 0, ...) skips the subject filter entirely, so without the
// guard a single accept with apply_to_subject_siblings=true would append the
// rule to every learning course under every subject —象棋 term into 数学/语文/...
func TestAcceptGlossaryCandidate_SiblingsSkippedWhenNoSubject(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)

	// Origin course with SubjectID=0 (the dangerous case).
	origin := model.Course{Title: "未分类课", SubjectID: 0, ContentType: model.ContentLearning}
	if err := courseRepo.Create(&origin); err != nil {
		t.Fatal(err)
	}
	// An unrelated course under a DIFFERENT subject — must NOT receive the rule.
	mathSubj := model.Subject{Key: "math", Label: "数学", SortOrder: 1}
	if err := subjectRepo.Create(&mathSubj); err != nil {
		t.Fatal(err)
	}
	mathCourse := model.Course{Title: "数学课", SubjectID: mathSubj.ID, ContentType: model.ContentLearning}
	if err := courseRepo.Create(&mathCourse); err != nil {
		t.Fatal(err)
	}

	cand := model.GlossaryCandidate{
		CourseID:  origin.ID,
		Original:  "军",
		Corrected: "车",
		Context:   "棋子",
		Status:    "pending",
	}
	if err := glossaryRepo.UpsertCandidate(&cand); err != nil {
		t.Fatal(err)
	}

	// Accept WITH sibling推广 — but origin has no subject, so推广 should be a no-op.
	if err := svc.AcceptGlossaryCandidate(cand.ID, "", "", true); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// The origin course still gets the rule (the primary accept path).
	originReloaded, _ := courseRepo.FindByID(origin.ID)
	if td := originReloaded.AIConfig().TermDict; !strings.Contains(td, "军→车") {
		t.Errorf("origin course TermDict = %q, should contain the rule", td)
	}
	// The math course must NOT have the rule — this is the污染 we're guarding against.
	mathReloaded, _ := courseRepo.FindByID(mathCourse.ID)
	if td := mathReloaded.AIConfig().TermDict; td != "" {
		t.Errorf("unrelated math course TermDict = %q, must be empty (would be污染)", td)
	}
}

// TestListGlossaryCandidates_Ordering: candidates come back by confidence desc
// so the highest-signal rules surface first in the admin review list.
func TestListGlossaryCandidates_Ordering(t *testing.T) {
	svc, glossaryRepo, courseRepo, subjectRepo := glossaryTestEnv(t)
	courseID, _ := seedCourseWithSubject(t, courseRepo, subjectRepo, "x", "c")

	// Insert out of confidence order; List should sort desc.
	for _, conf := range []float64{0.7, 0.95, 0.8} {
		c := model.GlossaryCandidate{
			CourseID:   courseID,
			Original:   "a",
			Corrected:  "b",
			Confidence: conf,
			Status:     "pending",
		}
		if err := glossaryRepo.UpsertCandidate(&c); err != nil {
			t.Fatal(err)
		}
	}

	// Note: all three have same (course, original, corrected) so UpsertCandidate
	// collapses them into ONE row (dedup). That's the intended behavior — a
	// second polish run with the same finding accumulates, not duplicates. So we
	// actually get 1 row here. To test ordering properly we need distinct keys:
	c2 := model.GlossaryCandidate{CourseID: courseID, Original: "x", Corrected: "y", Confidence: 0.5, Status: "pending"}
	c3 := model.GlossaryCandidate{CourseID: courseID, Original: "p", Corrected: "q", Confidence: 0.99, Status: "pending"}
	glossaryRepo.UpsertCandidate(&c2)
	glossaryRepo.UpsertCandidate(&c3)

	rows, err := svc.ListGlossaryCandidates(courseID, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) < 2 {
		t.Fatalf("expected >= 2 distinct candidates, got %d", len(rows))
	}
	// Verify desc ordering: each row's confidence >= the next.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Confidence < rows[i].Confidence {
			t.Errorf("row %d-1 conf %v < row %d conf %v (not desc)",
				i, rows[i-1].Confidence, i, rows[i].Confidence)
		}
	}
}

// TestFormatTermDictEntry + appendTermDict pin the TermDict format helpers.
func TestTermDictHelpers(t *testing.T) {
	// Format:
	if got := formatTermDictEntry("军", "车", "棋子"); got != "军→车（棋子）" {
		t.Errorf("format with context: %q", got)
	}
	if got := formatTermDictEntry("军", "车", ""); got != "军→车" {
		t.Errorf("format no context: %q", got)
	}
	if got := formatTermDictEntry("军", "车", "   "); got != "军→车" {
		t.Errorf("format whitespace context: %q", got)
	}

	// Append:
	if got := appendTermDict("", "a→b"); got != "a→b" {
		t.Errorf("append to empty: %q", got)
	}
	if got := appendTermDict("a→b", "c→d"); got != "a→b;c→d" {
		t.Errorf("append second: %q", got)
	}
	// Dedup exact:
	if got := appendTermDict("a→b;c→d", "a→b"); got != "a→b;c→d" {
		t.Errorf("append dup should no-op: %q", got)
	}
}
