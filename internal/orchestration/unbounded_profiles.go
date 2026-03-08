package orchestration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"accords-mcp/internal/vault"
)

type UnboundedRequirement struct {
	Slot                string   `json:"slot"`
	Intent              string   `json:"intent,omitempty"`
	ExpectedSecretTypes []string `json:"expected_secret_types,omitempty"`
	Mode                string   `json:"mode"`
	Target              string   `json:"target"`
	Required            bool     `json:"required"`
}

type ProfileMatchResult struct {
	ProfileID             string   `json:"profile_id"`
	Score                 int      `json:"score"`
	SatisfiedRequirements []string `json:"satisfied_requirements"`
	MissingRequirements   []string `json:"missing_requirements"`
}

type PlanStepProfileResolution struct {
	StepID         string                 `json:"step_id"`
	Status         string                 `json:"status"`
	ProfileID      string                 `json:"profile_id,omitempty"`
	Reason         string                 `json:"reason,omitempty"`
	Requirements   []UnboundedRequirement `json:"requirements,omitempty"`
	UnresolvedRefs []string               `json:"unresolved_refs,omitempty"`
}

type OrchestrationOptions struct {
	UnboundedProfiles  bool `json:"unbounded_profiles"`
	AutoCreateProfiles bool `json:"auto_create_profiles"`
}

type ResolveResult struct {
	ProfileID string                 `json:"profile_id"`
	Created   bool                   `json:"created"`
	Match     ProfileMatchResult     `json:"match"`
	Profile   vault.UnboundedProfile `json:"profile"`
}

type OrchestrationError struct {
	Code    string
	Message string
	Details map[string]any
	Retry   bool
}

func (e *OrchestrationError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Message) != "" {
		return e.Message
	}
	return e.Code
}

func DeriveRequirementsFromRequest(request map[string]any) ([]UnboundedRequirement, error) {
	if request == nil {
		return nil, nil
	}
	raw, _ := request["secret_attachments"].([]any)
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]UnboundedRequirement, 0, len(raw))
	for i, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("secret_attachments[%d] must be object", i)
		}
		req := UnboundedRequirement{
			Slot:     strings.TrimSpace(strVal(m["slot"])),
			Intent:   strings.TrimSpace(strVal(m["intent"])),
			Mode:     strings.TrimSpace(strVal(m["mode"])),
			Target:   strings.TrimSpace(strVal(m["target"])),
			Required: true,
		}
		if v, ok := m["required"].(bool); ok {
			req.Required = v
		}
		switch rawTypes := m["expected_secret_types"].(type) {
		case []any:
			for _, t := range rawTypes {
				if s, ok := t.(string); ok && strings.TrimSpace(s) != "" {
					req.ExpectedSecretTypes = append(req.ExpectedSecretTypes, strings.TrimSpace(s))
				}
			}
		case []string:
			req.ExpectedSecretTypes = append(req.ExpectedSecretTypes, rawTypes...)
		}
		if req.Slot == "" || req.Mode == "" || req.Target == "" {
			return nil, fmt.Errorf("secret_attachments[%d] requires slot/mode/target", i)
		}
		req.ExpectedSecretTypes = dedupeAndSort(req.ExpectedSecretTypes)
		out = append(out, req)
	}
	return NormalizeRequirements(out), nil
}

func NormalizeRequirements(reqs []UnboundedRequirement) []UnboundedRequirement {
	out := make([]UnboundedRequirement, 0, len(reqs))
	for _, r := range reqs {
		r.Slot = strings.TrimSpace(r.Slot)
		r.Intent = strings.TrimSpace(r.Intent)
		r.Mode = strings.TrimSpace(r.Mode)
		r.Target = strings.TrimSpace(r.Target)
		r.ExpectedSecretTypes = dedupeAndSort(r.ExpectedSecretTypes)
		if r.Slot == "" || r.Mode == "" || r.Target == "" {
			continue
		}
		if !r.Required {
			r.Required = true
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Slot != out[j].Slot {
			return out[i].Slot < out[j].Slot
		}
		if out[i].Mode != out[j].Mode {
			return out[i].Mode < out[j].Mode
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return out[i].Intent < out[j].Intent
	})
	return out
}

func ResolveUnboundedProfile(ctx context.Context, vc *vault.Client, requirements []UnboundedRequirement, autoCreate bool) (ResolveResult, error) {
	requirements = NormalizeRequirements(requirements)
	if len(requirements) == 0 {
		return ResolveResult{}, &OrchestrationError{Code: "MCP_UNBOUNDED_PROFILE_REQUIRED", Message: "no unbounded requirements found", Details: map[string]any{}}
	}
	profiles, err := listDetailedProfiles(ctx, vc)
	if err != nil {
		return ResolveResult{}, err
	}
	matches := make([]ProfileMatchResult, 0, len(profiles))
	profileByID := map[string]vault.UnboundedProfile{}
	for _, p := range profiles {
		profileByID[p.ProfileID] = p
		m := scoreProfileMatch(p, requirements)
		if len(m.MissingRequirements) == 0 {
			matches = append(matches, m)
		}
	}
	if len(matches) > 0 {
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].Score != matches[j].Score {
				return matches[i].Score > matches[j].Score
			}
			return matches[i].ProfileID < matches[j].ProfileID
		})
		best := matches[0]
		return ResolveResult{ProfileID: best.ProfileID, Created: false, Match: best, Profile: profileByID[best.ProfileID]}, nil
	}
	if !autoCreate {
		return ResolveResult{}, &OrchestrationError{
			Code:    "MCP_UNBOUNDED_PROFILE_REQUIRED",
			Message: "no compatible unbounded profile found",
			Details: map[string]any{"requirements": requirements},
		}
	}
	profile := BuildMinimalProfile(requirements)
	payload := map[string]any{"profile": profile}
	idempotencyKey := "mcp-unbounded-profile-" + requirementsHash(requirements)
	_, err = vc.PostWithIdempotencyKey(ctx, "/v0/connectors/unbounded/profiles/upsert", payload, idempotencyKey)
	if err != nil {
		var apiErr *vault.APIError
		if errors.As(err, &apiErr) && strings.EqualFold(strings.TrimSpace(apiErr.Code), "INSUFFICIENT_SCOPE") {
			return ResolveResult{}, &OrchestrationError{
				Code:    "MCP_UNBOUNDED_PROFILE_REQUIRED",
				Message: "profile auto-create requires connectors.unbounded.profiles.write scope",
				Details: map[string]any{
					"requirements":    requirements,
					"idempotency_key": idempotencyKey,
				},
			}
		}
		return ResolveResult{}, err
	}
	createdProfile, err := fetchProfileByID(ctx, vc, profile.ProfileID)
	if err != nil {
		// best effort fallback to locally built profile.
		createdProfile = profile
	}
	match := scoreProfileMatch(createdProfile, requirements)
	return ResolveResult{ProfileID: createdProfile.ProfileID, Created: true, Match: match, Profile: createdProfile}, nil
}

func BuildMinimalProfile(requirements []UnboundedRequirement) vault.UnboundedProfile {
	requirements = NormalizeRequirements(requirements)
	short := requirementsHash(requirements)
	profileID := "mcp.auto." + short

	slotsByName := map[string]*vault.UnboundedProfileSlot{}
	for _, r := range requirements {
		s := slotsByName[r.Slot]
		if s == nil {
			s = &vault.UnboundedProfileSlot{Slot: r.Slot, Required: r.Required}
			slotsByName[r.Slot] = s
		}
		if r.Intent != "" {
			s.AllowedIntents = append(s.AllowedIntents, r.Intent)
		}
		s.ExpectedSecretTypes = append(s.ExpectedSecretTypes, r.ExpectedSecretTypes...)
		s.AllowedModes = append(s.AllowedModes, r.Mode)
		s.AllowedTargets = append(s.AllowedTargets, r.Target)
	}
	slots := make([]vault.UnboundedProfileSlot, 0, len(slotsByName))
	for _, s := range slotsByName {
		s.AllowedIntents = dedupeAndSort(s.AllowedIntents)
		s.ExpectedSecretTypes = dedupeAndSort(s.ExpectedSecretTypes)
		s.AllowedModes = dedupeAndSort(s.AllowedModes)
		s.AllowedTargets = dedupeAndSort(s.AllowedTargets)
		slots = append(slots, *s)
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Slot < slots[j].Slot })

	return vault.UnboundedProfile{
		Type:        "connector.unbounded.profile.v1",
		ProfileID:   profileID,
		ConnectorID: "generic.http",
		Verb:        "generic.http.request.v1",
		Slots:       slots,
	}
}

func requirementsHash(requirements []UnboundedRequirement) string {
	type canonReq struct {
		Slot                string   `json:"slot"`
		Intent              string   `json:"intent,omitempty"`
		ExpectedSecretTypes []string `json:"expected_secret_types,omitempty"`
		Mode                string   `json:"mode"`
		Target              string   `json:"target"`
		Required            bool     `json:"required"`
	}
	canon := make([]canonReq, 0, len(requirements))
	for _, r := range NormalizeRequirements(requirements) {
		canon = append(canon, canonReq{
			Slot:                r.Slot,
			Intent:              r.Intent,
			ExpectedSecretTypes: dedupeAndSort(r.ExpectedSecretTypes),
			Mode:                r.Mode,
			Target:              r.Target,
			Required:            r.Required,
		})
	}
	raw, _ := json.Marshal(canon)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])[:12]
}

func scoreProfileMatch(profile vault.UnboundedProfile, reqs []UnboundedRequirement) ProfileMatchResult {
	result := ProfileMatchResult{ProfileID: profile.ProfileID}
	slots := map[string]vault.UnboundedProfileSlot{}
	for _, s := range profile.Slots {
		slots[s.Slot] = s
	}
	for _, req := range reqs {
		key := req.Slot + ":" + req.Mode + ":" + req.Target
		slot, ok := slots[req.Slot]
		if !ok {
			result.MissingRequirements = append(result.MissingRequirements, key+":slot_missing")
			continue
		}
		localScore := 1
		if req.Intent != "" {
			if len(slot.AllowedIntents) > 0 && !contains(slot.AllowedIntents, req.Intent) {
				result.MissingRequirements = append(result.MissingRequirements, key+":intent")
				continue
			}
			localScore++
		}
		if len(req.ExpectedSecretTypes) > 0 && len(slot.ExpectedSecretTypes) > 0 {
			if !overlap(req.ExpectedSecretTypes, slot.ExpectedSecretTypes) {
				result.MissingRequirements = append(result.MissingRequirements, key+":expected_secret_types")
				continue
			}
			localScore++
		}
		if len(slot.AllowedModes) > 0 && !contains(slot.AllowedModes, req.Mode) {
			result.MissingRequirements = append(result.MissingRequirements, key+":mode")
			continue
		}
		localScore++
		if len(slot.AllowedTargets) > 0 && !contains(slot.AllowedTargets, req.Target) {
			result.MissingRequirements = append(result.MissingRequirements, key+":target")
			continue
		}
		localScore++
		result.Score += localScore
		result.SatisfiedRequirements = append(result.SatisfiedRequirements, key)
	}
	sort.Strings(result.SatisfiedRequirements)
	sort.Strings(result.MissingRequirements)
	return result
}

func listDetailedProfiles(ctx context.Context, vc *vault.Client) ([]vault.UnboundedProfile, error) {
	res, err := vc.Get(ctx, "/v0/connectors/unbounded/profiles/list", map[string]string{
		"connector_id": "generic.http",
		"verb":         "generic.http.request.v1",
	})
	if err != nil {
		return nil, err
	}
	rows := vault.ExtractItems(res.Body)
	profiles := make([]vault.UnboundedProfile, 0, len(rows))
	for _, row := range rows {
		profileID := vault.String(row, "profile_id")
		if profileID == "" {
			continue
		}
		p, err := fetchProfileByID(ctx, vc, profileID)
		if err != nil {
			continue
		}
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i].ProfileID < profiles[j].ProfileID })
	return profiles, nil
}

func fetchProfileByID(ctx context.Context, vc *vault.Client, profileID string) (vault.UnboundedProfile, error) {
	res, err := vc.Get(ctx, "/v0/connectors/unbounded/profiles/get", map[string]string{"profile_id": profileID})
	if err != nil {
		return vault.UnboundedProfile{}, err
	}
	return vault.DecodeUnboundedProfile(res.Body["profile"])
}

func contains(items []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item), target) {
			return true
		}
	}
	return false
}

func overlap(a, b []string) bool {
	for _, x := range a {
		if contains(b, x) {
			return true
		}
	}
	return false
}

func dedupeAndSort(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		x := strings.TrimSpace(v)
		if x == "" {
			continue
		}
		if _, ok := seen[x]; ok {
			continue
		}
		seen[x] = struct{}{}
		out = append(out, x)
	}
	sort.Strings(out)
	return out
}

func strVal(v any) string {
	s, _ := v.(string)
	return s
}
