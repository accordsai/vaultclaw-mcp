package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"accords-mcp/internal/catalog"
	"accords-mcp/internal/routing"
)

const (
	defaultBundledCookbooksDir = "/opt/accords-mcp/cookbooks"
	envBundledCookbooksDir     = "ACCORDS_MCP_BUNDLED_COOKBOOKS_DIR"
	seedManifestFileName       = ".bundled-cookbooks-seed.v1.json"
	seedManifestVersion        = "bundled-cookbooks-seed.v1"
)

type bundledCookbookFile struct {
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
}

type seededBundleRef struct {
	CookbookID  string `json:"cookbook_id"`
	Version     string `json:"version"`
	ContentHash string `json:"content_hash"`
	SourcePath  string `json:"source_path"`
}

type bundledSeedManifest struct {
	Version      string                `json:"version"`
	ManifestHash string                `json:"manifest_hash"`
	SeededAtUTC  string                `json:"seeded_at_utc"`
	Files        []bundledCookbookFile `json:"files"`
	Bundles      []seededBundleRef     `json:"bundles"`
}

type seedResult struct {
	ManifestHash string
	Skipped      bool
	Seeded       int
}

func bootstrapCatalog() error {
	bundledDir, err := resolveBundledCookbooksDir()
	if err != nil {
		return err
	}
	store, err := catalog.NewStore("")
	if err != nil {
		return fmt.Errorf("initialize catalog store: %w", err)
	}
	seed, err := seedCatalogFromBundledCookbooks(store, bundledDir)
	if err != nil {
		return err
	}
	registry, err := routing.LoadDefaultRegistry()
	if err != nil {
		return fmt.Errorf("load routing registry: %w", err)
	}
	if err := validateRouteCatalogConsistency(store, registry); err != nil {
		return err
	}

	if seed.Skipped {
		_, _ = fmt.Fprintf(os.Stderr, "accords-mcp bootstrap: cookbook seed manifest unchanged (%s)\n", seed.ManifestHash)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "accords-mcp bootstrap: seeded %d cookbook bundle(s) (%s)\n", seed.Seeded, seed.ManifestHash)
	}
	return nil
}

func resolveBundledCookbooksDir() (string, error) {
	if configured := strings.TrimSpace(os.Getenv(envBundledCookbooksDir)); configured != "" {
		info, err := os.Stat(configured)
		if err != nil {
			return "", fmt.Errorf("%s path invalid %q: %w", envBundledCookbooksDir, configured, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%s path is not a directory: %q", envBundledCookbooksDir, configured)
		}
		return configured, nil
	}

	info, err := os.Stat(defaultBundledCookbooksDir)
	if err == nil {
		if !info.IsDir() {
			return "", fmt.Errorf("default bundled cookbooks path is not a directory: %q", defaultBundledCookbooksDir)
		}
		return defaultBundledCookbooksDir, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("stat default bundled cookbooks path %q: %w", defaultBundledCookbooksDir, err)
	}

	repoCookbooksDir, repoErr := findRepoCookbooksDir()
	if repoErr == nil {
		return repoCookbooksDir, nil
	}
	return "", fmt.Errorf("bundled cookbook directory not found (checked %q and repo fallback): %w", defaultBundledCookbooksDir, repoErr)
}

func findRepoCookbooksDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(dir, "cookbooks")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.IsDir() {
				return candidate, nil
			}
			return "", fmt.Errorf("cookbooks path is not a directory: %q", candidate)
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}

func seedCatalogFromBundledCookbooks(store *catalog.Store, bundledDir string) (seedResult, error) {
	if store == nil {
		return seedResult{}, fmt.Errorf("catalog store is required")
	}
	files, manifestHash, err := collectBundledCookbookFiles(bundledDir)
	if err != nil {
		return seedResult{}, err
	}

	markerPath := filepath.Join(store.Root(), seedManifestFileName)
	prev, err := readSeedManifest(markerPath)
	if err != nil {
		return seedResult{}, err
	}
	if prev != nil && prev.Version == seedManifestVersion && prev.ManifestHash == manifestHash {
		return seedResult{
			ManifestHash: manifestHash,
			Skipped:      true,
			Seeded:       len(prev.Bundles),
		}, nil
	}

	seeded := make([]seededBundleRef, 0, len(files))
	for _, file := range files {
		path := filepath.Join(bundledDir, file.RelativePath)
		raw, err := os.ReadFile(path)
		if err != nil {
			return seedResult{}, fmt.Errorf("read bundled cookbook %q: %w", file.RelativePath, err)
		}
		var payload any
		if err := json.Unmarshal(raw, &payload); err != nil {
			return seedResult{}, fmt.Errorf("decode bundled cookbook json %q: %w", file.RelativePath, err)
		}
		bundle, err := catalog.DecodeBundle(payload)
		if err != nil {
			return seedResult{}, fmt.Errorf("decode bundled cookbook %q: %w", file.RelativePath, err)
		}
		item, _, err := store.UpsertBundle(bundle, catalog.ConflictPolicyOverwrite)
		if err != nil {
			return seedResult{}, fmt.Errorf("upsert bundled cookbook %q: %w", file.RelativePath, err)
		}
		seeded = append(seeded, seededBundleRef{
			CookbookID:  item.CookbookID,
			Version:     item.Version,
			ContentHash: item.ContentHash,
			SourcePath:  file.RelativePath,
		})
	}

	sort.Slice(seeded, func(i, j int) bool {
		if seeded[i].CookbookID != seeded[j].CookbookID {
			return seeded[i].CookbookID < seeded[j].CookbookID
		}
		if seeded[i].Version != seeded[j].Version {
			return seeded[i].Version < seeded[j].Version
		}
		return seeded[i].SourcePath < seeded[j].SourcePath
	})

	manifest := bundledSeedManifest{
		Version:      seedManifestVersion,
		ManifestHash: manifestHash,
		SeededAtUTC:  time.Now().UTC().Format(time.RFC3339Nano),
		Files:        files,
		Bundles:      seeded,
	}
	if err := writeSeedManifest(markerPath, manifest); err != nil {
		return seedResult{}, err
	}

	return seedResult{
		ManifestHash: manifestHash,
		Skipped:      false,
		Seeded:       len(seeded),
	}, nil
}

func collectBundledCookbookFiles(root string) ([]bundledCookbookFile, string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, "", fmt.Errorf("stat bundled cookbook directory %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, "", fmt.Errorf("bundled cookbook path is not a directory: %q", root)
	}

	files := make([]bundledCookbookFile, 0, 16)
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.ToLower(filepath.Ext(path)) != ".json" {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" || strings.HasPrefix(rel, "../") {
			return fmt.Errorf("invalid bundled cookbook path %q", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		files = append(files, bundledCookbookFile{
			RelativePath: rel,
			SHA256:       hex.EncodeToString(sum[:]),
		})
		return nil
	}); err != nil {
		return nil, "", fmt.Errorf("walk bundled cookbook directory %q: %w", root, err)
	}
	if len(files) == 0 {
		return nil, "", fmt.Errorf("no bundled cookbook json files found under %q", root)
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].RelativePath < files[j].RelativePath
	})
	raw, err := json.Marshal(files)
	if err != nil {
		return nil, "", fmt.Errorf("encode bundled cookbook manifest: %w", err)
	}
	manifestSum := sha256.Sum256(raw)
	return files, hex.EncodeToString(manifestSum[:]), nil
}

func readSeedManifest(path string) (*bundledSeedManifest, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read cookbook seed marker %q: %w", path, err)
	}
	var marker bundledSeedManifest
	if err := json.Unmarshal(raw, &marker); err != nil {
		return nil, fmt.Errorf("decode cookbook seed marker %q: %w", path, err)
	}
	return &marker, nil
}

func writeSeedManifest(path string, marker bundledSeedManifest) error {
	raw, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return fmt.Errorf("encode cookbook seed marker: %w", err)
	}
	raw = append(raw, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, raw, 0o644); err != nil {
		return fmt.Errorf("write cookbook seed marker temp file %q: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace cookbook seed marker %q: %w", path, err)
	}
	return nil
}

func validateRouteCatalogConsistency(store *catalog.Store, registry routing.Registry) error {
	if store == nil {
		return fmt.Errorf("catalog store is required")
	}
	missing := make([]string, 0)
	for _, route := range registry.Routes {
		if !routeRequiresCatalogEntry(route) {
			continue
		}

		cookbookID := strings.TrimSpace(route.CookbookID)
		entryID := strings.TrimSpace(route.EntryID)
		if cookbookID == "" || entryID == "" {
			missing = append(missing, fmt.Sprintf("route_id=%s strategy=%s missing cookbook_id/entry_id", route.RouteID, route.Strategy))
			continue
		}
		version := strings.TrimSpace(route.Version)
		if _, _, err := store.GetEntry(cookbookID, version, entryID); err != nil {
			missing = append(missing, fmt.Sprintf("route_id=%s cookbook_id=%s version=%s entry_id=%s error=%v", route.RouteID, cookbookID, version, entryID, err))
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("route-to-catalog consistency check failed (%d issue(s)): %s", len(missing), strings.Join(missing, "; "))
	}
	return nil
}

func routeRequiresCatalogEntry(route routing.RegistryRoute) bool {
	switch route.Strategy {
	case routing.StrategyTemplate, routing.StrategyRecipe:
		return true
	case routing.StrategyPlanExecute:
		return strings.TrimSpace(route.CookbookID) != "" || strings.TrimSpace(route.EntryID) != ""
	default:
		return false
	}
}
