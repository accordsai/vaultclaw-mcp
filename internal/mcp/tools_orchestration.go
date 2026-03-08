package mcp

import (
	"context"
	"fmt"

	"accords-mcp/internal/orchestration"
)

func (s *Server) registerOrchestrationTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_unbounded_profile_resolve",
		Description: "Resolve or auto-create a compatible unbounded profile for explicit requirements.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"requirements": map[string]any{"type": "array"},
				"auto_create":  map[string]any{"type": "boolean"},
			},
			"required": []string{"requirements"},
		},
		Handler: s.handleUnboundedProfileResolve,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_plan_unbounded_profile_preview",
		Description: "Preview step-level unbounded profile resolution outcomes for a plan.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"plan":       map[string]any{"type": "object"},
				"plan_input": map[string]any{},
			},
			"required": []string{"plan"},
		},
		Handler: s.handlePlanUnboundedProfilePreview,
	})
}

func (s *Server) handleUnboundedProfileResolve(ctx context.Context, args map[string]any) (map[string]any, error) {
	reqs, err := parseRequirements(anySliceArg(args, "requirements"))
	if err != nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", err.Error(), false, "", map[string]any{}), nil
	}
	autoCreate := boolArg(args, "auto_create", true)

	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	resolved, err := orchestration.ResolveUnboundedProfile(ctx, c, reqs, autoCreate)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"profile_id":    resolved.ProfileID,
		"created":       resolved.Created,
		"profile_match": resolved.Match,
		"profile":       resolved.Profile,
	}, nil), nil
}

func (s *Server) handlePlanUnboundedProfilePreview(ctx context.Context, args map[string]any) (map[string]any, error) {
	plan := mapArg(args, "plan")
	if plan == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "plan is required", false, "", map[string]any{}), nil
	}
	planInput := args["plan_input"]
	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	opts := orchestration.OrchestrationOptions{UnboundedProfiles: true, AutoCreateProfiles: false}
	transformed, resolutions, err := orchestration.PreflightPlan(ctx, c, plan, planInput, opts, false)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"resolutions":      resolutions,
		"transformed_plan": transformed,
	}, nil), nil
}

func parseRequirements(raw []any) ([]orchestration.UnboundedRequirement, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("requirements must be a non-empty array")
	}
	out := make([]orchestration.UnboundedRequirement, 0, len(raw))
	for i, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("requirements[%d] must be object", i)
		}
		r := orchestration.UnboundedRequirement{
			Slot:     strArg(m, "slot"),
			Intent:   strArg(m, "intent"),
			Mode:     strArg(m, "mode"),
			Target:   strArg(m, "target"),
			Required: boolArg(m, "required", true),
		}
		if est, ok := m["expected_secret_types"].([]any); ok {
			for _, v := range est {
				if s, ok := v.(string); ok {
					r.ExpectedSecretTypes = append(r.ExpectedSecretTypes, s)
				}
			}
		}
		out = append(out, r)
	}
	return orchestration.NormalizeRequirements(out), nil
}
