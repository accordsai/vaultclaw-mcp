package vault

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	BaseURL   string
	Token     string
	Timeout   time.Duration
	UserAgent string
}

type Client struct {
	baseURL    string
	token      string
	userAgent  string
	socketPath string
	httpClient *http.Client
}

type APIResult struct {
	StatusCode int
	RequestID  string
	ErrorCode  string
	Body       map[string]any
	RawBody    []byte
}

type APIError struct {
	StatusCode int
	RequestID  string
	Code       string
	Message    string
	Details    map[string]any
	RawBody    []byte
	Retryable  bool
	Temporary  bool
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Code) != "" {
		return e.Code + ": " + e.Message
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return fmt.Sprintf("http status %d", e.StatusCode)
}

func NewClient(cfg Config) *Client {
	t := cfg.Timeout
	if t <= 0 {
		t = 20 * time.Second
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = "http://127.0.0.1:8080"
	}
	base, socketPath := normalizeTransport(base)
	ua := strings.TrimSpace(cfg.UserAgent)
	if ua == "" {
		ua = "accords-mcp/0.1.0"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if socketPath != "" {
		dialer := &net.Dialer{Timeout: t}
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", socketPath)
		}
	}
	return &Client{
		baseURL:    base,
		token:      strings.TrimSpace(cfg.Token),
		userAgent:  ua,
		socketPath: socketPath,
		httpClient: &http.Client{
			Timeout:   t,
			Transport: transport,
		},
	}
}

func normalizeTransport(base string) (string, string) {
	socketPath := strings.TrimSpace(os.Getenv("VC_UNIX_SOCKET"))
	if socketPath == "" {
		socketPath = strings.TrimSpace(os.Getenv("VAULT_UNIX_SOCKET"))
	}
	if strings.HasPrefix(base, "unix://") {
		socketPath = strings.TrimSpace(strings.TrimPrefix(base, "unix://"))
		base = "http://localhost"
	}
	if strings.HasPrefix(base, "http+unix://") {
		raw := strings.TrimSpace(strings.TrimPrefix(base, "http+unix://"))
		if unescaped, err := url.PathUnescape(raw); err == nil {
			raw = unescaped
		}
		socketPath = strings.TrimSpace(raw)
		base = "http://localhost"
	}
	if socketPath == "" {
		return base, ""
	}
	if expanded, err := expandPath(socketPath); err == nil {
		socketPath = expanded
	}
	return base, socketPath
}

func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if path == "~" {
			return home, nil
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
	}
	return path, nil
}

func (c *Client) Get(ctx context.Context, path string, query map[string]string) (APIResult, error) {
	u, err := c.resolveURL(path, query)
	if err != nil {
		return APIResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return APIResult{}, err
	}
	return c.do(req, false, "")
}

func (c *Client) Post(ctx context.Context, path string, payload any, mutate bool) (APIResult, error) {
	return c.post(ctx, path, payload, mutate, "")
}

func (c *Client) PostWithIdempotencyKey(ctx context.Context, path string, payload any, idempotencyKey string) (APIResult, error) {
	return c.post(ctx, path, payload, true, idempotencyKey)
}

func (c *Client) post(ctx context.Context, path string, payload any, mutate bool, idempotencyKey string) (APIResult, error) {
	u, err := c.resolveURL(path, nil)
	if err != nil {
		return APIResult{}, err
	}
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return APIResult{}, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, body)
	if err != nil {
		return APIResult{}, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, mutate, idempotencyKey)
}

func (c *Client) resolveURL(path string, query map[string]string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("path required")
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	u, err := url.Parse(c.baseURL + p)
	if err != nil {
		return "", err
	}
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			q.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (c *Client) do(req *http.Request, mutate bool, idempotencyKey string) (APIResult, error) {
	reqID := randomHex(16)
	req.Header.Set("X-Request-Id", reqID)
	req.Header.Set("User-Agent", c.userAgent)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if mutate {
		key := strings.TrimSpace(idempotencyKey)
		if key == "" {
			key = randomHex(16)
		}
		req.Header.Set("Idempotency-Key", key)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return APIResult{}, &APIError{Code: "NETWORK_ERROR", Message: err.Error(), Retryable: true, Temporary: true, RequestID: reqID}
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)
	result := APIResult{StatusCode: resp.StatusCode, RequestID: req.Header.Get("X-Request-Id"), RawBody: rawBody}
	var parsed map[string]any
	if len(rawBody) > 0 {
		_ = json.Unmarshal(rawBody, &parsed)
	}
	result.Body = parsed

	if resp.StatusCode >= 400 {
		errOut := &APIError{StatusCode: resp.StatusCode, RequestID: req.Header.Get("X-Request-Id"), RawBody: rawBody}
		if parsed != nil {
			if errObj, ok := parsed["error"].(map[string]any); ok {
				errOut.Code = stringValue(errObj["code"])
				errOut.Message = stringValue(errObj["message"])
				if details, ok := errObj["details"].(map[string]any); ok {
					errOut.Details = details
				}
			}
		}
		if errOut.Message == "" {
			errOut.Message = http.StatusText(resp.StatusCode)
		}
		if errOut.Code == "" {
			errOut.Code = "HTTP_ERROR"
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			errOut.Retryable = true
			errOut.Temporary = true
		}
		return result, errOut
	}

	if parsed != nil {
		if errObj, ok := parsed["error"].(map[string]any); ok {
			result.ErrorCode = stringValue(errObj["code"])
		}
	}
	return result, nil
}

func randomHex(n int) string {
	if n <= 0 {
		n = 8
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "req_fallback"
	}
	return hex.EncodeToString(buf)
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
