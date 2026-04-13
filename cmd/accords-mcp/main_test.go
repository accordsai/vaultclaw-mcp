package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"accords-mcp/internal/mcp"
)

func TestHandleRPC_MethodNotAllowed(t *testing.T) {
	t.Parallel()

	s := mcp.NewServer(strings.NewReader(""), io.Discard)
	req := httptest.NewRequest(http.MethodGet, "/v1/mcp", nil)
	rec := httptest.NewRecorder()

	handleRPC(rec, req, s, false)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleRPC_Initialize(t *testing.T) {
	t.Parallel()

	s := mcp.NewServer(strings.NewReader(""), io.Discard)
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	rec := httptest.NewRecorder()

	handleRPC(rec, req, s, false)

	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("initialize response missing protocolVersion: %s", rec.Body.String())
	}
}

func TestHandleRPC_NotificationReturnsNoContent(t *testing.T) {
	t.Parallel()

	s := mcp.NewServer(strings.NewReader(""), io.Discard)
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized","params":{}}`))
	rec := httptest.NewRecorder()

	handleRPC(rec, req, s, false)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status=%d want=%d", rec.Code, http.StatusNoContent)
	}
}

func TestHandleRPC_TenantIsolation(t *testing.T) {
	t.Setenv("VC_AGENT_TOKEN", "")
	t.Setenv("VC_BASE_URL", "")
	t.Setenv("VC_TIMEOUT_MS", "")
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")

	s := mcp.NewServer(strings.NewReader(""), io.Discard)

	rec := callMCP(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vaultclaw_session_configure","arguments":{"base_url":"http://tenant-a.internal","token":"token-a-123","timeout_ms":1111}}}`, map[string]string{
		"X-Accords-Tenant-Id": "tenant-a",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("configure status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	rec = callMCP(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vaultclaw_session_status","arguments":{}}}`, map[string]string{
		"X-Accords-Tenant-Id": "tenant-a",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant A status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	tenantA := parseStructuredContent(t, rec.Body.Bytes())
	if configured, _ := tenantA["configured"].(bool); !configured {
		t.Fatalf("expected tenant A configured=true, got: %v", tenantA)
	}
	if got := strings.TrimSpace(asString(tenantA["base_url"])); got != "http://tenant-a.internal" {
		t.Fatalf("tenant A base_url=%q want=%q", got, "http://tenant-a.internal")
	}

	rec = callMCP(t, s, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vaultclaw_session_status","arguments":{}}}`, map[string]string{
		"X-Accords-Tenant-Id": "tenant-b",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant B status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	tenantB := parseStructuredContent(t, rec.Body.Bytes())
	if configured, _ := tenantB["configured"].(bool); configured {
		t.Fatalf("expected tenant B configured=false after tenant A configure, got: %v", tenantB)
	}
}

func TestHandleRPC_StrictTenantHeaderRequiredWhenMissing(t *testing.T) {
	t.Parallel()

	s := mcp.NewServer(strings.NewReader(""), io.Discard)
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
	rec := httptest.NewRecorder()

	handleRPC(rec, req, s, true)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal error envelope: %v body=%s", err, rec.Body.String())
	}
	if ok, _ := body["ok"].(bool); ok {
		t.Fatalf("expected ok=false in error envelope: %v", body)
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj == nil {
		t.Fatalf("expected error object in envelope: %v", body)
	}
	if code := asString(errObj["code"]); code != "MCP_TENANT_HEADER_REQUIRED" {
		t.Fatalf("code=%q want=%q", code, "MCP_TENANT_HEADER_REQUIRED")
	}
}

func TestHandleRPC_StrictTenantHeaderPresentScopesNormally(t *testing.T) {
	t.Setenv("VC_AGENT_TOKEN", "")
	t.Setenv("VC_BASE_URL", "")
	t.Setenv("VC_TIMEOUT_MS", "")
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")

	s := mcp.NewServer(strings.NewReader(""), io.Discard)

	rec := callMCPWithStrict(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vaultclaw_session_configure","arguments":{"base_url":"http://tenant-a.internal","token":"token-a-123","timeout_ms":1111}}}`, map[string]string{
		"X-Accords-Tenant-Id": "tenant-a",
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("configure status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	rec = callMCPWithStrict(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vaultclaw_session_status","arguments":{}}}`, map[string]string{
		"X-Accords-Tenant-Id": "tenant-a",
	}, true)
	if rec.Code != http.StatusOK {
		t.Fatalf("tenant status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	tenant := parseStructuredContent(t, rec.Body.Bytes())
	if configured, _ := tenant["configured"].(bool); !configured {
		t.Fatalf("expected scoped tenant configured=true, got: %v", tenant)
	}
}

func TestHandleRPC_NonStrictPreservesDefaultFallback(t *testing.T) {
	t.Setenv("VC_AGENT_TOKEN", "")
	t.Setenv("VC_BASE_URL", "")
	t.Setenv("VC_TIMEOUT_MS", "")
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")

	s := mcp.NewServer(strings.NewReader(""), io.Discard)

	rec := callMCPWithStrict(t, s, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"vaultclaw_session_configure","arguments":{"base_url":"http://default.internal","token":"token-default-123","timeout_ms":1111}}}`, nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("configure status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	rec = callMCPWithStrict(t, s, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vaultclaw_session_status","arguments":{}}}`, nil, false)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	defaultScope := parseStructuredContent(t, rec.Body.Bytes())
	if configured, _ := defaultScope["configured"].(bool); !configured {
		t.Fatalf("expected default scope configured=true, got: %v", defaultScope)
	}
	if got := strings.TrimSpace(asString(defaultScope["base_url"])); got != "http://default.internal" {
		t.Fatalf("default scope base_url=%q want=%q", got, "http://default.internal")
	}
}

func TestResolveSessionScope_HeaderPrecedence(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", nil)
	req.Header.Set("X-Accords-Tenant-Id", "tenant-a")
	req.Header.Set("X-OpenClaw-Tenant-Id", "tenant-b")
	req.Header.Set("X-Tenant-Id", "tenant-c")
	if got, ok := resolveSessionScope(req, false); !ok || got != "tenant-a" {
		t.Fatalf("scope=%q want=%q", got, "tenant-a")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/mcp", nil)
	req.Header.Set("X-Accords-Tenant-Id", "  ")
	req.Header.Set("X-OpenClaw-Tenant-Id", "tenant-b")
	req.Header.Set("X-Tenant-Id", "tenant-c")
	if got, ok := resolveSessionScope(req, false); !ok || got != "tenant-b" {
		t.Fatalf("scope=%q want=%q", got, "tenant-b")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/mcp", nil)
	req.Header.Set("X-Tenant-Id", "tenant-c")
	if got, ok := resolveSessionScope(req, false); !ok || got != "tenant-c" {
		t.Fatalf("scope=%q want=%q", got, "tenant-c")
	}

	req = httptest.NewRequest(http.MethodPost, "/v1/mcp", nil)
	if got, ok := resolveSessionScope(req, false); !ok || got != "default" {
		t.Fatalf("scope=%q want=%q", got, "default")
	}
	if got, ok := resolveSessionScope(req, true); ok || got != "" {
		t.Fatalf("strict missing header scope=%q ok=%v; want empty,false", got, ok)
	}
}

func callMCP(t *testing.T, s *mcp.Server, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return callMCPWithStrict(t, s, body, headers, false)
}

func callMCPWithStrict(t *testing.T, s *mcp.Server, body string, headers map[string]string, requireTenantHeader bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/mcp", strings.NewReader(body))
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	handleRPC(rec, req, s, requireTenantHeader)
	return rec
}

func parseStructuredContent(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var rpcResp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(body, &rpcResp); err != nil {
		t.Fatalf("unmarshal response: %v body=%s", err, string(body))
	}
	result := rpcResp.Result
	if result == nil {
		t.Fatalf("missing result in response: %s", string(body))
	}
	payload, _ := result["structuredContent"].(map[string]any)
	if payload == nil {
		t.Fatalf("missing structuredContent in result: %v", result)
	}
	ok, _ := payload["ok"].(bool)
	if !ok {
		t.Fatalf("expected successful envelope: %v", payload)
	}
	data, _ := payload["data"].(map[string]any)
	if data == nil {
		return map[string]any{}
	}
	return data
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
