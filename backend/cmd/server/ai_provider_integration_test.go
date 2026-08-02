package main

import (
	"encoding/json"
	"net/http"
	"testing"
)

// AI provider CRUD integration tests exercise the DB-only half of the provider
// admin surface over HTTP:
//
//	POST   /admin/api/ai/providers      — create
//	GET    /admin/api/ai/providers      — list (api_key stripped)
//	PUT    /admin/api/ai/providers/:id  — update (blank api_key = keep existing)
//	DELETE /admin/api/ai/providers/:id  — delete
//
// The network-touching endpoints (/test, /test-real, /models) are intentionally
// NOT covered here: OpenAICompatProvider builds its own *http.Client internally
// with no transport-injection seam at the handler layer, so testing them would
// require an httptest.Server pointed at base_url — out of scope for this pass
// (tracked in docs/handoff). The four CRUD endpoints are pure DB and the
// highest-value coverage: they guard the api_key write-only contract, the
// openai_compat create-validation, and the "blank key on update keeps existing"
// convention.

// providerDTO mirrors the admin provider DTO for assertions.
type providerDTO struct {
	ID           uint     `json:"id"`
	Capability   string   `json:"capability"`
	Name         string   `json:"name"`
	ProviderType string   `json:"provider_type"`
	BaseURL      string   `json:"base_url"`
	APIKey       string   `json:"api_key"`
	ModelName    string   `json:"model_name"`
	ExtraJSON    string   `json:"extra_json,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	IsEnabled    bool     `json:"is_enabled"`
}

// createProvider POSTs a minimal valid openai_compat chat provider and returns
// the created DTO. Shared shape so each test starts from a known-good row.
func (e *testEnv) createProvider(t *testing.T, name string) providerDTO {
	t.Helper()
	resp := e.do(t, http.MethodPost, "/admin/api/ai/providers", map[string]any{
		"capability":    "chat",
		"name":          name,
		"provider_type": "openai_compat",
		"base_url":      "https://api.example.com",
		"api_key":       "sk-test-secret",
		"model_name":    "gpt-test",
		"is_enabled":    true,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("create provider %q: %d %s", name, resp.Code, resp.Body.String())
	}
	var p providerDTO
	if err := json.Unmarshal(resp.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode created provider: %v (body: %s)", err, resp.Body.String())
	}
	return p
}

// listProviders GETs /admin/api/ai/providers, failing on non-200.
func (e *testEnv) listProviders(t *testing.T) []providerDTO {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/admin/api/ai/providers", nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("list providers: %d %s", resp.Code, resp.Body.String())
	}
	var out []providerDTO
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode providers list: %v (body: %s)", err, resp.Body.String())
	}
	return out
}

// TestAIProvider_CreateThenList create → list round-trip: the new row appears,
// and CRUCIALLY the api_key is never echoed back (write-only secret contract —
// the admin UI shows a masked field, so leaking the key here would be a real
// secret exposure, not a cosmetic issue).
func TestAIProvider_CreateThenList(t *testing.T) {
	env := newTestEnv(t)
	created := env.createProvider(t, "chat-provider")

	if created.APIKey != "" {
		t.Errorf("create response leaked api_key = %q; must be stripped (write-only)", created.APIKey)
	}
	if created.ID == 0 {
		t.Fatal("create returned id=0")
	}

	list := env.listProviders(t)
	var found *providerDTO
	for i := range list {
		if list[i].ID == created.ID {
			found = &list[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("created provider id=%d not in list %v", created.ID, list)
	}
	if found.APIKey != "" {
		t.Errorf("list response leaked api_key = %q; must be stripped on every read path", found.APIKey)
	}
	if found.Name != "chat-provider" || !found.IsEnabled {
		t.Errorf("listed provider shape = %+v; want name=chat-provider is_enabled=true", found)
	}
}

// TestAIProvider_Update_BlankKeyKeepsExisting PUT with a blank api_key must
// succeed (200) and not wipe the stored secret — that's the "edit other fields
// without re-entering the key" convention. We can't read the key back to prove
// it survived (it's write-only), but we assert the update returns 200 and the
// row remains, which is the HTTP-layer contract; the keep-existing behavior is
// owned by the service/handler merge logic (admin_ai_provider.go:160).
func TestAIProvider_Update_BlankKeyKeepsExisting(t *testing.T) {
	env := newTestEnv(t)
	created := env.createProvider(t, "to-update")

	resp := env.do(t, http.MethodPut, "/admin/api/ai/providers/"+itoa(created.ID), map[string]any{
		"capability":    "chat",
		"name":          "renamed-provider",
		"provider_type": "openai_compat",
		"base_url":      "https://api.example.com",
		"api_key":       "", // blank ⇒ keep existing
		"model_name":    "gpt-test",
		"is_enabled":    false,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("update with blank key: want 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	list := env.listProviders(t)
	var found *providerDTO
	for i := range list {
		if list[i].ID == created.ID {
			found = &list[i]
			break
		}
	}
	if found == nil {
		t.Fatal("provider vanished after update")
	}
	if found.Name != "renamed-provider" {
		t.Errorf("name after update = %q; want renamed-provider", found.Name)
	}
	if found.IsEnabled {
		t.Error("is_enabled after update = true; want false (we set it)")
	}
}

// TestAIProvider_Delete DELETE removes the row; a follow-up list no longer has it.
func TestAIProvider_Delete(t *testing.T) {
	env := newTestEnv(t)
	created := env.createProvider(t, "to-delete")

	resp := env.do(t, http.MethodDelete, "/admin/api/ai/providers/"+itoa(created.ID), nil)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete: want 200, got %d (body: %s)", resp.Code, resp.Body.String())
	}

	for _, p := range env.listProviders(t) {
		if p.ID == created.ID {
			t.Errorf("provider id=%d still listed after delete", created.ID)
		}
	}
}

// TestAIProvider_Create_MissingAPIKey creating an openai_compat provider
// without an api_key is rejected with 400 (validateAIProvider requires it on
// create so a half-configured provider can't be saved). Guards the validation
// gate that keeps the resolver from picking up an un-authenticatable provider.
func TestAIProvider_Create_MissingAPIKey(t *testing.T) {
	env := newTestEnv(t)
	resp := env.do(t, http.MethodPost, "/admin/api/ai/providers", map[string]any{
		"capability":    "chat",
		"name":          "no-key",
		"provider_type": "openai_compat",
		"base_url":      "https://api.example.com",
		"api_key":       "", // missing
		"model_name":    "gpt-test",
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("create without api_key: want 400, got %d (body: %s)", resp.Code, resp.Body.String())
	}
}
