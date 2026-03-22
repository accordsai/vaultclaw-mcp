package routing

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed registry.v1.json
var embeddedRegistryFS embed.FS

type Registry struct {
	Version             string
	DocumentTypeAliases map[string]string
	Routes              []RegistryRoute
}

type RegistryRoute struct {
	RouteID        string
	Domain         string
	CookbookID     string
	Version        string
	EntryID        string
	EntryType      string
	Strategy       ExecutionStrategy
	ConnectorID    string
	Verb           string
	Keywords       []string
	RequiredInputs []string
	OptionalInputs []string
	Tags           []string
	Orchestration  map[string]any
}

type registryFile struct {
	Type                string             `json:"type"`
	Version             string             `json:"version"`
	DocumentTypeAliases map[string]string  `json:"document_type_aliases"`
	Routes              []registryRouteRaw `json:"routes"`
}

type registryRouteRaw struct {
	RouteID        string         `json:"route_id"`
	Domain         string         `json:"domain"`
	CookbookID     string         `json:"cookbook_id"`
	Version        string         `json:"version"`
	EntryID        string         `json:"entry_id"`
	EntryType      string         `json:"entry_type"`
	Strategy       string         `json:"strategy"`
	ConnectorID    string         `json:"connector_id"`
	Verb           string         `json:"verb"`
	Keywords       []string       `json:"keywords"`
	RequiredInputs []string       `json:"required_inputs"`
	OptionalInputs []string       `json:"optional_inputs"`
	Tags           []string       `json:"tags"`
	Orchestration  map[string]any `json:"orchestration"`
}

func LoadDefaultRegistry() (Registry, error) {
	blob, err := embeddedRegistryFS.ReadFile("registry.v1.json")
	if err != nil {
		return Registry{}, fmt.Errorf("read embedded routing registry: %w", err)
	}
	return decodeRegistry(blob)
}

func decodeRegistry(blob []byte) (Registry, error) {
	var raw registryFile
	if err := json.Unmarshal(blob, &raw); err != nil {
		return Registry{}, fmt.Errorf("decode routing registry: %w", err)
	}
	if strings.TrimSpace(raw.Type) != "vaultclaw.routing.registry.v1" {
		return Registry{}, fmt.Errorf("unsupported registry type %q", raw.Type)
	}
	if strings.TrimSpace(raw.Version) == "" {
		return Registry{}, fmt.Errorf("registry version is required")
	}
	if len(raw.Routes) == 0 {
		return Registry{}, fmt.Errorf("registry routes must not be empty")
	}

	out := Registry{
		Version:             strings.TrimSpace(raw.Version),
		DocumentTypeAliases: normalizeAliasMap(raw.DocumentTypeAliases),
		Routes:              make([]RegistryRoute, 0, len(raw.Routes)),
	}
	for i, route := range raw.Routes {
		parsed, err := normalizeRegistryRoute(route)
		if err != nil {
			return Registry{}, fmt.Errorf("registry route[%d] invalid: %w", i, err)
		}
		out.Routes = append(out.Routes, parsed)
	}
	return out, nil
}

func normalizeRegistryRoute(raw registryRouteRaw) (RegistryRoute, error) {
	strategy := ExecutionStrategy(strings.TrimSpace(strings.ToUpper(raw.Strategy)))
	switch strategy {
	case StrategyTemplate, StrategyRecipe, StrategyConnectorExecuteJob, StrategyPlanExecute:
		// valid
	default:
		return RegistryRoute{}, fmt.Errorf("unsupported strategy %q", raw.Strategy)
	}

	route := RegistryRoute{
		RouteID:        strings.TrimSpace(raw.RouteID),
		Domain:         strings.TrimSpace(strings.ToLower(raw.Domain)),
		CookbookID:     strings.TrimSpace(raw.CookbookID),
		Version:        strings.TrimSpace(raw.Version),
		EntryID:        strings.TrimSpace(raw.EntryID),
		EntryType:      strings.TrimSpace(strings.ToLower(raw.EntryType)),
		Strategy:       strategy,
		ConnectorID:    strings.TrimSpace(raw.ConnectorID),
		Verb:           strings.TrimSpace(raw.Verb),
		Keywords:       normalizeStringList(raw.Keywords),
		RequiredInputs: normalizeStringList(raw.RequiredInputs),
		OptionalInputs: normalizeStringList(raw.OptionalInputs),
		Tags:           normalizeStringList(raw.Tags),
		Orchestration:  cloneMap(raw.Orchestration),
	}

	if route.RouteID == "" {
		return RegistryRoute{}, fmt.Errorf("route_id is required")
	}
	if route.Domain == "" {
		return RegistryRoute{}, fmt.Errorf("domain is required")
	}
	if len(route.Keywords) == 0 {
		return RegistryRoute{}, fmt.Errorf("keywords must not be empty")
	}

	switch route.Strategy {
	case StrategyTemplate, StrategyRecipe:
		if route.CookbookID == "" || route.EntryID == "" || route.EntryType == "" {
			return RegistryRoute{}, fmt.Errorf("strategy %s requires cookbook_id, entry_id, and entry_type", route.Strategy)
		}
	case StrategyConnectorExecuteJob:
		if route.ConnectorID == "" || route.Verb == "" {
			return RegistryRoute{}, fmt.Errorf("strategy %s requires connector_id and verb", route.Strategy)
		}
	case StrategyPlanExecute:
		if route.CookbookID == "" && route.EntryID == "" {
			return RegistryRoute{}, fmt.Errorf("strategy %s requires executable plan reference", route.Strategy)
		}
	}
	return route, nil
}

func normalizeStringList(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		v := strings.TrimSpace(strings.ToLower(raw))
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func normalizeAliasMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for k, v := range values {
		key := normalizeAliasKey(k)
		val := strings.TrimSpace(v)
		if key == "" || val == "" {
			continue
		}
		out[key] = val
	}
	return out
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
