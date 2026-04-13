package mcp

import (
	"net/http"
	"os"
	"strconv"
	"strings"
)

const HTTPRequireTenantHeaderEnv = "ACCORDS_MCP_HTTP_REQUIRE_TENANT_HEADER"

var tenantHeaderKeys = []string{
	"X-Accords-Tenant-Id",
	"X-OpenClaw-Tenant-Id",
	"X-Tenant-Id",
}

func HTTPRequireTenantHeader() bool {
	raw := strings.TrimSpace(os.Getenv(HTTPRequireTenantHeaderEnv))
	if raw == "" {
		return false
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return false
	}
	return parsed
}

func ResolveHTTPSessionScope(headers http.Header, requireTenantHeader bool) (string, bool) {
	for _, key := range tenantHeaderKeys {
		if scope := strings.TrimSpace(headers.Get(key)); scope != "" {
			return scope, true
		}
	}
	if requireTenantHeader {
		return "", false
	}
	return defaultSessionScope, true
}

func RequiredTenantHeaders() []string {
	keys := make([]string, len(tenantHeaderKeys))
	copy(keys, tenantHeaderKeys)
	return keys
}
