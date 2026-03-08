package mcp

import (
	"accords-mcp/internal/catalog"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestCatalogToolRoundTrip(t *testing.T) {
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)

	bundle := map[string]any{
		"type":        "accords.cookbook.bundle.v1",
		"cookbook_id": "cb.http",
		"version":     "1.0.0",
		"title":       "HTTP",
		"entries": []any{
			map[string]any{
				"entry_id":       "tpl_get",
				"entry_type":     "template.verb.v1",
				"base_request":   map[string]any{"connector_id": "generic.http", "verb": "generic.http.request.v1", "request": map[string]any{"method": "GET", "url": "https://placeholder.invalid"}},
				"bindings":       []any{map[string]any{"target_path": "/request/url", "input_key": "url"}},
				"connector_id":   "generic.http",
				"verb":           "generic.http.request.v1",
				"policy_version": "1",
			},
		},
	}

	upsertResp, err := s.handleCookbookUpsert(context.Background(), map[string]any{"bundle": bundle})
	if err != nil {
		t.Fatalf("handleCookbookUpsert err: %v", err)
	}
	if ok, _ := upsertResp["ok"].(bool); !ok {
		t.Fatalf("expected upsert success, got: %v", upsertResp)
	}

	listResp, err := s.handleCookbooksList(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleCookbooksList err: %v", err)
	}
	if ok, _ := listResp["ok"].(bool); !ok {
		t.Fatalf("expected list success, got: %v", listResp)
	}

	renderResp, err := s.handleTemplateRender(context.Background(), map[string]any{
		"cookbook_id": "cb.http",
		"template_id": "tpl_get",
		"inputs":      map[string]any{"url": "https://example.invalid"},
	})
	if err != nil {
		t.Fatalf("handleTemplateRender err: %v", err)
	}
	if ok, _ := renderResp["ok"].(bool); !ok {
		t.Fatalf("expected render success, got: %v", renderResp)
	}
}

func TestCatalogToolRenderUnresolved(t *testing.T) {
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)

	bundle := map[string]any{
		"type":        "accords.cookbook.bundle.v1",
		"cookbook_id": "cb.http",
		"version":     "1.0.0",
		"title":       "HTTP",
		"entries": []any{
			map[string]any{
				"entry_id":     "tpl_get",
				"entry_type":   "template.verb.v1",
				"base_request": map[string]any{"connector_id": "generic.http", "verb": "generic.http.request.v1", "request": map[string]any{"method": "GET"}},
				"bindings":     []any{map[string]any{"target_path": "/request/url", "input_key": "url", "required": true}},
			},
		},
	}
	_, _ = s.handleCookbookUpsert(context.Background(), map[string]any{"bundle": bundle})

	renderResp, err := s.handleTemplateRender(context.Background(), map[string]any{
		"cookbook_id": "cb.http",
		"template_id": "tpl_get",
		"inputs":      map[string]any{},
	})
	if err != nil {
		t.Fatalf("handleTemplateRender err: %v", err)
	}
	if ok, _ := renderResp["ok"].(bool); ok {
		t.Fatalf("expected unresolved render failure, got success: %v", renderResp)
	}
	errObj, _ := renderResp["error"].(map[string]any)
	if errObj["code"] != "MCP_TEMPLATE_RENDER_UNRESOLVED" {
		t.Fatalf("unexpected error code: %v", errObj)
	}
}

func TestCatalogRemoteInstallTool(t *testing.T) {
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)

	bundle := map[string]any{
		"type":        "accords.cookbook.bundle.v1",
		"cookbook_id": "remote.cb",
		"version":     "1.0.0",
		"title":       "Remote",
		"entries": []any{
			map[string]any{
				"entry_id":   "recipe_1",
				"entry_type": "recipe.plan.v1",
				"plan":       map[string]any{"steps": []any{map[string]any{"step_id": "s1"}}},
			},
		},
	}
	rawBundle, _ := json.Marshal(bundle)
	sha := sha256HexForTest(rawBundle)

	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type":      "accords.cookbook.index.v1",
				"source_id": "remote",
				"items": []any{
					map[string]any{
						"cookbook_id":  "remote.cb",
						"version":      "1.0.0",
						"title":        "Remote",
						"download_url": ts.URL + "/bundle.json",
						"sha256":       sha,
					},
				},
			})
		case "/bundle.json":
			_, _ = w.Write(rawBundle)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	upsertSourceResp, err := s.handleCatalogSourceUpsert(context.Background(), map[string]any{
		"source": map[string]any{
			"source_id": "remote",
			"index_url": ts.URL + "/index.json",
			"enabled":   true,
			"auth_mode": "NONE",
		},
	})
	if err != nil {
		t.Fatalf("handleCatalogSourceUpsert err: %v", err)
	}
	if ok, _ := upsertSourceResp["ok"].(bool); !ok {
		t.Fatalf("expected source upsert success: %v", upsertSourceResp)
	}

	installResp, err := s.handleCookbookRemoteInstall(context.Background(), map[string]any{
		"source_id":   "remote",
		"cookbook_id": "remote.cb",
	})
	if err != nil {
		t.Fatalf("handleCookbookRemoteInstall err: %v", err)
	}
	if ok, _ := installResp["ok"].(bool); !ok {
		t.Fatalf("expected install success, got: %v", installResp)
	}
}

func TestCatalogToolRoundTripGmailBundle(t *testing.T) {
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)

	bundle := map[string]any{
		"type":        "accords.cookbook.bundle.v1",
		"cookbook_id": "google.workspace",
		"version":     "1.1.0",
		"title":       "Google Workspace Gmail Cookbook",
		"entries": []any{
			map[string]any{
				"entry_id":     "gmail_send_status_update_v1",
				"entry_type":   "recipe.verb.v1",
				"connector_id": "google",
				"verb":         "google.gmail.send.v1",
				"request":      map[string]any{"to": []any{"ops@example.com"}, "subject": "Daily Status"},
			},
			map[string]any{
				"entry_id":   "gmail_plan_triage_v1",
				"entry_type": "recipe.plan.v1",
				"plan": map[string]any{
					"type":          "connector.execution.plan.v1",
					"start_step_id": "s1",
					"steps": []any{
						map[string]any{
							"step_id":        "s1",
							"connector_id":   "google",
							"verb":           "google.gmail.thread.get.v1",
							"policy_version": "1",
							"request_base":   map[string]any{"thread_id": "thread_a"},
						},
					},
				},
			},
			map[string]any{
				"entry_id":   "gmail_tpl_plan_reply_and_triage_v1",
				"entry_type": "template.plan.v1",
				"base_plan": map[string]any{
					"type":          "connector.execution.plan.v1",
					"start_step_id": "reply",
					"steps": []any{
						map[string]any{
							"step_id":        "reply",
							"connector_id":   "google",
							"verb":           "google.gmail.send.v1",
							"policy_version": "1",
							"request_base":   map[string]any{"to": []any{"placeholder@example.com"}, "subject": "Re:", "body_text": "", "thread_id": "thread_placeholder"},
						},
						map[string]any{
							"step_id":        "label",
							"connector_id":   "google",
							"verb":           "google.gmail.thread.modify.v1",
							"policy_version": "1",
							"request_base":   map[string]any{"thread_id": "thread_placeholder", "add_label_ids": []any{"Label_TRIAGED"}, "remove_label_ids": []any{"INBOX"}},
						},
					},
				},
				"bindings": []any{
					map[string]any{"target_path": "/steps/0/request_base/to/0", "input_key": "reply_to", "required": true},
					map[string]any{"target_path": "/steps/0/request_base/body_text", "input_key": "reply_body", "required": true},
					map[string]any{"target_path": "/steps/0/request_base/thread_id", "input_key": "thread_id", "required": true},
					map[string]any{"target_path": "/steps/1/request_base/thread_id", "input_key": "thread_id", "required": true},
				},
			},
		},
	}

	upsertResp, err := s.handleCookbookUpsert(context.Background(), map[string]any{"bundle": bundle})
	if err != nil {
		t.Fatalf("handleCookbookUpsert err: %v", err)
	}
	if ok, _ := upsertResp["ok"].(bool); !ok {
		t.Fatalf("expected upsert success, got: %v", upsertResp)
	}

	getResp, err := s.handleCookbookGet(context.Background(), map[string]any{"cookbook_id": "google.workspace", "version": "1.1.0"})
	if err != nil {
		t.Fatalf("handleCookbookGet err: %v", err)
	}
	if ok, _ := getResp["ok"].(bool); !ok {
		t.Fatalf("expected get success, got: %v", getResp)
	}

	searchPlanResp, err := s.handleRecipesSearch(context.Background(), map[string]any{"entry_type": "recipe.plan.v1"})
	if err != nil {
		t.Fatalf("handleRecipesSearch err: %v", err)
	}
	searchPlanData, _ := searchPlanResp["data"].(map[string]any)
	if sliceLen(searchPlanData["items"]) == 0 {
		t.Fatalf("expected recipe.plan.v1 search results, got: %v", searchPlanResp)
	}

	searchTemplateResp, err := s.handleRecipesSearch(context.Background(), map[string]any{"entry_type": "template.plan.v1"})
	if err != nil {
		t.Fatalf("handleRecipesSearch err: %v", err)
	}
	searchTemplateData, _ := searchTemplateResp["data"].(map[string]any)
	if sliceLen(searchTemplateData["items"]) == 0 {
		t.Fatalf("expected template.plan.v1 search results, got: %v", searchTemplateResp)
	}

	renderResp, err := s.handleTemplateRender(context.Background(), map[string]any{
		"cookbook_id": "google.workspace",
		"template_id": "gmail_tpl_plan_reply_and_triage_v1",
		"inputs": map[string]any{
			"reply_to":   "requester@example.com",
			"reply_body": "Resolved and archived.",
			"thread_id":  "thread_a",
		},
		"output_kind": "PLAN",
	})
	if err != nil {
		t.Fatalf("handleTemplateRender err: %v", err)
	}
	if ok, _ := renderResp["ok"].(bool); !ok {
		t.Fatalf("expected render success, got: %v", renderResp)
	}
	renderData, _ := renderResp["data"].(map[string]any)
	rendered, _ := renderData["rendered"].(map[string]any)
	steps, _ := rendered["steps"].([]any)
	if len(steps) == 0 {
		t.Fatalf("expected rendered plan with non-empty steps, got: %v", rendered)
	}
	sourceKind := ""
	switch sourceRef := renderData["source_ref"].(type) {
	case map[string]any:
		sourceKind = strings.TrimSpace(strVal(sourceRef["output_kind"]))
	case catalog.SourceRef:
		sourceKind = strings.TrimSpace(sourceRef.OutputKind)
	}
	if sourceKind != "PLAN" {
		t.Fatalf("expected source_ref.output_kind=PLAN, got: %v", renderData["source_ref"])
	}
}

func sha256HexForTest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}

func sliceLen(v any) int {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return 0
	}
	if rv.Kind() != reflect.Slice {
		return 0
	}
	return rv.Len()
}
