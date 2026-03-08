package catalog

import "testing"

func TestValidateBundleEntryTypes(t *testing.T) {
	base := Bundle{
		Type:       BundleTypeV1,
		CookbookID: "cb",
		Version:    "1.0.0",
		Title:      "Title",
	}

	cases := []struct {
		name    string
		entry   Entry
		wantErr bool
	}{
		{
			name: "recipe verb valid",
			entry: Entry{
				EntryID:     "e1",
				EntryType:   EntryTypeRecipeVerb,
				ConnectorID: "google",
				Verb:        "google.gmail.send.v1",
				Request:     map[string]any{"method": "GET"},
			},
		},
		{
			name: "recipe plan valid",
			entry: Entry{
				EntryID:   "e2",
				EntryType: EntryTypeRecipePlan,
				Plan:      map[string]any{"steps": []any{map[string]any{"step_id": "s1"}}},
			},
		},
		{
			name: "template verb valid",
			entry: Entry{
				EntryID:       "e3",
				EntryType:     EntryTypeTemplateVerb,
				BaseRequest:   map[string]any{"connector_id": "google", "verb": "google.gmail.send.v1", "request": map[string]any{}},
				Bindings:      []Binding{{TargetPath: "/request/url", InputKey: "url"}},
				InputSchema:   map[string]any{"type": "object"},
				ConnectorID:   "google",
				Verb:          "google.gmail.send.v1",
				PolicyVersion: "1",
			},
		},
		{
			name: "template plan valid",
			entry: Entry{
				EntryID:   "e4",
				EntryType: EntryTypeTemplatePlan,
				BasePlan:  map[string]any{"steps": []any{map[string]any{"step_id": "s1"}}},
				Bindings:  []Binding{{TargetPath: "/steps/0/request_base/url", InputKey: "url"}},
			},
		},
		{
			name: "unknown type invalid",
			entry: Entry{
				EntryID:   "e5",
				EntryType: "bad.type",
			},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			b.Entries = []Entry{tc.entry}
			err := ValidateBundle(b)
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestValidateBundleMissingRequiredFieldsByEntryType(t *testing.T) {
	base := Bundle{
		Type:       BundleTypeV1,
		CookbookID: "google.workspace",
		Version:    "1.1.0",
		Title:      "Google Workspace Gmail Cookbook",
	}

	cases := []struct {
		name  string
		entry Entry
	}{
		{
			name: "recipe verb missing request",
			entry: Entry{
				EntryID:     "gmail_send_missing_request",
				EntryType:   EntryTypeRecipeVerb,
				ConnectorID: "google",
				Verb:        "google.gmail.send.v1",
			},
		},
		{
			name: "recipe plan missing plan",
			entry: Entry{
				EntryID:   "gmail_plan_missing_plan",
				EntryType: EntryTypeRecipePlan,
			},
		},
		{
			name: "template plan missing base_plan",
			entry: Entry{
				EntryID:   "gmail_tpl_missing_base_plan",
				EntryType: EntryTypeTemplatePlan,
				Bindings:  []Binding{{TargetPath: "/steps/0/request_base/thread_id", InputKey: "thread_id"}},
			},
		},
		{
			name: "unsupported entry type",
			entry: Entry{
				EntryID:   "gmail_bad_type",
				EntryType: "recipe.unknown.v1",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := base
			b.Entries = []Entry{tc.entry}
			err := ValidateBundle(b)
			if err == nil {
				t.Fatalf("expected validation error")
			}
			cErr := asCatalogErr(err)
			if cErr == nil || cErr.Code != "MCP_CATALOG_SCHEMA_INVALID" {
				t.Fatalf("expected MCP_CATALOG_SCHEMA_INVALID, got: %v", err)
			}
		})
	}
}

func TestValidateBundleDuplicateEntryID(t *testing.T) {
	bundle := Bundle{
		Type:       BundleTypeV1,
		CookbookID: "google.workspace",
		Version:    "1.1.0",
		Title:      "Google Workspace Gmail Cookbook",
		Entries: []Entry{
			{
				EntryID:     "dup",
				EntryType:   EntryTypeRecipeVerb,
				ConnectorID: "google",
				Verb:        "google.gmail.send.v1",
				Request:     map[string]any{"subject": "x"},
			},
			{
				EntryID:   "dup",
				EntryType: EntryTypeRecipePlan,
				Plan:      map[string]any{"steps": []any{map[string]any{"step_id": "s1"}}},
			},
		},
	}

	err := ValidateBundle(bundle)
	if err == nil {
		t.Fatalf("expected duplicate entry_id validation error")
	}
	cErr := asCatalogErr(err)
	if cErr == nil || cErr.Code != "MCP_CATALOG_SCHEMA_INVALID" {
		t.Fatalf("expected MCP_CATALOG_SCHEMA_INVALID, got: %v", err)
	}
}

func TestValidateSource(t *testing.T) {
	err := ValidateSource(SourceConfig{
		SourceID: "main",
		IndexURL: "https://example.invalid/index.json",
		Enabled:  true,
		AuthMode: AuthModeBearerEnv,
	})
	if err == nil {
		t.Fatalf("expected missing auth_env_var validation error")
	}
}
