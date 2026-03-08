package vault

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type UnboundedProfile struct {
	Type        string                 `json:"type,omitempty"`
	ProfileID   string                 `json:"profile_id"`
	ConnectorID string                 `json:"connector_id"`
	Verb        string                 `json:"verb"`
	Slots       []UnboundedProfileSlot `json:"slots,omitempty"`
}

type UnboundedProfileSlot struct {
	Slot                string   `json:"slot"`
	Required            bool     `json:"required,omitempty"`
	AllowedIntents      []string `json:"allowed_intents,omitempty"`
	ExpectedSecretTypes []string `json:"expected_secret_types,omitempty"`
	AllowedModes        []string `json:"allowed_modes,omitempty"`
	AllowedTargets      []string `json:"allowed_targets,omitempty"`
}

func DecodeUnboundedProfile(raw any) (UnboundedProfile, error) {
	buf, err := json.Marshal(raw)
	if err != nil {
		return UnboundedProfile{}, err
	}
	var p UnboundedProfile
	if err := json.Unmarshal(buf, &p); err != nil {
		return UnboundedProfile{}, err
	}
	p.ProfileID = strings.TrimSpace(p.ProfileID)
	p.ConnectorID = strings.TrimSpace(p.ConnectorID)
	p.Verb = strings.TrimSpace(p.Verb)
	for i := range p.Slots {
		p.Slots[i].Slot = strings.TrimSpace(p.Slots[i].Slot)
		p.Slots[i].AllowedIntents = normalizeStrings(p.Slots[i].AllowedIntents)
		p.Slots[i].ExpectedSecretTypes = normalizeStrings(p.Slots[i].ExpectedSecretTypes)
		p.Slots[i].AllowedModes = normalizeStrings(p.Slots[i].AllowedModes)
		p.Slots[i].AllowedTargets = normalizeStrings(p.Slots[i].AllowedTargets)
	}
	sort.Slice(p.Slots, func(i, j int) bool { return p.Slots[i].Slot < p.Slots[j].Slot })
	return p, nil
}

func ExtractItems(body map[string]any) []map[string]any {
	if body == nil {
		return nil
	}
	itemsRaw, _ := body["items"].([]any)
	if len(itemsRaw) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(itemsRaw))
	for _, it := range itemsRaw {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, m)
	}
	return out
}

func String(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, _ := m[key].(string)
	return strings.TrimSpace(v)
}

func Bool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	v, _ := m[key].(bool)
	return v
}

func NumberToInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}

func normalizeStrings(in []string) []string {
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

func RequireStringField(m map[string]any, key string) (string, error) {
	v := String(m, key)
	if v == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return v, nil
}
