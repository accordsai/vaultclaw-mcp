package mcp

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"accords-mcp/internal/orchestration"
	"accords-mcp/internal/vault"
)

var allowedPendingStates = map[string]struct{}{
	"WAITING":   {},
	"READY":     {},
	"RUNNING":   {},
	"SUCCEEDED": {},
	"DENIED":    {},
	"FAILED":    {},
	"EXPIRED":   {},
}

func (s *Server) registerApprovalTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_job_get",
		Description: "Get Vaultclaw job status by job id with computed approval decision outcome.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"job_id": map[string]any{"type": "string"}},
			"required":   []string{"job_id"},
		},
		Handler: s.handleJobGet,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_approvals_pending_list",
		Description: "List read-only connector approval pending items.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"states":       map[string]any{},
				"limit":        map[string]any{"type": "integer"},
				"connector_id": map[string]any{"type": "string"},
				"verb":         map[string]any{"type": "string"},
				"agent_id":     map[string]any{"type": "string"},
				"challenge_id": map[string]any{"type": "string"},
			},
		},
		Handler: s.handleApprovalsPendingList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_approvals_pending_get",
		Description: "Get one pending approval item by challenge_id + pending_id.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"challenge_id": map[string]any{"type": "string"},
				"pending_id":   map[string]any{"type": "string"},
			},
			"required": []string{"challenge_id", "pending_id"},
		},
		Handler: s.handleApprovalsPendingGet,
	})
}

func (s *Server) handleJobGet(ctx context.Context, args map[string]any) (map[string]any, error) {
	jobID := strings.TrimSpace(strArg(args, "job_id"))
	if jobID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "job_id is required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/jobs/"+jobID, nil)
	if err != nil {
		return failureFromError(err), nil
	}
	jobSnap, decErr := vault.DecodeJobSnapshot(res.Body)
	if decErr != nil {
		return envelopeFailure("MCP_INTERNAL", "internal", decErr.Error(), false, "", map[string]any{
			"request_id":        res.RequestID,
			"vault_http_status": res.StatusCode,
		}), nil
	}
	outcome := orchestration.DecisionOutcomeFromJob(jobSnap.Raw)
	state := "UNKNOWN"
	if orchestration.IsTerminalJobStatus(jobSnap.Status) {
		state = orchestration.ApprovalStateTerminal
	} else if outcome == orchestration.DecisionOutcomePending {
		state = orchestration.ApprovalStatePending
	}
	return envelopeSuccess(map[string]any{
		"job":              jobSnap.Raw,
		"decision_outcome": outcome,
		"approval_state": map[string]any{
			"status":           state,
			"decision_outcome": outcome,
			"pending_approval": jobSnap.PendingApproval,
		},
	}, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleApprovalsPendingList(ctx context.Context, args map[string]any) (map[string]any, error) {
	states, err := parsePendingStatesArg(args["states"])
	if err != nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", err.Error(), false, "", map[string]any{}), nil
	}
	query := map[string]string{
		"states": strings.Join(states, ","),
	}
	limit := intArg(args, "limit")
	if limit <= 0 {
		limit = 50
	}
	query["limit"] = strconv.Itoa(limit)
	if v := strings.TrimSpace(strArg(args, "connector_id")); v != "" {
		query["connector_id"] = v
	}
	if v := strings.TrimSpace(strArg(args, "verb")); v != "" {
		query["verb"] = v
	}
	if v := strings.TrimSpace(strArg(args, "agent_id")); v != "" {
		query["agent_id"] = v
	}
	if v := strings.TrimSpace(strArg(args, "challenge_id")); v != "" {
		query["challenge_id"] = v
	}
	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	res, getErr := c.Get(ctx, "/v0/connectors/approvals/pending", query)
	if getErr != nil {
		return failureFromError(getErr), nil
	}
	rows := vault.DecodePendingApprovalItems(res.Body)
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		obj := cloneMap(row.Raw)
		obj["decision_outcome"] = orchestration.DecisionOutcomeFromPending(row)
		enrichPendingAttestationFields(obj)
		items = append(items, obj)
	}
	return envelopeSuccess(map[string]any{"items": items}, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleApprovalsPendingGet(ctx context.Context, args map[string]any) (map[string]any, error) {
	challengeID := strings.TrimSpace(strArg(args, "challenge_id"))
	pendingID := strings.TrimSpace(strArg(args, "pending_id"))
	if challengeID == "" || pendingID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "challenge_id and pending_id are required", false, "", map[string]any{}), nil
	}
	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/connectors/approvals/pending", map[string]string{
		"challenge_id": challengeID,
		"states":       "WAITING,READY,RUNNING,SUCCEEDED,DENIED,FAILED,EXPIRED",
		"limit":        "200",
	})
	if err != nil {
		return failureFromError(err), nil
	}
	rows := vault.DecodePendingApprovalItems(res.Body)
	matches := make([]vault.PendingApprovalItem, 0)
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.PendingID), pendingID) {
			matches = append(matches, row)
		}
	}
	if len(matches) == 0 {
		return envelopeFailure("MCP_APPROVAL_PENDING_NOT_FOUND", "approval", "pending approval item not found for challenge_id + pending_id", false, "", map[string]any{
			"challenge_id": challengeID,
			"pending_id":   pendingID,
		}), nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].CreatedSeq != matches[j].CreatedSeq {
			return matches[i].CreatedSeq > matches[j].CreatedSeq
		}
		return matches[i].PendingID < matches[j].PendingID
	})
	item := cloneMap(matches[0].Raw)
	item["decision_outcome"] = orchestration.DecisionOutcomeFromPending(matches[0])
	enrichPendingAttestationFields(item)
	return envelopeSuccess(map[string]any{"item": item}, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func parsePendingStatesArg(raw any) ([]string, error) {
	if raw == nil {
		return []string{"WAITING", "READY", "RUNNING", "SUCCEEDED", "DENIED", "FAILED", "EXPIRED"}, nil
	}
	out := make([]string, 0)
	seen := map[string]struct{}{}
	appendState := func(v string) error {
		state := strings.ToUpper(strings.TrimSpace(v))
		if state == "" {
			return nil
		}
		if _, ok := allowedPendingStates[state]; !ok {
			return &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "states contains invalid value: " + state}
		}
		if _, ok := seen[state]; ok {
			return nil
		}
		seen[state] = struct{}{}
		out = append(out, state)
		return nil
	}

	switch v := raw.(type) {
	case string:
		for _, part := range strings.Split(v, ",") {
			if err := appendState(part); err != nil {
				return nil, err
			}
		}
	case []any:
		for _, item := range v {
			state, ok := item.(string)
			if !ok {
				return nil, &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "states array must contain strings"}
			}
			if err := appendState(state); err != nil {
				return nil, err
			}
		}
	default:
		return nil, &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "states must be CSV string or string array"}
	}
	if len(out) == 0 {
		return nil, &orchestration.OrchestrationError{Code: "MCP_VALIDATION_ERROR", Message: "states cannot be empty"}
	}
	return out, nil
}

func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func enrichPendingAttestationFields(item map[string]any) {
	if item == nil {
		return
	}
	url := strings.TrimSpace(strVal(item["remote_attestation_url"]))
	if url == "" {
		pending, _ := item["pending_approval"].(map[string]any)
		url = strings.TrimSpace(strVal(pending["remote_attestation_url"]))
	}
	if url == "" {
		return
	}
	item["remote_attestation_url"] = url
	item["remote_attestation_link_markdown"] = "[" + url + "](" + url + ")"
}
