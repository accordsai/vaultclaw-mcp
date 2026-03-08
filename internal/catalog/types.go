package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	BundleTypeV1 = "accords.cookbook.bundle.v1"
	IndexTypeV1  = "accords.cookbook.index.v1"
)

const (
	EntryTypeRecipeVerb   = "recipe.verb.v1"
	EntryTypeRecipePlan   = "recipe.plan.v1"
	EntryTypeTemplateVerb = "template.verb.v1"
	EntryTypeTemplatePlan = "template.plan.v1"
)

const (
	ConflictPolicyFail        = "FAIL"
	ConflictPolicyOverwrite   = "OVERWRITE"
	ConflictPolicySkipIfExist = "SKIP_IF_EXISTS"
)

const (
	OutputKindAuto        = "AUTO"
	OutputKindVerbRequest = "VERB_REQUEST"
	OutputKindPlan        = "PLAN"
)

const (
	AuthModeNone      = "NONE"
	AuthModeBearerEnv = "BEARER_ENV"
)

type Bundle struct {
	Type        string   `json:"type"`
	CookbookID  string   `json:"cookbook_id"`
	Version     string   `json:"version"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Entries     []Entry  `json:"entries"`
}

type Entry struct {
	EntryID           string         `json:"entry_id"`
	EntryType         string         `json:"entry_type"`
	Title             string         `json:"title,omitempty"`
	Description       string         `json:"description,omitempty"`
	Tags              []string       `json:"tags,omitempty"`
	ConnectorID       string         `json:"connector_id,omitempty"`
	Verb              string         `json:"verb,omitempty"`
	PolicyVersion     string         `json:"policy_version,omitempty"`
	Request           map[string]any `json:"request,omitempty"`
	Plan              map[string]any `json:"plan,omitempty"`
	PlanInputDefaults map[string]any `json:"plan_input_defaults,omitempty"`
	BaseRequest       map[string]any `json:"base_request,omitempty"`
	BasePlan          map[string]any `json:"base_plan,omitempty"`
	InputSchema       map[string]any `json:"input_schema,omitempty"`
	Bindings          []Binding      `json:"bindings,omitempty"`
}

type Binding struct {
	TargetPath string           `json:"target_path"`
	InputKey   string           `json:"input_key"`
	Required   *bool            `json:"required,omitempty"`
	DefaultRaw *json.RawMessage `json:"default,omitempty"`
}

func (b Binding) IsRequired() bool {
	if b.Required == nil {
		return true
	}
	return *b.Required
}

func (b Binding) HasDefault() bool {
	return b.DefaultRaw != nil
}

func (b Binding) DefaultValue() (any, error) {
	if b.DefaultRaw == nil {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(*b.DefaultRaw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type RemoteIndex struct {
	Type              string            `json:"type"`
	SourceID          string            `json:"source_id"`
	GeneratedAtUnixMS int64             `json:"generated_at_unix_ms"`
	Items             []RemoteIndexItem `json:"items"`
}

type RemoteIndexItem struct {
	CookbookID  string   `json:"cookbook_id"`
	Version     string   `json:"version"`
	Title       string   `json:"title"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	DownloadURL string   `json:"download_url"`
	SHA256      string   `json:"sha256,omitempty"`
}

type SourceConfig struct {
	SourceID   string `json:"source_id"`
	IndexURL   string `json:"index_url"`
	Enabled    bool   `json:"enabled"`
	AuthMode   string `json:"auth_mode"`
	AuthEnvVar string `json:"auth_env_var,omitempty"`
}

type SourcesFile struct {
	Sources []SourceConfig `json:"sources"`
}

type LocalIndex struct {
	GeneratedAtUnixMS int64               `json:"generated_at_unix_ms"`
	Cookbooks         []CookbookIndexItem `json:"cookbooks"`
}

type CookbookIndexItem struct {
	CookbookID  string           `json:"cookbook_id"`
	Version     string           `json:"version"`
	Title       string           `json:"title"`
	Description string           `json:"description,omitempty"`
	Tags        []string         `json:"tags,omitempty"`
	EntryCount  int              `json:"entry_count"`
	ContentHash string           `json:"content_hash"`
	Entries     []EntryIndexItem `json:"entries,omitempty"`
}

type EntryIndexItem struct {
	EntryID     string   `json:"entry_id"`
	EntryType   string   `json:"entry_type"`
	Title       string   `json:"title,omitempty"`
	ConnectorID string   `json:"connector_id,omitempty"`
	Verb        string   `json:"verb,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type SearchFilter struct {
	Query       string
	ConnectorID string
	Verb        string
	Tags        []string
	EntryType   string
}

type SearchResult struct {
	CookbookID  string   `json:"cookbook_id"`
	Version     string   `json:"version"`
	EntryID     string   `json:"entry_id"`
	EntryType   string   `json:"entry_type"`
	Title       string   `json:"title,omitempty"`
	ConnectorID string   `json:"connector_id,omitempty"`
	Verb        string   `json:"verb,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type SourceRef struct {
	CookbookID string `json:"cookbook_id"`
	Version    string `json:"version"`
	TemplateID string `json:"template_id"`
	EntryType  string `json:"entry_type"`
	OutputKind string `json:"output_kind"`
}

type RenderResult struct {
	Rendered      map[string]any `json:"rendered"`
	MissingInputs []MissingInput `json:"missing_inputs"`
	UsedDefaults  []UsedDefault  `json:"used_defaults"`
	SourceRef     SourceRef      `json:"source_ref"`
}

type MissingInput struct {
	InputKey   string `json:"input_key"`
	TargetPath string `json:"target_path"`
}

type UsedDefault struct {
	InputKey   string `json:"input_key"`
	TargetPath string `json:"target_path"`
}

type InstallResult struct {
	SourceID    string            `json:"source_id"`
	Selected    RemoteIndexItem   `json:"selected"`
	ContentHash string            `json:"content_hash"`
	Cookbook    CookbookIndexItem `json:"cookbook"`
}

type Error struct {
	Code      string
	Category  string
	Message   string
	Retryable bool
	Details   map[string]any
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Code) != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Message
}

func NewError(code, category, message string, retryable bool, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{
		Code:      strings.TrimSpace(code),
		Category:  strings.TrimSpace(category),
		Message:   strings.TrimSpace(message),
		Retryable: retryable,
		Details:   details,
	}
}

func DecodeBundle(raw any) (Bundle, error) {
	var b Bundle
	if raw == nil {
		return b, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle is required", false, map[string]any{})
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return b, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle must be valid json object", false, map[string]any{"cause": err.Error()})
	}
	if err := json.Unmarshal(blob, &b); err != nil {
		return b, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "bundle schema decode failed", false, map[string]any{"cause": err.Error()})
	}
	return b, nil
}

func NormalizeConflictPolicy(raw string) string {
	v := strings.ToUpper(strings.TrimSpace(raw))
	switch v {
	case ConflictPolicyOverwrite:
		return ConflictPolicyOverwrite
	case ConflictPolicySkipIfExist:
		return ConflictPolicySkipIfExist
	default:
		return ConflictPolicyFail
	}
}

func NormalizeOutputKind(raw string) string {
	v := strings.ToUpper(strings.TrimSpace(raw))
	switch v {
	case OutputKindVerbRequest:
		return OutputKindVerbRequest
	case OutputKindPlan:
		return OutputKindPlan
	default:
		return OutputKindAuto
	}
}

func NormalizeAuthMode(raw string) string {
	v := strings.ToUpper(strings.TrimSpace(raw))
	switch v {
	case AuthModeBearerEnv:
		return AuthModeBearerEnv
	default:
		return AuthModeNone
	}
}

func CanonicalizeTags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		tag := strings.TrimSpace(item)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func CanonicalizeBundle(in Bundle) Bundle {
	out := in
	out.Type = strings.TrimSpace(out.Type)
	out.CookbookID = strings.TrimSpace(out.CookbookID)
	out.Version = strings.TrimSpace(out.Version)
	out.Title = strings.TrimSpace(out.Title)
	out.Description = strings.TrimSpace(out.Description)
	out.Tags = CanonicalizeTags(out.Tags)
	out.Entries = append([]Entry(nil), out.Entries...)
	sort.Slice(out.Entries, func(i, j int) bool {
		return strings.TrimSpace(out.Entries[i].EntryID) < strings.TrimSpace(out.Entries[j].EntryID)
	})
	for i := range out.Entries {
		out.Entries[i].EntryID = strings.TrimSpace(out.Entries[i].EntryID)
		out.Entries[i].EntryType = strings.TrimSpace(out.Entries[i].EntryType)
		out.Entries[i].Title = strings.TrimSpace(out.Entries[i].Title)
		out.Entries[i].Description = strings.TrimSpace(out.Entries[i].Description)
		out.Entries[i].ConnectorID = strings.TrimSpace(out.Entries[i].ConnectorID)
		out.Entries[i].Verb = strings.TrimSpace(out.Entries[i].Verb)
		out.Entries[i].PolicyVersion = strings.TrimSpace(out.Entries[i].PolicyVersion)
		out.Entries[i].Tags = CanonicalizeTags(out.Entries[i].Tags)
		sort.Slice(out.Entries[i].Bindings, func(a, b int) bool {
			if out.Entries[i].Bindings[a].TargetPath != out.Entries[i].Bindings[b].TargetPath {
				return out.Entries[i].Bindings[a].TargetPath < out.Entries[i].Bindings[b].TargetPath
			}
			return out.Entries[i].Bindings[a].InputKey < out.Entries[i].Bindings[b].InputKey
		})
	}
	return out
}

func ContentHashBundle(b Bundle) (string, error) {
	canon := CanonicalizeBundle(b)
	raw, err := json.Marshal(canon)
	if err != nil {
		return "", err
	}
	return SHA256Hex(raw), nil
}

func ValidateVersion(raw string) bool {
	return strings.TrimSpace(raw) != ""
}

func SortVersions(versions []string) []string {
	cp := append([]string(nil), versions...)
	sort.Slice(cp, func(i, j int) bool {
		return compareVersion(cp[i], cp[j]) < 0
	})
	return cp
}

func LatestVersion(versions []string) string {
	if len(versions) == 0 {
		return ""
	}
	sorted := SortVersions(versions)
	return sorted[len(sorted)-1]
}

func compareVersion(a, b string) int {
	ta := tokenizeVersion(a)
	tb := tokenizeVersion(b)
	limit := len(ta)
	if len(tb) > limit {
		limit = len(tb)
	}
	for i := 0; i < limit; i++ {
		if i >= len(ta) {
			return -1
		}
		if i >= len(tb) {
			return 1
		}
		x := ta[i]
		y := tb[i]
		if x.isNum && y.isNum {
			if x.num < y.num {
				return -1
			}
			if x.num > y.num {
				return 1
			}
			continue
		}
		if x.isNum && !y.isNum {
			return 1
		}
		if !x.isNum && y.isNum {
			return -1
		}
		if x.raw < y.raw {
			return -1
		}
		if x.raw > y.raw {
			return 1
		}
	}
	return 0
}

type versionToken struct {
	raw   string
	num   int64
	isNum bool
}

func tokenizeVersion(v string) []versionToken {
	parts := strings.FieldsFunc(strings.TrimSpace(v), func(r rune) bool {
		return r == '.' || r == '-' || r == '_' || r == '+'
	})
	if len(parts) == 0 {
		return []versionToken{{raw: ""}}
	}
	out := make([]versionToken, 0, len(parts))
	for _, part := range parts {
		if n, err := strconv.ParseInt(part, 10, 64); err == nil {
			out = append(out, versionToken{raw: part, num: n, isNum: true})
		} else {
			out = append(out, versionToken{raw: part})
		}
	}
	return out
}

func bundlePath(root, cookbookID, version string) string {
	return filepath.Join(root, "cookbooks", cookbookID, version+".json")
}

func DecodeSource(raw any) (SourceConfig, error) {
	out := SourceConfig{
		Enabled:  true,
		AuthMode: AuthModeNone,
	}
	blob, err := json.Marshal(raw)
	if err != nil {
		return out, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "source must be object", false, map[string]any{"cause": err.Error()})
	}
	if err := json.Unmarshal(blob, &out); err != nil {
		return out, NewError("MCP_CATALOG_SCHEMA_INVALID", "validation", "source decode failed", false, map[string]any{"cause": err.Error()})
	}
	return out, nil
}

func validateURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return strings.TrimSpace(u.Host) != ""
}
