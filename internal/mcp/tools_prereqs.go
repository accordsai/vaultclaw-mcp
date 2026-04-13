package mcp

import (
	"context"
	"strings"
)

func (s *Server) registerPrereqTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_slot_bindings_list",
		Description: "List connector secret slot bindings.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"connector_id":    map[string]any{"type": "string"},
				"verb":            map[string]any{"type": "string"},
				"include_revoked": map[string]any{"type": "boolean"},
			},
			"required": []string{"connector_id", "verb"},
		},
		Handler: s.handleSlotBindingsList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_slot_bind",
		Description: "Bind a secret id to connector verb slot.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"connector_id": map[string]any{"type": "string"},
				"verb":         map[string]any{"type": "string"},
				"slot":         map[string]any{"type": "string"},
				"secret_id":    map[string]any{"type": "string"},
			},
			"required": []string{"connector_id", "verb", "slot", "secret_id"},
		},
		Handler: s.handleSlotBind,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_unbounded_profiles_list",
		Description: "List unbounded profiles.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"connector_id":    map[string]any{"type": "string"},
				"verb":            map[string]any{"type": "string"},
				"include_revoked": map[string]any{"type": "boolean"},
			},
		},
		Handler: s.handleUnboundedProfilesList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_unbounded_profile_get",
		Description: "Get one unbounded profile.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"profile_id": map[string]any{"type": "string"}},
			"required":   []string{"profile_id"},
		},
		Handler: s.handleUnboundedProfileGet,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_unbounded_profile_upsert",
		Description: "Upsert an unbounded profile object.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"profile": map[string]any{"type": "object"}},
			"required":   []string{"profile"},
		},
		Handler: s.handleUnboundedProfileUpsert,
	})
}

func (s *Server) handleSlotBindingsList(ctx context.Context, args map[string]any) (map[string]any, error) {
	connectorID := strings.TrimSpace(strArg(args, "connector_id"))
	verb := strings.TrimSpace(strArg(args, "verb"))
	if connectorID == "" || verb == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "connector_id and verb are required", false, "", map[string]any{}), nil
	}
	q := map[string]string{"connector_id": connectorID, "verb": verb}
	if boolArg(args, "include_revoked", false) {
		q["include_revoked"] = "true"
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors/secrets/slots/bindings", q)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleSlotBind(ctx context.Context, args map[string]any) (map[string]any, error) {
	connectorID := strings.TrimSpace(strArg(args, "connector_id"))
	verb := strings.TrimSpace(strArg(args, "verb"))
	slot := strings.TrimSpace(strArg(args, "slot"))
	secretID := strings.TrimSpace(strArg(args, "secret_id"))
	if connectorID == "" || verb == "" || slot == "" || secretID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "connector_id, verb, slot, secret_id are required", false, "", map[string]any{}), nil
	}
	body := map[string]any{"connector_id": connectorID, "verb": verb, "slot": slot, "secret_id": secretID}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Post(ctx, "/v0/connectors/secrets/slots/bind", body, true)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleUnboundedProfilesList(ctx context.Context, args map[string]any) (map[string]any, error) {
	connectorID := strings.TrimSpace(strArg(args, "connector_id"))
	verb := strings.TrimSpace(strArg(args, "verb"))
	q := map[string]string{}
	if connectorID != "" {
		q["connector_id"] = connectorID
	}
	if verb != "" {
		q["verb"] = verb
	}
	if boolArg(args, "include_revoked", false) {
		q["include_revoked"] = "true"
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors/unbounded/profiles/list", q)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleUnboundedProfileGet(ctx context.Context, args map[string]any) (map[string]any, error) {
	profileID := strings.TrimSpace(strArg(args, "profile_id"))
	if profileID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "profile_id is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors/unbounded/profiles/get", map[string]string{"profile_id": profileID})
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleUnboundedProfileUpsert(ctx context.Context, args map[string]any) (map[string]any, error) {
	profile := mapArg(args, "profile")
	if profile == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "profile is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}
	res, err := c.Post(ctx, "/v0/connectors/unbounded/profiles/upsert", map[string]any{"profile": profile}, true)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}
