package mcp

import (
	"context"
	"strings"

	"accords-mcp/internal/orchestration"
	"accords-mcp/internal/vault"
)

type moduleHashResolver struct {
	client *vault.Client
	cache  map[string]string
}

func newModuleHashResolver(client *vault.Client) *moduleHashResolver {
	return &moduleHashResolver{
		client: client,
		cache:  map[string]string{},
	}
}

func (r *moduleHashResolver) applyExecuteRequest(ctx context.Context, req map[string]any) (map[string]any, error) {
	if req == nil {
		return nil, &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "request is required", Details: map[string]any{}}
	}
	ensureGoogleGmailMessagesListQueryAST(req, strings.TrimSpace(strVal(req["verb"])), "query_ast_v1")
	if strings.TrimSpace(strVal(req["module_hash"])) != "" {
		return map[string]any{"applied": false, "reason": "module_hash already provided"}, nil
	}
	connectorID := strings.TrimSpace(strVal(req["connector_id"]))
	verb := strings.TrimSpace(strVal(req["verb"]))
	if connectorID == "" || verb == "" {
		return map[string]any{"applied": false, "reason": "connector_id/verb missing"}, nil
	}
	requestObj, _ := req["request"].(map[string]any)
	need, reason := shouldAutoModuleHash(connectorID, verb, requestObj, nil)
	if !need {
		return map[string]any{"applied": false, "reason": "module_hash not required"}, nil
	}
	policyHash, err := r.policyHashForConnector(ctx, connectorID)
	if err != nil {
		return nil, err
	}
	req["module_hash"] = policyHash
	return map[string]any{
		"applied":      true,
		"reason":       reason,
		"connector_id": connectorID,
		"verb":         verb,
		"module_hash":  policyHash,
	}, nil
}

func (r *moduleHashResolver) applyPlan(_ context.Context, plan map[string]any) (map[string]any, error) {
	if plan == nil {
		return nil, &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "plan is required", Details: map[string]any{}}
	}
	stepsRaw, _ := plan["steps"].([]any)
	if len(stepsRaw) == 0 {
		return map[string]any{"applied_count": 0, "applied": []any{}}, nil
	}
	applied := make([]map[string]any, 0)
	for i, rawStep := range stepsRaw {
		step, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		connectorID := strings.TrimSpace(strVal(step["connector_id"]))
		verb := strings.TrimSpace(strVal(step["verb"]))
		if connectorID == "" || verb == "" {
			continue
		}
		requestBase, _ := step["request_base"].(map[string]any)
		if requestBase == nil {
			requestBase = map[string]any{}
		}
		queryASTBase, _ := step["query_ast_base"].(map[string]any)
		ensureGoogleGmailMessagesListQueryAST(step, verb, "query_ast_base")
		queryASTBase, _ = step["query_ast_base"].(map[string]any)
		step["request_base"] = requestBase
		if queryASTBase != nil {
			step["query_ast_base"] = queryASTBase
		}
		stepsRaw[i] = step
	}
	plan["steps"] = stepsRaw
	return map[string]any{
		"note":          "module_hash is not injected into plan step request payloads",
		"applied_count": len(applied),
		"applied":       applied,
	}, nil
}

func (r *moduleHashResolver) policyHashForConnector(ctx context.Context, connectorID string) (string, error) {
	connectorID = strings.TrimSpace(connectorID)
	if connectorID == "" {
		return "", &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "connector_id is required for module_hash derivation", Details: map[string]any{}}
	}
	if cached, ok := r.cache[connectorID]; ok && strings.TrimSpace(cached) != "" {
		return cached, nil
	}
	res, err := r.client.Get(ctx, "/v0/connectors/"+connectorID, nil)
	if err != nil {
		return "", err
	}
	policyHash := strings.TrimSpace(strVal(res.Body["policy_hash"]))
	if policyHash == "" {
		return "", &orchestration.OrchestrationError{
			Code:    "MCP_MODULE_HASH_REQUIRED",
			Message: "unable to derive module_hash: connector policy_hash missing",
			Details: map[string]any{
				"connector_id":      connectorID,
				"request_id":        res.RequestID,
				"vault_http_status": res.StatusCode,
			},
		}
	}
	r.cache[connectorID] = policyHash
	return policyHash, nil
}

func shouldAutoModuleHash(connectorID, verb string, requestObj map[string]any, bindings []any) (bool, string) {
	connectorID = strings.ToLower(strings.TrimSpace(connectorID))
	verb = strings.ToLower(strings.TrimSpace(verb))
	if connectorID == "google" && strings.HasPrefix(verb, "google.gmail.") {
		return true, "google_gmail_bounded"
	}
	if hasLegacySecretBindingFields(requestObj) {
		return true, "legacy_secret_binding_fields"
	}
	if bindingsIncludeLegacySecretBindingPaths(bindings) {
		return true, "legacy_secret_binding_paths"
	}
	return false, ""
}

func ensureGoogleGmailMessagesListQueryAST(target map[string]any, verb, key string) {
	if target == nil || !isGoogleGmailMessagesListVerb(verb) {
		return
	}
	if strings.TrimSpace(key) == "" {
		key = "query_ast_v1"
	}
	if ast, ok := target[key].(map[string]any); ok {
		if isValidQueryASTRoot(ast) {
			return
		}
	}
	target[key] = map[string]any{
		"pred": map[string]any{
			"field": "label",
			"op":    "eq",
			"value": map[string]any{
				"kind": "enum",
				"enum": "inbox",
			},
		},
	}
}

func isGoogleGmailMessagesListVerb(verb string) bool {
	normalized := strings.ToLower(strings.TrimSpace(verb))
	return normalized == "google.gmail.messages.list" || normalized == "google.gmail.messages.list.v1"
}

func isValidQueryASTRoot(ast map[string]any) bool {
	if ast == nil {
		return false
	}
	if pred, ok := ast["pred"].(map[string]any); ok && len(pred) > 0 {
		return true
	}
	if b, ok := ast["bool"].(map[string]any); ok && len(b) > 0 {
		return true
	}
	return false
}

func hasLegacySecretBindingFields(requestObj map[string]any) bool {
	if requestObj == nil {
		return false
	}
	if strings.TrimSpace(strVal(requestObj["token_secret_id"])) != "" {
		return true
	}
	switch ids := requestObj["required_secret_ids"].(type) {
	case []any:
		for _, raw := range ids {
			if strings.TrimSpace(strVal(raw)) != "" {
				return true
			}
		}
	case []string:
		for _, id := range ids {
			if strings.TrimSpace(id) != "" {
				return true
			}
		}
	}
	return false
}

func bindingsIncludeLegacySecretBindingPaths(bindings []any) bool {
	for _, raw := range bindings {
		b, _ := raw.(map[string]any)
		if b == nil {
			continue
		}
		path := strings.TrimSpace(strVal(b["path"]))
		if path == "" {
			continue
		}
		if path == "/token_secret_id" || strings.HasSuffix(path, "/token_secret_id") {
			return true
		}
		if strings.HasPrefix(path, "/required_secret_ids") || strings.Contains(path, "/required_secret_ids/") {
			return true
		}
	}
	return false
}

func asAnySlice(v any) []any {
	switch vv := v.(type) {
	case []any:
		return vv
	case []map[string]any:
		out := make([]any, 0, len(vv))
		for _, item := range vv {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}
