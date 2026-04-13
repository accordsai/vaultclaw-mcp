package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"accords-mcp/internal/mcp"
)

const (
	defaultHTTPPath      = "/v1/mcp"
	defaultHTTPReadLimit = 1 << 20 // 1 MiB
	defaultSessionScope  = "default"
	tenantHeaderRequired = "MCP_TENANT_HEADER_REQUIRED"
)

func main() {
	httpAddr := strings.TrimSpace(os.Getenv("ACCORDS_MCP_HTTP_ADDR"))
	if httpAddr != "" {
		if err := serveHTTP(httpAddr, strings.TrimSpace(os.Getenv("ACCORDS_MCP_HTTP_PATH"))); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "accords-mcp http server error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	s := mcp.NewServer(os.Stdin, os.Stdout)
	if err := s.Serve(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "accords-mcp server error: %v\n", err)
		os.Exit(1)
	}
}

func serveHTTP(addr, path string) error {
	if strings.TrimSpace(path) == "" {
		path = defaultHTTPPath
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	requireTenantHeader := mcp.HTTPRequireTenantHeader()

	s := mcp.NewServer(strings.NewReader(""), io.Discard)

	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		handleRPC(w, r, s, requireTenantHeader)
	})
	if path != "/mcp" {
		mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
			handleRPC(w, r, s, requireTenantHeader)
		})
	}
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeHealthz(w, r)
	})
	mux.HandleFunc("/v1/healthz", func(w http.ResponseWriter, r *http.Request) {
		writeHealthz(w, r)
	})

	return http.ListenAndServe(addr, mux)
}

func writeHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":      true,
		"service": "accords-mcp",
	})
}

func handleRPC(w http.ResponseWriter, r *http.Request, s *mcp.Server, requireTenantHeader bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	scope, ok := resolveSessionScope(r, requireTenantHeader)
	if !ok {
		writeTenantHeaderRequired(w)
		return
	}
	defer r.Body.Close()

	body, err := io.ReadAll(io.LimitReader(r.Body, defaultHTTPReadLimit))
	if err != nil {
		http.Error(w, "read request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	ctx := mcp.WithSessionScope(r.Context(), scope)
	out, hasResponse, err := s.HandleJSONRPC(ctx, body)
	if err != nil {
		http.Error(w, "handle rpc: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !hasResponse {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(out)
}

func resolveSessionScope(r *http.Request, requireTenantHeader bool) (string, bool) {
	if r == nil {
		if requireTenantHeader {
			return "", false
		}
		return defaultSessionScope, true
	}
	return mcp.ResolveHTTPSessionScope(r.Header, requireTenantHeader)
}

func writeTenantHeaderRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":   false,
		"data": nil,
		"error": map[string]any{
			"code":       tenantHeaderRequired,
			"category":   "validation",
			"message":    "tenant header is required for HTTP requests when strict tenant header mode is enabled",
			"retryable":  false,
			"vault_code": "",
			"details": map[string]any{
				"required_headers": mcp.RequiredTenantHeaders(),
				"strict_env_var":   mcp.HTTPRequireTenantHeaderEnv,
			},
		},
		"meta": map[string]any{
			"request_id":        "",
			"vault_http_status": http.StatusBadRequest,
			"vault_code":        "",
		},
	})
}
