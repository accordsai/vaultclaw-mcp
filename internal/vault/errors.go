package vault

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

type NormalizedError struct {
	Code      string
	Category  string
	Message   string
	Retryable bool
	VaultCode string
	Details   map[string]any
}

func NormalizeError(err error) NormalizedError {
	if err == nil {
		return NormalizedError{}
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return normalizeAPIError(apiErr)
	}
	if isNetworkErr(err) {
		return NormalizedError{Code: "MCP_NETWORK_ERROR", Category: "network", Message: err.Error(), Retryable: true, Details: map[string]any{}}
	}
	return NormalizedError{Code: "MCP_INTERNAL", Category: "internal", Message: err.Error(), Retryable: false, Details: map[string]any{}}
}

func normalizeAPIError(e *APIError) NormalizedError {
	if e == nil {
		return NormalizedError{Code: "MCP_INTERNAL", Category: "internal", Message: "unknown error", Details: map[string]any{}}
	}
	vaultCode := strings.TrimSpace(strings.ToUpper(e.Code))
	category := "internal"
	code := "MCP_INTERNAL"
	retryable := e.Retryable

	switch vaultCode {
	case "CONNECTOR_APPROVAL_DECISION_REQUIRED", "PLAN_APPROVAL_REQUIRED", "CONNECTOR_APPROVAL_GRANT_REQUIRED":
		code, category = "MCP_APPROVAL_REQUIRED", "approval"
	case "PLAN_APPROVAL_DENIED", "CONNECTOR_APPROVAL_RULE_DENIED":
		code, category = "MCP_APPROVAL_DENIED", "approval"
	case "UNBOUNDED_PROFILE_SLOT_UNRESOLVED", "UNBOUNDED_PROFILE_SLOT_VIOLATION", "UNBOUNDED_PROFILE_NOT_FOUND", "UNBOUNDED_PROFILE_REQUIRED_FIELDS_MISSING":
		code, category = "MCP_UNBOUNDED_PROFILE_INVALID", "secrets"
	case "UNAUTHENTICATED", "UNAUTHORIZED":
		code, category = "MCP_AUTH_UNAUTHENTICATED", "auth"
	case "FORBIDDEN", "INSUFFICIENT_SCOPE":
		code, category = "MCP_AUTH_FORBIDDEN", "auth"
	case "INVALID_ARGUMENT", "PLAN_INVALID", "PLAN_STEP_INVALID", "PLAN_BINDING_INVALID", "PLAN_BINDING_SOURCE_MISSING", "PLAN_TRANSITION_AMBIGUOUS", "PLAN_TRANSITION_NO_MATCH":
		code, category = "MCP_VALIDATION_ERROR", "validation"
	case "PLAN_POLICY_DRIFT", "ERR_GENERIC_HTTP_RESPONSE_MODE_NOT_ALLOWED", "ERR_GENERIC_HTTP_CONTENT_TYPE_NOT_ALLOWED", "ERR_GENERIC_HTTP_SECRET_ATTACHED_BODY_NOT_ALLOWED", "ERR_GENERIC_HTTP_EXTRACT_SPEC_INVALID", "ERR_GENERIC_HTTP_EXTRACT_PARSE_FAILED", "ERR_GENERIC_HTTP_EXTRACT_REQUIRED_FIELD_MISSING":
		code, category = "MCP_POLICY_ERROR", "policy"
	case "SECRET_SLOT_NOT_DEFINED", "SECRET_SLOT_UNRESOLVED", "SECRET_SLOT_CONFLICT", "SECRET_SLOT_BINDING_INVALID", "SECRET_SLOT_BINDING_NOT_FOUND", "SECRET_INTENT_INVALID", "SECRET_INTENT_NOT_FOUND", "SECRET_INTENT_TYPE_MISMATCH":
		code, category = "MCP_SECRET_ERROR", "secrets"
	case "PLAN_RUN_NOT_FOUND":
		code, category = "MCP_PLAN_RUN_NOT_FOUND", "plans"
	case "PLAN_RUN_INTERRUPTED":
		code, category = "MCP_PLAN_RUN_INTERRUPTED", "plans"
	}

	if e.StatusCode >= 500 && code == "MCP_INTERNAL" {
		category = "network"
		retryable = true
		code = "MCP_NETWORK_ERROR"
	}

	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = fmt.Sprintf("request failed with status %d", e.StatusCode)
	}
	details := map[string]any{
		"request_id":        e.RequestID,
		"vault_http_status": e.StatusCode,
		"vault_error": map[string]any{
			"code":    e.Code,
			"message": e.Message,
			"details": e.Details,
		},
	}
	return NormalizedError{
		Code:      code,
		Category:  category,
		Message:   msg,
		Retryable: retryable,
		VaultCode: strings.TrimSpace(e.Code),
		Details:   details,
	}
}

func isNetworkErr(err error) bool {
	if err == nil {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "connection refused") || strings.Contains(msg, "timeout")
}
