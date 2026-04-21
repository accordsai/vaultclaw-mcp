package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"accords-mcp/internal/catalog"
	"accords-mcp/internal/routing"
)

func TestSeedCatalogFromBundledCookbooks_FirstRunSeedsAndWritesMarker(t *testing.T) {
	bundledDir := t.TempDir()
	catalogDir := t.TempDir()
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", catalogDir)

	if err := writeBundleFile(filepath.Join(bundledDir, "google.workspace", "1.3.0.json"), sampleBundle("google.workspace", "1.3.0", "First")); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	store, err := catalog.NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	result, err := seedCatalogFromBundledCookbooks(store, bundledDir)
	if err != nil {
		t.Fatalf("seed catalog: %v", err)
	}
	if result.Skipped {
		t.Fatalf("expected seeding to run on first pass")
	}
	if result.Seeded != 1 {
		t.Fatalf("seeded=%d want=1", result.Seeded)
	}

	bundle, err := store.GetBundle("google.workspace", "1.3.0")
	if err != nil {
		t.Fatalf("seeded bundle missing: %v", err)
	}
	if got := strings.TrimSpace(bundle.Title); got != "First" {
		t.Fatalf("seeded title=%q want=%q", got, "First")
	}

	if _, err := os.Stat(filepath.Join(store.Root(), seedManifestFileName)); err != nil {
		t.Fatalf("seed marker missing: %v", err)
	}
}

func TestSeedCatalogFromBundledCookbooks_NoOpOnMatchingManifest(t *testing.T) {
	bundledDir := t.TempDir()
	catalogDir := t.TempDir()
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", catalogDir)

	if err := writeBundleFile(filepath.Join(bundledDir, "google.workspace", "1.3.0.json"), sampleBundle("google.workspace", "1.3.0", "Stable")); err != nil {
		t.Fatalf("write bundle: %v", err)
	}

	store, err := catalog.NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	first, err := seedCatalogFromBundledCookbooks(store, bundledDir)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	second, err := seedCatalogFromBundledCookbooks(store, bundledDir)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if first.ManifestHash != second.ManifestHash {
		t.Fatalf("manifest hash changed unexpectedly: %q vs %q", first.ManifestHash, second.ManifestHash)
	}
	if !second.Skipped {
		t.Fatalf("expected second seed to skip")
	}
}

func TestSeedCatalogFromBundledCookbooks_OverwriteWhenBundleChanges(t *testing.T) {
	bundledDir := t.TempDir()
	catalogDir := t.TempDir()
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", catalogDir)

	path := filepath.Join(bundledDir, "google.workspace", "1.3.0.json")
	if err := writeBundleFile(path, sampleBundle("google.workspace", "1.3.0", "Original")); err != nil {
		t.Fatalf("write original bundle: %v", err)
	}

	store, err := catalog.NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := seedCatalogFromBundledCookbooks(store, bundledDir); err != nil {
		t.Fatalf("first seed: %v", err)
	}

	if err := writeBundleFile(path, sampleBundle("google.workspace", "1.3.0", "Updated")); err != nil {
		t.Fatalf("write updated bundle: %v", err)
	}
	if _, err := seedCatalogFromBundledCookbooks(store, bundledDir); err != nil {
		t.Fatalf("second seed: %v", err)
	}

	bundle, err := store.GetBundle("google.workspace", "1.3.0")
	if err != nil {
		t.Fatalf("fetch updated bundle: %v", err)
	}
	if got := strings.TrimSpace(bundle.Title); got != "Updated" {
		t.Fatalf("bundle title=%q want=%q", got, "Updated")
	}
}

func TestSeedCatalogFromBundledCookbooks_InvalidBundleFails(t *testing.T) {
	bundledDir := t.TempDir()
	catalogDir := t.TempDir()
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", catalogDir)

	if err := os.MkdirAll(filepath.Join(bundledDir, "invalid.cb"), 0o755); err != nil {
		t.Fatalf("mkdir invalid dir: %v", err)
	}
	invalidPath := filepath.Join(bundledDir, "invalid.cb", "1.0.0.json")
	if err := os.WriteFile(invalidPath, []byte(`{"type":"accords.cookbook.bundle.v1","cookbook_id":"invalid.cb","version":"1.0.0","title":"","entries":[]}`), 0o644); err != nil {
		t.Fatalf("write invalid bundle: %v", err)
	}

	store, err := catalog.NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := seedCatalogFromBundledCookbooks(store, bundledDir); err == nil {
		t.Fatalf("expected invalid bundle to fail seeding")
	}
}

func TestValidateRouteCatalogConsistency(t *testing.T) {
	catalogDir := t.TempDir()
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", catalogDir)

	store, err := catalog.NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	bundle, err := catalog.DecodeBundle(sampleBundle("google.workspace", "1.3.0", "Seeded"))
	if err != nil {
		t.Fatalf("decode sample bundle: %v", err)
	}
	if _, _, err := store.UpsertBundle(bundle, catalog.ConflictPolicyFail); err != nil {
		t.Fatalf("upsert sample bundle: %v", err)
	}

	okRegistry := routing.Registry{
		Version: "1.0.0",
		Routes: []routing.RegistryRoute{
			{
				RouteID:    "google.gmail.send_email.v1",
				Strategy:   routing.StrategyRecipe,
				CookbookID: "google.workspace",
				Version:    "1.3.0",
				EntryID:    "gmail_recipe_labels_list_v1",
			},
			{
				RouteID:        "generic.http.request.v1",
				Strategy:       routing.StrategyConnectorExecuteJob,
				ConnectorID:    "generic.http",
				Verb:           "generic.http.request.v1",
				CookbookID:     "",
				Version:        "",
				EntryID:        "",
				RequiredInputs: []string{"url"},
			},
		},
	}
	if err := validateRouteCatalogConsistency(store, okRegistry); err != nil {
		t.Fatalf("expected consistency success, got error: %v", err)
	}

	badRegistry := routing.Registry{
		Version: "1.0.0",
		Routes: []routing.RegistryRoute{
			{
				RouteID:    "google.gmail.reply_email.v1",
				Strategy:   routing.StrategyRecipe,
				CookbookID: "google.workspace",
				Version:    "1.3.0",
				EntryID:    "missing_entry",
			},
		},
	}
	err = validateRouteCatalogConsistency(store, badRegistry)
	if err == nil {
		t.Fatalf("expected consistency error for missing route entry")
	}
	if !strings.Contains(err.Error(), "route_id=google.gmail.reply_email.v1") {
		t.Fatalf("unexpected consistency error: %v", err)
	}
}

func TestDefaultRegistryConsistentWithRepositoryCookbooks(t *testing.T) {
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatalf("find repo root: %v", err)
	}
	repoCookbooks := filepath.Join(repoRoot, "cookbooks")
	info, err := os.Stat(repoCookbooks)
	if errors.Is(err, os.ErrNotExist) {
		t.Skipf("repository cookbooks directory missing: %s", repoCookbooks)
	}
	if err != nil {
		t.Fatalf("stat repository cookbooks: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("repository cookbooks path is not a directory: %s", repoCookbooks)
	}

	catalogDir := t.TempDir()
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", catalogDir)
	store, err := catalog.NewStore("")
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := seedCatalogFromBundledCookbooks(store, repoCookbooks); err != nil {
		t.Fatalf("seed repository cookbooks: %v", err)
	}

	registry, err := routing.LoadDefaultRegistry()
	if err != nil {
		t.Fatalf("load default registry: %v", err)
	}
	if err := validateRouteCatalogConsistency(store, registry); err != nil {
		t.Fatalf("registry consistency failed: %v", err)
	}
}

func sampleBundle(cookbookID, version, title string) map[string]any {
	return map[string]any{
		"type":        catalog.BundleTypeV1,
		"cookbook_id": cookbookID,
		"version":     version,
		"title":       title,
		"entries": []map[string]any{
			{
				"entry_id":     "gmail_recipe_labels_list_v1",
				"entry_type":   catalog.EntryTypeRecipeVerb,
				"title":        "List Gmail Labels",
				"connector_id": "google.gmail",
				"verb":         "google.gmail.list_labels.v1",
				"request": map[string]any{
					"connector_id": "google.gmail",
					"verb":         "google.gmail.list_labels.v1",
					"payload":      map[string]any{},
				},
			},
		},
	}
}

func writeBundleFile(path string, bundle map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	return os.WriteFile(path, raw, 0o644)
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
