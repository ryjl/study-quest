package ai

import (
	"errors"
	"testing"

	"gorm.io/gorm"

	"studyquest/backend/internal/model"
	"studyquest/backend/internal/repository"
)

// fakeProviderRepo is a minimal AIProviderRepository for testing the resolver's
// purpose-routing logic without a DB. Only List() is exercised by the resolver
// paths under test; the other methods panic because they shouldn't be reached.
type fakeProviderRepo struct {
	rows []model.AIProvider
}

func (f *fakeProviderRepo) List() ([]model.AIProvider, error) { return f.rows, nil }
func (f *fakeProviderRepo) Create(*model.AIProvider) error    { panic("not used") }
func (f *fakeProviderRepo) Update(*model.AIProvider) error    { panic("not used") }
func (f *fakeProviderRepo) Delete(uint) error                 { panic("not used") }
func (f *fakeProviderRepo) FindByID(uint) (*model.AIProvider, error) {
	panic("not used")
}
func (f *fakeProviderRepo) ListByCapability(string) ([]model.AIProvider, error) {
	panic("not used")
}
func (f *fakeProviderRepo) WithTx(*gorm.DB) repository.AIProviderRepository { panic("not used") }

// prow is a builder to keep the test table terse.
func prow(id uint, tags string, enabled bool) model.AIProvider {
	return model.AIProvider{
		ID:         id,
		Capability: model.AICapabilityChat,
		Name:       "p" + itoaUint(id),
		ProviderType: "openai_compat",
		BaseURL:    "http://x",
		APIKey:     "k",
		ModelName:  "model-" + itoaUint(id),
		Tags:       tags,
		IsEnabled:  enabled,
	}
}

func itoaUint(n uint) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestEnabledRowByPurpose covers the tag-matching matrix that drives PR5's
// purpose routing. We test enabledRowByPurpose directly (it's the routing
// primitive) rather than ResolveChatByPurpose, because the latter calls
// buildChat which needs a real network client — we only need to verify the
// RIGHT row is selected.
func TestEnabledRowByPurpose(t *testing.T) {
	cases := []struct {
		name    string
		rows    []model.AIProvider
		purpose string
		wantID  uint      // 0 = expect ErrNoProvider
		wantErr error
	}{
		{
			name: "purpose-tagged provider preferred",
			rows: []model.AIProvider{
				prow(1, "", true),                // general-purpose
				prow(2, `["polish"]`, true),      // polish-tagged
			},
			purpose: "polish",
			wantID:  2,
		},
		{
			name: "fallback to general when no tagged provider",
			rows: []model.AIProvider{
				prow(1, "", true),
				prow(2, `["quiz"]`, true),
			},
			purpose: "polish",
			// No polish-tagged row → ErrNoProvider from enabledRowByPurpose.
			// (ResolveChatByPurpose would fall back to the general one; tested
			// separately by the caller. Here we just assert the primitive.)
			wantErr: ErrNoProvider,
		},
		{
			name: "empty purpose matches only untagged",
			rows: []model.AIProvider{
				prow(1, "", true),            // general-purpose
				prow(2, `["polish"]`, true),  // tagged — should NOT match ""
			},
			purpose: "",
			wantID:  1,
		},
		{
			name: "empty purpose with no untagged provider returns ErrNoProvider",
			rows: []model.AIProvider{
				prow(1, `["polish"]`, true),
			},
			purpose: "",
			wantErr: ErrNoProvider,
		},
		{
			name: "disabled provider ignored",
			rows: []model.AIProvider{
				prow(1, `["polish"]`, false),  // disabled
				prow(2, "", true),             // general
			},
			purpose: "polish",
			// The disabled polish row doesn't count → no enabled polish provider.
			wantErr: ErrNoProvider,
		},
		{
			name: "lowest ID wins among matching",
			rows: []model.AIProvider{
				prow(5, `["polish"]`, true),
				prow(3, `["polish"]`, true),
				prow(7, `["polish"]`, true),
			},
			purpose: "polish",
			wantID:  3,
		},
		{
			name: "multi-tag provider matches any of its tags",
			rows: []model.AIProvider{
				prow(1, `["polish","quiz-check"]`, true),
			},
			purpose: "quiz-check",
			wantID:  1,
		},
		{
			name: "comma-form tags tolerated",
			rows: []model.AIProvider{
				prow(1, "polish,quiz", true), // hand-typed, not JSON
			},
			purpose: "quiz",
			wantID:  1,
		},
		{
			name:    "empty repo",
			rows:    nil,
			purpose: "polish",
			wantErr: ErrNoProvider,
		},
		{
			name: "non-chat capability ignored",
			rows: []model.AIProvider{
				{ID: 1, Capability: model.AICapabilityEmbedding, Tags: `["polish"]`, IsEnabled: true},
			},
			purpose: "polish",
			wantErr: ErrNoProvider,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &ProviderResolver{providerRepo: &fakeProviderRepo{rows: c.rows}}
			got, err := r.enabledRowByPurpose(model.AICapabilityChat, c.purpose)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				if got != nil {
					t.Errorf("expected nil row on error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got.ID != c.wantID {
				t.Errorf("got row ID=%d, want %d", got.ID, c.wantID)
			}
		})
	}
}

// TestParseTags pins the tag-parsing tolerance: real JSON arrays, hand-typed
// comma lists, empty, and malformed all behave sanely.
func TestParseTags(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`["polish","quiz"]`, []string{"polish", "quiz"}},
		{`["polish"]`, []string{"polish"}},
		{`polish,quiz`, []string{"polish", "quiz"}},
		{`polish`, []string{"polish"}},
		{``, nil},
		{`   `, nil},
		{`[invalid json`, nil}, // malformed → nil, NOT a panic
		{`[]`, []string{}},     // empty array parses to empty slice (not nil)
	}
	for _, c := range cases {
		p := model.AIProvider{Tags: c.in}
		got := p.ParseTags()
		// Treat nil and []string{} as equivalent for the "no tags" check — the
		// resolver only cares about len() == 0 either way.
		if len(got) != len(c.want) {
			t.Errorf("ParseTags(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ParseTags(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}
