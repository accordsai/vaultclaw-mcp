package mcp

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestReadMessage_ContentLengthFraming(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"ping"}`
	msg := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)

	got, err := readMessage(bufio.NewReader(strings.NewReader(msg)))
	if err != nil {
		t.Fatalf("readMessage returned error: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("payload mismatch got=%q want=%q", string(got), payload)
	}

	got2, ndjson, err := readMessageWithMode(bufio.NewReader(strings.NewReader(msg)))
	if err != nil {
		t.Fatalf("readMessageWithMode returned error: %v", err)
	}
	if ndjson {
		t.Fatalf("expected framed mode, got ndjson=true")
	}
	if string(got2) != payload {
		t.Fatalf("payload mismatch got=%q want=%q", string(got2), payload)
	}
}

func TestReadMessage_NDJSON(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`
	msg := payload + "\n"

	got, err := readMessage(bufio.NewReader(strings.NewReader(msg)))
	if err != nil {
		t.Fatalf("readMessage returned error: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("payload mismatch got=%q want=%q", string(got), payload)
	}

	got2, ndjson, err := readMessageWithMode(bufio.NewReader(strings.NewReader(msg)))
	if err != nil {
		t.Fatalf("readMessageWithMode returned error: %v", err)
	}
	if !ndjson {
		t.Fatalf("expected ndjson mode, got framed")
	}
	if string(got2) != payload {
		t.Fatalf("payload mismatch got=%q want=%q", string(got2), payload)
	}
}

func TestReadMessage_NDJSON_LeadingBlankLines(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	msg := "\n\r\n  \n" + payload + "\n"

	got, err := readMessage(bufio.NewReader(strings.NewReader(msg)))
	if err != nil {
		t.Fatalf("readMessage returned error: %v", err)
	}
	if string(got) != payload {
		t.Fatalf("payload mismatch got=%q want=%q", string(got), payload)
	}
}

func TestWriteRPC_NDJSON(t *testing.T) {
	var out bytes.Buffer
	s := NewServer(strings.NewReader(""), &out)
	err := s.writeRPC(rpcResponse{
		JSONRPC: "2.0",
		ID:      1,
		Result:  map[string]any{"ok": true},
	}, true)
	if err != nil {
		t.Fatalf("writeRPC returned error: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "Content-Length:") {
		t.Fatalf("expected ndjson output without Content-Length header, got=%q", got)
	}
	if !strings.HasSuffix(got, "\n") {
		t.Fatalf("expected ndjson output to end with newline, got=%q", got)
	}
}

func TestConfiguredClient_AutoSessionFromEnv(t *testing.T) {
	t.Setenv("VC_AGENT_TOKEN", "tok_env")
	t.Setenv("VC_BASE_URL", "http://localhost")
	t.Setenv("VC_UNIX_SOCKET", "")
	t.Setenv("VAULT_UNIX_SOCKET", "")
	t.Setenv("VC_TIMEOUT_MS", "12345")

	s := NewServer(strings.NewReader(""), io.Discard)
	_, cfg, _, ok := s.configuredClient()
	if !ok {
		t.Fatalf("expected configuredClient to auto-configure from env")
	}
	if cfg.Token != "tok_env" {
		t.Fatalf("unexpected token: %q", cfg.Token)
	}
	if cfg.BaseURL != "http://localhost" {
		t.Fatalf("unexpected base_url: %q", cfg.BaseURL)
	}
	if cfg.TimeoutMS != 12345 {
		t.Fatalf("unexpected timeout_ms: %d", cfg.TimeoutMS)
	}
}
