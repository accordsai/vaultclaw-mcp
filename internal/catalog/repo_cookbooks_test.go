package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRepositoryCookbookBundlesValid(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("unable to locate repository root: %v", err)
	}
	cookbooksDir := filepath.Join(repoRoot, "cookbooks")
	info, err := os.Stat(cookbooksDir)
	if errors.Is(err, os.ErrNotExist) {
		t.Log("no repository cookbooks directory found; skipping bundle validation")
		return
	}
	if err != nil {
		t.Fatalf("unable to stat cookbooks directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", cookbooksDir)
	}

	bundleFiles := make([]string, 0)
	if err := filepath.WalkDir(cookbooksDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}
		bundleFiles = append(bundleFiles, path)
		return nil
	}); err != nil {
		t.Fatalf("unable to walk cookbooks directory: %v", err)
	}
	if len(bundleFiles) == 0 {
		t.Log("no cookbook bundle files found under cookbooks/")
		return
	}

	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("unable to initialize validation store: %v", err)
	}
	for _, path := range bundleFiles {
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("unable to read bundle file %s: %v", path, readErr)
		}
		var payload any
		if unmarshalErr := json.Unmarshal(raw, &payload); unmarshalErr != nil {
			t.Fatalf("invalid json in bundle file %s: %v", path, unmarshalErr)
		}
		bundle, decodeErr := DecodeBundle(payload)
		if decodeErr != nil {
			t.Fatalf("invalid bundle shape in file %s: %v", path, decodeErr)
		}
		if _, _, upsertErr := store.UpsertBundle(bundle, ConflictPolicyFail); upsertErr != nil {
			t.Fatalf("bundle failed validation in file %s: %v", path, upsertErr)
		}
	}
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir, nil
		}
		next := filepath.Dir(dir)
		if next == dir {
			return "", os.ErrNotExist
		}
		dir = next
	}
}
