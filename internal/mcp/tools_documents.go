package mcp

import (
	"context"
	"strings"
)

func (s *Server) registerDocumentTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_document_types_suggest",
		Description: "Suggest Vaultclaw document types for free-form document phrases.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type": "string",
				},
				"top_k": map[string]any{
					"type": "integer",
				},
			},
			"required": []string{"query"},
		},
		Handler: s.handleDocumentTypesSuggest,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_document_types_latest",
		Description: "Resolve the latest active document binding by document type id and optional subject id.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"type_id": map[string]any{
					"type": "string",
				},
				"subject_id": map[string]any{
					"type": "string",
				},
			},
			"required": []string{"type_id"},
		},
		Handler: s.handleDocumentTypesLatest,
	})
}

func (s *Server) handleDocumentTypesSuggest(ctx context.Context, args map[string]any) (map[string]any, error) {
	query := strings.TrimSpace(strArg(args, "query"))
	if query == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "query is required", false, "", map[string]any{}), nil
	}
	topK := intArg(args, "top_k")
	if topK <= 0 {
		topK = 5
	}
	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	res, err := c.Post(ctx, "/v0/docs/types/suggest", map[string]any{
		"query": query,
		"top_k": topK,
	}, false)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}

func (s *Server) handleDocumentTypesLatest(ctx context.Context, args map[string]any) (map[string]any, error) {
	typeID := strings.TrimSpace(strArg(args, "type_id"))
	if typeID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "type_id is required", false, "", map[string]any{}), nil
	}
	subjectID := strings.TrimSpace(strArg(args, "subject_id"))
	if subjectID == "" {
		subjectID = "self"
	}
	c, _, fail, ok := s.configuredClient()
	if !ok {
		return fail, nil
	}
	res, err := c.Get(ctx, "/v0/docs/types/latest", map[string]string{
		"type_id":    typeID,
		"subject_id": subjectID,
	})
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(res.Body, map[string]any{"request_id": res.RequestID, "vault_http_status": res.StatusCode}), nil
}
