package orchestration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"accords-mcp/internal/vault"
)

func TestExtractExecuteJobApprovalHandoff(t *testing.T) {
	res := vault.APIResult{
		StatusCode: 202,
		RequestID:  "req-1",
		Body: map[string]any{
			"job": map[string]any{
				"job_id":                     "job_1",
				"status":                     "PENDING",
				"pending_expires_at_unix_ms": float64(123456789),
				"error":                      map[string]any{"code": "CONNECTOR_APPROVAL_DECISION_REQUIRED"},
				"pending_approval": map[string]any{
					"challenge": map[string]any{"challenge_id": "ach_1"},
				},
			},
		},
	}
	handoff, ok := ExtractExecuteJobApprovalHandoff(res)
	if !ok {
		t.Fatalf("expected handoff")
	}
	if handoff.Handle.Kind != ApprovalKindJob || handoff.Handle.JobID != "job_1" || handoff.Handle.ChallengeID != "ach_1" {
		t.Fatalf("unexpected handle: %+v", handoff.Handle)
	}
	if handoff.State.Status != ApprovalStatePending || handoff.State.DecisionOutcome != DecisionOutcomePending {
		t.Fatalf("unexpected state: %+v", handoff.State)
	}
}

func TestExtractPlanExecuteApprovalHandoff(t *testing.T) {
	res := vault.APIResult{
		StatusCode: 202,
		RequestID:  "req-2",
		Body: map[string]any{
			"run_id": "run_1",
			"job": map[string]any{
				"job_id":                     "job_1",
				"status":                     "PENDING",
				"pending_expires_at_unix_ms": float64(444),
				"error":                      map[string]any{"code": "PLAN_APPROVAL_REQUIRED"},
				"pending_approval": map[string]any{
					"challenge": map[string]any{"challenge_id": "ach_plan"},
				},
			},
		},
	}
	handoff, ok := ExtractPlanExecuteApprovalHandoff(res)
	if !ok {
		t.Fatalf("expected plan handoff")
	}
	if handoff.Handle.Kind != ApprovalKindPlanRun || handoff.Handle.RunID != "run_1" || handoff.Handle.JobID != "job_1" {
		t.Fatalf("unexpected handle: %+v", handoff.Handle)
	}
	if handoff.Handle.ChallengeID != "ach_plan" {
		t.Fatalf("expected challenge id ach_plan, got %s", handoff.Handle.ChallengeID)
	}
}

func TestResolvePendingIDByChallengeAndJob(t *testing.T) {
	t.Run("deterministic tie-break", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/v0/connectors/approvals/pending" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"pending_id": "apj_old", "job_id": "job_1", "created_seq": float64(10)},
				{"pending_id": "apj_b", "job_id": "job_1", "created_seq": float64(12)},
				{"pending_id": "apj_a", "job_id": "job_1", "created_seq": float64(12)},
				{"pending_id": "apj_other", "job_id": "job_other", "created_seq": float64(99)},
			}})
		}))
		defer ts.Close()

		vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
		pendingID, ok, err := ResolvePendingIDByChallengeAndJob(context.Background(), vc, "ach_1", "job_1")
		if err != nil {
			t.Fatalf("resolve failed: %v", err)
		}
		if !ok || pendingID != "apj_a" {
			t.Fatalf("expected apj_a from tie-break, got ok=%v id=%s", ok, pendingID)
		}
	})

	t.Run("missing scope is non-fatal", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "INSUFFICIENT_SCOPE", "message": "forbidden"}})
		}))
		defer ts.Close()

		vc := vault.NewClient(vault.Config{BaseURL: ts.URL, Token: "t"})
		pendingID, ok, err := ResolvePendingIDByChallengeAndJob(context.Background(), vc, "ach_1", "job_1")
		if err != nil {
			t.Fatalf("expected non-fatal scope miss, got err=%v", err)
		}
		if ok || pendingID != "" {
			t.Fatalf("expected unresolved pending id on scope miss, got ok=%v id=%s", ok, pendingID)
		}
	})
}

func TestDecisionOutcomeProjection(t *testing.T) {
	jobPending := map[string]any{"status": "PENDING", "error": map[string]any{"code": "CONNECTOR_APPROVAL_DECISION_REQUIRED"}}
	if got := DecisionOutcomeFromJob(jobPending); got != DecisionOutcomePending {
		t.Fatalf("expected PENDING, got %s", got)
	}

	jobDenied := map[string]any{"status": "DENIED", "error": map[string]any{"code": "PLAN_APPROVAL_DENIED"}}
	if got := DecisionOutcomeFromJob(jobDenied); got != DecisionOutcomeDeny {
		t.Fatalf("expected DENY, got %s", got)
	}

	runSucceeded := map[string]any{"state": "SUCCEEDED", "last_error_code": ""}
	if got := DecisionOutcomeFromPlanRun(runSucceeded, nil); got != DecisionOutcomeAllow {
		t.Fatalf("expected ALLOW, got %s", got)
	}

	pending := vault.PendingApprovalItem{State: "WAITING"}
	if got := DecisionOutcomeFromPending(pending); got != DecisionOutcomePending {
		t.Fatalf("expected PENDING from WAITING, got %s", got)
	}
}

func TestBuildApprovalRequiredErrorIncludesAttestationLink(t *testing.T) {
	h := ApprovalHandoff{
		Handle: ApprovalHandle{
			Kind:        ApprovalKindJob,
			JobID:       "job_1",
			ChallengeID: "ach_1",
			PendingID:   "apj_1",
		},
		State: ApprovalState{
			Status:          ApprovalStatePending,
			DecisionOutcome: DecisionOutcomePending,
			PendingApproval: map[string]any{
				"remote_attestation_url": "https://alerts.accords.ai/a/req_1?t=abc",
			},
		},
		Vault: map[string]any{
			"request_id":        "req_1",
			"vault_http_status": float64(202),
		},
	}

	oe := BuildApprovalRequiredError(h)
	details, _ := oe.Details["approval"].(map[string]any)
	if details == nil {
		t.Fatalf("approval details missing: %v", oe.Details)
	}
	if got := strVal(details["remote_attestation_url"]); got != "https://alerts.accords.ai/a/req_1?t=abc" {
		t.Fatalf("unexpected remote_attestation_url=%q", got)
	}
	if got := strVal(details["remote_attestation_link_markdown"]); got != "[https://alerts.accords.ai/a/req_1?t=abc](https://alerts.accords.ai/a/req_1?t=abc)" {
		t.Fatalf("unexpected remote_attestation_link_markdown=%q", got)
	}
}
