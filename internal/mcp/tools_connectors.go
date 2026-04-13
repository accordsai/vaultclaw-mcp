package mcp

import (
	"context"
	"strings"

	"accords-mcp/internal/orchestration"
	"accords-mcp/internal/vault"
)

func (s *Server) registerConnectorTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_connectors_list",
		Description: "List available connector policies from Vaultclaw.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:     s.handleConnectorsList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_connector_get",
		Description: "Get connector policy and approval metadata for a connector id.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"connector_id": map[string]any{"type": "string"}},
			"required":   []string{"connector_id"},
		},
		Handler: s.handleConnectorGet,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_connector_validate",
		Description: "Validate a connector request via /v0/connectors/validate before approval/execution.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"request": map[string]any{"type": "object"}},
			"required":   []string{"request"},
		},
		Handler: s.handleConnectorValidate,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_connector_execute",
		Description: "Execute a connector request via /v0/connectors/execute.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"request": map[string]any{"type": "object"}},
			"required":   []string{"request"},
		},
		Handler: s.handleConnectorExecute,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_connector_execute_job",
		Description: "Execute a connector request as a job with automatic unbounded profile orchestration.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request":       map[string]any{"type": "object"},
				"orchestration": map[string]any{"type": "object"},
			},
			"required": []string{"request"},
		},
		Handler: s.handleConnectorExecuteJob,
	})
}

func (s *Server) handleConnectorsList(ctx context.Context, _ map[string]any) (map[string]any, error) {
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors", nil)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleConnectorGet(ctx context.Context, args map[string]any) (map[string]any, error) {
	connectorID := strings.TrimSpace(strArg(args, "connector_id"))
	if connectorID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "connector_id is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors/"+connectorID, nil)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleConnectorValidate(ctx context.Context, args map[string]any) (map[string]any, error) {
	req := mapArg(args, "request")
	if req == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "request is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	moduleHashPreflight, err := newModuleHashResolver(c).applyExecuteRequest(ctx, req)
	if err != nil {
		return failureFromError(err), nil
	}
	res, err := s.validateConnectorRequest(ctx, c, req)
	if err != nil {
		return failureFromError(err), nil
	}
	data := map[string]any{
		"validation":  res.Body,
		"request":     req,
		"module_hash": moduleHashPreflight,
	}
	return envelopeSuccess(data, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleConnectorExecute(ctx context.Context, args map[string]any) (map[string]any, error) {
	req := mapArg(args, "request")
	if req == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "request is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	if _, err := newModuleHashResolver(c).applyExecuteRequest(ctx, req); err != nil {
		return failureFromError(err), nil
	}
	if _, err := s.validateConnectorRequest(ctx, c, req); err != nil {
		return failureFromError(err), nil
	}
	res, err := c.Post(ctx, "/v0/connectors/execute", req, true)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleConnectorExecuteJob(ctx context.Context, args map[string]any) (map[string]any, error) {
	req := mapArg(args, "request")
	if req == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "request is required", false, "", map[string]any{}), nil
	}
	orchRaw := mapArg(args, "orchestration")
	opts := orchestration.OrchestrationOptions{
		UnboundedProfiles:  boolArg(orchRaw, "unbounded_profiles", true),
		AutoCreateProfiles: boolArg(orchRaw, "auto_create_profiles", true),
	}

	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	preflight := map[string]any{"orchestrated": false}
	if opts.UnboundedProfiles {
		updated, pref, err := s.orchestrateExecuteJobRequest(ctx, c, req, opts)
		if err != nil {
			return failureFromError(err), nil
		}
		req = updated
		preflight = pref
	}
	moduleHashPreflight, err := newModuleHashResolver(c).applyExecuteRequest(ctx, req)
	if err != nil {
		return failureFromError(err), nil
	}
	preflight["module_hash"] = moduleHashPreflight
	validateRes, err := s.validateConnectorRequest(ctx, c, req)
	if err != nil {
		return failureFromError(err), nil
	}
	preflight["validation"] = validateRes.Body

	res, err := c.Post(ctx, "/v0/connectors/execute-job", req, true)
	if err != nil {
		return failureFromError(err), nil
	}
	if handoff, ok := orchestration.ExtractExecuteJobApprovalHandoff(res); ok {
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
	data := map[string]any{"response": res.Body, "preflight": preflight, "request": req}
	return envelopeSuccess(data, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) validateConnectorRequest(ctx context.Context, c *vault.Client, req map[string]any) (vault.APIResult, error) {
	return c.Post(ctx, "/v0/connectors/validate", req, false)
}

func (s *Server) orchestrateExecuteJobRequest(ctx context.Context, c *vault.Client, req map[string]any, opts orchestration.OrchestrationOptions) (map[string]any, map[string]any, error) {
	connectorID := strings.TrimSpace(strVal(req["connector_id"]))
	verb := strings.TrimSpace(strVal(req["verb"]))
	if connectorID != "generic.http" || verb != "generic.http.request.v1" {
		return req, map[string]any{"orchestrated": false, "reason": "connector is not generic.http.request.v1"}, nil
	}
	requestObj, _ := req["request"].(map[string]any)
	if requestObj == nil {
		return nil, nil, &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "request.request object is required"}
	}
	if strings.TrimSpace(strVal(requestObj["profile_id"])) != "" {
		return req, map[string]any{"orchestrated": true, "reason": "profile_id already provided", "profile_id": strings.TrimSpace(strVal(requestObj["profile_id"]))}, nil
	}
	reqs, err := orchestration.DeriveRequirementsFromRequest(requestObj)
	if err != nil {
		return nil, nil, err
	}
	if len(reqs) == 0 {
		return req, map[string]any{"orchestrated": true, "reason": "no unbounded requirements derived"}, nil
	}
	resolved, err := orchestration.ResolveUnboundedProfile(ctx, c, reqs, opts.AutoCreateProfiles)
	if err != nil {
		return nil, nil, err
	}
	requestObj["profile_id"] = resolved.ProfileID
	req["request"] = requestObj
	status := "resolved"
	if resolved.Created {
		status = "created"
	}
	return req, map[string]any{
		"orchestrated":  true,
		"status":        status,
		"profile_id":    resolved.ProfileID,
		"requirements":  reqs,
		"profile_match": resolved.Match,
	}, nil
}
