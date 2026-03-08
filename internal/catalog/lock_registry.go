package catalog

import (
	"path/filepath"
	"sync"
)

var (
	catalogRootLocksMu sync.Mutex
	catalogRootLocks   = map[string]*sync.Mutex{}
)

func mutexForCatalogRoot(root string) *sync.Mutex {
	catalogRootLocksMu.Lock()
	defer catalogRootLocksMu.Unlock()
	if mu, ok := catalogRootLocks[root]; ok {
		return mu
	}
	mu := &sync.Mutex{}
	catalogRootLocks[root] = mu
	return mu
}

func (s *Store) withWriteLock(operation string, fn func() error) error {
	mu := mutexForCatalogRoot(s.root)
	mu.Lock()
	defer mu.Unlock()

	lockPath := filepath.Join(s.root, ".catalog.lock")
	fl, err := acquireFileLock(lockPath)
	if err != nil {
		return internalStoreError("failed acquiring catalog file lock", operation, lockPath, err)
	}

	unlocked := false
	if fl != nil {
		defer func() {
			if !unlocked {
				_ = fl.Unlock()
			}
		}()
	}

	runErr := fn()
	if fl != nil {
		if unlockErr := fl.Unlock(); unlockErr != nil {
			if runErr == nil {
				return internalStoreError("failed releasing catalog file lock", operation, lockPath, unlockErr)
			}
			return runErr
		}
		unlocked = true
	}
	return runErr
}
