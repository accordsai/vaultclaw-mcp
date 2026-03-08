package mcp

import (
	"context"
	"strings"

	"accords-mcp/internal/catalog"
)

func (s *Server) registerCatalogTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_cookbooks_list",
		Description: "List locally installed cookbook bundles from catalog storage.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filter": map[string]any{"type": "object"},
			},
		},
		Handler: s.handleCookbooksList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbook_get",
		Description: "Get one cookbook bundle by cookbook_id and optional version.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cookbook_id": map[string]any{"type": "string"},
				"version":     map[string]any{"type": "string"},
			},
			"required": []string{"cookbook_id"},
		},
		Handler: s.handleCookbookGet,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbook_upsert",
		Description: "Create or update a local cookbook bundle.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bundle":          map[string]any{"type": "object"},
				"conflict_policy": map[string]any{"type": "string"},
			},
			"required": []string{"bundle"},
		},
		Handler: s.handleCookbookUpsert,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbook_delete",
		Description: "Delete a local cookbook bundle version. If version omitted, latest version is deleted.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cookbook_id": map[string]any{"type": "string"},
				"version":     map[string]any{"type": "string"},
			},
			"required": []string{"cookbook_id"},
		},
		Handler: s.handleCookbookDelete,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbook_export",
		Description: "Export a local cookbook bundle payload.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cookbook_id": map[string]any{"type": "string"},
				"version":     map[string]any{"type": "string"},
			},
			"required": []string{"cookbook_id"},
		},
		Handler: s.handleCookbookExport,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbook_import",
		Description: "Import a cookbook bundle into local storage.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"bundle":          map[string]any{"type": "object"},
				"conflict_policy": map[string]any{"type": "string"},
			},
			"required": []string{"bundle"},
		},
		Handler: s.handleCookbookImport,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_recipes_search",
		Description: "Search cookbook entries by query, connector_id, verb, tags, and entry_type.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":        map[string]any{"type": "string"},
				"connector_id": map[string]any{"type": "string"},
				"verb":         map[string]any{"type": "string"},
				"tags":         map[string]any{},
				"entry_type":   map[string]any{"type": "string"},
			},
		},
		Handler: s.handleRecipesSearch,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_recipe_get",
		Description: "Get one recipe/template entry by cookbook_id + recipe_id and optional version.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cookbook_id": map[string]any{"type": "string"},
				"recipe_id":   map[string]any{"type": "string"},
				"version":     map[string]any{"type": "string"},
			},
			"required": []string{"cookbook_id", "recipe_id"},
		},
		Handler: s.handleRecipeGet,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_template_render",
		Description: "Render a template entry to concrete VERB_REQUEST or PLAN payload without executing.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"cookbook_id": map[string]any{"type": "string"},
				"template_id": map[string]any{"type": "string"},
				"version":     map[string]any{"type": "string"},
				"inputs":      map[string]any{"type": "object"},
				"output_kind": map[string]any{"type": "string"},
			},
			"required": []string{"cookbook_id", "template_id", "inputs"},
		},
		Handler: s.handleTemplateRender,
	})
}

func (s *Server) handleCookbooksList(_ context.Context, args map[string]any) (map[string]any, error) {
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	filter := mapArg(args, "filter")
	items, err := store.ListCookbooks(filter)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"items":    items,
		"root_dir": store.Root(),
	}, nil), nil
}

func (s *Server) handleCookbookGet(_ context.Context, args map[string]any) (map[string]any, error) {
	cookbookID := strings.TrimSpace(strArg(args, "cookbook_id"))
	if cookbookID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "cookbook_id is required", false, "", map[string]any{}), nil
	}
	version := strings.TrimSpace(strArg(args, "version"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	bundle, err := store.GetBundle(cookbookID, version)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{"bundle": bundle}, nil), nil
}

func (s *Server) handleCookbookUpsert(_ context.Context, args map[string]any) (map[string]any, error) {
	return s.handleCookbookWrite(args)
}

func (s *Server) handleCookbookImport(_ context.Context, args map[string]any) (map[string]any, error) {
	return s.handleCookbookWrite(args)
}

func (s *Server) handleCookbookWrite(args map[string]any) (map[string]any, error) {
	bundle, err := catalog.DecodeBundle(args["bundle"])
	if err != nil {
		return failureFromError(err), nil
	}
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	conflictPolicy := catalog.NormalizeConflictPolicy(strArg(args, "conflict_policy"))
	item, created, err := store.UpsertBundle(bundle, conflictPolicy)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"cookbook":           item,
		"created_or_updated": created,
		"conflict_policy":    conflictPolicy,
	}, nil), nil
}

func (s *Server) handleCookbookDelete(_ context.Context, args map[string]any) (map[string]any, error) {
	cookbookID := strings.TrimSpace(strArg(args, "cookbook_id"))
	if cookbookID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "cookbook_id is required", false, "", map[string]any{}), nil
	}
	version := strings.TrimSpace(strArg(args, "version"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	deletedVersion, deleted, err := store.DeleteBundle(cookbookID, version)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"cookbook_id": cookbookID,
		"version":     deletedVersion,
		"deleted":     deleted,
	}, nil), nil
}

func (s *Server) handleCookbookExport(_ context.Context, args map[string]any) (map[string]any, error) {
	cookbookID := strings.TrimSpace(strArg(args, "cookbook_id"))
	if cookbookID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "cookbook_id is required", false, "", map[string]any{}), nil
	}
	version := strings.TrimSpace(strArg(args, "version"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	bundle, err := store.GetBundle(cookbookID, version)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{"bundle": bundle}, nil), nil
}

func (s *Server) handleRecipesSearch(_ context.Context, args map[string]any) (map[string]any, error) {
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	tags, parseErr := parseStringListArg(args["tags"])
	if parseErr != nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", parseErr.Error(), false, "", map[string]any{}), nil
	}
	filter := catalog.SearchFilter{
		Query:       strArg(args, "query"),
		ConnectorID: strArg(args, "connector_id"),
		Verb:        strArg(args, "verb"),
		Tags:        tags,
		EntryType:   strArg(args, "entry_type"),
	}
	items, err := store.SearchRecipes(filter)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{"items": items}, nil), nil
}

func (s *Server) handleRecipeGet(_ context.Context, args map[string]any) (map[string]any, error) {
	cookbookID := strings.TrimSpace(strArg(args, "cookbook_id"))
	recipeID := strings.TrimSpace(strArg(args, "recipe_id"))
	if cookbookID == "" || recipeID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "cookbook_id and recipe_id are required", false, "", map[string]any{}), nil
	}
	version := strings.TrimSpace(strArg(args, "version"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	bundle, entry, err := store.GetEntry(cookbookID, version, recipeID)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"cookbook_id": bundle.CookbookID,
		"version":     bundle.Version,
		"entry":       entry,
	}, nil), nil
}

func (s *Server) handleTemplateRender(_ context.Context, args map[string]any) (map[string]any, error) {
	cookbookID := strings.TrimSpace(strArg(args, "cookbook_id"))
	templateID := strings.TrimSpace(strArg(args, "template_id"))
	if cookbookID == "" || templateID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "cookbook_id and template_id are required", false, "", map[string]any{}), nil
	}
	inputs := mapArg(args, "inputs")
	if inputs == nil {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "inputs must be an object", false, "", map[string]any{}), nil
	}
	version := strings.TrimSpace(strArg(args, "version"))
	outputKind := strings.TrimSpace(strArg(args, "output_kind"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	result, err := store.RenderTemplate(cookbookID, templateID, version, inputs, outputKind)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"rendered":       result.Rendered,
		"missing_inputs": result.MissingInputs,
		"used_defaults":  result.UsedDefaults,
		"source_ref":     result.SourceRef,
	}, nil), nil
}

func parseStringListArg(raw any) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	switch v := raw.(type) {
	case string:
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			p := strings.TrimSpace(part)
			if p == "" {
				continue
			}
			out = append(out, p)
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, catalog.NewError("MCP_VALIDATION_ERROR", "validation", "string list must only include strings", false, map[string]any{"index": i})
			}
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			out = append(out, s)
		}
		return out, nil
	default:
		return nil, catalog.NewError("MCP_VALIDATION_ERROR", "validation", "value must be csv string or string array", false, map[string]any{})
	}
}
