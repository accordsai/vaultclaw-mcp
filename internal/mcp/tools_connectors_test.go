package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestConnectorValidateSuccess(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/connectors/validate" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"validated": true,
		})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleConnectorValidate(context.Background(), map[string]any{
		"request": map[string]any{
			"connector_id": "generic.http",
			"verb":         "generic.http.request.v1",
			"request": map[string]any{
				"method": "GET",
				"url":    "https://example.invalid",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response, got: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	validation, _ := data["validation"].(map[string]any)
	if got, _ := validation["validated"].(bool); !got {
		t.Fatalf("expected validated=true, got: %v", validation)
	}
}

func TestConnectorExecuteValidationFailureSkipsExecute(t *testing.T) {
	var executeCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/connectors/validate":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "INVALID_ARGUMENT",
					"message": "request failed validation",
					"details": map[string]any{
						"errors": []any{
							map[string]any{
								"path":     "/to",
								"code":     "TYPE_MISMATCH",
								"message":  "to must be an array",
								"expected": "array",
								"actual":   "string",
							},
						},
					},
				},
			})
		case "/v0/connectors/execute":
			atomic.AddInt32(&executeCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleConnectorExecute(context.Background(), map[string]any{
		"request": map[string]any{
			"connector_id": "generic.http",
			"verb":         "generic.http.request.v1",
			"request": map[string]any{
				"method": "GET",
				"url":    "https://example.invalid",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected failure response, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["code"] != "MCP_VALIDATION_ERROR" {
		t.Fatalf("expected MCP_VALIDATION_ERROR, got: %v", errObj)
	}
	if got := atomic.LoadInt32(&executeCalls); got != 0 {
		t.Fatalf("expected /execute not called on validation failure, got %d", got)
	}
}

func TestConnectorExecuteJobValidationFailureSkipsExecuteJob(t *testing.T) {
	var executeJobCalls int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/connectors/validate":
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{
					"code":    "INVALID_ARGUMENT",
					"message": "request failed validation",
					"details": map[string]any{
						"errors": []any{
							map[string]any{
								"path":     "/text_plain",
								"code":     "UNKNOWN_FIELD",
								"message":  "field not allowed",
								"expected": "schema",
								"actual":   "present",
							},
						},
					},
				},
			})
		case "/v0/connectors/execute-job":
			atomic.AddInt32(&executeJobCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleConnectorExecuteJob(context.Background(), map[string]any{
		"request": map[string]any{
			"connector_id": "generic.http",
			"verb":         "generic.http.request.v1",
			"request": map[string]any{
				"method": "GET",
				"url":    "https://example.invalid",
			},
		},
		"orchestration": map[string]any{
			"unbounded_profiles": false,
		},
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected failure response, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["code"] != "MCP_VALIDATION_ERROR" {
		t.Fatalf("expected MCP_VALIDATION_ERROR, got: %v", errObj)
	}
	if got := atomic.LoadInt32(&executeJobCalls); got != 0 {
		t.Fatalf("expected /execute-job not called on validation failure, got %d", got)
	}
}
