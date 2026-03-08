package catalog

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const envCatalogDir = "ACCORDS_MCP_CATALOG_DIR"

type Store struct {
	root string
}

func NewStore(root string) (*Store, error) {
	resolved, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}
	s := &Store{root: resolved}
	if err := s.ensureLayout(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Root() string {
	return s.root
}

func resolveRoot(input string) (string, error) {
	root := strings.TrimSpace(input)
	if root == "" {
		root = strings.TrimSpace(os.Getenv(envCatalogDir))
	}
	if root == "" {
		cfgDir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		root = filepath.Join(cfgDir, "accords-mcp", "catalog")
	}
	return root, nil
}

func (s *Store) ensureLayout() error {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return internalStoreError("failed creating catalog root directory", "ensure_layout", s.root, err)
	}
	return s.withWriteLock("ensure_layout", func() error {
		if err := os.MkdirAll(filepath.Join(s.root, "cookbooks"), 0o755); err != nil {
			return internalStoreError("failed creating catalog directories", "ensure_layout", filepath.Join(s.root, "cookbooks"), err)
		}
		if _, err := os.Stat(s.sourcesPath()); errors.Is(err, os.ErrNotExist) {
			if wErr := s.writeJSONFile(s.sourcesPath(), SourcesFile{Sources: []SourceConfig{}}); wErr != nil {
				return internalStoreError("failed initializing sources file", "ensure_layout", s.sourcesPath(), wErr)
			}
		}
		if _, err := os.Stat(s.indexPath()); errors.Is(err, os.ErrNotExist) {
			if wErr := s.writeJSONFile(s.indexPath(), LocalIndex{GeneratedAtUnixMS: nowMS(), Cookbooks: []CookbookIndexItem{}}); wErr != nil {
				return internalStoreError("failed initializing index file", "ensure_layout", s.indexPath(), wErr)
			}
		}
		return nil
	})
}

func (s *Store) UpsertBundle(bundle Bundle, conflictPolicy string) (CookbookIndexItem, bool, error) {
	var empty CookbookIndexItem
	bundle = CanonicalizeBundle(bundle)
	if err := ValidateBundle(bundle); err != nil {
		return empty, false, err
	}
	if !isSafeIdentifier(bundle.CookbookID) || !isSafeIdentifier(bundle.Version) {
		return empty, false, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "cookbook_id/version contains unsupported path characters", false, map[string]any{})
	}
	hash, err := ContentHashBundle(bundle)
	if err != nil {
		return empty, false, internalStoreError("failed to hash bundle", "bundle_upsert", "", err)
	}
	path := bundlePath(s.root, bundle.CookbookID, bundle.Version)
	if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
		return empty, false, internalStoreError("failed creating cookbook directory", "bundle_upsert", filepath.Dir(path), mkErr)
	}

	policy := NormalizeConflictPolicy(conflictPolicy)
	var outItem CookbookIndexItem
	var changed bool
	if err := s.withWriteLock("bundle_upsert", func() error {
		existing, exists, readErr := s.readBundleFile(path)
		if readErr != nil {
			return readErr
		}
		if exists {
			existingHash, hErr := ContentHashBundle(existing)
			if hErr != nil {
				return internalStoreError("failed to hash existing bundle", "bundle_upsert", path, hErr)
			}
			if existingHash == hash {
				item, idxErr := s.upsertIndexItem(bundle)
				if idxErr != nil {
					return idxErr
				}
				outItem = item
				changed = false
				return nil
			}
			switch policy {
			case ConflictPolicyFail:
				return NewError("MCP_CATALOG_CONFLICT", "validation", "bundle already exists with different content", false, map[string]any{
					"cookbook_id": bundle.CookbookID,
					"version":     bundle.Version,
				})
			case ConflictPolicySkipIfExist:
				item, idxErr := s.upsertIndexItem(existing)
				if idxErr != nil {
					return idxErr
				}
				outItem = item
				changed = false
				return nil
			case ConflictPolicyOverwrite:
				// continue below.
			}
		}

		if wErr := s.writeJSONFile(path, bundle); wErr != nil {
			return internalStoreError("failed to write bundle", "bundle_upsert", path, wErr)
		}
		item, idxErr := s.upsertIndexItem(bundle)
		if idxErr != nil {
			return idxErr
		}
		outItem = item
		changed = true
		return nil
	}); err != nil {
		return empty, false, err
	}
	return outItem, changed, nil
}

func (s *Store) DeleteBundle(cookbookID, version string) (string, bool, error) {
	cookbookID = strings.TrimSpace(cookbookID)
	version = strings.TrimSpace(version)
	if cookbookID == "" {
		return "", false, NewError("MCP_VALIDATION_ERROR", "validation", "cookbook_id is required", false, map[string]any{})
	}
	if !isSafeIdentifier(cookbookID) {
		return "", false, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "cookbook_id contains unsupported path characters", false, map[string]any{})
	}

	resolvedVersion, err := s.resolveVersion(cookbookID, version)
	if err != nil {
		return "", false, err
	}
	path := bundlePath(s.root, cookbookID, resolvedVersion)
	deleted := false
	if err := s.withWriteLock("bundle_delete", func() error {
		if _, statErr := os.Stat(path); errors.Is(statErr, os.ErrNotExist) {
			_ = s.deleteIndexItem(cookbookID, resolvedVersion)
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil {
			return internalStoreError("failed to delete bundle file", "bundle_delete", path, rmErr)
		}
		if cleanErr := s.cleanupCookbookDir(cookbookID); cleanErr != nil {
			return internalStoreError("failed cleaning cookbook directory", "bundle_delete", filepath.Join(s.root, "cookbooks", cookbookID), cleanErr)
		}
		if idxErr := s.deleteIndexItem(cookbookID, resolvedVersion); idxErr != nil {
			return idxErr
		}
		deleted = true
		return nil
	}); err != nil {
		return resolvedVersion, false, err
	}
	return resolvedVersion, deleted, nil
}

func (s *Store) GetBundle(cookbookID, version string) (Bundle, error) {
	var empty Bundle
	cookbookID = strings.TrimSpace(cookbookID)
	if cookbookID == "" {
		return empty, NewError("MCP_VALIDATION_ERROR", "validation", "cookbook_id is required", false, map[string]any{})
	}
	if !isSafeIdentifier(cookbookID) {
		return empty, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "cookbook_id contains unsupported path characters", false, map[string]any{})
	}
	resolvedVersion, err := s.resolveVersion(cookbookID, version)
	if err != nil {
		return empty, err
	}
	path := bundlePath(s.root, cookbookID, resolvedVersion)
	bundle, exists, err := s.readBundleFile(path)
	if err != nil {
		return empty, err
	}
	if !exists {
		return empty, NewError("MCP_CATALOG_NOT_FOUND", "validation", "cookbook not found", false, map[string]any{
			"cookbook_id": cookbookID,
			"version":     resolvedVersion,
		})
	}
	return bundle, nil
}

func (s *Store) ListCookbooks(filter map[string]any) ([]CookbookIndexItem, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(stringVal(filter["query"])))
	cookbookID := strings.TrimSpace(stringVal(filter["cookbook_id"]))
	tag := strings.TrimSpace(stringVal(filter["tag"]))
	out := make([]CookbookIndexItem, 0, len(idx.Cookbooks))
	for _, item := range idx.Cookbooks {
		if cookbookID != "" && item.CookbookID != cookbookID {
			continue
		}
		if tag != "" && !hasTag(item.Tags, tag) {
			continue
		}
		if query != "" && !containsAny(query, item.CookbookID, item.Title, item.Description, strings.Join(item.Tags, " ")) {
			continue
		}
		out = append(out, item)
	}
	sortCookbookIndex(out)
	return out, nil
}

func (s *Store) SearchRecipes(filter SearchFilter) ([]SearchResult, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return nil, err
	}
	query := strings.ToLower(strings.TrimSpace(filter.Query))
	connectorID := strings.TrimSpace(filter.ConnectorID)
	verb := strings.TrimSpace(filter.Verb)
	entryType := strings.TrimSpace(filter.EntryType)
	tags := CanonicalizeTags(filter.Tags)

	out := make([]SearchResult, 0)
	for _, item := range idx.Cookbooks {
		for _, entry := range item.Entries {
			if connectorID != "" && entry.ConnectorID != connectorID {
				continue
			}
			if verb != "" && entry.Verb != verb {
				continue
			}
			if entryType != "" && entry.EntryType != entryType {
				continue
			}
			if len(tags) > 0 && !hasAllTags(entry.Tags, tags) {
				continue
			}
			if query != "" && !containsAny(query, entry.EntryID, entry.Title, entry.EntryType, entry.ConnectorID, entry.Verb, strings.Join(entry.Tags, " "), item.CookbookID, item.Title) {
				continue
			}
			out = append(out, SearchResult{
				CookbookID:  item.CookbookID,
				Version:     item.Version,
				EntryID:     entry.EntryID,
				EntryType:   entry.EntryType,
				Title:       entry.Title,
				ConnectorID: entry.ConnectorID,
				Verb:        entry.Verb,
				Tags:        append([]string(nil), entry.Tags...),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CookbookID != out[j].CookbookID {
			return out[i].CookbookID < out[j].CookbookID
		}
		if out[i].Version != out[j].Version {
			return compareVersion(out[i].Version, out[j].Version) > 0
		}
		return out[i].EntryID < out[j].EntryID
	})
	return out, nil
}

func (s *Store) GetEntry(cookbookID, version, entryID string) (Bundle, Entry, error) {
	var emptyEntry Entry
	bundle, err := s.GetBundle(cookbookID, version)
	if err != nil {
		return Bundle{}, emptyEntry, err
	}
	entryID = strings.TrimSpace(entryID)
	for _, entry := range bundle.Entries {
		if entry.EntryID == entryID {
			return bundle, entry, nil
		}
	}
	return bundle, emptyEntry, NewError("MCP_CATALOG_NOT_FOUND", "validation", "entry not found in cookbook", false, map[string]any{
		"cookbook_id": cookbookID,
		"version":     bundle.Version,
		"entry_id":    entryID,
	})
}

func (s *Store) ListSources() ([]SourceConfig, error) {
	doc, err := s.loadSources()
	if err != nil {
		return nil, err
	}
	out := append([]SourceConfig(nil), doc.Sources...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].SourceID < out[j].SourceID
	})
	return out, nil
}

func (s *Store) UpsertSource(source SourceConfig) (SourceConfig, bool, error) {
	source.SourceID = strings.TrimSpace(source.SourceID)
	source.IndexURL = strings.TrimSpace(source.IndexURL)
	source.AuthMode = NormalizeAuthMode(source.AuthMode)
	if source.AuthMode != AuthModeBearerEnv {
		source.AuthEnvVar = ""
	}
	if err := ValidateSource(source); err != nil {
		return SourceConfig{}, false, err
	}

	var created bool
	if err := s.withWriteLock("source_upsert", func() error {
		doc, readErr := s.loadSources()
		if readErr != nil {
			return readErr
		}
		updated := false
		for i := range doc.Sources {
			if doc.Sources[i].SourceID == source.SourceID {
				doc.Sources[i] = source
				updated = true
				break
			}
		}
		if !updated {
			doc.Sources = append(doc.Sources, source)
		}
		sort.Slice(doc.Sources, func(i, j int) bool {
			return doc.Sources[i].SourceID < doc.Sources[j].SourceID
		})
		if wErr := s.writeJSONFile(s.sourcesPath(), doc); wErr != nil {
			return internalStoreError("failed to persist source", "source_upsert", s.sourcesPath(), wErr)
		}
		created = !updated
		return nil
	}); err != nil {
		return SourceConfig{}, false, err
	}
	return source, created, nil
}

func (s *Store) DeleteSource(sourceID string) (bool, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return false, NewError("MCP_VALIDATION_ERROR", "validation", "source_id is required", false, map[string]any{})
	}
	removed := false
	if err := s.withWriteLock("source_delete", func() error {
		doc, readErr := s.loadSources()
		if readErr != nil {
			return readErr
		}
		next := make([]SourceConfig, 0, len(doc.Sources))
		for _, src := range doc.Sources {
			if src.SourceID == sourceID {
				removed = true
				continue
			}
			next = append(next, src)
		}
		if !removed {
			return nil
		}
		doc.Sources = next
		if wErr := s.writeJSONFile(s.sourcesPath(), doc); wErr != nil {
			return internalStoreError("failed to persist source delete", "source_delete", s.sourcesPath(), wErr)
		}
		return nil
	}); err != nil {
		return false, err
	}
	return removed, nil
}

func (s *Store) GetSource(sourceID string) (SourceConfig, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return SourceConfig{}, NewError("MCP_VALIDATION_ERROR", "validation", "source_id is required", false, map[string]any{})
	}
	doc, err := s.loadSources()
	if err != nil {
		return SourceConfig{}, err
	}
	for _, src := range doc.Sources {
		if src.SourceID == sourceID {
			return src, nil
		}
	}
	return SourceConfig{}, NewError("MCP_CATALOG_SOURCE_NOT_FOUND", "validation", "catalog source not found", false, map[string]any{
		"source_id": sourceID,
	})
}

func (s *Store) resolveVersion(cookbookID, version string) (string, error) {
	if strings.TrimSpace(version) != "" {
		if !isSafeIdentifier(version) {
			return "", NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "version contains unsupported path characters", false, map[string]any{})
		}
		return strings.TrimSpace(version), nil
	}
	versions, err := s.availableVersions(cookbookID)
	if err != nil {
		return "", err
	}
	if len(versions) == 0 {
		return "", NewError("MCP_CATALOG_NOT_FOUND", "validation", "cookbook not found", false, map[string]any{"cookbook_id": cookbookID})
	}
	return LatestVersion(versions), nil
}

func (s *Store) availableVersions(cookbookID string) ([]string, error) {
	dir := filepath.Join(s.root, "cookbooks", cookbookID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, internalStoreError("failed reading cookbook versions", "versions_read", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		version := strings.TrimSuffix(name, ".json")
		if strings.TrimSpace(version) == "" {
			continue
		}
		out = append(out, version)
	}
	return out, nil
}

func (s *Store) readBundleFile(path string) (Bundle, bool, error) {
	var bundle Bundle
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return bundle, false, nil
	}
	if err != nil {
		return bundle, false, internalStoreError("failed reading bundle file", "bundle_read", path, err)
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		return bundle, false, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "stored bundle is not valid json", false, map[string]any{"path": path, "cause": err.Error()})
	}
	return bundle, true, nil
}

func (s *Store) rebuildIndex() error {
	return s.withWriteLock("index_rebuild", func() error {
		return s.rebuildIndexLocked()
	})
}

func (s *Store) rebuildIndexLocked() error {
	root := filepath.Join(s.root, "cookbooks")
	cookbookDirs, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return s.saveIndex(LocalIndex{GeneratedAtUnixMS: nowMS(), Cookbooks: []CookbookIndexItem{}})
	}
	if err != nil {
		return internalStoreError("failed to read cookbooks directory", "index_rebuild", root, err)
	}

	items := make([]CookbookIndexItem, 0)
	for _, cookbookDir := range cookbookDirs {
		if !cookbookDir.IsDir() {
			continue
		}
		cookbookID := cookbookDir.Name()
		versionDir := filepath.Join(root, cookbookID)
		versionFiles, dirErr := os.ReadDir(versionDir)
		if dirErr != nil {
			return internalStoreError("failed to read cookbook versions directory", "index_rebuild", versionDir, dirErr)
		}
		for _, vf := range versionFiles {
			if vf.IsDir() || !strings.HasSuffix(vf.Name(), ".json") {
				continue
			}
			version := strings.TrimSuffix(vf.Name(), ".json")
			path := filepath.Join(root, cookbookID, vf.Name())
			bundle, exists, readErr := s.readBundleFile(path)
			if readErr != nil {
				return readErr
			}
			if !exists {
				continue
			}
			item, itemErr := indexItemFromBundle(bundle)
			if itemErr != nil {
				return itemErr
			}
			item.CookbookID = cookbookID
			item.Version = version
			items = append(items, item)
		}
	}
	sortCookbookIndex(items)
	return s.saveIndex(LocalIndex{GeneratedAtUnixMS: nowMS(), Cookbooks: items})
}

func sortCookbookIndex(items []CookbookIndexItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].CookbookID != items[j].CookbookID {
			return items[i].CookbookID < items[j].CookbookID
		}
		return compareVersion(items[i].Version, items[j].Version) < 0
	})
}

func (s *Store) findIndexItem(cookbookID, version string) (CookbookIndexItem, error) {
	idx, err := s.loadIndex()
	if err != nil {
		return CookbookIndexItem{}, err
	}
	for _, item := range idx.Cookbooks {
		if item.CookbookID == cookbookID && item.Version == version {
			return item, nil
		}
	}
	return CookbookIndexItem{}, NewError("MCP_CATALOG_NOT_FOUND", "validation", "cookbook not found in index", false, map[string]any{
		"cookbook_id": cookbookID,
		"version":     version,
	})
}

func (s *Store) loadIndex() (LocalIndex, error) {
	idx, needsRepair, err := s.loadIndexNoRepair()
	if err != nil {
		return LocalIndex{}, err
	}
	if !needsRepair {
		return idx, nil
	}
	if err := s.withWriteLock("index_repair", func() error {
		lockedIdx, lockedNeedsRepair, lockedErr := s.loadIndexNoRepair()
		if lockedErr != nil {
			return lockedErr
		}
		if !lockedNeedsRepair {
			_ = lockedIdx
			return nil
		}
		return s.rebuildIndexLocked()
	}); err != nil {
		return LocalIndex{}, err
	}
	idx, needsRepair, err = s.loadIndexNoRepair()
	if err != nil {
		return LocalIndex{}, err
	}
	if needsRepair {
		return LocalIndex{}, internalStoreError("failed repairing catalog index", "index_repair", s.indexPath(), errors.New("index still invalid after repair"))
	}
	return idx, nil
}

func (s *Store) loadIndexNoRepair() (LocalIndex, bool, error) {
	raw, err := os.ReadFile(s.indexPath())
	if errors.Is(err, os.ErrNotExist) {
		return LocalIndex{GeneratedAtUnixMS: nowMS(), Cookbooks: []CookbookIndexItem{}}, true, nil
	}
	if err != nil {
		return LocalIndex{}, false, internalStoreError("failed reading index", "index_read", s.indexPath(), err)
	}
	if len(raw) == 0 {
		return LocalIndex{GeneratedAtUnixMS: nowMS(), Cookbooks: []CookbookIndexItem{}}, true, nil
	}
	var idx LocalIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		return LocalIndex{GeneratedAtUnixMS: nowMS(), Cookbooks: []CookbookIndexItem{}}, true, nil
	}
	if idx.Cookbooks == nil {
		idx.Cookbooks = []CookbookIndexItem{}
	}
	return idx, false, nil
}

func (s *Store) loadSources() (SourcesFile, error) {
	var doc SourcesFile
	raw, err := os.ReadFile(s.sourcesPath())
	if errors.Is(err, os.ErrNotExist) {
		return SourcesFile{Sources: []SourceConfig{}}, nil
	}
	if err != nil {
		return doc, internalStoreError("failed reading sources file", "sources_read", s.sourcesPath(), err)
	}
	if len(raw) == 0 {
		return SourcesFile{Sources: []SourceConfig{}}, nil
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return doc, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "sources file schema invalid", false, map[string]any{"cause": err.Error()})
	}
	return doc, nil
}

func (s *Store) upsertIndexItem(bundle Bundle) (CookbookIndexItem, error) {
	idx, needsRepair, err := s.loadIndexNoRepair()
	if err != nil {
		return CookbookIndexItem{}, err
	}
	if needsRepair {
		if rbErr := s.rebuildIndexLocked(); rbErr != nil {
			return CookbookIndexItem{}, rbErr
		}
		idx, _, err = s.loadIndexNoRepair()
		if err != nil {
			return CookbookIndexItem{}, err
		}
	}
	item, err := indexItemFromBundle(bundle)
	if err != nil {
		return CookbookIndexItem{}, err
	}
	updated := false
	for i := range idx.Cookbooks {
		if idx.Cookbooks[i].CookbookID == item.CookbookID && idx.Cookbooks[i].Version == item.Version {
			idx.Cookbooks[i] = item
			updated = true
			break
		}
	}
	if !updated {
		idx.Cookbooks = append(idx.Cookbooks, item)
	}
	sortCookbookIndex(idx.Cookbooks)
	idx.GeneratedAtUnixMS = nowMS()
	if err := s.saveIndex(idx); err != nil {
		return CookbookIndexItem{}, err
	}
	return item, nil
}

func (s *Store) deleteIndexItem(cookbookID, version string) error {
	idx, needsRepair, err := s.loadIndexNoRepair()
	if err != nil {
		return err
	}
	if needsRepair {
		if rbErr := s.rebuildIndexLocked(); rbErr != nil {
			return rbErr
		}
		idx, _, err = s.loadIndexNoRepair()
		if err != nil {
			return err
		}
	}
	next := make([]CookbookIndexItem, 0, len(idx.Cookbooks))
	for _, item := range idx.Cookbooks {
		if item.CookbookID == cookbookID && item.Version == version {
			continue
		}
		next = append(next, item)
	}
	idx.Cookbooks = next
	sortCookbookIndex(idx.Cookbooks)
	idx.GeneratedAtUnixMS = nowMS()
	return s.saveIndex(idx)
}

func indexItemFromBundle(bundle Bundle) (CookbookIndexItem, error) {
	hash, err := ContentHashBundle(bundle)
	if err != nil {
		return CookbookIndexItem{}, internalStoreError("failed to hash bundle for index", "index_item", "", err)
	}
	entryRows := make([]EntryIndexItem, 0, len(bundle.Entries))
	for _, entry := range bundle.Entries {
		entryRows = append(entryRows, EntryIndexItem{
			EntryID:     entry.EntryID,
			EntryType:   entry.EntryType,
			Title:       entry.Title,
			ConnectorID: entryConnectorForIndex(entry),
			Verb:        entry.Verb,
			Tags:        CanonicalizeTags(entry.Tags),
		})
	}
	sort.Slice(entryRows, func(i, j int) bool {
		return entryRows[i].EntryID < entryRows[j].EntryID
	})
	return CookbookIndexItem{
		CookbookID:  bundle.CookbookID,
		Version:     bundle.Version,
		Title:       bundle.Title,
		Description: bundle.Description,
		Tags:        CanonicalizeTags(bundle.Tags),
		EntryCount:  len(bundle.Entries),
		ContentHash: hash,
		Entries:     entryRows,
	}, nil
}

func (s *Store) saveIndex(idx LocalIndex) error {
	if idx.Cookbooks == nil {
		idx.Cookbooks = []CookbookIndexItem{}
	}
	sortCookbookIndex(idx.Cookbooks)
	idx.GeneratedAtUnixMS = nowMS()
	if err := s.writeJSONFile(s.indexPath(), idx); err != nil {
		return internalStoreError("failed writing index", "index_save", s.indexPath(), err)
	}
	return nil
}

func (s *Store) writeJSONFile(path string, value any) error {
	return writeJSONFileAtomic(path, value)
}

func (s *Store) cleanupCookbookDir(cookbookID string) error {
	dir := filepath.Join(s.root, "cookbooks", cookbookID)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return os.Remove(dir)
	}
	return nil
}

func (s *Store) indexPath() string {
	return filepath.Join(s.root, "index.json")
}

func (s *Store) sourcesPath() string {
	return filepath.Join(s.root, "sources.json")
}

func nowMS() int64 {
	return time.Now().UnixMilli()
}

func entryConnectorForIndex(entry Entry) string {
	if connectorID := strings.TrimSpace(entry.ConnectorID); connectorID != "" {
		return connectorID
	}
	var plan map[string]any
	switch strings.TrimSpace(entry.EntryType) {
	case EntryTypeRecipePlan:
		plan = entry.Plan
	case EntryTypeTemplatePlan:
		plan = entry.BasePlan
	default:
		return ""
	}
	if len(plan) == 0 {
		return ""
	}
	rawSteps, _ := plan["steps"].([]any)
	if len(rawSteps) == 0 {
		return ""
	}
	seen := map[string]struct{}{}
	for _, raw := range rawSteps {
		step, _ := raw.(map[string]any)
		if len(step) == 0 {
			continue
		}
		connectorID := strings.TrimSpace(stringVal(step["connector_id"]))
		if connectorID == "" {
			continue
		}
		seen[connectorID] = struct{}{}
		if len(seen) > 1 {
			return ""
		}
	}
	for connectorID := range seen {
		return connectorID
	}
	return ""
}

func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func isSafeIdentifier(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if strings.Contains(v, "..") {
		return false
	}
	if strings.ContainsRune(v, os.PathSeparator) {
		return false
	}
	if strings.Contains(v, "/") || strings.Contains(v, "\\") {
		return false
	}
	return true
}

func containsAny(query string, fields ...string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), q) {
			return true
		}
	}
	return false
}

func hasTag(tags []string, needle string) bool {
	target := strings.TrimSpace(needle)
	for _, tag := range tags {
		if tag == target {
			return true
		}
	}
	return false
}

func hasAllTags(existing []string, required []string) bool {
	set := map[string]struct{}{}
	for _, tag := range existing {
		set[tag] = struct{}{}
	}
	for _, need := range required {
		if _, ok := set[need]; !ok {
			return false
		}
	}
	return true
}

func stringVal(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func internalStoreError(message, operation, path string, cause error) error {
	details := map[string]any{
		"operation": strings.TrimSpace(operation),
		"path":      strings.TrimSpace(path),
	}
	if cause != nil {
		details["cause"] = cause.Error()
	}
	return NewError("MCP_INTERNAL", "internal", strings.TrimSpace(message), false, details)
}
