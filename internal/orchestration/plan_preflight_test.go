package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"accords-mcp/internal/vault"
)

func TestPreflightPlanStaticRequestResolved(t *testing.T) {
	plan := map[string]any{
		"steps": []any{
			map[string]any{
				"step_id":      "http-1",
				"connector_id": "generic.http",
				"verb":         "generic.http.request.v1",
				"request_base": map[string]any{
					"secret_attachments": []any{map[string]any{
						"slot":                  "api_key",
						"intent":                "http.auth",
						"expected_secret_types": []any{"api-key"},
						"mode":                  "read",
						"target":                "https://api.example.com",
					}},
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"profile_id": "profile-1"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/get":
			_ = json.NewEncoder(w).Encode(map[string]any{"profile": map[string]any{
				"profile_id":   "profile-1",
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
	outPlan, resolutions, err := PreflightPlan(context.Background(), vc, plan, nil, OrchestrationOptions{UnboundedProfiles: true, AutoCreateProfiles: false}, true)
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if len(resolutions) != 1 {
		t.Fatalf("expected one resolution, got %d", len(resolutions))
	}
	if resolutions[0].Status != "RESOLVED" {
		t.Fatalf("expected RESOLVED, got %s", resolutions[0].Status)
	}
	steps := outPlan["steps"].([]any)
	step := steps[0].(map[string]any)
	requestBase := step["request_base"].(map[string]any)
	if got := requestBase["profile_id"]; got != "profile-1" {
		t.Fatalf("expected injected profile_id profile-1, got %v", got)
	}
}

func TestPreflightPlanUnresolvedBindingStrict(t *testing.T) {
	plan := map[string]any{
		"steps": []any{
			map[string]any{
				"step_id":      "http-1",
				"connector_id": "generic.http",
				"verb":         "generic.http.request.v1",
				"request_base": map[string]any{
					"secret_attachments": []any{map[string]any{
						"slot":   "",
						"mode":   "read",
						"target": "https://api.example.com",
					}},
				},
				"request_bindings": []any{map[string]any{
					"path": "/secret_attachments/0/slot",
					"ref": map[string]any{
						"source": "plan_input",
						"path":   "/http/slot",
					},
				}},
			},
		},
	}

	_, resolutions, err := PreflightPlan(context.Background(), nil, plan, map[string]any{}, OrchestrationOptions{UnboundedProfiles: true, AutoCreateProfiles: true}, true)
	if err == nil {
		t.Fatalf("expected unresolved precheck error")
	}
	oe, ok := err.(*OrchestrationError)
	if !ok {
		t.Fatalf("expected OrchestrationError, got %T", err)
	}
	if oe.Code != "MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED" {
		t.Fatalf("expected MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED, got %s", oe.Code)
	}
	if len(resolutions) != 1 || resolutions[0].Status != "UNRESOLVED" {
		t.Fatalf("expected unresolved resolution entry, got %+v", resolutions)
	}
	required, _ := oe.Details["required_plan_input_paths"].([]string)
	if len(required) != 1 || required[0] != "/http/slot" {
		t.Fatalf("expected required plan input /http/slot, got %v", required)
	}
}

func TestPreflightPlanMixedSteps(t *testing.T) {
	plan := map[string]any{
		"steps": []any{
			map[string]any{"step_id": "noop-1", "connector_id": "other.connector", "verb": "x"},
			map[string]any{
				"step_id":      "http-1",
				"connector_id": "generic.http",
				"verb":         "generic.http.request.v1",
				"request_base": map[string]any{
					"secret_attachments": []any{map[string]any{
						"slot":   "auth",
						"mode":   "read",
						"target": "https://example.com",
					}},
				},
			},
		},
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"profile_id": "p1"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/connectors/unbounded/profiles/get":
			_ = json.NewEncoder(w).Encode(map[string]any{"profile": map[string]any{
				"profile_id":   "p1",
				"connector_id": "generic.http",
				"verb":         "generic.http.request.v1",
				"slots": []map[string]any{{
					"slot":            "auth",
					"allowed_modes":   []string{"read"},
					"allowed_targets": []string{"https://example.com"},
				}},
			}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
	outPlan, resolutions, err := PreflightPlan(context.Background(), vc, plan, nil, OrchestrationOptions{UnboundedProfiles: true, AutoCreateProfiles: false}, true)
	if err != nil {
		t.Fatalf("preflight failed: %v", err)
	}
	if len(resolutions) != 1 {
		t.Fatalf("expected only generic.http step to be resolved, got %d", len(resolutions))
	}
	steps := outPlan["steps"].([]any)
	if steps[0].(map[string]any)["step_id"] != "noop-1" {
		t.Fatalf("expected non-http step unchanged")
	}
}
