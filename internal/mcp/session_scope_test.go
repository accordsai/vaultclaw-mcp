package mcp

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestSessionTools_IsolatedByScope(t *testing.T) {
	t.Setenv("VC_AGENT_TOKEN", "")
	t.Setenv("VC_BASE_URL", "")
	t.Setenv("VC_TIMEOUT_MS", "")
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")

	s := NewServer(strings.NewReader(""), io.Discard)
	scopeA := WithSessionScope(context.Background(), "tenant-a")
	scopeB := WithSessionScope(context.Background(), "tenant-b")

	if resp, err := s.handleSessionConfigure(scopeA, map[string]any{
		"base_url":   "http://tenant-a.internal",
		"token":      "token-a-123",
		"timeout_ms": 1111,
	}); err != nil {
		t.Fatalf("configure scope A returned error: %v", err)
	} else {
		requireSessionConfigured(t, resp, true)
	}

	if resp, err := s.handleSessionConfigure(scopeB, map[string]any{
		"base_url":   "http://tenant-b.internal",
		"token":      "token-b-456",
		"timeout_ms": 2222,
	}); err != nil {
		t.Fatalf("configure scope B returned error: %v", err)
	} else {
		requireSessionConfigured(t, resp, true)
	}

	statusA, err := s.handleSessionStatus(scopeA, map[string]any{})
	if err != nil {
		t.Fatalf("status scope A returned error: %v", err)
	}
	dataA := requireSessionConfigured(t, statusA, true)
	if got := strVal(dataA["base_url"]); got != "http://tenant-a.internal" {
		t.Fatalf("scope A base_url=%q want=%q", got, "http://tenant-a.internal")
	}
	if got := intArg(dataA, "timeout_ms"); got != 1111 {
		t.Fatalf("scope A timeout_ms=%d want=%d", got, 1111)
	}

	statusB, err := s.handleSessionStatus(scopeB, map[string]any{})
	if err != nil {
		t.Fatalf("status scope B returned error: %v", err)
	}
	dataB := requireSessionConfigured(t, statusB, true)
	if got := strVal(dataB["base_url"]); got != "http://tenant-b.internal" {
		t.Fatalf("scope B base_url=%q want=%q", got, "http://tenant-b.internal")
	}
	if got := intArg(dataB, "timeout_ms"); got != 2222 {
		t.Fatalf("scope B timeout_ms=%d want=%d", got, 2222)
	}

	if _, err := s.handleSessionClear(scopeA, map[string]any{}); err != nil {
		t.Fatalf("clear scope A returned error: %v", err)
	}

	statusAAfterClear, err := s.handleSessionStatus(scopeA, map[string]any{})
	if err != nil {
		t.Fatalf("status scope A after clear returned error: %v", err)
	}
	requireSessionConfigured(t, statusAAfterClear, false)

	statusBAfterClear, err := s.handleSessionStatus(scopeB, map[string]any{})
	if err != nil {
		t.Fatalf("status scope B after scope A clear returned error: %v", err)
	}
	requireSessionConfigured(t, statusBAfterClear, true)
}

func TestSessionTools_DefaultScopeCompatibility(t *testing.T) {
	t.Setenv("VC_AGENT_TOKEN", "")
	t.Setenv("VC_BASE_URL", "")
	t.Setenv("VC_TIMEOUT_MS", "")
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")

	s := NewServer(strings.NewReader(""), io.Discard)
	if _, err := s.handleSessionConfigure(context.Background(), map[string]any{
		"base_url":   "http://default.internal",
		"token":      "token-default-123",
		"timeout_ms": 3333,
	}); err != nil {
		t.Fatalf("configure default scope returned error: %v", err)
	}

	statusFromBackground, err := s.handleSessionStatus(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("status with background context returned error: %v", err)
	}
	dataFromBackground := requireSessionConfigured(t, statusFromBackground, true)
	if got := strVal(dataFromBackground["base_url"]); got != "http://default.internal" {
		t.Fatalf("background scope base_url=%q want=%q", got, "http://default.internal")
	}

	statusFromDefaultScope, err := s.handleSessionStatus(WithSessionScope(context.Background(), "default"), map[string]any{})
	if err != nil {
		t.Fatalf("status with explicit default scope returned error: %v", err)
	}
	dataFromDefaultScope := requireSessionConfigured(t, statusFromDefaultScope, true)
	if got := strVal(dataFromDefaultScope["base_url"]); got != "http://default.internal" {
		t.Fatalf("explicit default scope base_url=%q want=%q", got, "http://default.internal")
	}
}

func requireSessionConfigured(t *testing.T, resp map[string]any, want bool) map[string]any {
	t.Helper()
	ok, _ := resp["ok"].(bool)
	if !ok {
		t.Fatalf("expected success envelope, got: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		data = map[string]any{}
	}
	configured, _ := data["configured"].(bool)
	if configured != want {
		t.Fatalf("configured=%v want=%v response=%v", configured, want, resp)
	}
	return data
}
