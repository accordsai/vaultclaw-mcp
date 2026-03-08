package catalog

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func (s *Store) RenderTemplate(cookbookID, templateID, version string, inputs map[string]any, outputKind string) (RenderResult, error) {
	var empty RenderResult
	if inputs == nil {
		inputs = map[string]any{}
	}
	if strings.TrimSpace(templateID) == "" {
		return empty, NewError("MCP_VALIDATION_ERROR", "validation", "template_id is required", false, map[string]any{})
	}
	bundle, entry, err := s.GetEntry(cookbookID, version, templateID)
	if err != nil {
		return empty, err
	}
	entryType := strings.TrimSpace(entry.EntryType)
	if entryType != EntryTypeTemplateVerb && entryType != EntryTypeTemplatePlan {
		return empty, NewError("MCP_TEMPLATE_NOT_FOUND", "validation", "entry is not a template", false, map[string]any{
			"template_id": templateID,
			"entry_type":  entryType,
		})
	}

	kind := NormalizeOutputKind(outputKind)
	if kind == OutputKindAuto {
		if entryType == EntryTypeTemplateVerb {
			kind = OutputKindVerbRequest
		} else {
			kind = OutputKindPlan
		}
	}
	if entryType == EntryTypeTemplateVerb && kind != OutputKindVerbRequest {
		return empty, NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "template.verb.v1 requires output_kind VERB_REQUEST or AUTO", false, map[string]any{})
	}
	if entryType == EntryTypeTemplatePlan && kind != OutputKindPlan {
		return empty, NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "template.plan.v1 requires output_kind PLAN or AUTO", false, map[string]any{})
	}

	var base map[string]any
	if entryType == EntryTypeTemplateVerb {
		base, err = deepCopyMap(entry.BaseRequest)
	} else {
		base, err = deepCopyMap(entry.BasePlan)
	}
	if err != nil {
		return empty, NewError("MCP_INTERNAL", "internal", "failed to clone template payload", false, map[string]any{"cause": err.Error()})
	}

	missing := make([]MissingInput, 0)
	usedDefaults := make([]UsedDefault, 0)
	var root any = base
	for _, binding := range entry.Bindings {
		value, ok := inputs[binding.InputKey]
		if !ok {
			if binding.HasDefault() {
				dv, dErr := binding.DefaultValue()
				if dErr != nil {
					return empty, NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "binding default is invalid json", false, map[string]any{
						"input_key":   binding.InputKey,
						"target_path": binding.TargetPath,
						"cause":       dErr.Error(),
					})
				}
				value = dv
				usedDefaults = append(usedDefaults, UsedDefault{InputKey: binding.InputKey, TargetPath: binding.TargetPath})
			} else if binding.IsRequired() {
				missing = append(missing, MissingInput{InputKey: binding.InputKey, TargetPath: binding.TargetPath})
				continue
			} else {
				continue
			}
		}
		root, err = setJSONPointer(root, binding.TargetPath, value)
		if err != nil {
			return empty, NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "failed setting rendered value by json pointer", false, map[string]any{
				"input_key":   binding.InputKey,
				"target_path": binding.TargetPath,
				"cause":       err.Error(),
			})
		}
	}
	if len(missing) > 0 {
		return empty, NewError("MCP_TEMPLATE_RENDER_UNRESOLVED", "validation", "required template inputs are missing", false, map[string]any{
			"cookbook_id":    bundle.CookbookID,
			"version":        bundle.Version,
			"template_id":    templateID,
			"missing_inputs": missing,
		})
	}

	rendered, ok := root.(map[string]any)
	if !ok {
		return empty, NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "rendered payload must be object", false, map[string]any{})
	}
	if err := validateRenderedShape(kind, rendered); err != nil {
		return empty, err
	}

	return RenderResult{
		Rendered:      rendered,
		MissingInputs: missing,
		UsedDefaults:  usedDefaults,
		SourceRef: SourceRef{
			CookbookID: bundle.CookbookID,
			Version:    bundle.Version,
			TemplateID: templateID,
			EntryType:  entryType,
			OutputKind: kind,
		},
	}, nil
}

func validateRenderedShape(kind string, rendered map[string]any) error {
	switch kind {
	case OutputKindVerbRequest:
		connectorID := strings.TrimSpace(stringValue(rendered["connector_id"]))
		verb := strings.TrimSpace(stringValue(rendered["verb"]))
		if connectorID == "" || verb == "" {
			return NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "rendered VERB_REQUEST must include connector_id and verb", false, map[string]any{})
		}
		if _, ok := rendered["request"].(map[string]any); !ok {
			return NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "rendered VERB_REQUEST must include request object", false, map[string]any{})
		}
	case OutputKindPlan:
		steps, ok := rendered["steps"].([]any)
		if !ok || len(steps) == 0 {
			return NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "rendered PLAN must include non-empty steps array", false, map[string]any{})
		}
	default:
		return NewError("MCP_TEMPLATE_INPUT_INVALID", "validation", "unsupported output_kind", false, map[string]any{"output_kind": kind})
	}
	return nil
}

func setJSONPointer(root any, pointer string, value any) (any, error) {
	tokens, err := parseJSONPointer(pointer)
	if err != nil {
		return nil, err
	}
	out, err := setJSONPointerTokens(root, tokens, value)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func setJSONPointerTokens(node any, tokens []string, value any) (any, error) {
	if len(tokens) == 0 {
		return deepCopyAny(value)
	}
	token := tokens[0]
	if idx, isIndex := parseIndexToken(token); isIndex {
		var arr []any
		switch cur := node.(type) {
		case nil:
			arr = []any{}
		case []any:
			arr = append([]any(nil), cur...)
		default:
			return nil, fmt.Errorf("path segment %q expects array", token)
		}
		if idx < 0 {
			return nil, fmt.Errorf("path segment %q has negative array index", token)
		}
		if idx >= len(arr) {
			expanded := make([]any, idx+1)
			copy(expanded, arr)
			arr = expanded
		}
		next, err := setJSONPointerTokens(arr[idx], tokens[1:], value)
		if err != nil {
			return nil, err
		}
		arr[idx] = next
		return arr, nil
	}

	var obj map[string]any
	switch cur := node.(type) {
	case nil:
		obj = map[string]any{}
	case map[string]any:
		obj = make(map[string]any, len(cur))
		for k, v := range cur {
			obj[k] = v
		}
	default:
		return nil, fmt.Errorf("path segment %q expects object", token)
	}

	next, err := setJSONPointerTokens(obj[token], tokens[1:], value)
	if err != nil {
		return nil, err
	}
	obj[token] = next
	return obj, nil
}

func parseJSONPointer(pointer string) ([]string, error) {
	ptr := strings.TrimSpace(pointer)
	if ptr == "" || ptr[0] != '/' {
		return nil, fmt.Errorf("json pointer must start with '/'")
	}
	parts := strings.Split(ptr[1:], "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		decoded, err := decodePointerSegment(part)
		if err != nil {
			return nil, err
		}
		out = append(out, decoded)
	}
	return out, nil
}

func decodePointerSegment(segment string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(segment); i++ {
		ch := segment[i]
		if ch != '~' {
			b.WriteByte(ch)
			continue
		}
		if i+1 >= len(segment) {
			return "", fmt.Errorf("invalid json pointer escape in segment")
		}
		next := segment[i+1]
		switch next {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid json pointer escape in segment")
		}
		i++
	}
	return b.String(), nil
}

func parseIndexToken(raw string) (int, bool) {
	if raw == "" {
		return 0, false
	}
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return v, true
}

func deepCopyMap(in map[string]any) (map[string]any, error) {
	if in == nil {
		return nil, nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func deepCopyAny(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func stringValue(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
