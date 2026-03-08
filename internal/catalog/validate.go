package catalog

import (
	"strings"
)

func ValidateBundle(bundle Bundle) error {
	if strings.TrimSpace(bundle.Type) != BundleTypeV1 {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle.type must be accords.cookbook.bundle.v1", false, map[string]any{})
	}
	if strings.TrimSpace(bundle.CookbookID) == "" {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle.cookbook_id is required", false, map[string]any{})
	}
	if strings.TrimSpace(bundle.Version) == "" {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle.version is required", false, map[string]any{})
	}
	if strings.TrimSpace(bundle.Title) == "" {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle.title is required", false, map[string]any{})
	}
	if len(bundle.Entries) == 0 {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle.entries must be non-empty", false, map[string]any{})
	}
	seen := map[string]struct{}{}
	for i, entry := range bundle.Entries {
		id := strings.TrimSpace(entry.EntryID)
		if id == "" {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "entry.entry_id is required", false, map[string]any{"entry_index": i})
		}
		if _, ok := seen[id]; ok {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "duplicate entry_id in bundle", false, map[string]any{"entry_id": id})
		}
		seen[id] = struct{}{}
		if err := ValidateEntry(entry); err != nil {
			cErr := asCatalogErr(err)
			if cErr == nil {
				return err
			}
			details := cloneDetails(cErr.Details)
			details["entry_id"] = id
			return NewError(cErr.Code, cErr.Category, cErr.Message, cErr.Retryable, details)
		}
	}
	return nil
}

func ValidateEntry(entry Entry) error {
	if strings.TrimSpace(entry.EntryID) == "" {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "entry_id is required", false, map[string]any{})
	}
	switch strings.TrimSpace(entry.EntryType) {
	case EntryTypeRecipeVerb:
		if strings.TrimSpace(entry.ConnectorID) == "" {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "recipe.verb.v1 requires connector_id", false, map[string]any{})
		}
		if strings.TrimSpace(entry.Verb) == "" {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "recipe.verb.v1 requires verb", false, map[string]any{})
		}
		if entry.Request == nil {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "recipe.verb.v1 requires request object", false, map[string]any{})
		}
	case EntryTypeRecipePlan:
		if entry.Plan == nil {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "recipe.plan.v1 requires plan object", false, map[string]any{})
		}
	case EntryTypeTemplateVerb:
		if entry.BaseRequest == nil {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "template.verb.v1 requires base_request object", false, map[string]any{})
		}
		if err := validateBindings(entry.Bindings); err != nil {
			return err
		}
	case EntryTypeTemplatePlan:
		if entry.BasePlan == nil {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "template.plan.v1 requires base_plan object", false, map[string]any{})
		}
		if err := validateBindings(entry.Bindings); err != nil {
			return err
		}
	default:
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "unsupported entry_type", false, map[string]any{
			"entry_type": entry.EntryType,
		})
	}
	return nil
}

func ValidateSource(source SourceConfig) error {
	if strings.TrimSpace(source.SourceID) == "" {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "source.source_id is required", false, map[string]any{})
	}
	if !validateURL(source.IndexURL) {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "source.index_url must be http(s) url", false, map[string]any{})
	}
	mode := NormalizeAuthMode(source.AuthMode)
	if mode == AuthModeBearerEnv && strings.TrimSpace(source.AuthEnvVar) == "" {
		return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "source.auth_env_var is required for BEARER_ENV", false, map[string]any{})
	}
	return nil
}

func ValidateRemoteIndex(idx RemoteIndex) error {
	if strings.TrimSpace(idx.Type) != IndexTypeV1 {
		return NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "remote index type is invalid", false, map[string]any{})
	}
	if len(idx.Items) == 0 {
		return nil
	}
	for _, item := range idx.Items {
		if strings.TrimSpace(item.CookbookID) == "" || strings.TrimSpace(item.Version) == "" || strings.TrimSpace(item.DownloadURL) == "" {
			return NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "remote index item missing required fields", false, map[string]any{
				"cookbook_id": item.CookbookID,
				"version":     item.Version,
			})
		}
		if !validateURL(item.DownloadURL) {
			return NewError("MCP_CATALOG_REMOTE_FETCH_FAILED", "network", "remote download_url must be http(s)", false, map[string]any{
				"cookbook_id": item.CookbookID,
				"version":     item.Version,
			})
		}
	}
	return nil
}

func validateBindings(bindings []Binding) error {
	seen := map[string]struct{}{}
	for i, binding := range bindings {
		target := strings.TrimSpace(binding.TargetPath)
		if target == "" {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "binding.target_path is required", false, map[string]any{"binding_index": i})
		}
		if !isValidJSONPointer(target) {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "binding.target_path must be valid json pointer", false, map[string]any{"binding_index": i, "target_path": target})
		}
		inputKey := strings.TrimSpace(binding.InputKey)
		if inputKey == "" {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "binding.input_key is required", false, map[string]any{"binding_index": i})
		}
		compound := target + "|" + inputKey
		if _, ok := seen[compound]; ok {
			return NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "duplicate binding target_path+input_key", false, map[string]any{
				"binding_index": i,
				"target_path":   target,
				"input_key":     inputKey,
			})
		}
		seen[compound] = struct{}{}
	}
	return nil
}

func isValidJSONPointer(ptr string) bool {
	if ptr == "" {
		return false
	}
	return strings.HasPrefix(ptr, "/")
}

func asCatalogErr(err error) *Error {
	if err == nil {
		return nil
	}
	cErr, _ := err.(*Error)
	return cErr
}

func cloneDetails(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
