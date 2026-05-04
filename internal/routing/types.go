package routing

import "context"

type ResolveStatus string

const (
	StatusResolvedExecutable ResolveStatus = "RESOLVED_EXECUTABLE"
	StatusResolvedMissing    ResolveStatus = "RESOLVED_MISSING_INPUTS"
	StatusNotVaultEligible   ResolveStatus = "NOT_VAULT_ELIGIBLE"
	StatusAmbiguous          ResolveStatus = "AMBIGUOUS"
)

type Confidence string

const (
	ConfidenceHigh   Confidence = "HIGH"
	ConfidenceMedium Confidence = "MEDIUM"
	ConfidenceLow    Confidence = "LOW"
)

type ExecutionStrategy string

const (
	StrategyTemplate            ExecutionStrategy = "TEMPLATE"
	StrategyRecipe              ExecutionStrategy = "RECIPE"
	StrategyConnectorExecuteJob ExecutionStrategy = "CONNECTOR_EXECUTE_JOB"
	StrategyPlanExecute         ExecutionStrategy = "PLAN_EXECUTE"
	StrategyToolInvoke          ExecutionStrategy = "TOOL_INVOKE"
)

type ResolveOptions struct {
	AllowSearchFallback bool
}

type ResolveRequest struct {
	RequestText string
	Options     ResolveOptions
	Facts       map[string]any
}

type AutofilledInput struct {
	InputKey string `json:"input_key"`
	Value    any    `json:"value"`
	Source   string `json:"source"`
	Reason   string `json:"reason"`
}

type MissingInputResolutionMode string

const (
	ResolutionModeAutoRetryWithFacts MissingInputResolutionMode = "AUTO_RETRY_WITH_FACTS"
	ResolutionModeAskUser            MissingInputResolutionMode = "ASK_USER"
)

type MissingInputGuidance struct {
	InputKey            string                     `json:"input_key"`
	RequiredForRoute    string                     `json:"required_for_route,omitempty"`
	ResolutionMode      MissingInputResolutionMode `json:"resolution_mode"`
	Question            string                     `json:"question,omitempty"`
	ExternalFactRequest map[string]any             `json:"external_fact_request,omitempty"`
}

type ProgressHintMode string

const (
	ProgressHintModeAutoEnrichAndRetry     ProgressHintMode = "AUTO_ENRICH_AND_RETRY"
	ProgressHintModeAskUser                ProgressHintMode = "ASK_USER"
	ProgressHintModePartialAutoThenAskUser ProgressHintMode = "PARTIAL_AUTO_ENRICH_THEN_ASK_USER"
)

type ProgressHint struct {
	Mode             ProgressHintMode `json:"mode"`
	Message          string           `json:"message"`
	NextAction       string           `json:"next_action,omitempty"`
	RetryRecommended bool             `json:"retry_recommended,omitempty"`
	Parallelizable   bool             `json:"parallelizable,omitempty"`
	FactKeys         []string         `json:"fact_keys,omitempty"`
	BatchGroups      []string         `json:"batch_groups,omitempty"`
}

type RouteRef struct {
	RouteID    string `json:"route_id,omitempty"`
	CookbookID string `json:"cookbook_id,omitempty"`
	Version    string `json:"version,omitempty"`
	EntryID    string `json:"entry_id,omitempty"`
	EntryType  string `json:"entry_type,omitempty"`
	Source     string `json:"source,omitempty"`
}

type ExecutionSpec struct {
	Strategy      ExecutionStrategy `json:"strategy"`
	Tool          string            `json:"tool,omitempty"`
	ConnectorID   string            `json:"connector_id,omitempty"`
	Verb          string            `json:"verb,omitempty"`
	Orchestration map[string]any    `json:"orchestration,omitempty"`
}

type ResolveResult struct {
	Status               ResolveStatus          `json:"status"`
	Confidence           Confidence             `json:"confidence"`
	Domain               string                 `json:"domain,omitempty"`
	Route                RouteRef               `json:"route,omitempty"`
	Execution            ExecutionSpec          `json:"execution,omitempty"`
	Inputs               map[string]any         `json:"inputs,omitempty"`
	MissingInputs        []string               `json:"missing_inputs,omitempty"`
	AutofilledInputs     []AutofilledInput      `json:"autofilled_inputs,omitempty"`
	MissingInputGuidance []MissingInputGuidance `json:"missing_input_guidance,omitempty"`
	ProgressHint         *ProgressHint          `json:"progress_hint,omitempty"`
	NeedsClarification   bool                   `json:"needs_clarification"`
	Reasons              []string               `json:"reasons,omitempty"`
	FallbackHint         string                 `json:"fallback_hint,omitempty"`
}

type SearchFilter struct {
	Query       string
	ConnectorID string
	EntryType   string
	Tags        []string
}

type SearchCandidate struct {
	CookbookID     string
	Version        string
	EntryID        string
	EntryType      string
	Title          string
	ConnectorID    string
	Verb           string
	Tags           []string
	RequiredInputs []string
}

type SearchFn func(ctx context.Context, filter SearchFilter) ([]SearchCandidate, error)
