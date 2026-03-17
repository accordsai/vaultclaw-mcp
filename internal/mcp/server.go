package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"accords-mcp/internal/vault"
)

type ToolHandler func(ctx context.Context, args map[string]any) (map[string]any, error)

type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Handler     ToolHandler
}

type SessionConfig struct {
	BaseURL   string `json:"base_url"`
	Token     string `json:"token"`
	TimeoutMS int    `json:"timeout_ms"`
}

type sessionState struct {
	mu         sync.RWMutex
	configured bool
	cfg        SessionConfig
}

func (s *sessionState) set(cfg SessionConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = cfg
	s.configured = true
}

func (s *sessionState) get() (SessionConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if !s.configured {
		return SessionConfig{}, false
	}
	return s.cfg, true
}

func (s *sessionState) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = SessionConfig{}
	s.configured = false
}

type Server struct {
	reader  *bufio.Reader
	writer  io.Writer
	writeMu sync.Mutex

	tools   map[string]Tool
	session sessionState
}

func NewServer(r io.Reader, w io.Writer) *Server {
	s := &Server{
		reader: bufio.NewReader(r),
		writer: w,
		tools:  map[string]Tool{},
	}
	s.registerTools()
	return s
}

func (s *Server) registerTools() {
	s.registerSessionTools()
	s.registerConnectorTools()
	s.registerDocumentTools()
	s.registerPlanTools()
	s.registerPrereqTools()
	s.registerOrchestrationTools()
	s.registerApprovalTools()
	s.registerWaitTools()
	s.registerCatalogTools()
	s.registerCatalogRemoteTools()
}

func (s *Server) addTool(t Tool) {
	s.tools[t.Name] = t
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) Serve() error {
	for {
		payload, ndjson, err := readMessageWithMode(s.reader)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		var req rpcRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			_ = s.writeRPC(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}, ndjson)
			continue
		}
		if req.JSONRPC == "" {
			req.JSONRPC = "2.0"
		}
		if req.JSONRPC != "2.0" {
			_ = s.writeRPC(rpcResponse{JSONRPC: "2.0", ID: decodeID(req.ID), Error: &rpcError{Code: -32600, Message: "invalid request"}}, ndjson)
			continue
		}
		if len(req.ID) == 0 {
			// Notification.
			_ = s.handleRequest(context.Background(), req)
			continue
		}
		res := s.handleRequest(context.Background(), req)
		_ = s.writeRPC(res, ndjson)
	}
}

func (s *Server) handleRequest(ctx context.Context, req rpcRequest) rpcResponse {
	id := decodeID(req.ID)
	switch req.Method {
	case "initialize":
		return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "accords-mcp",
				"version": "0.1.0",
			},
		}}
	case "notifications/initialized":
		return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{}}
	case "ping":
		return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{}}
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.tools))
		for _, t := range s.tools {
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			})
		}
		sortTools(tools)
		return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{"tools": tools}}
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32602, Message: "invalid params"}}
		}
		t, ok := s.tools[strings.TrimSpace(p.Name)]
		if !ok {
			return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32601, Message: "tool not found"}}
		}
		if p.Arguments == nil {
			p.Arguments = map[string]any{}
		}
		resp, err := t.Handler(ctx, p.Arguments)
		if err != nil {
			resp = envelopeFailure("MCP_INTERNAL", "internal", err.Error(), false, "", map[string]any{})
		}
		textBytes, _ := json.Marshal(resp)
		content := []map[string]any{{
			"type": "text",
			"text": string(textBytes),
		}}
		if summary := approvalSummaryText(resp); strings.TrimSpace(summary) != "" {
			content = append(content, map[string]any{
				"type": "text",
				"text": summary,
			})
		}
		return rpcResponse{JSONRPC: "2.0", ID: id, Result: map[string]any{
			"structuredContent": resp,
			"content":           content,
		}}
	default:
		return rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: -32601, Message: "method not found"}}
	}
}

func decodeID(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func (s *Server) writeRPC(res rpcResponse, ndjson bool) error {
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if ndjson {
		if _, err := s.writer.Write(b); err != nil {
			return err
		}
		_, err = s.writer.Write([]byte("\n"))
		return err
	}
	if _, err := fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n", len(b)); err != nil {
		return err
	}
	_, err = s.writer.Write(b)
	return err
}

func readMessage(r *bufio.Reader) ([]byte, error) {
	payload, _, err := readMessageWithMode(r)
	return payload, err
}

func readMessageWithMode(r *bufio.Reader) ([]byte, bool, error) {
	// Accept both MCP stdio framing (Content-Length headers) and NDJSON one-line messages.
	for {
		line, err := r.ReadString('\n')
		if err != nil && !(err == io.EOF && len(line) > 0) {
			return nil, false, err
		}
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			if err == io.EOF {
				return nil, false, io.EOF
			}
			continue
		}

		// NDJSON mode: one JSON-RPC message per line.
		if strings.HasPrefix(trimmedLine, "{") || strings.HasPrefix(trimmedLine, "[") {
			return []byte(trimmedLine), true, nil
		}

		// Header-framed mode: first line is a header (typically Content-Length).
		contentLength := -1
		parseHeader := func(headerLine string) error {
			parts := strings.SplitN(headerLine, ":", 2)
			if len(parts) != 2 {
				return nil
			}
			if strings.EqualFold(strings.TrimSpace(parts[0]), "Content-Length") {
				v, convErr := strconv.Atoi(strings.TrimSpace(parts[1]))
				if convErr != nil {
					return fmt.Errorf("invalid content length: %w", convErr)
				}
				contentLength = v
			}
			return nil
		}

		if err := parseHeader(strings.TrimRight(line, "\r\n")); err != nil {
			return nil, false, err
		}

		for {
			headerLine, headerErr := r.ReadString('\n')
			if headerErr != nil {
				return nil, false, headerErr
			}
			headerLine = strings.TrimRight(headerLine, "\r\n")
			if headerLine == "" {
				break
			}
			if err := parseHeader(headerLine); err != nil {
				return nil, false, err
			}
		}

		if contentLength < 0 {
			return nil, false, fmt.Errorf("missing content length")
		}
		payload := make([]byte, contentLength)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, false, err
		}
		return payload, false, nil
	}
}

func sortTools(tools []map[string]any) {
	for i := 0; i < len(tools); i++ {
		for j := i + 1; j < len(tools); j++ {
			a, _ := tools[i]["name"].(string)
			b, _ := tools[j]["name"].(string)
			if strings.Compare(a, b) > 0 {
				tools[i], tools[j] = tools[j], tools[i]
			}
		}
	}
}

func (s *Server) configuredClient() (*vault.Client, SessionConfig, map[string]any, bool) {
	cfg, ok := s.session.get()
	if !ok {
		if envCfg, envOK := sessionConfigFromEnv(); envOK {
			s.session.set(envCfg)
			cfg = envCfg
			ok = true
		}
	}
	if !ok {
		return nil, SessionConfig{}, envelopeFailure("MCP_SESSION_NOT_CONFIGURED", "auth", "session not configured", false, "", map[string]any{}), false
	}
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		baseURL = "http://127.0.0.1:8080"
	}
	timeoutMS := cfg.TimeoutMS
	if timeoutMS <= 0 {
		timeoutMS = 20000
	}
	c := vault.NewClient(vault.Config{
		BaseURL:   baseURL,
		Token:     cfg.Token,
		Timeout:   time.Duration(timeoutMS) * time.Millisecond,
		UserAgent: "accords-mcp/0.1.0",
	})
	return c, cfg, nil, true
}

func sessionConfigFromEnv() (SessionConfig, bool) {
	token := strings.TrimSpace(os.Getenv("VC_AGENT_TOKEN"))
	if token == "" {
		return SessionConfig{}, false
	}
	baseURL := strings.TrimSpace(os.Getenv("VC_BASE_URL"))
	if baseURL == "" {
		if strings.TrimSpace(os.Getenv("VC_UNIX_SOCKET")) != "" || strings.TrimSpace(os.Getenv("VAULT_UNIX_SOCKET")) != "" {
			baseURL = "http://localhost"
		} else {
			baseURL = "http://127.0.0.1:8080"
		}
	}
	timeoutMS := 20000
	if raw := strings.TrimSpace(os.Getenv("VC_TIMEOUT_MS")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			timeoutMS = parsed
		}
	}
	return SessionConfig{
		BaseURL:   baseURL,
		Token:     token,
		TimeoutMS: timeoutMS,
	}, true
}

func envelopeSuccess(data any, meta map[string]any) map[string]any {
	if meta == nil {
		meta = map[string]any{}
	}
	if _, ok := meta["request_id"]; !ok {
		meta["request_id"] = ""
	}
	if _, ok := meta["vault_http_status"]; !ok {
		meta["vault_http_status"] = 0
	}
	if _, ok := meta["vault_code"]; !ok {
		meta["vault_code"] = ""
	}
	return map[string]any{
		"ok":    true,
		"data":  data,
		"error": nil,
		"meta":  meta,
	}
}

func envelopeFailure(code, category, message string, retryable bool, vaultCode string, details map[string]any) map[string]any {
	if details == nil {
		details = map[string]any{}
	}
	return map[string]any{
		"ok":   false,
		"data": nil,
		"error": map[string]any{
			"code":       code,
			"category":   category,
			"message":    message,
			"retryable":  retryable,
			"vault_code": vaultCode,
			"details":    details,
		},
		"meta": map[string]any{
			"request_id":        valueOr(details["request_id"], ""),
			"vault_http_status": valueOr(details["vault_http_status"], 0),
			"vault_code":        vaultCode,
		},
	}
}

func valueOr(v any, fallback any) any {
	if v == nil {
		return fallback
	}
	return v
}

func approvalSummaryText(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	ok, _ := resp["ok"].(bool)
	if ok {
		return ""
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj == nil || !strings.EqualFold(strings.TrimSpace(strVal(errObj["code"])), "MCP_APPROVAL_REQUIRED") {
		return ""
	}
	details, _ := errObj["details"].(map[string]any)
	approval, _ := details["approval"].(map[string]any)
	if approval == nil {
		return ""
	}

	lines := []string{"Approval required before execution can continue."}
	if kind := strings.ToUpper(strings.TrimSpace(strVal(approval["kind"]))); kind != "" {
		lines = append(lines, "Kind: "+kind)
	}
	if challengeID := strings.TrimSpace(strVal(approval["challenge_id"])); challengeID != "" {
		lines = append(lines, "Challenge ID: "+challengeID)
	}
	if pendingID := strings.TrimSpace(strVal(approval["pending_id"])); pendingID != "" {
		lines = append(lines, "Pending ID: "+pendingID)
	}
	if url := approvalRemoteAttestationURL(approval); url != "" {
		lines = append(lines, "Attestation URL: ["+url+"]("+url+")")
	}
	lines = append(lines, "Next action: call `vaultclaw_approval_wait` with the provided handle.")
	return strings.Join(lines, "\n")
}

func approvalRemoteAttestationURL(approval map[string]any) string {
	if approval == nil {
		return ""
	}
	if url := strings.TrimSpace(strVal(approval["remote_attestation_url"])); url != "" {
		return url
	}
	pending, _ := approval["pending_approval"].(map[string]any)
	if pending == nil {
		return ""
	}
	return strings.TrimSpace(strVal(pending["remote_attestation_url"]))
}
