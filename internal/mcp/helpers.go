package mcp

import (
	"errors"
	"strings"

	"accords-mcp/internal/catalog"
	"accords-mcp/internal/orchestration"
	"accords-mcp/internal/vault"
)

func failureFromError(err error) map[string]any {
	if err == nil {
		return envelopeFailure("MCP_INTERNAL", "internal", "unknown error", false, "", map[string]any{})
	}
	var orchErr *orchestration.OrchestrationError
	if errors.As(err, &orchErr) {
		return envelopeFailure(orchErr.Code, inferCategoryFromCode(orchErr.Code), orchErr.Message, orchErr.Retry, "", orchErr.Details)
	}
	var catErr *catalog.Error
	if errors.As(err, &catErr) {
		code := strings.TrimSpace(catErr.Code)
		if code == "" {
			code = "MCP_INTERNAL"
		}
		category := strings.TrimSpace(catErr.Category)
		if category == "" {
			category = inferCategoryFromCode(code)
		}
		return envelopeFailure(code, category, catErr.Message, catErr.Retryable, "", catErr.Details)
	}
	norm := vault.NormalizeError(err)
	if norm.Code == "" {
		norm.Code = "MCP_INTERNAL"
	}
	if norm.Category == "" {
		norm.Category = "internal"
	}
	return envelopeFailure(norm.Code, norm.Category, norm.Message, norm.Retryable, norm.VaultCode, norm.Details)
}

func inferCategoryFromCode(code string) string {
	switch code {
	case "MCP_UNBOUNDED_PROFILE_REQUIRED", "MCP_UNBOUNDED_PROFILE_INVALID":
		return "secrets"
	case "MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED":
		return "plans"
	case "MCP_APPROVAL_REQUIRED", "MCP_WAIT_TIMEOUT", "MCP_APPROVAL_PENDING_NOT_FOUND":
		return "approval"
	case "MCP_MODULE_HASH_REQUIRED":
		return "policy"
	case "MCP_CATALOG_NOT_FOUND", "MCP_CATALOG_CONFLICT", "MCP_CATALOG_SCHEMA_INVALID",
		"MCP_TEMPLATE_NOT_FOUND", "MCP_TEMPLATE_RENDER_UNRESOLVED", "MCP_TEMPLATE_INPUT_INVALID",
		"MCP_CATALOG_SOURCE_NOT_FOUND", "MCP_CATALOG_REMOTE_CHECKSUM_MISMATCH":
		return "validation"
	case "MCP_CATALOG_REMOTE_FETCH_FAILED":
		return "network"
	case "MCP_VALIDATION_ERROR":
		return "validation"
	default:
		return "validation"
	}
}

func mapArg(args map[string]any, key string) map[string]any {
	m, _ := args[key].(map[string]any)
	return m
}

func anySliceArg(args map[string]any, key string) []any {
	v, _ := args[key].([]any)
	return v
}

func strVal(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
