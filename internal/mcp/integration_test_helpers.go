//go:build integration
// +build integration

package mcp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type smokeEnv struct {
	BaseURL        string
	UnixSocket     string
	AgentToken     string
	ManualApproval bool
	WaitTimeoutMS  int
	PollIntervalMS int
	ExpectOutcome  string
}

const (
	smokeDefaultBaseURL        = "http://127.0.0.1:8787"
	smokeDefaultWaitTimeoutMS  = 600000
	smokeDefaultPollIntervalMS = 1500
)

var smokeSeq uint64

var smokeToolScopes = map[string][]string{
	"vaultclaw_connectors_list":        {"connectors.list.v1"},
	"vaultclaw_connector_get":          {"connectors.get.v1"},
	"vaultclaw_connector_execute":      {"connectors.execute.v1"},
	"vaultclaw_connector_execute_job":  {"connectors.execute_job.v1"},
	"vaultclaw_plan_validate":          {"connectors.plans.execute.v1"},
	"vaultclaw_plan_execute":           {"connectors.plans.execute.v1"},
	"vaultclaw_plan_run_get":           {"connectors.plans.read.v1"},
	"vaultclaw_approvals_pending_list": {"connectors.approvals.pending.read.v1"},
	"vaultclaw_approvals_pending_get":  {"connectors.approvals.pending.read.v1"},
	"vaultclaw_approval_wait":          {"connectors.plans.read.v1"},
}

func loadSmokeEnv(t *testing.T) smokeEnv {
	t.Helper()
	unixSocket := resolveSmokeUnixSocket()
	baseURL := strings.TrimSpace(os.Getenv("VC_BASE_URL"))
	if baseURL == "" {
		if unixSocket != "" {
			baseURL = "http://localhost"
		} else {
			baseURL = smokeDefaultBaseURL
		}
	}
	token := strings.TrimSpace(os.Getenv("VC_AGENT_TOKEN"))
	if token == "" {
		t.Fatalf("VC_AGENT_TOKEN is required for integration smoke tests (pre-provided agent token contract)")
	}
	if unixSocket != "" {
		t.Setenv("VC_UNIX_SOCKET", unixSocket)
	}
	waitTimeout := envIntOrDefault(t, "VC_SMOKE_WAIT_TIMEOUT_MS", smokeDefaultWaitTimeoutMS)
	pollInterval := envIntOrDefault(t, "VC_SMOKE_POLL_INTERVAL_MS", smokeDefaultPollIntervalMS)
	expectOutcome := strings.ToUpper(strings.TrimSpace(os.Getenv("VC_SMOKE_EXPECT_OUTCOME")))
	if expectOutcome == "" {
		expectOutcome = "DENY"
	}
	if expectOutcome != "DENY" {
		t.Fatalf("VC_SMOKE_EXPECT_OUTCOME=%q is not supported in this plan; expected DENY", expectOutcome)
	}
	return smokeEnv{
		BaseURL:        baseURL,
		UnixSocket:     unixSocket,
		AgentToken:     token,
		ManualApproval: envBool(os.Getenv("VC_SMOKE_MANUAL_APPROVAL")),
		WaitTimeoutMS:  waitTimeout,
		PollIntervalMS: pollInterval,
		ExpectOutcome:  expectOutcome,
	}
}

func resolveSmokeUnixSocket() string {
	socket := strings.TrimSpace(os.Getenv("VC_UNIX_SOCKET"))
	if socket != "" {
		return socket
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	candidate := filepath.Join(home, "Library", "Application Support", "vaultclaw", "vaultd.sock")
	if _, statErr := os.Stat(candidate); statErr == nil {
		return candidate
	}
	return ""
}

func newConfiguredSmokeServer(t *testing.T, env smokeEnv) *Server {
	t.Helper()
	s := NewServer(strings.NewReader(""), io.Discard)
	resp := callTool(t, s, "vaultclaw_session_configure", map[string]any{
		"base_url":   env.BaseURL,
		"token":      env.AgentToken,
		"timeout_ms": 20000,
	})
	data := requireSuccess(t, "vaultclaw_session_configure", resp)
	if !boolFrom(data["configured"]) {
		t.Fatalf("session configure did not set configured=true: %v", data)
	}
	return s
}

func callTool(t *testing.T, s *Server, toolName string, args map[string]any) map[string]any {
	t.Helper()
	return callToolWithContext(t, context.Background(), s, toolName, args)
}

func callToolWithContext(t *testing.T, ctx context.Context, s *Server, toolName string, args map[string]any) map[string]any {
	t.Helper()
	tool, ok := s.tools[toolName]
	if !ok {
		t.Fatalf("tool %q is not registered", toolName)
	}
	if args == nil {
		args = map[string]any{}
	}
	resp, err := tool.Handler(ctx, args)
	if err != nil {
		t.Fatalf("tool %s handler returned error: %v", toolName, err)
	}
	return resp
}

func requireSuccess(t *testing.T, toolName string, resp map[string]any) map[string]any {
	t.Helper()
	ok, _ := resp["ok"].(bool)
	if !ok {
		code, msg, _ := responseError(resp)
		failIfScopeIssue(t, toolName, code, msg)
		t.Fatalf("tool %s expected success, got code=%s message=%s response=%v", toolName, code, msg, resp)
	}
	data, _ := resp["data"].(map[string]any)
	if data == nil {
		return map[string]any{}
	}
	return data
}

func requireFailureCode(t *testing.T, toolName string, resp map[string]any, wantCode string) map[string]any {
	t.Helper()
	ok, _ := resp["ok"].(bool)
	if ok {
		t.Fatalf("tool %s expected failure code=%s, got success response=%v", toolName, wantCode, resp)
	}
	code, msg, errObj := responseError(resp)
	failIfScopeIssue(t, toolName, code, msg)
	if code != wantCode {
		t.Fatalf("tool %s expected error code=%s got=%s message=%s response=%v", toolName, wantCode, code, msg, resp)
	}
	return errObj
}

func responseError(resp map[string]any) (string, string, map[string]any) {
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil {
		return "", "", map[string]any{}
	}
	code := strings.TrimSpace(strVal(errObj["code"]))
	msg := strings.TrimSpace(strVal(errObj["message"]))
	return code, msg, errObj
}

func failIfScopeIssue(t *testing.T, toolName, code, message string) {
	t.Helper()
	if code != "MCP_AUTH_FORBIDDEN" && code != "MCP_AUTH_UNAUTHENTICATED" {
		return
	}
	scopes := smokeToolScopes[toolName]
	scopeHint := strings.Join(scopes, ", ")
	if scopeHint == "" {
		scopeHint = "see suite required scopes in README"
	}
	t.Fatalf("tool %s failed with %s (%s). Required scopes likely missing: %s", toolName, code, message, scopeHint)
}

func buildGenericHTTPExecuteRequest(url string) map[string]any {
	return map[string]any{
		"connector_id":   "generic.http",
		"policy_version": "1",
		"verb":           "generic.http.request.v1",
		"request": map[string]any{
			"endpoint_key": "request",
			"method":       "GET",
			"url":          strings.TrimSpace(url),
		},
		"usage": map[string]any{
			"pages": 1,
			"bytes": 0,
		},
	}
}

func buildGenericHTTPPlan(url string) map[string]any {
	return map[string]any{
		"type":          "connector.execution.plan.v1",
		"start_step_id": "s1",
		"steps": []any{
			map[string]any{
				"step_id":        "s1",
				"connector_id":   "generic.http",
				"verb":           "generic.http.request.v1",
				"policy_version": "1",
				"request_base": map[string]any{
					"endpoint_key": "request",
					"method":       "GET",
					"url":          strings.TrimSpace(url),
				},
			},
		},
	}
}

func newSmokeURL(prefix string) string {
	prefix = sanitizePrefix(prefix)
	seq := atomic.AddUint64(&smokeSeq, 1)
	return fmt.Sprintf("https://smoke-%s-%d-%d.example.invalid/v1/a", prefix, time.Now().Unix(), seq)
}

func sanitizePrefix(prefix string) string {
	s := strings.ToLower(strings.TrimSpace(prefix))
	if s == "" {
		s = "run"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "run"
	}
	if len(out) > 24 {
		return out[:24]
	}
	return out
}

func envIntOrDefault(t *testing.T, key string, fallback int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("%s must be an integer, got %q", key, raw)
	}
	return v
}

func envBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func boolFrom(v any) bool {
	b, _ := v.(bool)
	return b
}

func approvalFromMCPError(t *testing.T, errObj map[string]any) map[string]any {
	t.Helper()
	details, _ := errObj["details"].(map[string]any)
	if details == nil {
		t.Fatalf("approval-required error missing details: %v", errObj)
	}
	approval, _ := details["approval"].(map[string]any)
	if approval == nil {
		// Some paths (for example connector_execute pass-through errors) may not
		// attach the orchestration approval envelope yet; fall back to raw vault
		// pending payload if available.
		vErr, _ := details["vault_error"].(map[string]any)
		vErrDetails, _ := vErr["details"].(map[string]any)
		pending, _ := vErrDetails["pending_approval"].(map[string]any)
		if pending != nil {
			return map[string]any{
				"status":           "PENDING_APPROVAL",
				"pending_approval": pending,
			}
		}
		t.Fatalf("approval-required error missing details.approval and vault pending payload: %v", errObj)
	}
	return approval
}

func waitHandleFromApproval(t *testing.T, approval map[string]any) map[string]any {
	t.Helper()
	nextAction, _ := approval["next_action"].(map[string]any)
	if nextAction == nil {
		t.Fatalf("approval details missing next_action: %v", approval)
	}
	args, _ := nextAction["arguments"].(map[string]any)
	if args == nil {
		t.Fatalf("approval next_action missing arguments: %v", approval)
	}
	handle, _ := args["handle"].(map[string]any)
	if handle == nil {
		t.Fatalf("approval next_action missing handle: %v", approval)
	}
	return handle
}
