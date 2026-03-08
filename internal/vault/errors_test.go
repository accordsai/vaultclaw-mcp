package vault

import "testing"

func TestNormalizeErrorMappings(t *testing.T) {
	t.Run("approval required", func(t *testing.T) {
		err := &APIError{StatusCode: 409, Code: "PLAN_APPROVAL_REQUIRED", Message: "approval needed"}
		norm := NormalizeError(err)
		if norm.Code != "MCP_APPROVAL_REQUIRED" || norm.Category != "approval" {
			t.Fatalf("unexpected mapping: %+v", norm)
		}
	})

	t.Run("unbounded profile invalid", func(t *testing.T) {
		err := &APIError{StatusCode: 400, Code: "UNBOUNDED_PROFILE_NOT_FOUND", Message: "missing"}
		norm := NormalizeError(err)
		if norm.Code != "MCP_UNBOUNDED_PROFILE_INVALID" || norm.Category != "secrets" {
			t.Fatalf("unexpected mapping: %+v", norm)
		}
	})

	t.Run("insufficient scope remains auth forbidden", func(t *testing.T) {
		err := &APIError{StatusCode: 403, Code: "INSUFFICIENT_SCOPE", Message: "missing scope"}
		norm := NormalizeError(err)
		if norm.Code != "MCP_AUTH_FORBIDDEN" || norm.Category != "auth" {
			t.Fatalf("unexpected mapping: %+v", norm)
		}
	})

	t.Run("server error maps network retryable", func(t *testing.T) {
		err := &APIError{StatusCode: 502, Code: "HTTP_ERROR", Message: "bad gateway", Retryable: true}
		norm := NormalizeError(err)
		if norm.Code != "MCP_NETWORK_ERROR" || norm.Category != "network" || !norm.Retryable {
			t.Fatalf("unexpected mapping: %+v", norm)
		}
	})
}
