package orchestration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"accords-mcp/internal/vault"
)

type WaitOptions struct {
	TimeoutMS      int `json:"timeout_ms"`
	PollIntervalMS int `json:"poll_interval_ms"`
}

type WaitResult struct {
	Done            bool           `json:"done"`
	TerminalStatus  string         `json:"terminal_status,omitempty"`
	DecisionOutcome string         `json:"decision_outcome"`
	Handle          ApprovalHandle `json:"handle"`
	Job             map[string]any `json:"job,omitempty"`
	Run             map[string]any `json:"run,omitempty"`
}

func NormalizeWaitOptions(opts WaitOptions) WaitOptions {
	timeout := opts.TimeoutMS
	if timeout <= 0 {
		timeout = 600000
	}
	if timeout < 1000 {
		timeout = 1000
	}
	if timeout > 3600000 {
		timeout = 3600000
	}
	poll := opts.PollIntervalMS
	if poll <= 0 {
		poll = 1500
	}
	if poll < 250 {
		poll = 250
	}
	if poll > 10000 {
		poll = 10000
	}
	return WaitOptions{TimeoutMS: timeout, PollIntervalMS: poll}
}

func WaitForApproval(ctx context.Context, vc *vault.Client, handle ApprovalHandle, opts WaitOptions) (WaitResult, error) {
	if vc == nil {
		return WaitResult{}, &OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "vault client is required", Details: map[string]any{}}
	}
	handle.Kind = strings.ToUpper(strings.TrimSpace(handle.Kind))
	switch handle.Kind {
	case ApprovalKindJob:
		if strings.TrimSpace(handle.JobID) == "" {
			return WaitResult{}, &OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "handle.job_id is required for JOB wait", Details: map[string]any{}}
		}
	case ApprovalKindPlanRun:
		if strings.TrimSpace(handle.RunID) == "" {
			return WaitResult{}, &OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "handle.run_id is required for PLAN_RUN wait", Details: map[string]any{}}
		}
	default:
		return WaitResult{}, &OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "handle.kind must be JOB or PLAN_RUN", Details: map[string]any{}}
	}

	opts = NormalizeWaitOptions(opts)
	deadline := time.Now().Add(time.Duration(opts.TimeoutMS) * time.Millisecond)
	interval := time.Duration(opts.PollIntervalMS) * time.Millisecond

	out := WaitResult{
		Done:            false,
		DecisionOutcome: DecisionOutcomePending,
		Handle:          handle,
	}

	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}

		switch handle.Kind {
		case ApprovalKindJob:
			res, err := vc.Get(ctx, "/v0/jobs/"+strings.TrimSpace(handle.JobID), nil)
			if err != nil {
				return out, err
			}
			jobSnap, err := vault.DecodeJobSnapshot(res.Body)
			if err != nil {
				return out, err
			}
			out.Job = jobSnap.Raw
			out.DecisionOutcome = DecisionOutcomeFromJob(jobSnap.Raw)
			if IsTerminalJobStatus(jobSnap.Status) {
				out.Done = true
				out.TerminalStatus = strings.ToUpper(strings.TrimSpace(jobSnap.Status))
				if out.DecisionOutcome == DecisionOutcomePending {
					out.DecisionOutcome = DecisionOutcomeUnknown
				}
				return out, nil
			}
		case ApprovalKindPlanRun:
			res, err := vc.Get(ctx, "/v0/connectors/plans/runs/"+strings.TrimSpace(handle.RunID), nil)
			if err != nil {
				return out, err
			}
			runSnap, err := vault.DecodePlanRunSnapshot(res.Body)
			if err != nil {
				return out, err
			}
			out.Run = runSnap.Run
			out.Job = runSnap.Job
			out.DecisionOutcome = DecisionOutcomeFromPlanRun(runSnap.Run, runSnap.Job)
			if IsTerminalPlanRunState(runSnap.State) {
				out.Done = true
				out.TerminalStatus = strings.ToUpper(strings.TrimSpace(runSnap.State))
				if out.DecisionOutcome == DecisionOutcomePending {
					out.DecisionOutcome = DecisionOutcomeUnknown
				}
				return out, nil
			}
		}

		if time.Now().After(deadline) {
			return out, &OrchestrationError{
				Code:    "MCP_WAIT_TIMEOUT",
				Message: "timed out while waiting for approval resolution",
				Retry:   true,
				Details: map[string]any{
					"handle": out.Handle,
					"latest": map[string]any{
						"job": out.Job,
						"run": out.Run,
					},
					"retry_hint": "Call vaultclaw_approval_wait again with the same handle.",
					"wait_options": map[string]any{
						"timeout_ms":       opts.TimeoutMS,
						"poll_interval_ms": opts.PollIntervalMS,
					},
				},
			}
		}
		if err := sleepWithContext(ctx, interval); err != nil {
			return out, fmt.Errorf("approval wait interrupted: %w", err)
		}
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
