package repository

import (
	"strconv"
	"studyquest/backend/internal/model"
	"testing"
)

func TestReleaseFindLatest(t *testing.T) {
	db := setupTestDB(t)
	repo := NewReleaseRepository(db)

	// Seed three arm64-v8a builds: 10 (active), 12 (active), 13 (withdrawn).
	for _, vc := range []int{10, 12, 13} {
		rel := &model.AppRelease{
			VersionCode: vc, VersionName: "1.0", ABI: "arm64-v8a",
			Filepath: "releases/" + strconv.Itoa(vc) + "/arm64-v8a.apk",
			FileSize: 100, IsActive: true,
		}
		if vc == 13 {
			rel.IsActive = false // withdrawn — must be hidden from clients
		}
		if err := repo.Create(rel); err != nil {
			t.Fatalf("Create vc=%d: %v", vc, err)
		}
	}

	got, err := repo.FindLatest("arm64-v8a")
	if err != nil || got == nil {
		t.Fatalf("FindLatest: %v %v", got, err)
	}
	// Highest ACTIVE build is 12; withdrawn 13 must be skipped.
	if got.VersionCode != 12 {
		t.Errorf("FindLatest version_code = %d, want 12 (13 is withdrawn)", got.VersionCode)
	}
}

func TestReleaseFindLatestNone(t *testing.T) {
	db := setupTestDB(t)
	repo := NewReleaseRepository(db)

	got, err := repo.FindLatest("x86_64")
	if err != nil {
		t.Fatalf("FindLatest on empty: unexpected err %v", err)
	}
	if got != nil {
		t.Errorf("FindLatest on empty = %+v, want nil", got)
	}
}

func TestReleaseFindByVersionAndABI(t *testing.T) {
	db := setupTestDB(t)
	repo := NewReleaseRepository(db)

	if err := repo.Create(&model.AppRelease{
		VersionCode: 5, VersionName: "1.0.5", ABI: "armeabi-v7a",
		Filepath: "releases/5/armeabi-v7a.apk", IsActive: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, err := repo.FindByVersionAndABI(5, "armeabi-v7a")
	if err != nil || got == nil {
		t.Fatalf("FindByVersionAndABI: %v %v", got, err)
	}
	if got.VersionName != "1.0.5" {
		t.Errorf("VersionName = %s, want 1.0.5", got.VersionName)
	}

	// Wrong ABI → nil.
	if got2, _ := repo.FindByVersionAndABI(5, "arm64-v8a"); got2 != nil {
		t.Errorf("FindByVersionAndABI cross-abi = %+v, want nil", got2)
	}
}

func TestReleaseUniqueConstraint(t *testing.T) {
	db := setupTestDB(t)
	repo := NewReleaseRepository(db)

	first := &model.AppRelease{
		VersionCode: 1, VersionName: "1.0", ABI: "arm64-v8a",
		Filepath: "releases/1/arm64-v8a.apk", IsActive: true,
	}
	if err := repo.Create(first); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	// Same (version_code, abi) must collide.
	dup := &model.AppRelease{
		VersionCode: 1, VersionName: "1.0-patched", ABI: "arm64-v8a",
		Filepath: "releases/1/arm64-v8a.apk", IsActive: true,
	}
	if err := repo.Create(dup); err == nil {
		t.Error("Create duplicate (version_code, abi) succeeded; want unique-constraint error")
	}
	// Different ABI is fine.
	if err := repo.Create(&model.AppRelease{
		VersionCode: 1, VersionName: "1.0", ABI: "x86_64",
		Filepath: "releases/1/x86_64.apk", IsActive: true,
	}); err != nil {
		t.Fatalf("Create same vc different abi: %v", err)
	}
}

func TestReleaseFindAllOrdering(t *testing.T) {
	db := setupTestDB(t)
	repo := NewReleaseRepository(db)

	for _, vc := range []int{1, 3, 2} {
		if err := repo.Create(&model.AppRelease{
			VersionCode: vc, VersionName: "v", ABI: "arm64-v8a",
			Filepath: "releases/" + strconv.Itoa(vc) + "/arm64-v8a.apk", IsActive: true,
		}); err != nil {
			t.Fatalf("Create vc=%d: %v", vc, err)
		}
	}

	rels, err := repo.FindAll()
	if err != nil {
		t.Fatalf("FindAll: %v", err)
	}
	if len(rels) != 3 || rels[0].VersionCode != 3 || rels[2].VersionCode != 1 {
		t.Errorf("FindAll ordering = %v, want [3,2,1] by version_code DESC", rels)
	}
}

