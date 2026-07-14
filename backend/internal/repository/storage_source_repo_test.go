package repository

import (
	"testing"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/testutil"
)

// newStorageRepoTestDB returns a freshly-migrated in-memory DB for one test.
func newStorageRepoTestDB(t *testing.T) *repoTestDB {
	t.Helper()
	db := testutil.NewDB(t)
	return &repoTestDB{db: db, repo: NewStorageSourceRepository(db)}
}

type repoTestDB struct {
	db   interface{}
	repo StorageSourceRepository
}

// TestStorageSourceCRUD covers the basic Create/FindByID/List/Update/Delete
// lifecycle plus the at-most-one-default invariant.
func TestStorageSourceCRUD(t *testing.T) {
	env := newStorageRepoTestDB(t)
	repo := env.repo

	a := &model.StorageSource{Name: "家长盘", Type: "alist", URL: "http://a", IsDefault: true}
	if err := repo.Create(a); err != nil {
		t.Fatalf("Create A: %v", err)
	}
	if a.ID == 0 {
		t.Fatal("Create did not assign ID")
	}

	// FindByID round-trips.
	got, err := repo.FindByID(a.ID)
	if err != nil || got == nil || got.Name != "家长盘" {
		t.Fatalf("FindByID A: got=%+v err=%v", got, err)
	}

	// GetDefault returns the flagged row.
	def, err := repo.GetDefault()
	if err != nil || def == nil || def.ID != a.ID {
		t.Fatalf("GetDefault: got=%+v err=%v", def, err)
	}

	// A second source; when promoted to default, A loses the flag.
	b := &model.StorageSource{Name: "小朋友盘", Type: "webdav", URL: "http://b"}
	if err := repo.Create(b); err != nil {
		t.Fatalf("Create B: %v", err)
	}
	if err := repo.ClearDefault(); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}
	b.IsDefault = true
	if err := repo.Update(b); err != nil {
		t.Fatalf("Update B: %v", err)
	}
	def, _ = repo.GetDefault()
	if def == nil || def.ID != b.ID {
		t.Fatalf("GetDefault after swap: got=%+v", def)
	}
	a2, _ := repo.FindByID(a.ID)
	if a2.IsDefault {
		t.Fatal("A should no longer be default after ClearDefault + B promoted")
	}

	// List returns both (default first by the ORDER BY).
	list, err := repo.List()
	if err != nil || len(list) != 2 {
		t.Fatalf("List: got %d (err %v)", len(list), err)
	}
	if !list[0].IsDefault {
		t.Fatalf("List[0] should be the default row, got name=%q", list[0].Name)
	}

	// Delete removes one.
	if err := repo.Delete(a.ID); err != nil {
		t.Fatalf("Delete A: %v", err)
	}
	got, _ = repo.FindByID(a.ID)
	if got != nil {
		t.Fatal("FindByID after Delete should be nil")
	}
}

// TestStorageWhitelistEmptyMeansUnrestricted is THE backward-compatibility
// assertion: a user with no whitelist rows is allowed every source. This is
// the must-test case called out in the handoff.
func TestStorageWhitelistEmptyMeansUnrestricted(t *testing.T) {
	env := newStorageRepoTestDB(t)
	repo := env.repo
	repo.Create(&model.StorageSource{Name: "s1", Type: "alist", URL: "u1"})

	allowed, err := repo.IsAllowed(42, 1)
	if err != nil {
		t.Fatalf("IsAllowed empty: %v", err)
	}
	if !allowed {
		t.Fatal("empty whitelist must allow (empty=unrestricted invariant)")
	}
}

// TestStorageWhitelistNonEmptyRestricts verifies a populated whitelist only
// permits its members.
func TestStorageWhitelistNonEmptyRestricts(t *testing.T) {
	env := newStorageRepoTestDB(t)
	repo := env.repo
	repo.Create(&model.StorageSource{Name: "A", Type: "alist", URL: "ua"})
	repo.Create(&model.StorageSource{Name: "B", Type: "webdav", URL: "ub"})

	if err := repo.SetWhitelist(7, []uint{1}); err != nil { // user 7 → [A]
		t.Fatalf("SetWhitelist: %v", err)
	}
	if ok, _ := repo.IsAllowed(7, 1); !ok {
		t.Error("user 7 should be allowed source 1 (A)")
	}
	if ok, _ := repo.IsAllowed(7, 2); ok {
		t.Error("user 7 should be DENIED source 2 (B)")
	}
}

// TestStorageWhitelistSetReplacesWholesale verifies SetWhitelist overwrites the
// prior set (delete-then-insert), not appends.
func TestStorageWhitelistSetReplacesWholesale(t *testing.T) {
	env := newStorageRepoTestDB(t)
	repo := env.repo
	for i := 1; i <= 3; i++ {
		repo.Create(&model.StorageSource{Name: "s", Type: "alist", URL: "u"})
	}
	repo.SetWhitelist(9, []uint{1, 2, 3})
	wl, _ := repo.WhitelistForUser(9)
	if len(wl) != 3 {
		t.Fatalf("after SetWhitelist [1,2,3]: got %v", wl)
	}
	// Replace with a single-element set → the other two must be gone.
	repo.SetWhitelist(9, []uint{2})
	wl, _ = repo.WhitelistForUser(9)
	if len(wl) != 1 || wl[0] != 2 {
		t.Fatalf("after replace [2]: got %v", wl)
	}
	// Empty array clears the whitelist → unrestricted again.
	repo.SetWhitelist(9, []uint{})
	wl, _ = repo.WhitelistForUser(9)
	if len(wl) != 0 {
		t.Fatalf("after clear: got %v", wl)
	}
	if ok, _ := repo.IsAllowed(9, 1); !ok {
		t.Error("cleared whitelist should be unrestricted again")
	}
}

// TestStorageWhitelistZeroSourceIDAllowed: IsAllowed with sourceID=0 (caller
// couldn't resolve a source for a legacy row) must allow — keeps legacy NULL-
// source content reachable.
func TestStorageWhitelistZeroSourceIDAllowed(t *testing.T) {
	env := newStorageRepoTestDB(t)
	repo := env.repo
	repo.Create(&model.StorageSource{Name: "s", Type: "alist", URL: "u"})
	repo.SetWhitelist(5, []uint{1}) // restrictive whitelist
	if ok, _ := repo.IsAllowed(5, 0); !ok {
		t.Error("sourceID=0 must always allow (legacy NULL-source content)")
	}
}

// TestStorageWhitelistDedupesInput verifies SetWhitelist dedupes repeated ids
// so the stored row count matches the intent.
func TestStorageWhitelistDedupesInput(t *testing.T) {
	env := newStorageRepoTestDB(t)
	repo := env.repo
	repo.Create(&model.StorageSource{Name: "s", Type: "alist", URL: "u"})
	repo.SetWhitelist(3, []uint{1, 1, 1})
	wl, _ := repo.WhitelistForUser(3)
	if len(wl) != 1 {
		t.Fatalf("deduped [1,1,1] should store 1 row, got %v", wl)
	}
}
