//go:build darwin || linux

package catalog

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const (
	lockHelperEnv     = "ACCORDS_MCP_LOCK_HELPER"
	lockHelperPathEnv = "ACCORDS_MCP_LOCK_HELPER_PATH"
	lockReadyPathEnv  = "ACCORDS_MCP_LOCK_HELPER_READY_PATH"
	lockHoldMSEnv     = "ACCORDS_MCP_LOCK_HELPER_HOLD_MS"
)

func TestAcquireFileLockTimeout(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), ".catalog.lock")
	readyPath := filepath.Join(t.TempDir(), ".lock-helper-ready")

	cmd := exec.Command(os.Args[0], "-test.run=TestCatalogLockHelperProcess", "--")
	cmd.Env = append(os.Environ(),
		lockHelperEnv+"=1",
		lockHelperPathEnv+"="+lockPath,
		lockReadyPathEnv+"="+readyPath,
		lockHoldMSEnv+"=1500",
	)
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start lock helper process: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	waitReadyDeadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(readyPath); err == nil {
			break
		}
		if time.Now().After(waitReadyDeadline) {
			t.Fatalf("lock helper did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Setenv(envCatalogLockTimeoutMS, "80")
	start := time.Now()
	l, err := acquireFileLock(lockPath)
	if err == nil {
		_ = l.Unlock()
		t.Fatalf("expected timeout error acquiring held lock")
	}
	if !errors.Is(err, ErrCatalogFileLockTimeout) {
		t.Fatalf("expected ErrCatalogFileLockTimeout, got: %v", err)
	}
	elapsed := time.Since(start)
	if elapsed > 450*time.Millisecond {
		t.Fatalf("timeout should fail fast; elapsed too high: %s", elapsed)
	}
}

func TestCatalogLockHelperProcess(t *testing.T) {
	if os.Getenv(lockHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	lockPath := os.Getenv(lockHelperPathEnv)
	readyPath := os.Getenv(lockReadyPathEnv)
	holdMS := 1500
	if raw := os.Getenv(lockHoldMSEnv); raw != "" {
		if parsed, err := time.ParseDuration(raw + "ms"); err == nil {
			holdMS = int(parsed / time.Millisecond)
		}
	}

	l, err := acquireFileLock(lockPath)
	if err != nil {
		t.Fatalf("helper failed to acquire lock: %v", err)
	}
	if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("helper failed to mark readiness: %v", err)
	}
	time.Sleep(time.Duration(holdMS) * time.Millisecond)
	if err := l.Unlock(); err != nil {
		t.Fatalf("helper failed to unlock: %v", err)
	}
}
