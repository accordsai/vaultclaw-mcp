package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newConfiguredMCPServer(t *testing.T, baseURL string) *Server {
	t.Helper()
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")
	s := NewServer(strings.NewReader(""), io.Discard)
	s.session.set(SessionConfig{BaseURL: baseURL, Token: "token", TimeoutMS: 2000})
	return s
}

func TestApprovalsPendingGetNotFound(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/approvals/pending" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleApprovalsPendingGet(context.Background(), map[string]any{"challenge_id": "ach_1", "pending_id": "apj_missing"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected failure response, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["code"] != "MCP_APPROVAL_PENDING_NOT_FOUND" {
		t.Fatalf("unexpected code: %v", errObj)
	}
}

func TestApprovalsPendingGetMalformedInput(t *testing.T) {
	s := newConfiguredMCPServer(t, "http://127.0.0.1:1")
	resp, err := s.handleApprovalsPendingGet(context.Background(), map[string]any{"challenge_id": "ach_1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected validation failure, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["code"] != "MCP_VALIDATION_ERROR" {
		t.Fatalf("unexpected validation code: %v", errObj)
	}
}
