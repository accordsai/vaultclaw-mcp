package mcp

import (
	"context"
	"strings"
)

func (s *Server) registerSessionTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_session_configure",
		Description: "Configure Vaultclaw base URL and bearer token for this MCP process.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"base_url":   map[string]any{"type": "string"},
				"token":      map[string]any{"type": "string"},
				"timeout_ms": map[string]any{"type": "integer"},
			},
			"required": []string{"token"},
		},
		Handler: s.handleSessionConfigure,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_session_status",
		Description: "Get current MCP Vaultclaw session status.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:     s.handleSessionStatus,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_session_clear",
		Description: "Clear in-memory Vaultclaw session state.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:     s.handleSessionClear,
	})
}

func (s *Server) handleSessionConfigure(ctx context.Context, args map[string]any) (map[string]any, error) {
	token := strings.TrimSpace(strArg(args, "token"))
	if token == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "token is required", false, "", map[string]any{}), nil
	}
	baseURL := strings.TrimSpace(strArg(args, "base_url"))
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	timeout := intArg(args, "timeout_ms")
	if timeout <= 0 {
		timeout = 20000
	}
	scope := sessionScopeFromContext(ctx)
	s.sessions.set(scope, SessionConfig{BaseURL: baseURL, Token: token, TimeoutMS: timeout})
	masked := "***"
	if len(token) >= 6 {
		masked = token[:3] + "***" + token[len(token)-3:]
	}
	return envelopeSuccess(map[string]any{
		"configured": true,
		"base_url":   baseURL,
		"timeout_ms": timeout,
		"token":      masked,
	}, nil), nil
}

func (s *Server) handleSessionStatus(ctx context.Context, _ map[string]any) (map[string]any, error) {
	scope := sessionScopeFromContext(ctx)
	cfg, ok := s.sessions.get(scope)
	if !ok {
		if envCfg, envOK := sessionConfigFromEnv(); envOK {
			s.sessions.set(scope, envCfg)
			cfg = envCfg
			ok = true
		}
	}
	if !ok {
		return envelopeSuccess(map[string]any{"configured": false}, nil), nil
	}
	masked := "***"
	if len(cfg.Token) >= 6 {
		masked = cfg.Token[:3] + "***" + cfg.Token[len(cfg.Token)-3:]
	}
	return envelopeSuccess(map[string]any{
		"configured": true,
		"base_url":   cfg.BaseURL,
		"timeout_ms": cfg.TimeoutMS,
		"token":      masked,
	}, nil), nil
}

func (s *Server) handleSessionClear(ctx context.Context, _ map[string]any) (map[string]any, error) {
	s.sessions.clear(sessionScopeFromContext(ctx))
	return envelopeSuccess(map[string]any{"cleared": true}, nil), nil
}

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return strings.TrimSpace(v)
}

func boolArg(args map[string]any, key string, def bool) bool {
	v, ok := args[key]
	if !ok {
		return def
	}
	b, ok := v.(bool)
	if !ok {
		return def
	}
	return b
}

func intArg(args map[string]any, key string) int {
	v, ok := args[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	default:
		return 0
	}
}
