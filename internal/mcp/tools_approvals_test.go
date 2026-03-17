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

func TestApprovalsPendingListAddsAttestationLinkFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/approvals/pending" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"pending_id": "apj_1",
					"state":      "WAITING",
					"pending_approval": map[string]any{
						"remote_attestation_url": "https://alerts.accords.ai/a/req_1?t=abc",
					},
				},
			},
		})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleApprovalsPendingList(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	row := firstPendingRow(t, data["items"])
	if got := strVal(row["remote_attestation_url"]); got != "https://alerts.accords.ai/a/req_1?t=abc" {
		t.Fatalf("unexpected remote_attestation_url=%q row=%v", got, row)
	}
	if got := strVal(row["remote_attestation_link_markdown"]); got != "[https://alerts.accords.ai/a/req_1?t=abc](https://alerts.accords.ai/a/req_1?t=abc)" {
		t.Fatalf("unexpected remote_attestation_link_markdown=%q row=%v", got, row)
	}
}

func TestApprovalsPendingGetAddsAttestationLinkFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/approvals/pending" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{
				{
					"pending_id": "apj_1",
					"state":      "WAITING",
					"pending_approval": map[string]any{
						"remote_attestation_url": "https://alerts.accords.ai/a/req_1?t=abc",
					},
				},
			},
		})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleApprovalsPendingGet(context.Background(), map[string]any{"challenge_id": "ach_1", "pending_id": "apj_1"})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	item, _ := data["item"].(map[string]any)
	if item == nil {
		t.Fatalf("missing data.item: %v", data)
	}
	if got := strVal(item["remote_attestation_url"]); got != "https://alerts.accords.ai/a/req_1?t=abc" {
		t.Fatalf("unexpected remote_attestation_url=%q item=%v", got, item)
	}
	if got := strVal(item["remote_attestation_link_markdown"]); got != "[https://alerts.accords.ai/a/req_1?t=abc](https://alerts.accords.ai/a/req_1?t=abc)" {
		t.Fatalf("unexpected remote_attestation_link_markdown=%q item=%v", got, item)
	}
}

func firstPendingRow(t *testing.T, raw any) map[string]any {
	t.Helper()
	switch rows := raw.(type) {
	case []map[string]any:
		if len(rows) == 0 {
			t.Fatalf("expected at least one row, got empty slice")
		}
		return rows[0]
	case []any:
		if len(rows) == 0 {
			t.Fatalf("expected at least one row, got empty slice")
		}
		row, _ := rows[0].(map[string]any)
		if row == nil {
			t.Fatalf("expected first row object, got: %T", rows[0])
		}
		return row
	default:
		t.Fatalf("unexpected items type: %T", raw)
		return nil
	}
}
