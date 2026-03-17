package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDocumentTypesSuggestSuccess(t *testing.T) {
	var capturedQuery string
	var capturedTopK float64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/docs/types/suggest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		capturedQuery, _ = body["query"].(string)
		capturedTopK, _ = body["top_k"].(float64)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"suggestions": []any{
				map[string]any{
					"type_id": "identity.passport",
					"score":   80,
				},
			},
		})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleDocumentTypesSuggest(context.Background(), map[string]any{
		"query": "passport",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response, got: %v", resp)
	}
	if capturedQuery != "passport" {
		t.Fatalf("unexpected query payload: %q", capturedQuery)
	}
	if capturedTopK != 5 {
		t.Fatalf("expected default top_k=5, got: %v", capturedTopK)
	}
}

func TestDocumentTypesSuggestMissingQuery(t *testing.T) {
	s := newConfiguredMCPServer(t, "http://127.0.0.1:1")
	resp, err := s.handleDocumentTypesSuggest(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected validation failure, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["code"] != "MCP_VALIDATION_ERROR" {
		t.Fatalf("unexpected code: %v", errObj)
	}
}

func TestDocumentTypesLatestSuccessDefaultSubject(t *testing.T) {
	var gotTypeID string
	var gotSubjectID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/docs/types/latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gotTypeID = r.URL.Query().Get("type_id")
		gotSubjectID = r.URL.Query().Get("subject_id")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type_id":     "identity.passport",
			"subject_id":  "self",
			"document_id": "doc_123",
		})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleDocumentTypesLatest(context.Background(), map[string]any{
		"type_id": "identity.passport",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success response, got: %v", resp)
	}
	if gotTypeID != "identity.passport" {
		t.Fatalf("unexpected type_id query: %q", gotTypeID)
	}
	if gotSubjectID != "self" {
		t.Fatalf("expected default subject_id=self, got: %q", gotSubjectID)
	}
}

func TestDocumentTypesLatestUnresolvedPreservesVaultCode(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v0/docs/types/latest" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    "DOCUMENT_SLOT_UNRESOLVED",
				"message": "document slot unresolved",
				"details": map[string]any{"type_id": "identity.passport"},
			},
		})
	}))
	defer ts.Close()

	s := newConfiguredMCPServer(t, ts.URL)
	resp, err := s.handleDocumentTypesLatest(context.Background(), map[string]any{
		"type_id": "identity.passport",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected failure response, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if errObj["vault_code"] != "DOCUMENT_SLOT_UNRESOLVED" {
		t.Fatalf("expected vault_code passthrough, got: %v", errObj)
	}
	details, _ := errObj["details"].(map[string]any)
	vaultErr, _ := details["vault_error"].(map[string]any)
	if vaultErr["code"] != "DOCUMENT_SLOT_UNRESOLVED" {
		t.Fatalf("expected vault error details passthrough, got: %v", details)
	}
}
