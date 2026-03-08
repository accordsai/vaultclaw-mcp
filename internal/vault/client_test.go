package vault

import "testing"

func TestNormalizeTransportFromEnvSocket(t *testing.T) {
	t.Setenv("VC_UNIX_SOCKET", "/tmp/vaultd.sock")
	base, socket := normalizeTransport("http://127.0.0.1:8787")
	if base != "http://127.0.0.1:8787" {
		t.Fatalf("unexpected base url: %s", base)
	}
	if socket != "/tmp/vaultd.sock" {
		t.Fatalf("unexpected socket path: %s", socket)
	}
}

func TestNormalizeTransportFromUnixBaseURL(t *testing.T) {
	t.Setenv("VC_UNIX_SOCKET", "")
	base, socket := normalizeTransport("unix:///tmp/vaultd.sock")
	if base != "http://localhost" {
		t.Fatalf("unexpected base url: %s", base)
	}
	if socket != "/tmp/vaultd.sock" {
		t.Fatalf("unexpected socket path: %s", socket)
	}
}

func TestNormalizeTransportFromHTTPUnixBaseURL(t *testing.T) {
	t.Setenv("VC_UNIX_SOCKET", "")
	base, socket := normalizeTransport("http+unix://%2Ftmp%2Fvaultd.sock")
	if base != "http://localhost" {
		t.Fatalf("unexpected base url: %s", base)
	}
	if socket != "/tmp/vaultd.sock" {
		t.Fatalf("unexpected socket path: %s", socket)
	}
}
