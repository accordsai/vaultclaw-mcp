package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderTemplateResolved(t *testing.T) {
	store := newTestStore(t)
	required := true
	defaultRaw := rawJSON(t, `"GET"`)
	bundle := Bundle{
		Type:       BundleTypeV1,
		CookbookID: "render.http",
		Version:    "1.0.0",
		Title:      "Render",
		Entries: []Entry{
			{
				EntryID:   "tpl",
				EntryType: EntryTypeTemplateVerb,
				BaseRequest: map[string]any{
					"connector_id": "generic.http",
					"verb":         "generic.http.request.v1",
					"request": map[string]any{
						"headers": []any{map[string]any{"key": "x", "value": "v"}},
					},
				},
				Bindings: []Binding{
					{TargetPath: "/request/url", InputKey: "url", Required: &required},
					{TargetPath: "/request/method", InputKey: "method", DefaultRaw: &defaultRaw},
					{TargetPath: "/request/headers/1/key", InputKey: "h1_key", Required: &required},
					{TargetPath: "/request/headers/1/value", InputKey: "h1_val", Required: &required},
				},
			},
		},
	}
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	res, err := store.RenderTemplate("render.http", "tpl", "", map[string]any{
		"url":    "https://example.invalid",
		"h1_key": "authorization",
		"h1_val": "Bearer token",
	}, OutputKindAuto)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if got := stringValue(res.Rendered["connector_id"]); got != "generic.http" {
		t.Fatalf("unexpected connector_id: %v", got)
	}
	req := mustMap(t, res.Rendered["request"])
	if req["url"] != "https://example.invalid" {
		t.Fatalf("unexpected rendered url: %v", req["url"])
	}
	if req["method"] != "GET" {
		t.Fatalf("expected defaulted method GET, got %v", req["method"])
	}
	if len(res.UsedDefaults) != 1 {
		t.Fatalf("expected one used default, got %d", len(res.UsedDefaults))
	}
}

func TestRenderTemplateMissingRequired(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleBundle("1.0.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}
	_, err := store.RenderTemplate("net.http", "tpl_get", "", map[string]any{}, OutputKindAuto)
	if err == nil {
		t.Fatalf("expected unresolved input error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_TEMPLATE_RENDER_UNRESOLVED" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderTemplateOutputKindMismatch(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleBundle("1.0.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}
	_, err := store.RenderTemplate("net.http", "tpl_get", "", map[string]any{"url": "https://ok.invalid"}, OutputKindPlan)
	if err == nil {
		t.Fatalf("expected output kind mismatch error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_TEMPLATE_INPUT_INVALID" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderTemplatePlanMissingRequired(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleGmailTemplatePlanBundle(t), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	_, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_reply_and_triage_v1", "", map[string]any{
		"reply_to":  "requester@example.com",
		"thread_id": "thread_123",
	}, OutputKindAuto)
	if err == nil {
		t.Fatalf("expected unresolved input error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_TEMPLATE_RENDER_UNRESOLVED" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderTemplatePlanDefaultsAndSteps(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleGmailTemplatePlanBundle(t), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	res, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_reply_and_triage_v1", "", map[string]any{
		"reply_to":   "requester@example.com",
		"reply_body": "Resolved and archived.",
		"thread_id":  "thread_abc",
	}, OutputKindAuto)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if res.SourceRef.OutputKind != OutputKindPlan {
		t.Fatalf("expected output_kind PLAN, got %s", res.SourceRef.OutputKind)
	}
	if len(res.UsedDefaults) != 3 {
		t.Fatalf("expected three used defaults, got %d", len(res.UsedDefaults))
	}
	steps, ok := res.Rendered["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected two rendered steps, got: %#v", res.Rendered["steps"])
	}
	step0 := mustMap(t, steps[0])
	request0 := mustMap(t, step0["request_base"])
	if request0["subject"] != "Re:" {
		t.Fatalf("expected default reply subject, got %v", request0["subject"])
	}
	step1 := mustMap(t, steps[1])
	request1 := mustMap(t, step1["request_base"])
	addLabels, ok := request1["add_label_ids"].([]any)
	if !ok || len(addLabels) == 0 || addLabels[0] != "Label_TRIAGED" {
		t.Fatalf("expected default triage label id, got %#v", request1["add_label_ids"])
	}
}

func TestRenderTemplatePlanOutputKindMismatch(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleGmailTemplatePlanBundle(t), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	_, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_reply_and_triage_v1", "", map[string]any{
		"reply_to":   "requester@example.com",
		"reply_body": "Resolved and archived.",
		"thread_id":  "thread_abc",
	}, OutputKindVerbRequest)
	if err == nil {
		t.Fatalf("expected output kind mismatch error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_TEMPLATE_INPUT_INVALID" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderTemplatePlanRequiresNonEmptySteps(t *testing.T) {
	store := newTestStore(t)
	bundle := Bundle{
		Type:       BundleTypeV1,
		CookbookID: "google.workspace",
		Version:    "1.1.0",
		Title:      "Google Workspace Gmail Cookbook",
		Entries: []Entry{
			{
				EntryID:   "gmail_tpl_plan_empty_steps_v1",
				EntryType: EntryTypeTemplatePlan,
				BasePlan: map[string]any{
					"type":  "connector.execution.plan.v1",
					"steps": []any{},
				},
			},
		},
	}
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	_, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_empty_steps_v1", "", map[string]any{}, OutputKindAuto)
	if err == nil {
		t.Fatalf("expected rendered plan shape validation error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_TEMPLATE_INPUT_INVALID" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenderTemplateGoogleWorkspace120PlanDefaults(t *testing.T) {
	store := newTestStore(t)
	bundle := mustLoadRepoBundle(t, "google.workspace", "1.2.0")
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	res, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_send_email_v1", "1.2.0", map[string]any{
		"to":         []any{"recipient@example.com"},
		"subject":    "Status update",
		"text_plain": "Everything is green.",
	}, OutputKindAuto)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if res.SourceRef.OutputKind != OutputKindPlan {
		t.Fatalf("expected output_kind PLAN, got %s", res.SourceRef.OutputKind)
	}
	if len(res.UsedDefaults) != 3 {
		t.Fatalf("expected defaults for cc/bcc/text_html, got %d (%v)", len(res.UsedDefaults), res.UsedDefaults)
	}
	steps, ok := res.Rendered["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected two rendered steps, got %#v", res.Rendered["steps"])
	}
	step0 := mustMap(t, steps[0])
	req0 := mustMap(t, step0["request_base"])
	if _, ok := req0["cc"].([]any); !ok {
		t.Fatalf("expected defaulted cc array, got %#v", req0["cc"])
	}
	if _, ok := req0["bcc"].([]any); !ok {
		t.Fatalf("expected defaulted bcc array, got %#v", req0["bcc"])
	}
	if req0["text_html"] != "" {
		t.Fatalf("expected default empty text_html, got %#v", req0["text_html"])
	}
	if _, ok := req0["document_attachments"]; ok {
		t.Fatalf("did not expect document_attachments in 1.2.0 payload, got %#v", req0["document_attachments"])
	}
}

func TestRenderTemplateGoogleWorkspace130PlanDocumentAttachments(t *testing.T) {
	store := newTestStore(t)
	bundle := mustLoadRepoBundle(t, "google.workspace", "1.3.0")
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	res, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_send_email_v1", "1.3.0", map[string]any{
		"to":         []any{"recipient@example.com"},
		"subject":    "Passport attached",
		"text_plain": "Attached is my passport.",
		"document_attachments": []any{
			map[string]any{
				"type_id": "identity.passport",
			},
		},
	}, OutputKindAuto)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if res.SourceRef.OutputKind != OutputKindPlan {
		t.Fatalf("expected output_kind PLAN, got %s", res.SourceRef.OutputKind)
	}
	if len(res.UsedDefaults) != 3 {
		t.Fatalf("expected defaults for cc/bcc/text_html, got %d (%v)", len(res.UsedDefaults), res.UsedDefaults)
	}
	steps, ok := res.Rendered["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected two rendered steps, got %#v", res.Rendered["steps"])
	}
	step0 := mustMap(t, steps[0])
	req0 := mustMap(t, step0["request_base"])
	docAttachments, ok := req0["document_attachments"].([]any)
	if !ok || len(docAttachments) != 1 {
		t.Fatalf("expected one rendered document attachment, got %#v", req0["document_attachments"])
	}
	firstDoc := mustMap(t, docAttachments[0])
	if firstDoc["type_id"] != "identity.passport" {
		t.Fatalf("expected identity.passport type_id, got %#v", firstDoc["type_id"])
	}
}

func TestRenderTemplateGoogleWorkspace130PlanDocumentAttachmentsDefault(t *testing.T) {
	store := newTestStore(t)
	bundle := mustLoadRepoBundle(t, "google.workspace", "1.3.0")
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	res, err := store.RenderTemplate("google.workspace", "gmail_tpl_plan_send_email_v1", "1.3.0", map[string]any{
		"to":         []any{"recipient@example.com"},
		"subject":    "Status update",
		"text_plain": "Everything is green.",
	}, OutputKindAuto)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if len(res.UsedDefaults) != 4 {
		t.Fatalf("expected defaults for cc/bcc/text_html/document_attachments, got %d (%v)", len(res.UsedDefaults), res.UsedDefaults)
	}
	steps, ok := res.Rendered["steps"].([]any)
	if !ok || len(steps) != 2 {
		t.Fatalf("expected two rendered steps, got %#v", res.Rendered["steps"])
	}
	step0 := mustMap(t, steps[0])
	req0 := mustMap(t, step0["request_base"])
	docAttachments, ok := req0["document_attachments"].([]any)
	if !ok {
		t.Fatalf("expected defaulted document_attachments array, got %#v", req0["document_attachments"])
	}
	if len(docAttachments) != 0 {
		t.Fatalf("expected empty defaulted document_attachments, got %#v", docAttachments)
	}
}

func TestRenderTemplateGoogleWorkspace130DocumentAttachmentsAcrossDraftTemplates(t *testing.T) {
	store := newTestStore(t)
	bundle := mustLoadRepoBundle(t, "google.workspace", "1.3.0")
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	cases := []struct {
		templateID string
		inputs     map[string]any
	}{
		{
			templateID: "gmail_tpl_plan_send_email_v1",
			inputs: map[string]any{
				"to":         []any{"recipient@example.com"},
				"subject":    "Doc",
				"text_plain": "Attached",
			},
		},
		{
			templateID: "gmail_tpl_plan_create_draft_v1",
			inputs: map[string]any{
				"to":         []any{"recipient@example.com"},
				"subject":    "Doc",
				"text_plain": "Attached",
			},
		},
		{
			templateID: "gmail_tpl_plan_reply_in_thread_v1",
			inputs: map[string]any{
				"to":         []any{"recipient@example.com"},
				"thread_id":  "thread_123",
				"text_plain": "Attached",
			},
		},
	}
	for _, tc := range cases {
		in := map[string]any{
			"document_attachments": []any{
				map[string]any{"type_id": "identity.passport"},
			},
		}
		for k, v := range tc.inputs {
			in[k] = v
		}
		res, err := store.RenderTemplate("google.workspace", tc.templateID, "1.3.0", in, OutputKindAuto)
		if err != nil {
			t.Fatalf("RenderTemplate(%s) failed: %v", tc.templateID, err)
		}
		steps, ok := res.Rendered["steps"].([]any)
		if !ok || len(steps) == 0 {
			t.Fatalf("expected rendered steps for %s, got %#v", tc.templateID, res.Rendered["steps"])
		}
		step0 := mustMap(t, steps[0])
		req0 := mustMap(t, step0["request_base"])
		docs, ok := req0["document_attachments"].([]any)
		if !ok || len(docs) != 1 {
			t.Fatalf("expected rendered document_attachments for %s, got %#v", tc.templateID, req0["document_attachments"])
		}
		firstDoc := mustMap(t, docs[0])
		if firstDoc["type_id"] != "identity.passport" {
			t.Fatalf("expected identity.passport for %s, got %#v", tc.templateID, firstDoc["type_id"])
		}
	}
}

func TestRenderTemplateGoogleWorkspace130MultiStepGraphLinks(t *testing.T) {
	store := newTestStore(t)
	bundle := mustLoadRepoBundle(t, "google.workspace", "1.3.0")
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	cases := []struct {
		templateID       string
		inputs           map[string]any
		expectedStepID   string
		expectedNextStep string
	}{
		{
			templateID: "gmail_tpl_plan_send_email_v1",
			inputs: map[string]any{
				"to":         []any{"recipient@example.com"},
				"subject":    "Doc",
				"text_plain": "Attached",
			},
			expectedStepID:   "create_draft",
			expectedNextStep: "send_draft",
		},
		{
			templateID: "gmail_tpl_plan_reply_in_thread_v1",
			inputs: map[string]any{
				"to":         []any{"recipient@example.com"},
				"thread_id":  "thread_123",
				"text_plain": "Attached",
			},
			expectedStepID:   "create_reply_draft",
			expectedNextStep: "send_reply_draft",
		},
	}
	for _, tc := range cases {
		res, err := store.RenderTemplate("google.workspace", tc.templateID, "1.3.0", tc.inputs, OutputKindAuto)
		if err != nil {
			t.Fatalf("RenderTemplate(%s) failed: %v", tc.templateID, err)
		}
		steps, ok := res.Rendered["steps"].([]any)
		if !ok || len(steps) < 2 {
			t.Fatalf("expected multi-step rendered plan for %s, got %#v", tc.templateID, res.Rendered["steps"])
		}
		step0 := mustMap(t, steps[0])
		if step0["step_id"] != tc.expectedStepID {
			t.Fatalf("unexpected first step for %s: got=%#v want=%q", tc.templateID, step0["step_id"], tc.expectedStepID)
		}
		if step0["default_success_next_step_id"] != tc.expectedNextStep {
			t.Fatalf("expected default_success_next_step_id=%q for %s, got %#v", tc.expectedNextStep, tc.templateID, step0["default_success_next_step_id"])
		}
	}
}

func TestRenderTemplateGoogleWorkspace120VerbDefaultBinding(t *testing.T) {
	store := newTestStore(t)
	bundle := mustLoadRepoBundle(t, "google.workspace", "1.2.0")
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert bundle failed: %v", err)
	}

	res, err := store.RenderTemplate("google.workspace", "gmail_tpl_verb_messages_get_metadata_v1", "1.2.0", map[string]any{
		"message_id": "msg_123",
	}, OutputKindAuto)
	if err != nil {
		t.Fatalf("RenderTemplate failed: %v", err)
	}
	if res.SourceRef.OutputKind != OutputKindVerbRequest {
		t.Fatalf("expected output_kind VERB_REQUEST, got %s", res.SourceRef.OutputKind)
	}
	if len(res.UsedDefaults) != 1 {
		t.Fatalf("expected defaulted format binding, got %d (%v)", len(res.UsedDefaults), res.UsedDefaults)
	}
	req := mustMap(t, res.Rendered["request"])
	if req["message_id"] != "msg_123" {
		t.Fatalf("expected rendered message_id, got %#v", req["message_id"])
	}
	if req["format"] != "metadata" {
		t.Fatalf("expected default format=metadata, got %#v", req["format"])
	}
}

func sampleGmailTemplatePlanBundle(t *testing.T) Bundle {
	t.Helper()
	required := true
	optional := false
	defaultSubject := rawJSON(t, `"Re:"`)
	defaultTriage := rawJSON(t, `"Label_TRIAGED"`)
	defaultRemove := rawJSON(t, `"INBOX"`)
	return Bundle{
		Type:       BundleTypeV1,
		CookbookID: "google.workspace",
		Version:    "1.1.0",
		Title:      "Google Workspace Gmail Cookbook",
		Entries: []Entry{
			{
				EntryID:   "gmail_tpl_plan_reply_and_triage_v1",
				EntryType: EntryTypeTemplatePlan,
				BasePlan: map[string]any{
					"type":          "connector.execution.plan.v1",
					"start_step_id": "reply",
					"steps": []any{
						map[string]any{
							"step_id":        "reply",
							"connector_id":   "google",
							"verb":           "google.gmail.send.v1",
							"policy_version": "1",
							"request_base": map[string]any{
								"to":        []any{"placeholder@example.com"},
								"subject":   "Re:",
								"body_text": "",
								"thread_id": "thread_placeholder",
							},
						},
						map[string]any{
							"step_id":        "label",
							"connector_id":   "google",
							"verb":           "google.gmail.thread.modify.v1",
							"policy_version": "1",
							"request_base": map[string]any{
								"thread_id":        "thread_placeholder",
								"add_label_ids":    []any{"Label_TRIAGED"},
								"remove_label_ids": []any{"INBOX"},
							},
						},
					},
				},
				Bindings: []Binding{
					{TargetPath: "/steps/0/request_base/to/0", InputKey: "reply_to", Required: &required},
					{TargetPath: "/steps/0/request_base/subject", InputKey: "reply_subject", Required: &optional, DefaultRaw: &defaultSubject},
					{TargetPath: "/steps/0/request_base/body_text", InputKey: "reply_body", Required: &required},
					{TargetPath: "/steps/0/request_base/thread_id", InputKey: "thread_id", Required: &required},
					{TargetPath: "/steps/1/request_base/thread_id", InputKey: "thread_id", Required: &required},
					{TargetPath: "/steps/1/request_base/add_label_ids/0", InputKey: "triage_label_id", Required: &optional, DefaultRaw: &defaultTriage},
					{TargetPath: "/steps/1/request_base/remove_label_ids/0", InputKey: "remove_label_id", Required: &optional, DefaultRaw: &defaultRemove},
				},
			},
		},
	}
}

func rawJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	return json.RawMessage(value)
}

func mustMap(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("value is not map: %#v", v)
	}
	return m
}

func mustLoadRepoBundle(t *testing.T, cookbookID, version string) Bundle {
	t.Helper()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("unable to locate repository root: %v", err)
	}
	path := filepath.Join(repoRoot, "cookbooks", cookbookID, version+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundle %s: %v", path, err)
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("unmarshal bundle %s: %v", path, err)
	}
	bundle, err := DecodeBundle(payload)
	if err != nil {
		t.Fatalf("decode bundle %s: %v", path, err)
	}
	return bundle
}
