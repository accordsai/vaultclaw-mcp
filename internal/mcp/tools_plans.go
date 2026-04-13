package mcp

import (
	"context"
	"strings"

	"accords-mcp/internal/orchestration"
	"accords-mcp/internal/vault"
)

func (s *Server) registerPlanTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_plan_validate",
		Description: "Validate a connector execution plan.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"plan": map[string]any{"type": "object"}},
			"required":   []string{"plan"},
		},
		Handler: s.handlePlanValidate,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_plan_execute",
		Description: "Execute a connector plan with optional unbounded profile orchestration.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan":          map[string]any{"type": "object"},
				"plan_input":    map[string]any{},
				"orchestration": map[string]any{"type": "object"},
			},
			"required": []string{"plan"},
		},
		Handler: s.handlePlanExecute,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_plan_run_get",
		Description: "Get plan run status by run id.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"run_id": map[string]any{"type": "string"}},
			"required":   []string{"run_id"},
		},
		Handler: s.handlePlanRunGet,
	})
}

func (s *Server) handlePlanValidate(ctx context.Context, args map[string]any) (map[string]any, error) {
	plan := mapArg(args, "plan")
	if plan == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "plan is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	if _, err := newModuleHashResolver(c).applyPlan(ctx, plan); err != nil {
		return failureFromError(err), nil
	}
	res, err := c.Post(ctx, "/v0/connectors/plans/validate", map[string]any{"plan": plan}, true)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handlePlanExecute(ctx context.Context, args map[string]any) (map[string]any, error) {
	plan := mapArg(args, "plan")
	if plan == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "plan is required", false, "", map[string]any{}), nil
	}
	planInput, hasPlanInput := args["plan_input"]
	orchRaw := mapArg(args, "orchestration")
	opts := orchestration.OrchestrationOptions{
		UnboundedProfiles:  boolArg(orchRaw, "unbounded_profiles", true),
		AutoCreateProfiles: boolArg(orchRaw, "auto_create_profiles", true),
	}

	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	preflightOut := map[string]any{"orchestrated": false}
	if opts.UnboundedProfiles {
		prePlan, resolutions, err := orchestration.PreflightPlan(ctx, c, plan, planInput, opts, true)
		if err != nil {
			return failureFromError(err), nil
		}
		plan = prePlan
		preflightOut = map[string]any{"orchestrated": true, "resolutions": resolutions}
	}
	moduleHashPreflight, err := newModuleHashResolver(c).applyPlan(ctx, plan)
	if err != nil {
		return failureFromError(err), nil
	}
	preflightOut["module_hash"] = moduleHashPreflight

	body := map[string]any{"plan": plan}
	if hasPlanInput {
		body["plan_input"] = planInput
	}
	if analysis, ok := args["analysis"].(bool); ok {
		body["analysis"] = analysis
	}
	if v := strings.TrimSpace(strArg(args, "analysis_api_key_secret_id")); v != "" {
		body["analysis_api_key_secret_id"] = v
	}
	if v := strings.TrimSpace(strArg(args, "analysis_model")); v != "" {
		body["analysis_model"] = v
	}

	res, err := c.Post(ctx, "/v0/connectors/plans/execute", body, true)
	if err != nil {
		return failureFromError(err), nil
	}
	if handoff, ok := orchestration.ExtractPlanExecuteApprovalHandoff(res); ok {
		if strings.TrimSpace(handoff.Handle.PendingID) == "" {
			pendingID, resolved, resolveErr := orchestration.ResolvePendingIDByChallengeAndJob(ctx, c, handoff.Handle.ChallengeID, handoff.Handle.JobID)
			if resolveErr != nil {
				return failureFromError(resolveErr), nil
			}
			if resolved {
				handoff.Handle.PendingID = pendingID
			}
		}
		return failureFromError(orchestration.BuildApprovalRequiredError(handoff)), nil
	}
	return envelopeSuccess(map[string]any{"response": res.Body, "preflight": preflightOut, "plan": plan}, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handlePlanRunGet(ctx context.Context, args map[string]any) (map[string]any, error) {
	runID := strings.TrimSpace(strArg(args, "run_id"))
	if runID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "run_id is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors/plans/runs/"+runID, nil)
	if err != nil {
		return failureFromError(err), nil
	}
	snap, decErr := vault.DecodePlanRunSnapshot(res.Body)
	if decErr != nil {
		return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
	}
	outcome := orchestration.DecisionOutcomeFromPlanRun(snap.Run, snap.Job)
	state := "UNKNOWN"
	if orchestration.IsTerminalPlanRunState(snap.State) {
		state = orchestration.ApprovalStateTerminal
	} else if outcome == orchestration.DecisionOutcomePending {
		state = orchestration.ApprovalStatePending
	}
	data := cloneMap(res.Body)
	data["decision_outcome"] = outcome
	data["approval_state"] = map[string]any{
		"status":           state,
		"decision_outcome": outcome,
	}
	return envelopeSuccess(data, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}
