package mcp

import (
	"net/http"
	"reflect"
	"testing"
)

func TestHTTPRequireTenantHeader(t *testing.T) {
	t.Run("default false", func(t *testing.T) {
		t.Setenv(HTTPRequireTenantHeaderEnv, "")
		if HTTPRequireTenantHeader() {
			t.Fatalf("expected strict mode default to false")
		}
	})

	t.Run("true enables strict mode", func(t *testing.T) {
		t.Setenv(HTTPRequireTenantHeaderEnv, "true")
		if !HTTPRequireTenantHeader() {
			t.Fatalf("expected strict mode to be true")
		}
	})

	t.Run("invalid value is treated as false", func(t *testing.T) {
		t.Setenv(HTTPRequireTenantHeaderEnv, "not-a-bool")
		if HTTPRequireTenantHeader() {
			t.Fatalf("expected invalid strict mode value to be false")
		}
	})
}

func TestResolveHTTPSessionScope(t *testing.T) {
	t.Run("strict mode rejects missing tenant headers", func(t *testing.T) {
		scope, ok := ResolveHTTPSessionScope(http.Header{}, true)
		if ok {
			t.Fatalf("expected strict mode to reject missing headers")
		}
		if scope != "" {
			t.Fatalf("scope=%q want empty", scope)
		}
	})

	t.Run("strict mode accepts header with precedence", func(t *testing.T) {
		h := http.Header{}
		h.Set("X-OpenClaw-Tenant-Id", "tenant-b")
		h.Set("X-Accords-Tenant-Id", "tenant-a")
		scope, ok := ResolveHTTPSessionScope(h, true)
		if !ok {
			t.Fatalf("expected strict mode to accept request with tenant header")
		}
		if scope != "tenant-a" {
			t.Fatalf("scope=%q want=%q", scope, "tenant-a")
		}
	})

	t.Run("non-strict mode preserves default fallback", func(t *testing.T) {
		scope, ok := ResolveHTTPSessionScope(http.Header{}, false)
		if !ok {
			t.Fatalf("expected non-strict mode to allow default fallback")
		}
		if scope != "default" {
			t.Fatalf("scope=%q want=%q", scope, "default")
		}
	})
}

func TestRequiredTenantHeaders(t *testing.T) {
	got := RequiredTenantHeaders()
	want := []string{"X-Accords-Tenant-Id", "X-OpenClaw-Tenant-Id", "X-Tenant-Id"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headers=%v want=%v", got, want)
	}
}
