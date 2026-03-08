package orchestration

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"accords-mcp/internal/vault"
)

func PreflightPlan(ctx context.Context, vc *vault.Client, plan map[string]any, planInput any, opts OrchestrationOptions, strict bool) (map[string]any, []PlanStepProfileResolution, error) {
	if !opts.UnboundedProfiles {
		copied, _ := deepCopyMap(plan)
		return copied, nil, nil
	}
	copiedPlan, err := deepCopyMap(plan)
	if err != nil {
		return nil, nil, err
	}
	stepsRaw, _ := copiedPlan["steps"].([]any)
	if len(stepsRaw) == 0 {
		return copiedPlan, nil, nil
	}
	resolutions := make([]PlanStepProfileResolution, 0)
	for i, rawStep := range stepsRaw {
		step, ok := rawStep.(map[string]any)
		if !ok {
			continue
		}
		stepID := strings.TrimSpace(strVal(step["step_id"]))
		connectorID := strings.TrimSpace(strVal(step["connector_id"]))
		verb := strings.TrimSpace(strVal(step["verb"]))
		if connectorID != "generic.http" || verb != "generic.http.request.v1" {
			continue
		}
		resolvedReq, unresolvedRefs, err := resolveStepRequest(step, planInput)
		if err != nil {
			resolutions = append(resolutions, PlanStepProfileResolution{StepID: stepID, Status: "FAILED", Reason: err.Error()})
			if strict {
				return nil, resolutions, err
			}
			continue
		}
		if len(unresolvedRefs) > 0 {
			res := PlanStepProfileResolution{StepID: stepID, Status: "UNRESOLVED", Reason: "runtime bindings required for profile inference", UnresolvedRefs: unresolvedRefs}
			resolutions = append(resolutions, res)
			if strict {
				requiredInputs := requiredPlanInputs(unresolvedRefs)
				return nil, resolutions, &OrchestrationError{
					Code:    "MCP_PLAN_PROFILE_PRECHECK_UNRESOLVED",
					Message: "unable to infer unbounded profile requirements before runtime",
					Details: map[string]any{
						"step_id":                   stepID,
						"unresolved_refs":           unresolvedRefs,
						"required_plan_input_paths": requiredInputs,
					},
				}
			}
			continue
		}
		if strings.TrimSpace(strVal(resolvedReq["profile_id"])) != "" {
			resolutions = append(resolutions, PlanStepProfileResolution{StepID: stepID, Status: "RESOLVED", ProfileID: strings.TrimSpace(strVal(resolvedReq["profile_id"])), Reason: "request already includes profile_id"})
			step["request_base"] = resolvedReq
			step["request_bindings"] = []any{}
			stepsRaw[i] = step
			continue
		}
		reqs, err := DeriveRequirementsFromRequest(resolvedReq)
		if err != nil {
			resolutions = append(resolutions, PlanStepProfileResolution{StepID: stepID, Status: "FAILED", Reason: err.Error()})
			if strict {
				return nil, resolutions, err
			}
			continue
		}
		if len(reqs) == 0 {
			resolutions = append(resolutions, PlanStepProfileResolution{StepID: stepID, Status: "RESOLVED", Reason: "no secret_attachments requirements found"})
			step["request_base"] = resolvedReq
			step["request_bindings"] = []any{}
			stepsRaw[i] = step
			continue
		}
		resolved, err := ResolveUnboundedProfile(ctx, vc, reqs, opts.AutoCreateProfiles)
		if err != nil {
			if strict {
				return nil, resolutions, err
			}
			resolutions = append(resolutions, PlanStepProfileResolution{StepID: stepID, Status: "FAILED", Reason: err.Error(), Requirements: reqs})
			continue
		}
		resolvedReq["profile_id"] = resolved.ProfileID
		status := "RESOLVED"
		if resolved.Created {
			status = "CREATED"
		}
		resolutions = append(resolutions, PlanStepProfileResolution{StepID: stepID, Status: status, ProfileID: resolved.ProfileID, Requirements: reqs})
		step["request_base"] = resolvedReq
		step["request_bindings"] = []any{}
		stepsRaw[i] = step
	}
	copiedPlan["steps"] = stepsRaw
	return copiedPlan, resolutions, nil
}

func resolveStepRequest(step map[string]any, planInput any) (map[string]any, []string, error) {
	base := map[string]any{}
	if requestBase, ok := step["request_base"].(map[string]any); ok {
		copyBase, err := deepCopyMap(requestBase)
		if err != nil {
			return nil, nil, err
		}
		base = copyBase
	}
	bindingsRaw, _ := step["request_bindings"].([]any)
	if len(bindingsRaw) == 0 {
		return base, nil, nil
	}
	unresolved := make([]string, 0)
	for i, rawBinding := range bindingsRaw {
		binding, ok := rawBinding.(map[string]any)
		if !ok {
			return nil, unresolved, fmt.Errorf("request_bindings[%d] must be object", i)
		}
		path := strings.TrimSpace(strVal(binding["path"]))
		if path == "" {
			return nil, unresolved, fmt.Errorf("request_bindings[%d] path is required", i)
		}
		ref, _ := binding["ref"].(map[string]any)
		if ref == nil {
			return nil, unresolved, fmt.Errorf("request_bindings[%d] ref is required", i)
		}
		value, ok, unresolvedReason := resolveRefValue(ref, planInput)
		if !ok {
			unresolved = append(unresolved, path+" -> "+unresolvedReason)
			continue
		}
		if err := setJSONPointer(base, path, value); err != nil {
			return nil, unresolved, err
		}
	}
	return base, unresolved, nil
}

func resolveRefValue(ref map[string]any, planInput any) (any, bool, string) {
	source := strings.TrimSpace(strVal(ref["source"]))
	if source == "" {
		if v, ok := ref["value"]; ok {
			return v, true, ""
		}
		return nil, false, "ref:missing_source"
	}
	switch source {
	case "literal", "value", "const":
		v, ok := ref["value"]
		if !ok {
			return nil, false, "ref:literal_missing_value"
		}
		return v, true, ""
	case "plan_input":
		path := strings.TrimSpace(strVal(ref["path"]))
		if path == "" {
			return nil, false, "ref:plan_input_missing_path"
		}
		v, ok := getJSONPointer(planInput, path)
		if !ok {
			return nil, false, "plan_input:" + path
		}
		return v, true, ""
	default:
		if strings.HasPrefix(source, "step") || source == "runtime" {
			path := strings.TrimSpace(strVal(ref["path"]))
			return nil, false, source + ":" + path
		}
		return nil, false, "ref:unsupported_source:" + source
	}
}

func deepCopyMap(in map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func setJSONPointer(root map[string]any, pointer string, value any) error {
	if !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("json pointer must start with '/': %s", pointer)
	}
	parts := splitJSONPointer(pointer)
	if len(parts) == 0 {
		return fmt.Errorf("empty pointer")
	}
	curr := map[string]any(root)
	for i := 0; i < len(parts)-1; i++ {
		part := parts[i]
		next, ok := curr[part]
		if !ok {
			nm := map[string]any{}
			curr[part] = nm
			curr = nm
			continue
		}
		nm, ok := next.(map[string]any)
		if !ok {
			return fmt.Errorf("pointer conflict at %s", part)
		}
		curr = nm
	}
	curr[parts[len(parts)-1]] = value
	return nil
}

func getJSONPointer(root any, pointer string) (any, bool) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, false
	}
	parts := splitJSONPointer(pointer)
	curr := root
	for _, part := range parts {
		switch n := curr.(type) {
		case map[string]any:
			next, ok := n[part]
			if !ok {
				return nil, false
			}
			curr = next
		default:
			return nil, false
		}
	}
	return curr, true
}

func splitJSONPointer(pointer string) []string {
	parts := strings.Split(pointer, "/")
	out := make([]string, 0, len(parts)-1)
	for _, p := range parts[1:] {
		p = strings.ReplaceAll(p, "~1", "/")
		p = strings.ReplaceAll(p, "~0", "~")
		out = append(out, p)
	}
	return out
}

func requiredPlanInputs(unresolvedRefs []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, ref := range unresolvedRefs {
		idx := strings.Index(ref, "plan_input:")
		if idx < 0 {
			continue
		}
		path := strings.TrimSpace(ref[idx+len("plan_input:"):])
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}
