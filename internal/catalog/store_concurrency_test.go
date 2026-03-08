package catalog

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
)

func TestStoreConcurrentUpsertSameStore(t *testing.T) {
	store := newTestStore(t)
	const n = 32

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			bundle := sampleBundle(fmt.Sprintf("1.0.%d", i))
			_, _, err := store.UpsertBundle(bundle, ConflictPolicyFail)
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent upsert failed: %v", err)
		}
	}
	items, err := store.ListCookbooks(nil)
	if err != nil {
		t.Fatalf("ListCookbooks failed: %v", err)
	}
	if len(items) != n {
		t.Fatalf("expected %d items, got %d", n, len(items))
	}
}

func TestStoreConcurrentUpsertAcrossStoreInstances(t *testing.T) {
	root := t.TempDir()
	const n = 24

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			store, err := NewStore(root)
			if err != nil {
				errCh <- err
				return
			}
			bundle := sampleBundle(fmt.Sprintf("2.0.%d", i))
			_, _, err = store.UpsertBundle(bundle, ConflictPolicyFail)
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent multi-store upsert failed: %v", err)
		}
	}
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	items, err := store.ListCookbooks(nil)
	if err != nil {
		t.Fatalf("ListCookbooks failed: %v", err)
	}
	if len(items) != n {
		t.Fatalf("expected %d items, got %d", n, len(items))
	}
}

func TestStoreConcurrentSourceAndBundleWrites(t *testing.T) {
	store := newTestStore(t)
	const n = 40

	var wg sync.WaitGroup
	errCh := make(chan error, n*3)

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			bundle := sampleBundle(fmt.Sprintf("3.0.%d", i))
			_, _, err := store.UpsertBundle(bundle, ConflictPolicyFail)
			errCh <- err
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			_, _, err := store.UpsertSource(SourceConfig{
				SourceID: "main",
				IndexURL: "https://example.invalid/index.json",
				Enabled:  i%2 == 0,
				AuthMode: AuthModeNone,
			})
			errCh <- err
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			if i%3 == 0 {
				_, err := store.DeleteSource("main")
				errCh <- err
				continue
			}
			_, _, err := store.UpsertSource(SourceConfig{
				SourceID: fmt.Sprintf("aux-%d", i),
				IndexURL: "https://example.invalid/index.json",
				Enabled:  true,
				AuthMode: AuthModeNone,
			})
			errCh <- err
		}
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent mixed write failed: %v", err)
		}
	}

	assertJSONFileValid[LocalIndex](t, store.indexPath())
	assertJSONFileValid[SourcesFile](t, store.sourcesPath())
}

func TestStoreConcurrentReadWriteIndexAlwaysValidJSON(t *testing.T) {
	store := newTestStore(t)
	const writes = 120

	var wg sync.WaitGroup
	readerDone := make(chan struct{})
	errCh := make(chan error, 8)

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-readerDone:
					return
				default:
					if err := validateJSONFile[LocalIndex](store.indexPath()); err != nil {
						errCh <- err
						return
					}
					if err := validateJSONFile[SourcesFile](store.sourcesPath()); err != nil {
						errCh <- err
						return
					}
				}
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < writes; i++ {
			bundle := sampleBundle(fmt.Sprintf("4.0.%d", i))
			if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
				errCh <- err
				return
			}
		}
		close(readerDone)
	}()

	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("read/write contention produced invalid file: %v", err)
		}
	}
}

func assertJSONFileValid[T any](t *testing.T, path string) {
	t.Helper()
	if err := validateJSONFile[T](path); err != nil {
		t.Fatalf("json file invalid %s: %v", path, err)
	}
}

func validateJSONFile[T any](path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(raw) == 0 {
		return fmt.Errorf("empty file")
	}
	var doc T
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}
	return nil
}
