package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"accords-mcp/internal/catalog"
	"accords-mcp/internal/routing"
)

func (s *Server) registerRoutingTools() {
	s.addTool(Tool{
		Name:        "vaultclaw_route_resolve",
		Description: "Deterministically resolve a natural-language Vault request into an executable route specification.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"request_text": map[string]any{"type": "string"},
				"options":      map[string]any{"type": "object"},
				"context": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"facts": map[string]any{"type": "object"},
					},
				},
			},
			"required": []string{"request_text"},
		},
		Handler: s.handleRouteResolve,
	})
}

func (s *Server) handleRouteResolve(ctx context.Context, args map[string]any) (map[string]any, error) {
	requestText := strings.TrimSpace(strArg(args, "request_text"))
	if requestText == "" {
		requestText = strings.TrimSpace(strArg(args, "request"))
	}
	if requestText == "" {
		requestText = strings.TrimSpace(strArg(args, "text"))
	}
	if requestText == "" {
		return envelopeFailure("MCP_VALIDATION_ERROR", "validation", "request_text is required", false, "", map[string]any{}), nil
	}

	optionsRaw := mapArg(args, "options")
	allowSearchFallback := true
	if optionsRaw != nil {
		allowSearchFallback = boolArg(optionsRaw, "allow_search_fallback", true)
	}
	contextRaw := mapArg(args, "context")
	facts := map[string]any{}
	if contextRaw != nil {
		if candidate := mapArg(contextRaw, "facts"); candidate != nil {
			facts = candidate
		}
	}

	resolver, err := routing.NewDefaultResolver()
	if err != nil {
		return envelopeFailure(
			"MCP_INTERNAL",
			"internal",
			fmt.Sprintf("resolver initialization failed: %v", err),
			false,
			"",
			map[string]any{},
		), nil
	}

	result := resolver.Resolve(ctx, routing.ResolveRequest{
		RequestText: requestText,
		Options: routing.ResolveOptions{
			AllowSearchFallback: allowSearchFallback,
		},
		Facts: facts,
	}, func(_ context.Context, filter routing.SearchFilter) ([]routing.SearchCandidate, error) {
		store, err := catalog.NewStore("")
		if err != nil {
			return nil, err
		}
		rows, err := store.SearchRecipes(catalog.SearchFilter{
			Query:       strings.TrimSpace(filter.Query),
			ConnectorID: strings.TrimSpace(filter.ConnectorID),
			EntryType:   strings.TrimSpace(filter.EntryType),
			Tags:        append([]string(nil), filter.Tags...),
		})
		if err != nil {
			return nil, err
		}
		out := make([]routing.SearchCandidate, 0, len(rows))
		for _, row := range rows {
			requiredInputs := requiredInputsFromCatalogEntry(store, row)
			out = append(out, routing.SearchCandidate{
				CookbookID:     row.CookbookID,
				Version:        row.Version,
				EntryID:        row.EntryID,
				EntryType:      row.EntryType,
				Title:          row.Title,
				ConnectorID:    row.ConnectorID,
				Verb:           row.Verb,
				Tags:           append([]string(nil), row.Tags...),
				RequiredInputs: requiredInputs,
			})
		}
		return out, nil
	})

	meta := map[string]any{"resolver": "registry+search.v1"}
	return envelopeSuccess(map[string]any{
		"status":                 string(result.Status),
		"confidence":             string(result.Confidence),
		"domain":                 result.Domain,
		"route":                  result.Route,
		"execution":              result.Execution,
		"inputs":                 result.Inputs,
		"missing_inputs":         result.MissingInputs,
		"autofilled_inputs":      result.AutofilledInputs,
		"missing_input_guidance": result.MissingInputGuidance,
		"progress_hint":          result.ProgressHint,
		"needs_clarification":    result.NeedsClarification,
		"reasons":                result.Reasons,
		"fallback_hint":          result.FallbackHint,
	}, meta), nil
}

func requiredInputsFromCatalogEntry(store *catalog.Store, row catalog.SearchResult) []string {
	switch strings.TrimSpace(row.EntryType) {
	case catalog.EntryTypeTemplateVerb, catalog.EntryTypeTemplatePlan:
	default:
		return nil
	}
	_, entry, err := store.GetEntry(strings.TrimSpace(row.CookbookID), strings.TrimSpace(row.Version), strings.TrimSpace(row.EntryID))
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, binding := range entry.Bindings {
		if !binding.IsRequired() || binding.HasDefault() {
			continue
		}
		inputKey := strings.TrimSpace(binding.InputKey)
		if inputKey == "" {
			continue
		}
		if _, ok := seen[inputKey]; ok {
			continue
		}
		seen[inputKey] = struct{}{}
		out = append(out, inputKey)
	}
	sort.Strings(out)
	return out
}
