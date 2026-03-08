//go:build darwin || linux

package catalog

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type fileLock struct {
	file *os.File
}

const (
	envCatalogLockTimeoutMS     = "ACCORDS_MCP_LOCK_TIMEOUT_MS"
	defaultCatalogLockTimeoutMS = 10000
	catalogLockPollInterval     = 25 * time.Millisecond
)

var ErrCatalogFileLockTimeout = errors.New("catalog file lock acquire timeout")

func acquireFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	timeout := catalogLockTimeout()
	deadline := time.Now().Add(timeout)
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			if time.Now().After(deadline) {
				_ = f.Close()
				return nil, fmt.Errorf("%w: path=%s timeout_ms=%d", ErrCatalogFileLockTimeout, path, timeout/time.Millisecond)
			}
			sleepFor := catalogLockPollInterval
			remaining := time.Until(deadline)
			if remaining < sleepFor {
				sleepFor = remaining
			}
			if sleepFor > 0 {
				time.Sleep(sleepFor)
			}
			continue
		}
		if err != nil {
			_ = f.Close()
			return nil, err
		}
	}
	return &fileLock{file: f}, nil
}

func (l *fileLock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	fd := int(l.file.Fd())
	for {
		err := syscall.Flock(fd, syscall.LOCK_UN)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			_ = l.file.Close()
			return err
		}
		break
	}
	return l.file.Close()
}

func catalogLockTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv(envCatalogLockTimeoutMS))
	if raw == "" {
		return time.Duration(defaultCatalogLockTimeoutMS) * time.Millisecond
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return time.Duration(defaultCatalogLockTimeoutMS) * time.Millisecond
	}
	return time.Duration(n) * time.Millisecond
}
