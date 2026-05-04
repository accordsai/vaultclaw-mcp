package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPassportEmailWorkflowHappyPath(t *testing.T) {
	var planExecuteCalls int
	var postedPlan map[string]any
	var postedPlanInput map[string]any

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/docs/types/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"type_id":     "identity.passport",
				"subject_id":  "self",
				"document_id": "doc_passport_1",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/connectors/validate":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"validated": true,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/connectors/plans/execute":
			planExecuteCalls++
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			postedPlan, _ = body["plan"].(map[string]any)
			postedPlanInput, _ = body["plan_input"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run_id": "run_1",
				"job_id": "job_1",
				"job": map[string]any{
					"status": "SUCCEEDED",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handlePassportEmailWorkflow(context.Background(), map[string]any{
		"request_text": "Send an email to recipient@example.com with a copy of my passport and include my passport fields in the email body.",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response, got: %v", resp)
	}
	if planExecuteCalls != 1 {
		t.Fatalf("expected one plan execute call, got %d", planExecuteCalls)
	}

	data := mustMap(t, resp["data"])
	if got := strVal(data["status"]); got != "SENT" {
		t.Fatalf("expected SENT status, got %q data=%v", got, data)
	}
	summary := mustMap(t, data["pre_send_summary"])
	if got := strVal(summary["recipient"]); got != "recipient@example.com" {
		t.Fatalf("unexpected summary recipient: %q", got)
	}

	assertPlanStepIDs(t, postedPlan, []string{
		"read_existing_profile",
		"extract_passport_fields",
		"read_after_extract_profile",
		"create_draft_from_existing",
		"send_draft_from_existing",
		"create_draft_after_extract",
		"send_draft_after_extract",
	})
	assertPlanVerbs(t, postedPlan, []string{
		"identity.profile.read.v1",
		"identity.extraction.run.v1",
		"identity.profile.read.v1",
		"google.gmail.drafts.create",
		"google.gmail.drafts.send",
		"google.gmail.drafts.create",
		"google.gmail.drafts.send",
	})

	planInputPassport := mustMap(t, mustMap(t, postedPlanInput)["passport"])
	if got := strVal(planInputPassport["document_id"]); got != "doc_passport_1" {
		t.Fatalf("unexpected passport document id in plan_input: %q", got)
	}
	if got := strVal(planInputPassport["subject_ref"]); got != "self" {
		t.Fatalf("unexpected passport subject_ref in plan_input: %q", got)
	}
	slots := mustSlice(t, planInputPassport["slots"])
	if len(slots) != len(passportEmailRequiredSlots) {
		t.Fatalf("unexpected slot count in plan_input: %v", slots)
	}
	assertIdentitySubjectRefs(t, postedPlan, "self")
}

func TestPassportEmailWorkflowApprovalRequiredReturnsSinglePlanApproval(t *testing.T) {
	var planExecuteCalls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/docs/types/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"document_id": "doc_passport_approval"})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/connectors/plans/execute":
			planExecuteCalls++
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"run_id": "run_plan_approval_1",
				"job_id": "job_plan_approval_1",
				"job": map[string]any{
					"job_id": "job_plan_approval_1",
					"status": "PENDING",
					"error": map[string]any{
						"code":    "PLAN_APPROVAL_REQUIRED",
						"message": "approval required",
					},
					"pending_approval": map[string]any{
						"pending_id": "apj_plan_approval_1",
						"challenge": map[string]any{
							"challenge_id": "ach_plan_approval_1",
						},
					},
				},
				"pending_approval": map[string]any{
					"pending_id": "apj_plan_approval_1",
					"challenge": map[string]any{
						"challenge_id": "ach_plan_approval_1",
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handlePassportEmailWorkflow(context.Background(), map[string]any{
		"request_text": "Send an email to recipient@example.com with a copy of my passport and include my passport fields in the email body.",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected failure response, got success: %v", resp)
	}
	if planExecuteCalls != 1 {
		t.Fatalf("expected one plan execute call, got %d", planExecuteCalls)
	}

	errObj := mustMap(t, resp["error"])
	if got := strVal(errObj["code"]); got != "MCP_APPROVAL_REQUIRED" {
		t.Fatalf("expected MCP_APPROVAL_REQUIRED, got %q", got)
	}
	details := mustMap(t, errObj["details"])
	approval := mustMap(t, details["approval"])
	if got := strVal(approval["kind"]); got != "PLAN_RUN" {
		t.Fatalf("expected approval kind PLAN_RUN, got %q", got)
	}
	if got := strVal(approval["job_id"]); got != "job_plan_approval_1" {
		t.Fatalf("unexpected approval job_id: %q", got)
	}
}

func TestPassportEmailWorkflowExecuteFalseReturnsReadyPlan(t *testing.T) {
	var planExecuteCalls int

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/docs/types/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"document_id": "doc_passport_preview"})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/connectors/plans/execute":
			planExecuteCalls++
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handlePassportEmailWorkflow(context.Background(), map[string]any{
		"request_text": "Send an email to recipient@example.com with a copy of my passport and include my passport fields in the email body.",
		"execute":      false,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response, got: %v", resp)
	}
	if planExecuteCalls != 0 {
		t.Fatalf("did not expect plan execution when execute=false")
	}

	data := mustMap(t, resp["data"])
	if got := strVal(data["status"]); got != "READY_TO_SEND" {
		t.Fatalf("expected READY_TO_SEND, got %q", got)
	}
	bodyTemplate := strVal(data["composed_text"])
	if !strings.Contains(bodyTemplate, "{{step_output:read_existing_profile:/values/given_name}}") {
		t.Fatalf("expected composed_text to keep runtime template placeholders, got %q", bodyTemplate)
	}
}

func TestPassportEmailWorkflowMissingPassportRequiresUpload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v0/docs/types/latest" {
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "DOCUMENT_SLOT_UNRESOLVED",
					"message": "no passport on file",
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handlePassportEmailWorkflow(context.Background(), map[string]any{
		"request_text": "Send an email to recipient@example.com with a copy of my passport and include my passport fields in the email body.",
		"execute":      false,
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected failure response, got success: %v", resp)
	}
	errObj := mustMap(t, resp["error"])
	if got := strVal(errObj["code"]); got != "MCP_DOCUMENT_UPLOAD_REQUIRED" {
		t.Fatalf("expected MCP_DOCUMENT_UPLOAD_REQUIRED, got %q", got)
	}
}

func assertPlanVerbs(t *testing.T, plan map[string]any, want []string) {
	t.Helper()
	steps := mustSlice(t, plan["steps"])
	got := make([]string, 0, len(steps))
	for _, raw := range steps {
		step := mustMap(t, raw)
		got = append(got, strVal(step["verb"]))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected step verbs: got=%v want=%v", got, want)
	}
}

func assertPlanStepIDs(t *testing.T, plan map[string]any, want []string) {
	t.Helper()
	steps := mustSlice(t, plan["steps"])
	got := make([]string, 0, len(steps))
	for _, raw := range steps {
		step := mustMap(t, raw)
		got = append(got, strVal(step["step_id"]))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected step ids: got=%v want=%v", got, want)
	}
}

func assertIdentitySubjectRefs(t *testing.T, plan map[string]any, want string) {
	t.Helper()
	steps := mustSlice(t, plan["steps"])
	for _, raw := range steps {
		step := mustMap(t, raw)
		if strVal(step["connector_id"]) != "identity" {
			continue
		}
		requestBase := mustMap(t, step["request_base"])
		if got := strVal(requestBase["subject_ref"]); got != want {
			t.Fatalf("identity step %q subject_ref=%q want=%q", strVal(step["step_id"]), got, want)
		}
	}
}

func mustMap(t *testing.T, raw any) map[string]any {
	t.Helper()
	m, _ := raw.(map[string]any)
	if m == nil {
		t.Fatalf("expected map[string]any, got %T (%v)", raw, raw)
	}
	return m
}

func mustSlice(t *testing.T, raw any) []any {
	t.Helper()
	switch typed := raw.(type) {
	case []any:
		return typed
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, row := range typed {
			out = append(out, row)
		}
		return out
	default:
		t.Fatalf("expected []any, got %T (%v)", raw, raw)
		return nil
	}
}
