package mcp

import (
	"context"
	"strings"

	"accords-mcp/internal/orchestration"
)

func (s *Server) registerWaitTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_approval_wait",
		Description: "Poll and resume after external human approval until terminal job/run state or timeout.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"handle": map[string]any{"type": "object"},
				"timeout_ms": map[string]any{
					"type": "integer",
				},
				"poll_interval_ms": map[string]any{
					"type": "integer",
				},
			},
			"required": []string{"handle"},
		},
		Handler: s.handleApprovalWait,
	})
}

func (s *Server) handleApprovalWait(ctx context.Context, args map[string]any) (map[string]any, error) {
	raw := mapArg(args, "handle")
	if raw == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "handle is required", false, "", map[string]any{}), nil
	}
	handle := orchestration.ApprovalHandle{
		Kind:        strings.ToUpper(strings.TrimSpace(strArg(raw, "kind"))),
		JobID:       strings.TrimSpace(strArg(raw, "job_id")),
		RunID:       strings.TrimSpace(strArg(raw, "run_id")),
		ChallengeID: strings.TrimSpace(strArg(raw, "challenge_id")),
		PendingID:   strings.TrimSpace(strArg(raw, "pending_id")),
	}
	waitOpts := orchestration.WaitOptions{
		TimeoutMS:      intArg(args, "timeout_ms"),
		PollIntervalMS: intArg(args, "poll_interval_ms"),
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := orchestration.WaitForApproval(ctx, c, handle, waitOpts)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"done":             res.Done,
		"terminal_status":  res.TerminalStatus,
		"decision_outcome": res.DecisionOutcome,
		"handle":           res.Handle,
		"job":              res.Job,
		"run":              res.Run,
	}, nil), nil
}
