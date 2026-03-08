package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"accords-mcp/internal/orchestration"
	"accords-mcp/internal/vault"
)

func newTestVaultClient(t *testing.T, baseURL string) *vault.Client {
	t.Helper()
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")
	return vault.NewClient(vault.Config{BaseURL: baseURL, Token: "t"})
}

func TestShouldAutoModuleHash(t *testing.T) {
	t.Run("google gmail bounded", func(t *testing.T) {
		ok, reason := shouldAutoModuleHash("google", "google.gmail.send.v1", map[string]any{}, nil)
		if !ok || reason != "google_gmail_bounded" {
			t.Fatalf("expected google gmail auto module hash, got ok=%v reason=%s", ok, reason)
		}
	})

	t.Run("legacy secret fields", func(t *testing.T) {
		ok, reason := shouldAutoModuleHash(
			"generic.http",
			"generic.http.request.v1",
			map[string]any{"token_secret_id": "sec_123"},
			nil,
		)
		if !ok || reason != "legacy_secret_binding_fields" {
			t.Fatalf("expected legacy fields auto module hash, got ok=%v reason=%s", ok, reason)
		}
	})

	t.Run("legacy binding paths", func(t *testing.T) {
		ok, reason := shouldAutoModuleHash(
			"generic.http",
			"generic.http.request.v1",
			map[string]any{},
			[]any{
				map[string]any{"path": "/required_secret_ids/0"},
			},
		)
		if !ok || reason != "legacy_secret_binding_paths" {
			t.Fatalf("expected legacy binding auto module hash, got ok=%v reason=%s", ok, reason)
		}
	})

	t.Run("no module hash required", func(t *testing.T) {
		ok, reason := shouldAutoModuleHash("generic.http", "generic.http.request.v1", map[string]any{}, nil)
		if ok || reason != "" {
			t.Fatalf("expected no module hash requirement, got ok=%v reason=%s", ok, reason)
		}
	})
}

func TestApplyExecuteRequestInjectsAndCachesPolicyHash(t *testing.T) {
	var getCount atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/google" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		getCount.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "google",
			"policy_hash": "ph_google",
		})
	}))
	defer ts.Close()

	resolver := newModuleHashResolver(newTestVaultClient(t, ts.URL))
	req := map[string]any{
		"connector_id": "google",
		"verb":         "google.gmail.send.v1",
		"request":      map[string]any{"to": "person@example.com"},
	}
	preflight, err := resolver.applyExecuteRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("applyExecuteRequest failed: %v", err)
	}
	if strVal(req["module_hash"]) != "ph_google" {
		t.Fatalf("expected module_hash=ph_google, got %v", req["module_hash"])
	}
	if applied, _ := preflight["applied"].(bool); !applied {
		t.Fatalf("expected preflight applied, got: %v", preflight)
	}

	req2 := map[string]any{
		"connector_id": "google",
		"verb":         "google.gmail.get.v1",
		"request":      map[string]any{"id": "m_1"},
	}
	if _, err := resolver.applyExecuteRequest(context.Background(), req2); err != nil {
		t.Fatalf("second applyExecuteRequest failed: %v", err)
	}
	if getCount.Load() != 1 {
		t.Fatalf("expected one connector lookup due to cache, got %d", getCount.Load())
	}
}

func TestApplyExecuteRequestDefaultsGmailMessagesListQueryAST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/google" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "google",
			"policy_hash": "ph_google",
		})
	}))
	defer ts.Close()

	resolver := newModuleHashResolver(newTestVaultClient(t, ts.URL))
	req := map[string]any{
		"connector_id": "google",
		"verb":         "google.gmail.messages.list",
		"request":      map[string]any{"user_id": "me", "page_limit": 1},
	}
	if _, err := resolver.applyExecuteRequest(context.Background(), req); err != nil {
		t.Fatalf("applyExecuteRequest failed: %v", err)
	}
	queryAST, _ := req["query_ast_v1"].(map[string]any)
	if queryAST == nil {
		t.Fatalf("expected query_ast_v1 default to be injected")
	}
	assertGmailInboxQueryAST(t, queryAST)
}

func TestApplyExecuteRequestPreservesExistingGmailMessagesListQueryAST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/google" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "google",
			"policy_hash": "ph_google",
		})
	}))
	defer ts.Close()

	resolver := newModuleHashResolver(newTestVaultClient(t, ts.URL))
	req := map[string]any{
		"connector_id": "google",
		"verb":         "google.gmail.messages.list",
		"request":      map[string]any{"user_id": "me", "page_limit": 1},
		"query_ast_v1": map[string]any{
			"pred": map[string]any{
				"field": "from",
				"op":    "eq",
				"value": map[string]any{
					"kind":   "string",
					"string": "alerts@example.com",
				},
			},
		},
	}
	if _, err := resolver.applyExecuteRequest(context.Background(), req); err != nil {
		t.Fatalf("applyExecuteRequest failed: %v", err)
	}
	queryAST, _ := req["query_ast_v1"].(map[string]any)
	pred, _ := queryAST["pred"].(map[string]any)
	if strings.TrimSpace(strVal(pred["field"])) != "from" {
		t.Fatalf("expected existing query_ast_v1 to be preserved, got: %v", queryAST)
	}
}

func TestApplyExecuteRequestMissingPolicyHashReturnsError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/google" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "google"})
	}))
	defer ts.Close()

	resolver := newModuleHashResolver(newTestVaultClient(t, ts.URL))
	_, err := resolver.applyExecuteRequest(context.Background(), map[string]any{
		"connector_id": "google",
		"verb":         "google.gmail.send.v1",
		"request":      map[string]any{},
	})
	if err == nil {
		t.Fatalf("expected error when policy_hash is missing")
	}
	var orchErr *orchestration.OrchestrationError
	if !errors.As(err, &orchErr) {
		t.Fatalf("expected orchestration error, got %T", err)
	}
	if orchErr.Code != "MCP_MODULE_HASH_REQUIRED" {
		t.Fatalf("unexpected code: %s", orchErr.Code)
	}
}

func TestHandleConnectorExecuteInjectsModuleHash(t *testing.T) {
	postedModuleHash := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/connectors/google":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "google",
				"policy_hash": "ph_google",
			})
		case "/v0/connectors/validate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"validated": true,
			})
		case "/v0/connectors/execute":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			postedModuleHash = strings.TrimSpace(strVal(body["module_hash"]))
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "SUCCEEDED"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleConnectorExecute(context.Background(), map[string]any{
		"request": map[string]any{
			"connector_id": "google",
			"verb":         "google.gmail.send.v1",
			"request":      map[string]any{"to": "person@example.com"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success, got: %v", resp)
	}
	if postedModuleHash != "ph_google" {
		t.Fatalf("expected posted module_hash=ph_google, got %s", postedModuleHash)
	}
}

func TestHandlePlanExecuteInjectsModuleHash(t *testing.T) {
	postedModuleHash := ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/connectors/google":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":          "google",
				"policy_hash": "ph_google",
			})
		case "/v0/connectors/plans/execute":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			plan, _ := body["plan"].(map[string]any)
			steps, _ := plan["steps"].([]any)
			if len(steps) > 0 {
				step, _ := steps[0].(map[string]any)
				requestBase, _ := step["request_base"].(map[string]any)
				postedModuleHash = strings.TrimSpace(strVal(requestBase["module_hash"]))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"run_id": "run_1", "state": "SUCCEEDED"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handlePlanExecute(context.Background(), map[string]any{
		"plan": map[string]any{
			"steps": []any{
				map[string]any{
					"step_id":      "s1",
					"connector_id": "google",
					"verb":         "google.gmail.send.v1",
					"request_base": map[string]any{"to": "person@example.com"},
				},
			},
		},
		"orchestration": map[string]any{
			"unbounded_profiles": false,
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success, got: %v", resp)
	}
	if postedModuleHash != "ph_google" {
		t.Fatalf("expected posted module_hash=ph_google, got %s", postedModuleHash)
	}
}

func TestApplyPlanDefaultsGmailMessagesListQueryAST(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/google" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          "google",
			"policy_hash": "ph_google",
		})
	}))
	defer ts.Close()

	resolver := newModuleHashResolver(newTestVaultClient(t, ts.URL))
	plan := map[string]any{
		"steps": []any{
			map[string]any{
				"step_id":      "s_list",
				"connector_id": "google",
				"verb":         "google.gmail.messages.list",
				"request_base": map[string]any{"user_id": "me", "page_limit": 1},
			},
		},
	}
	if _, err := resolver.applyPlan(context.Background(), plan); err != nil {
		t.Fatalf("applyPlan failed: %v", err)
	}
	steps, _ := plan["steps"].([]any)
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	step, _ := steps[0].(map[string]any)
	queryAST, _ := step["query_ast_base"].(map[string]any)
	if queryAST == nil {
		t.Fatalf("expected query_ast_base default in plan step")
	}
	assertGmailInboxQueryAST(t, queryAST)
}

func TestFailureFromErrorModuleHashCategory(t *testing.T) {
	resp := failureFromError(&orchestration.OrchestrationError{
		Code:    "MCP_MODULE_HASH_REQUIRED",
		Message: "module hash is required",
		Details: map[string]any{},
	})
	errObj, _ := resp["error"].(map[string]any)
	if category, _ := errObj["category"].(string); category != "policy" {
		t.Fatalf("expected policy category, got %q", category)
	}
}

func assertGmailInboxQueryAST(t *testing.T, ast map[string]any) {
	t.Helper()
	pred, _ := ast["pred"].(map[string]any)
	if pred == nil {
		t.Fatalf("expected pred root in query AST, got: %v", ast)
	}
	if strings.TrimSpace(strVal(pred["field"])) != "label" || strings.TrimSpace(strVal(pred["op"])) != "eq" {
		t.Fatalf("unexpected predicate in query AST: %v", pred)
	}
	value, _ := pred["value"].(map[string]any)
	if value == nil {
		t.Fatalf("expected predicate value in query AST: %v", pred)
	}
	if strings.TrimSpace(strVal(value["kind"])) != "enum" || strings.TrimSpace(strVal(value["enum"])) != "inbox" {
		t.Fatalf("unexpected query AST enum literal: %v", value)
	}
}
