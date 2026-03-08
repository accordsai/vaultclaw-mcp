package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreNewStoreDoesNotRewriteIndexWhenValid(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	if _, _, err := store.UpsertBundle(sampleBundle("1.0.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("UpsertBundle failed: %v", err)
	}
	indexPath := filepath.Join(root, "index.json")
	before, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index before failed: %v", err)
	}
	beforeMod := before.ModTime()
	time.Sleep(20 * time.Millisecond)
	if _, err := NewStore(root); err != nil {
		t.Fatalf("NewStore second call failed: %v", err)
	}
	after, err := os.Stat(indexPath)
	if err != nil {
		t.Fatalf("stat index after failed: %v", err)
	}
	if !after.ModTime().Equal(beforeMod) {
		t.Fatalf("expected NewStore not to rewrite valid index; before=%v after=%v", beforeMod, after.ModTime())
	}
}

func TestStoreIncrementalIndexUpsertDelete(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleBundle("1.0.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v1 failed: %v", err)
	}
	if _, _, err := store.UpsertBundle(sampleBundle("1.1.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v2 failed: %v", err)
	}

	before, err := store.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex failed: %v", err)
	}
	if len(before.Cookbooks) != 2 {
		t.Fatalf("expected two index entries, got %d", len(before.Cookbooks))
	}
	beforeByVersion := map[string]CookbookIndexItem{}
	for _, item := range before.Cookbooks {
		beforeByVersion[item.Version] = item
	}

	overwritten := sampleBundle("1.0.0")
	overwritten.Title = "HTTP Cookbook Updated"
	if _, _, err := store.UpsertBundle(overwritten, ConflictPolicyOverwrite); err != nil {
		t.Fatalf("overwrite upsert failed: %v", err)
	}
	afterOverwrite, err := store.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex after overwrite failed: %v", err)
	}
	if len(afterOverwrite.Cookbooks) != 2 {
		t.Fatalf("expected two index entries after overwrite, got %d", len(afterOverwrite.Cookbooks))
	}
	afterByVersion := map[string]CookbookIndexItem{}
	for _, item := range afterOverwrite.Cookbooks {
		afterByVersion[item.Version] = item
	}
	if afterByVersion["1.1.0"].ContentHash != beforeByVersion["1.1.0"].ContentHash {
		t.Fatalf("expected untouched version hash to remain stable")
	}
	if afterByVersion["1.0.0"].Title != "HTTP Cookbook Updated" {
		t.Fatalf("expected overwritten index title update, got %q", afterByVersion["1.0.0"].Title)
	}

	deletedVersion, deleted, err := store.DeleteBundle("net.http", "1.1.0")
	if err != nil {
		t.Fatalf("DeleteBundle failed: %v", err)
	}
	if !deleted || deletedVersion != "1.1.0" {
		t.Fatalf("unexpected delete result deleted=%v version=%s", deleted, deletedVersion)
	}
	afterDelete, err := store.loadIndex()
	if err != nil {
		t.Fatalf("loadIndex after delete failed: %v", err)
	}
	if len(afterDelete.Cookbooks) != 1 {
		t.Fatalf("expected one index entry after delete, got %d", len(afterDelete.Cookbooks))
	}
	if afterDelete.Cookbooks[0].Version != "1.0.0" {
		t.Fatalf("expected remaining version 1.0.0, got %s", afterDelete.Cookbooks[0].Version)
	}
}

func TestStoreLoadIndexRepairsCorruptIndex(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleBundle("1.0.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	if err := os.WriteFile(store.indexPath(), []byte("{bad json"), 0o644); err != nil {
		t.Fatalf("failed writing corrupt index: %v", err)
	}

	items, err := store.ListCookbooks(nil)
	if err != nil {
		t.Fatalf("ListCookbooks should repair index, got error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one cookbook after repair, got %d", len(items))
	}

	raw, err := os.ReadFile(store.indexPath())
	if err != nil {
		t.Fatalf("failed reading repaired index: %v", err)
	}
	var decoded LocalIndex
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("repaired index is not valid json: %v", err)
	}
}

func TestWriteJSONFileAtomicReplacesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.json")
	if err := writeJSONFileAtomic(path, map[string]any{"before": true}); err != nil {
		t.Fatalf("writeJSONFileAtomic before failed: %v", err)
	}
	if err := writeJSONFileAtomic(path, map[string]any{"after": true}); err != nil {
		t.Fatalf("writeJSONFileAtomic after failed: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("atomic file content invalid json: %v", err)
	}
	if _, ok := obj["after"]; !ok {
		t.Fatalf("expected replaced file to include 'after' key")
	}
	if _, ok := obj["before"]; ok {
		t.Fatalf("expected replaced file not to include stale 'before' key")
	}
}
