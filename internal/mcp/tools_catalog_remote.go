package mcp

import (
	"context"
	"strings"

	"accords-mcp/internal/catalog"
)

func (s *Server) registerCatalogRemoteTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_catalog_sources_list",
		Description: "List configured remote catalog pull sources.",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		Handler:     s.handleCatalogSourcesList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_catalog_source_upsert",
		Description: "Create or update a remote catalog source.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"source": map[string]any{"type": "object"}},
			"required":   []string{"source"},
		},
		Handler: s.handleCatalogSourceUpsert,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_catalog_source_delete",
		Description: "Delete a configured remote catalog source.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"source_id": map[string]any{"type": "string"}},
			"required":   []string{"source_id"},
		},
		Handler: s.handleCatalogSourceDelete,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbooks_remote_list",
		Description: "Fetch and list cookbooks from one remote source index.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source_id": map[string]any{"type": "string"},
				"query":     map[string]any{"type": "string"},
			},
			"required": []string{"source_id"},
		},
		Handler: s.handleCookbooksRemoteList,
	})
	s.addTool(Tool{
		Name:        "vaultclaw_cookbook_remote_install",
		Description: "Install one cookbook bundle from a remote source index into local storage.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source_id":       map[string]any{"type": "string"},
				"cookbook_id":     map[string]any{"type": "string"},
				"version":         map[string]any{"type": "string"},
				"conflict_policy": map[string]any{"type": "string"},
				"expected_sha256": map[string]any{"type": "string"},
			},
			"required": []string{"source_id", "cookbook_id"},
		},
		Handler: s.handleCookbookRemoteInstall,
	})
}

func (s *Server) handleCatalogSourcesList(_ context.Context, _ map[string]any) (map[string]any, error) {
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	sources, err := store.ListSources()
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"sources":  sources,
		"root_dir": store.Root(),
	}, nil), nil
}

func (s *Server) handleCatalogSourceUpsert(_ context.Context, args map[string]any) (map[string]any, error) {
	source, err := catalog.DecodeSource(args["source"])
	if err != nil {
		return failureFromError(err), nil
	}
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	out, created, err := store.UpsertSource(source)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"source":  out,
		"created": created,
	}, nil), nil
}

func (s *Server) handleCatalogSourceDelete(_ context.Context, args map[string]any) (map[string]any, error) {
	sourceID := strings.TrimSpace(strArg(args, "source_id"))
	if sourceID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "source_id is required", false, "", map[string]any{}), nil
	}
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	deleted, err := store.DeleteSource(sourceID)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"source_id": sourceID,
		"deleted":   deleted,
	}, nil), nil
}

func (s *Server) handleCookbooksRemoteList(ctx context.Context, args map[string]any) (map[string]any, error) {
	sourceID := strings.TrimSpace(strArg(args, "source_id"))
	if sourceID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "source_id is required", false, "", map[string]any{}), nil
	}
	query := strings.TrimSpace(strArg(args, "query"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	items, idx, err := store.RemoteList(ctx, sourceID, query)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"source_id": sourceID,
		"index":     idx,
		"items":     items,
	}, nil), nil
}

func (s *Server) handleCookbookRemoteInstall(ctx context.Context, args map[string]any) (map[string]any, error) {
	sourceID := strings.TrimSpace(strArg(args, "source_id"))
	cookbookID := strings.TrimSpace(strArg(args, "cookbook_id"))
	if sourceID == "" || cookbookID == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "source_id and cookbook_id are required", false, "", map[string]any{}), nil
	}
	version := strings.TrimSpace(strArg(args, "version"))
	conflictPolicy := catalog.NormalizeConflictPolicy(strArg(args, "conflict_policy"))
	expectedSHA := strings.TrimSpace(strArg(args, "expected_sha256"))
	store, err := catalog.NewStore("")
	if err != nil {
		return failureFromError(err), nil
	}
	installed, err := store.RemoteInstall(ctx, sourceID, cookbookID, version, conflictPolicy, expectedSHA)
	if err != nil {
		return failureFromError(err), nil
	}
	return envelopeSuccess(map[string]any{
		"install":         installed,
		"conflict_policy": conflictPolicy,
	}, nil), nil
}
