package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"accords-mcp/internal/vault"
)

func TestScoreProfileMatch(t *testing.T) {
	t.Run("exact match", func(t *testing.T) {
		profile := vault.UnboundedProfile{
			ProfileID: "p1",
			Slots: []vault.UnboundedProfileSlot{{
				Slot:                "api_key",
				AllowedIntents:      []string{"http.auth"},
				ExpectedSecretTypes: []string{"api-key"},
				AllowedModes:        []string{"read"},
				AllowedTargets:      []string{"https://api.example.com"},
			}},
		}
		reqs := []UnboundedRequirement{{
			Slot:                "api_key",
			Intent:              "http.auth",
			ExpectedSecretTypes: []string{"api-key"},
			Mode:                "read",
			Target:              "https://api.example.com",
			Required:            true,
		}}
		m := scoreProfileMatch(profile, reqs)
		if len(m.MissingRequirements) != 0 {
			t.Fatalf("expected no missing requirements, got %v", m.MissingRequirements)
		}
		if m.Score != 5 {
			t.Fatalf("expected score 5, got %d", m.Score)
		}
	})

	t.Run("mode mismatch", func(t *testing.T) {
		profile := vault.UnboundedProfile{ProfileID: "p1", Slots: []vault.UnboundedProfileSlot{{Slot: "api_key", AllowedModes: []string{"write"}, AllowedTargets: []string{"https://api.example.com"}}}}
		reqs := []UnboundedRequirement{{Slot: "api_key", Mode: "read", Target: "https://api.example.com", Required: true}}
		m := scoreProfileMatch(profile, reqs)
		if len(m.MissingRequirements) != 1 || !strings.Contains(m.MissingRequirements[0], ":mode") {
			t.Fatalf("expected mode mismatch, got %v", m.MissingRequirements)
		}
	})

	t.Run("target mismatch", func(t *testing.T) {
		profile := vault.UnboundedProfile{ProfileID: "p1", Slots: []vault.UnboundedProfileSlot{{Slot: "api_key", AllowedModes: []string{"read"}, AllowedTargets: []string{"https://internal.example.com"}}}}
		reqs := []UnboundedRequirement{{Slot: "api_key", Mode: "read", Target: "https://api.example.com", Required: true}}
		m := scoreProfileMatch(profile, reqs)
		if len(m.MissingRequirements) != 1 || !strings.Contains(m.MissingRequirements[0], ":target") {
			t.Fatalf("expected target mismatch, got %v", m.MissingRequirements)
		}
	})

	t.Run("intent and type mismatch", func(t *testing.T) {
		profile := vault.UnboundedProfile{ProfileID: "p1", Slots: []vault.UnboundedProfileSlot{{
			Slot:                "api_key",
			AllowedIntents:      []string{"http.auth"},
			ExpectedSecretTypes: []string{"api-key"},
			AllowedModes:        []string{"read"},
			AllowedTargets:      []string{"https://api.example.com"},
		}}}
		reqs := []UnboundedRequirement{{
			Slot:                "api_key",
			Intent:              "billing",
			ExpectedSecretTypes: []string{"oauth"},
			Mode:                "read",
			Target:              "https://api.example.com",
			Required:            true,
		}}
		m := scoreProfileMatch(profile, reqs)
		if len(m.MissingRequirements) != 1 || !strings.Contains(m.MissingRequirements[0], ":intent") {
			t.Fatalf("expected intent mismatch to fail first, got %v", m.MissingRequirements)
		}
	})
}

func TestBuildMinimalProfileDeterministic(t *testing.T) {
	reqsA := []UnboundedRequirement{
		{Slot: "auth", Intent: "http.auth", ExpectedSecretTypes: []string{"api-key", "api-key"}, Mode: "read", Target: "https://b.example.com", Required: true},
		{Slot: "auth", Intent: "http.auth", ExpectedSecretTypes: []string{"api-key"}, Mode: "read", Target: "https://a.example.com", Required: true},
		{Slot: "token", Intent: "http.auth", ExpectedSecretTypes: []string{"oauth"}, Mode: "read", Target: "https://a.example.com", Required: true},
	}
	reqsB := []UnboundedRequirement{reqsA[2], reqsA[1], reqsA[0]}

	pa := BuildMinimalProfile(reqsA)
	pb := BuildMinimalProfile(reqsB)

	if pa.ProfileID != pb.ProfileID {
		t.Fatalf("expected stable profile id, got %s vs %s", pa.ProfileID, pb.ProfileID)
	}
	if !strings.HasPrefix(pa.ProfileID, "mcp.auto.") {
		t.Fatalf("expected mcp.auto prefix, got %s", pa.ProfileID)
	}
	if len(pa.Slots) != 2 {
		t.Fatalf("expected minimal 2 slots, got %d", len(pa.Slots))
	}
	if pa.Slots[0].Slot != "auth" || pa.Slots[1].Slot != "token" {
		t.Fatalf("expected deterministic slot ordering, got %+v", pa.Slots)
	}
	if got := strings.Join(pa.Slots[0].AllowedTargets, ","); got != "https://a.example.com,https://b.example.com" {
		t.Fatalf("expected sorted deduped targets, got %s", got)
	}
}

func TestResolveUnboundedProfileTieBreakAndAutoCreateIdempotency(t *testing.T) {
	reqs := []UnboundedRequirement{{Slot: "api_key", Intent: "http.auth", ExpectedSecretTypes: []string{"api-key"}, Mode: "read", Target: "https://api.example.com", Required: true}}

	t.Run("tie-break lexicographic profile id", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/list":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"profile_id": "zz-last"}, {"profile_id": "aa-first"}}})
			case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/get":
				id := r.URL.Query().Get("profile_id")
				_ = json.NewEncoder(w).Encode(map[string]any{"profile": map[string]any{
					"profile_id":   id,
					"connector_id": "generic.http",
					"verb":         "generic.http.request.v1",
					"slots": []map[string]any{{
						"slot":                  "api_key",
						"allowed_intents":       []string{"http.auth"},
						"expected_secret_types": []string{"api-key"},
						"allowed_modes":         []string{"read"},
						"allowed_targets":       []string{"https://api.example.com"},
					}},
				}})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
		res, err := ResolveUnboundedProfile(context.Background(), vc, reqs, false)
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}
		if res.ProfileID != "aa-first" {
			t.Fatalf("expected lexicographic tie-break winner aa-first, got %s", res.ProfileID)
		}
	})

	t.Run("auto-create uses deterministic idempotency key", func(t *testing.T) {
		expectedKey := "mcp-unbounded-profile-" + requirementsHash(reqs)
		var gotKey string
		created := BuildMinimalProfile(reqs)

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/list":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			case r.Method == http.MethodPost && r.URL.Path == "/v0/connectors/unbounded/profiles/upsert":
				gotKey = r.Header.Get("Idempotency-Key")
				_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
			case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/get":
				_ = json.NewEncoder(w).Encode(map[string]any{"profile": created})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
		res, err := ResolveUnboundedProfile(context.Background(), vc, reqs, true)
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}
		if !res.Created {
			t.Fatalf("expected profile to be created")
		}
		if gotKey != expectedKey {
			t.Fatalf("expected idempotency key %s, got %s", expectedKey, gotKey)
		}
		if res.ProfileID != created.ProfileID {
			t.Fatalf("expected profile_id %s, got %s", created.ProfileID, res.ProfileID)
		}
	})

	t.Run("auto-create missing scope returns MCP_UNBOUNDED_PROFILE_REQUIRED", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/list":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
			case r.Method == http.MethodPost && r.URL.Path == "/v0/connectors/unbounded/profiles/upsert":
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "INSUFFICIENT_SCOPE", "message": "missing scope"}})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
		_, err := ResolveUnboundedProfile(context.Background(), vc, reqs, true)
		if err == nil {
			t.Fatalf("expected error")
		}
		oe, ok := err.(*OrchestrationError)
		if !ok {
			t.Fatalf("expected OrchestrationError, got %T", err)
		}
		if oe.Code != "MCP_UNBOUNDED_PROFILE_REQUIRED" {
			t.Fatalf("expected MCP_UNBOUNDED_PROFILE_REQUIRED, got %s", oe.Code)
		}
	})
}
