package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"accords-mcp/internal/vault"
)

func TestNormalizeWaitOptions(t *testing.T) {
	norm := NormalizeWaitOptions(WaitOptions{TimeoutMS: 1, PollIntervalMS: 1})
	if norm.TimeoutMS != 1000 || norm.PollIntervalMS != 250 {
		t.Fatalf("unexpected clamps: %+v", norm)
	}
	norm = NormalizeWaitOptions(WaitOptions{TimeoutMS: 9_999_999, PollIntervalMS: 50_000})
	if norm.TimeoutMS != 3_600_000 || norm.PollIntervalMS != 10_000 {
		t.Fatalf("unexpected max clamps: %+v", norm)
	}
}

func TestWaitForApprovalJobTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/jobs/job_1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"job": map[string]any{
			"job_id": "job_1",
			"status": "PENDING",
			"error":  map[string]any{"code": "CONNECTOR_APPROVAL_DECISION_REQUIRED"},
		}})
	}))
	defer ts.Close()

	vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
	_, err := WaitForApproval(context.Background(), vc, ApprovalHandle{Kind: ApprovalKindJob, JobID: "job_1"}, WaitOptions{TimeoutMS: 1000, PollIntervalMS: 250})
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	oe, ok := err.(*OrchestrationError)
	if !ok {
		t.Fatalf("expected OrchestrationError, got %T", err)
	}
	if oe.Code != "MCP_WAIT_TIMEOUT" || !oe.Retry {
		t.Fatalf("unexpected timeout error: %+v", oe)
	}
}

func TestWaitForApprovalJobSuccess(t *testing.T) {
	var calls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/jobs/job_1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cur := atomic.AddInt32(&calls, 1)
		status := "PENDING"
		errorCode := "CONNECTOR_APPROVAL_DECISION_REQUIRED"
		if cur >= 3 {
			status = "SUCCEEDED"
			errorCode = ""
		}
		job := map[string]any{"job_id": "job_1", "status": status}
		if errorCode != "" {
			job["error"] = map[string]any{"code": errorCode}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"job": job})
	}))
	defer ts.Close()

	vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
	res, err := WaitForApproval(context.Background(), vc, ApprovalHandle{Kind: ApprovalKindJob, JobID: "job_1"}, WaitOptions{TimeoutMS: 4000, PollIntervalMS: 200})
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if !res.Done || res.TerminalStatus != "SUCCEEDED" || res.DecisionOutcome != DecisionOutcomeAllow {
		t.Fatalf("unexpected wait result: %+v", res)
	}
}

func TestWaitForApprovalPlanRunDenied(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/plans/runs/run_1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"run": map[string]any{
				"run_id":          "run_1",
				"job_id":          "job_1",
				"state":           "DENIED",
				"last_error_code": "PLAN_APPROVAL_DENIED",
			},
			"job": map[string]any{
				"job_id": "job_1",
				"status": "DENIED",
				"error":  map[string]any{"code": "PLAN_APPROVAL_DENIED"},
			},
		})
	}))
	defer ts.Close()

	vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
	res, err := WaitForApproval(context.Background(), vc, ApprovalHandle{Kind: ApprovalKindPlanRun, RunID: "run_1"}, WaitOptions{TimeoutMS: 3000, PollIntervalMS: 250})
	if err != nil {
		t.Fatalf("wait failed: %v", err)
	}
	if !res.Done || res.TerminalStatus != "DENIED" || res.DecisionOutcome != DecisionOutcomeDeny {
		t.Fatalf("unexpected denied result: %+v", res)
	}
}
