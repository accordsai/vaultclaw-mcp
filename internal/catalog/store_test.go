package catalog

import (
	"testing"
)

func TestStoreUpsertListGetDelete(t *testing.T) {
	store := newTestStore(t)
	bundle := sampleBundle("1.0.0")

	item, changed, err := store.UpsertBundle(bundle, ConflictPolicyFail)
	if err != nil {
		t.Fatalf("UpsertBundle failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true on first upsert")
	}
	if item.CookbookID != bundle.CookbookID || item.Version != bundle.Version {
		t.Fatalf("unexpected index item: %+v", item)
	}

	list, err := store.ListCookbooks(nil)
	if err != nil {
		t.Fatalf("ListCookbooks failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one cookbook in index, got %d", len(list))
	}

	got, err := store.GetBundle(bundle.CookbookID, "")
	if err != nil {
		t.Fatalf("GetBundle latest failed: %v", err)
	}
	if got.Version != "1.0.0" {
		t.Fatalf("expected latest version 1.0.0 got %s", got.Version)
	}

	deletedVersion, deleted, err := store.DeleteBundle(bundle.CookbookID, "")
	if err != nil {
		t.Fatalf("DeleteBundle failed: %v", err)
	}
	if !deleted || deletedVersion != "1.0.0" {
		t.Fatalf("unexpected delete result deleted=%v version=%s", deleted, deletedVersion)
	}
}

func TestStoreConflictPolicies(t *testing.T) {
	store := newTestStore(t)
	base := sampleBundle("1.0.0")

	if _, _, err := store.UpsertBundle(base, ConflictPolicyFail); err != nil {
		t.Fatalf("initial upsert failed: %v", err)
	}

	changed := base
	changed.Title = "Changed Title"

	if _, _, err := store.UpsertBundle(changed, ConflictPolicyFail); err == nil {
		t.Fatalf("expected conflict error for FAIL policy")
	} else if e := asCatalogErr(err); e == nil || e.Code != "MCP_CATALOG_CONFLICT" {
		t.Fatalf("expected MCP_CATALOG_CONFLICT, got %v", err)
	}

	item, changedWrite, err := store.UpsertBundle(changed, ConflictPolicySkipIfExist)
	if err != nil {
		t.Fatalf("skip if exists upsert failed: %v", err)
	}
	if changedWrite {
		t.Fatalf("expected changed=false for SKIP_IF_EXISTS")
	}
	if item.Title != base.Title {
		t.Fatalf("expected existing bundle metadata for SKIP_IF_EXISTS")
	}

	item, changedWrite, err = store.UpsertBundle(changed, ConflictPolicyOverwrite)
	if err != nil {
		t.Fatalf("overwrite upsert failed: %v", err)
	}
	if !changedWrite {
		t.Fatalf("expected changed=true for OVERWRITE")
	}
	if item.Title != changed.Title {
		t.Fatalf("expected overwritten title, got %s", item.Title)
	}
}

func TestStoreVersionResolution(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleBundle("1.0.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v1.0.0 failed: %v", err)
	}
	if _, _, err := store.UpsertBundle(sampleBundle("1.10.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v1.10.0 failed: %v", err)
	}
	if _, _, err := store.UpsertBundle(sampleBundle("1.2.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v1.2.0 failed: %v", err)
	}

	got, err := store.GetBundle("net.http", "")
	if err != nil {
		t.Fatalf("GetBundle latest failed: %v", err)
	}
	if got.Version != "1.10.0" {
		t.Fatalf("expected semver-like latest 1.10.0, got %s", got.Version)
	}
}

func TestSearchRecipesPrefersLatestVersion(t *testing.T) {
	store := newTestStore(t)
	if _, _, err := store.UpsertBundle(sampleBundle("1.1.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v1.1.0 failed: %v", err)
	}
	if _, _, err := store.UpsertBundle(sampleBundle("1.2.0"), ConflictPolicyFail); err != nil {
		t.Fatalf("upsert v1.2.0 failed: %v", err)
	}

	results, err := store.SearchRecipes(SearchFilter{
		Query:     "recipe_get",
		EntryType: EntryTypeRecipeVerb,
	})
	if err != nil {
		t.Fatalf("SearchRecipes failed: %v", err)
	}
	if len(results) < 2 {
		t.Fatalf("expected at least two results across versions, got %d", len(results))
	}
	if results[0].Version != "1.2.0" {
		t.Fatalf("expected latest version first, got %s", results[0].Version)
	}
}

func TestSearchRecipesTemplatePlanMatchesConnectorFromPlanSteps(t *testing.T) {
	store := newTestStore(t)
	bundle := Bundle{
		Type:       BundleTypeV1,
		CookbookID: "google.workspace",
		Version:    "1.2.0",
		Title:      "Google Workspace Gmail Cookbook",
		Entries: []Entry{
			{
				EntryID:   "gmail_tpl_plan_send_email_v1",
				EntryType: EntryTypeTemplatePlan,
				BasePlan: map[string]any{
					"type":          "connector.execution.plan.v1",
					"start_step_id": "s1",
					"steps": []any{
						map[string]any{
							"step_id":      "s1",
							"connector_id": "google",
							"verb":         "google.gmail.drafts.create",
						},
						map[string]any{
							"step_id":      "s2",
							"connector_id": "google",
							"verb":         "google.gmail.drafts.send",
						},
					},
				},
			},
		},
	}
	if _, _, err := store.UpsertBundle(bundle, ConflictPolicyFail); err != nil {
		t.Fatalf("upsert template plan bundle failed: %v", err)
	}

	results, err := store.SearchRecipes(SearchFilter{
		ConnectorID: "google",
		EntryType:   EntryTypeTemplatePlan,
	})
	if err != nil {
		t.Fatalf("SearchRecipes failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected one template.plan result, got %d (%v)", len(results), results)
	}
	if results[0].EntryID != "gmail_tpl_plan_send_email_v1" {
		t.Fatalf("unexpected entry_id %s", results[0].EntryID)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	return store
}

func sampleBundle(version string) Bundle {
	required := true
	return Bundle{
		Type:       BundleTypeV1,
		CookbookID: "net.http",
		Version:    version,
		Title:      "HTTP Cookbook",
		Tags:       []string{"http", "demo"},
		Entries: []Entry{
			{
				EntryID:     "recipe_get",
				EntryType:   EntryTypeRecipeVerb,
				ConnectorID: "generic.http",
				Verb:        "generic.http.request.v1",
				Request: map[string]any{
					"method": "GET",
					"url":    "https://example.invalid",
				},
			},
			{
				EntryID:   "tpl_get",
				EntryType: EntryTypeTemplateVerb,
				BaseRequest: map[string]any{
					"connector_id": "generic.http",
					"verb":         "generic.http.request.v1",
					"request": map[string]any{
						"method": "GET",
						"url":    "https://placeholder.invalid",
					},
				},
				Bindings: []Binding{
					{
						TargetPath: "/request/url",
						InputKey:   "url",
						Required:   &required,
					},
				},
			},
		},
	}
}
