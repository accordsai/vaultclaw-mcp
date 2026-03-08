package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestRemoteInstallAndChecksum(t *testing.T) {
	bundle := sampleBundle("2.0.0")
	bundleBytes, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle failed: %v", err)
	}
	sha := SHA256Hex(bundleBytes)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			_ = json.NewEncoder(w).Encode(RemoteIndex{
				Type:     IndexTypeV1,
				SourceID: "main",
				Items: []RemoteIndexItem{
					{
						CookbookID:  "net.http",
						Version:     "2.0.0",
						Title:       "Bundle",
						DownloadURL: srv.URL + "/bundle.json",
						SHA256:      sha,
					},
				},
			})
		case "/bundle.json":
			_, _ = w.Write(bundleBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	store := newTestStore(t)
	_, _, err = store.UpsertSource(SourceConfig{
		SourceID: "main",
		IndexURL: srv.URL + "/index.json",
		Enabled:  true,
		AuthMode: AuthModeNone,
	})
	if err != nil {
		t.Fatalf("UpsertSource failed: %v", err)
	}
	result, err := store.RemoteInstall(context.Background(), "main", "net.http", "", ConflictPolicyFail, "")
	if err != nil {
		t.Fatalf("RemoteInstall failed: %v", err)
	}
	if result.Selected.Version != "2.0.0" {
		t.Fatalf("unexpected selected version: %+v", result.Selected)
	}

	_, err = store.RemoteInstall(context.Background(), "main", "net.http", "2.0.0", ConflictPolicyOverwrite, "bad")
	if err == nil {
		t.Fatalf("expected checksum mismatch error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_CATALOG_REMOTE_CHECKSUM_MISMATCH" {
		t.Fatalf("unexpected error for checksum mismatch: %v", err)
	}
}

func TestRemoteBearerEnvAuth(t *testing.T) {
	const token = "tok_123"
	t.Setenv("REMOTE_TOKEN_TEST", token)
	sawAuth := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+token {
			sawAuth = true
		}
		_ = json.NewEncoder(w).Encode(RemoteIndex{
			Type:  IndexTypeV1,
			Items: []RemoteIndexItem{},
		})
	}))
	defer ts.Close()

	source := SourceConfig{
		SourceID:   "auth",
		IndexURL:   ts.URL,
		Enabled:    true,
		AuthMode:   AuthModeBearerEnv,
		AuthEnvVar: "REMOTE_TOKEN_TEST",
	}
	if _, err := FetchRemoteIndex(context.Background(), source); err != nil {
		t.Fatalf("FetchRemoteIndex failed: %v", err)
	}
	if !sawAuth {
		t.Fatalf("expected bearer auth header to be sent")
	}

	_ = os.Unsetenv("REMOTE_TOKEN_TEST")
	if _, err := FetchRemoteIndex(context.Background(), source); err == nil {
		t.Fatalf("expected auth env missing error")
	}
}
