package routing

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	minRegistryScore            = 4
	searchScoreMinimum          = 3
	passportFieldsWorkflowRoute = "vaultclaw.passport_email_fields.v1"
	passportFieldsWorkflowTool  = "vaultclaw_passport_email_workflow"
)

var (
	emailPattern      = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	httpURLPattern    = regexp.MustCompile(`(?i)https?://[^\s'"<>]+`)
	aliasCleanupRegex = regexp.MustCompile(`[^a-z0-9\s]+`)
	aboutPattern      = regexp.MustCompile(`(?i)\b(?:about|regarding|re)\s+['"]?([^'"\n,.;:!?]+(?:\s+[^'"\n,.;:!?]+){0,10})`)
	weatherPattern    = regexp.MustCompile(`(?i)\b(?:(today|tomorrow|tonight|this\s+weekend|this\s+week|next\s+week)(?:'s)?\s+)?weather(?:\s+forecast)?\s+(?:in|for)\s+([a-z0-9][a-z0-9\s,\-']*)(?:\s+(today|tomorrow|tonight|this\s+weekend|this\s+week|next\s+week))?\b`)
)

type Resolver struct {
	registry Registry
}

func NewDefaultResolver() (*Resolver, error) {
	registry, err := LoadDefaultRegistry()
	if err != nil {
		return nil, err
	}
	return NewResolver(registry), nil
}

func NewResolver(reg Registry) *Resolver {
	return &Resolver{registry: reg}
}

func (r *Resolver) Resolve(ctx context.Context, req ResolveRequest, search SearchFn) ResolveResult {
	textRaw := strings.TrimSpace(req.RequestText)
	if textRaw == "" {
		return ResolveResult{
			Status:       StatusNotVaultEligible,
			Confidence:   ConfidenceLow,
			Reasons:      []string{"request_text is empty"},
			FallbackHint: "Provide a natural-language Vault request after /vault.",
		}
	}

	text := normalizeFreeText(textRaw)
	tokens := tokenSet(text)
	inputs := extractInputs(textRaw, r.registry.DocumentTypeAliases)
	if special, ok := r.resolvePassportFieldsWorkflow(textRaw, text, tokens, inputs); ok {
		return special
	}

	primary, topScore, secondScore, tied := r.pickRegistryRoute(text, tokens)
	if primary != nil && topScore >= minRegistryScore {
		if tied && secondScore >= minRegistryScore-1 {
			return ResolveResult{
				Status:             StatusAmbiguous,
				Confidence:         confidenceFromScore(topScore),
				Reasons:            []string{"multiple deterministic routes matched with similar confidence"},
				FallbackHint:       "Rephrase with explicit action (send, draft, reply, list, trash, untrash) or use strict route wording.",
				Inputs:             inputs,
				NeedsClarification: true,
			}
		}
		return r.resolveMatchedRoute(*primary, topScore, textRaw, text, inputs, req.Facts, "registry")
	}

	if req.Options.AllowSearchFallback && search != nil {
		searchResult := r.resolveWithSearch(ctx, textRaw, text, tokens, inputs, req.Facts, search)
		if searchResult.Status != StatusNotVaultEligible {
			return searchResult
		}
	}

	return ResolveResult{
		Status:     StatusNotVaultEligible,
		Confidence: ConfidenceLow,
		Domain:     "",
		Inputs:     inputs,
		Reasons: []string{
			"request did not match deterministic Vault routing rules",
		},
		FallbackHint: "Use /vault for explicit Vault actions or run the request without /vault for normal agent routing.",
	}
}

func (r *Resolver) resolvePassportFieldsWorkflow(
	rawText string,
	normalizedText string,
	tokens map[string]struct{},
	baseInputs map[string]any,
) (ResolveResult, bool) {
	if !wantsPassportFieldsWorkflow(tokens, normalizedText) {
		return ResolveResult{}, false
	}

	recipients := recipientsFromInputs(baseInputs["to"])
	commonRoute := RouteRef{
		RouteID:   passportFieldsWorkflowRoute,
		EntryID:   passportFieldsWorkflowTool,
		EntryType: "tool.workflow.v1",
		Source:    "intent",
	}
	commonExec := ExecutionSpec{
		Strategy: StrategyToolInvoke,
		Tool:     passportFieldsWorkflowTool,
	}

	if len(recipients) == 0 {
		guidance := []MissingInputGuidance{
			{
				InputKey:         "to",
				RequiredForRoute: passportFieldsWorkflowRoute,
				ResolutionMode:   ResolutionModeAskUser,
				Question:         questionForMissingInput("to"),
			},
		}
		return ResolveResult{
			Status:               StatusResolvedMissing,
			Confidence:           ConfidenceHigh,
			Domain:               "google.gmail",
			Route:                commonRoute,
			Execution:            commonExec,
			Inputs:               map[string]any{"request_text": strings.TrimSpace(rawText), "execute": true},
			MissingInputs:        []string{"to"},
			MissingInputGuidance: guidance,
			ProgressHint:         buildProgressHint(guidance),
			NeedsClarification:   true,
			Reasons:              []string{"passport field extraction requested but recipient email is missing"},
			FallbackHint:         "Provide a recipient email to continue the passport-field workflow.",
		}, true
	}

	if len(recipients) > 1 {
		return ResolveResult{
			Status:             StatusAmbiguous,
			Confidence:         ConfidenceHigh,
			Domain:             "google.gmail",
			Route:              commonRoute,
			Execution:          commonExec,
			Inputs:             map[string]any{"request_text": strings.TrimSpace(rawText), "execute": true},
			NeedsClarification: true,
			Reasons:            []string{"passport field extraction requested with multiple recipient emails"},
			FallbackHint:       "Provide exactly one recipient email when requesting passport fields in the email body.",
		}, true
	}

	toolArgs := map[string]any{
		"request_text":    strings.TrimSpace(rawText),
		"recipient_email": recipients[0],
		"execute":         true,
	}
	if subject := asString(baseInputs["subject"]); subject != "" {
		toolArgs["subject"] = subject
	}
	return ResolveResult{
		Status:     StatusResolvedExecutable,
		Confidence: ConfidenceHigh,
		Domain:     "google.gmail",
		Route:      commonRoute,
		Execution:  commonExec,
		Inputs:     toolArgs,
		Reasons: []string{
			"matched explicit intent to include passport fields/details in the email body",
		},
	}, true
}

func (r *Resolver) resolveMatchedRoute(
	route RegistryRoute,
	score int,
	rawText string,
	normalizedText string,
	baseInputs map[string]any,
	facts map[string]any,
	source string,
) ResolveResult {
	inputs := cloneMap(baseInputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	autofilled := make([]AutofilledInput, 0)
	applyComposeAutofill(route, rawText, normalizedText, facts, inputs, &autofilled)
	applyFactInputAutofill(inputs, facts, inputKeySet(route.RequiredInputs, route.OptionalInputs), &autofilled)
	missing := missingRequiredInputs(route.RequiredInputs, inputs)
	guidance := buildMissingInputGuidance(route, missing, rawText, normalizedText, inputs, facts)
	needsClarification := hasAskUserGuidance(guidance)
	return r.routeToResult(route, score, inputs, source, missing, autofilled, guidance, needsClarification)
}

func (r *Resolver) routeToResult(
	route RegistryRoute,
	score int,
	inputs map[string]any,
	source string,
	missing []string,
	autofilled []AutofilledInput,
	guidance []MissingInputGuidance,
	needsClarification bool,
) ResolveResult {
	status := StatusResolvedExecutable
	if len(missing) > 0 {
		status = StatusResolvedMissing
	}
	tool := strategyToolHint(route.Strategy)
	reasons := []string{
		fmt.Sprintf("matched route %s via deterministic keyword scoring", route.RouteID),
	}
	if len(autofilled) > 0 {
		reasons = append(reasons, fmt.Sprintf("autofilled %d missing inputs from deterministic enrichment", len(autofilled)))
	}

	return ResolveResult{
		Status:     status,
		Confidence: confidenceFromScore(score),
		Domain:     route.Domain,
		Route: RouteRef{
			RouteID:    route.RouteID,
			CookbookID: route.CookbookID,
			Version:    route.Version,
			EntryID:    route.EntryID,
			EntryType:  route.EntryType,
			Source:     source,
		},
		Execution: ExecutionSpec{
			Strategy:      route.Strategy,
			Tool:          tool,
			ConnectorID:   route.ConnectorID,
			Verb:          route.Verb,
			Orchestration: cloneMap(route.Orchestration),
		},
		Inputs:               inputs,
		MissingInputs:        missing,
		AutofilledInputs:     autofilled,
		MissingInputGuidance: guidance,
		ProgressHint:         buildProgressHint(guidance),
		NeedsClarification:   needsClarification,
		Reasons:              reasons,
		FallbackHint:         "",
	}
}

func (r *Resolver) resolveWithSearch(
	ctx context.Context,
	rawText string,
	normalizedText string,
	tokens map[string]struct{},
	inputs map[string]any,
	facts map[string]any,
	search SearchFn,
) ResolveResult {
	searchQuery := buildSearchQuery(tokens, normalizedText)
	tags := make([]string, 0, 2)
	if _, ok := tokens["gmail"]; ok {
		tags = append(tags, "gmail")
	}
	if _, ok := tokens["http"]; ok {
		tags = append(tags, "http")
	}
	if _, ok := tokens["webhook"]; ok {
		tags = append(tags, "http")
	}

	candidates, err := search(ctx, SearchFilter{
		Query: searchQuery,
		Tags:  tags,
	})
	if err != nil {
		return ResolveResult{
			Status:       StatusNotVaultEligible,
			Confidence:   ConfidenceLow,
			Inputs:       inputs,
			Reasons:      []string{fmt.Sprintf("search fallback failed: %v", err)},
			FallbackHint: "Try again without /vault or verify cookbook catalog availability.",
		}
	}
	if len(candidates) == 0 {
		return ResolveResult{Status: StatusNotVaultEligible, Confidence: ConfidenceLow, Inputs: inputs}
	}

	type scoredCandidate struct {
		candidate SearchCandidate
		score     int
	}
	scored := make([]scoredCandidate, 0, len(candidates))
	for _, item := range candidates {
		score := scoreSearchCandidate(item, tokens, normalizedText)
		scored = append(scored, scoredCandidate{candidate: item, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		if scored[i].candidate.CookbookID != scored[j].candidate.CookbookID {
			return scored[i].candidate.CookbookID < scored[j].candidate.CookbookID
		}
		return scored[i].candidate.EntryID < scored[j].candidate.EntryID
	})

	top := scored[0]
	if top.score < searchScoreMinimum {
		return ResolveResult{Status: StatusNotVaultEligible, Confidence: ConfidenceLow, Inputs: inputs}
	}
	if len(scored) > 1 && scored[1].score >= top.score-1 {
		return ResolveResult{
			Status:       StatusAmbiguous,
			Confidence:   confidenceFromScore(top.score),
			Inputs:       inputs,
			Reasons:      []string{"search fallback matched multiple cookbook entries with similar rank"},
			FallbackHint: "Refine the request with connector domain or action details.",
		}
	}

	strategy := strategyFromEntryType(top.candidate.EntryType)
	if strategy == "" {
		strategy = StrategyRecipe
	}

	tool := strategyToolHint(strategy)
	domain := inferDomain(top.candidate)
	autofilled := make([]AutofilledInput, 0)
	applyFactInputAutofill(inputs, facts, inputKeySet(top.candidate.RequiredInputs), &autofilled)
	missing := missingRequiredInputs(top.candidate.RequiredInputs, inputs)
	routeID := fmt.Sprintf("search:%s:%s", top.candidate.CookbookID, top.candidate.EntryID)
	guidance := buildMissingInputGuidance(
		RegistryRoute{
			RouteID:        routeID,
			Domain:         domain,
			EntryID:        top.candidate.EntryID,
			RequiredInputs: append([]string(nil), top.candidate.RequiredInputs...),
		},
		missing,
		rawText,
		normalizedText,
		inputs,
		facts,
	)
	needsClarification := hasAskUserGuidance(guidance)
	status := StatusResolvedExecutable
	if len(missing) > 0 {
		status = StatusResolvedMissing
	}
	reasons := []string{
		"resolved via controlled recipes_search fallback",
	}
	if len(autofilled) > 0 {
		reasons = append(reasons, fmt.Sprintf("autofilled %d inputs from context facts", len(autofilled)))
	}
	return ResolveResult{
		Status:     status,
		Confidence: confidenceFromScore(top.score),
		Domain:     domain,
		Route: RouteRef{
			RouteID:    routeID,
			CookbookID: top.candidate.CookbookID,
			Version:    top.candidate.Version,
			EntryID:    top.candidate.EntryID,
			EntryType:  top.candidate.EntryType,
			Source:     "search",
		},
		Execution: ExecutionSpec{
			Strategy:    strategy,
			Tool:        tool,
			ConnectorID: top.candidate.ConnectorID,
			Verb:        top.candidate.Verb,
		},
		Inputs:               inputs,
		MissingInputs:        missing,
		AutofilledInputs:     autofilled,
		MissingInputGuidance: guidance,
		ProgressHint:         buildProgressHint(guidance),
		NeedsClarification:   needsClarification,
		Reasons:              reasons,
	}
}

func (r *Resolver) pickRegistryRoute(text string, tokens map[string]struct{}) (*RegistryRoute, int, int, bool) {
	var best *RegistryRoute
	top := 0
	second := 0
	tied := false

	for i := range r.registry.Routes {
		route := &r.registry.Routes[i]
		score := scoreRegistryRoute(*route, text, tokens)
		if score > top {
			second = top
			top = score
			best = route
			tied = false
			continue
		}
		if score == top && score > 0 {
			tied = true
		}
		if score > second {
			second = score
		}
	}
	return best, top, second, tied
}

func scoreRegistryRoute(route RegistryRoute, text string, tokens map[string]struct{}) int {
	score := 0
	if strings.Contains(route.Domain, "gmail") {
		domainMatches := 0
		for _, marker := range []string{"gmail", "inbox", "email", "mail", "message", "messages", "thread", "label", "labels", "draft"} {
			if _, ok := tokens[marker]; ok {
				domainMatches += 1
			}
		}
		if domainMatches == 0 {
			return 0
		}
		score += domainMatches
	}
	if strings.Contains(route.Domain, "http") {
		domainMatches := 0
		for _, marker := range []string{"http", "url", "webhook", "endpoint", "api"} {
			if _, ok := tokens[marker]; ok {
				domainMatches += 1
			}
		}
		if domainMatches == 0 {
			return 0
		}
		score += domainMatches
	}
	for _, keyword := range route.Keywords {
		if strings.Contains(text, keyword) {
			score += 2
		}
		if _, ok := tokens[keyword]; ok {
			score += 1
		}
	}
	for _, tag := range route.Tags {
		if _, ok := tokens[tag]; ok {
			score += 1
		}
	}
	for _, word := range splitWords(route.RouteID + " " + route.EntryID) {
		if _, ok := tokens[word]; ok {
			score += 1
		}
	}
	return score
}

func scoreSearchCandidate(item SearchCandidate, tokens map[string]struct{}, text string) int {
	score := 0
	if strings.TrimSpace(item.EntryID) == "" {
		return score
	}
	for _, word := range splitWords(strings.Join([]string{item.EntryID, item.Title, item.EntryType, item.ConnectorID, item.Verb, strings.Join(item.Tags, " ")}, " ")) {
		if _, ok := tokens[word]; ok {
			score += 1
		}
	}
	if strings.Contains(strings.ToLower(item.EntryType), "template") {
		score += 2
	}
	if strings.Contains(strings.ToLower(item.EntryType), "recipe") {
		score += 1
	}
	if strings.Contains(strings.ToLower(item.ConnectorID), "google") && strings.Contains(text, "gmail") {
		score += 2
	}
	if strings.Contains(strings.ToLower(item.Verb), "http") && (strings.Contains(text, "http") || strings.Contains(text, "url")) {
		score += 2
	}
	return score
}

func applyComposeAutofill(
	route RegistryRoute,
	rawText string,
	normalizedText string,
	facts map[string]any,
	inputs map[string]any,
	out *[]AutofilledInput,
) {
	if !isGmailComposeRoute(route) {
		return
	}
	weatherLocation, weatherTimeframe, weatherRequested := detectWeatherIntent(normalizedText)
	weatherSummary := weatherSummaryFromFacts(facts)
	subjectFromFacts := composeSubjectFromFacts(facts)
	bodyFromFacts := composeBodyFromFacts(facts, weatherLocation, weatherTimeframe)
	bodyNeedsExternal := shouldDeferBodyAutofillForExternal(rawText, normalizedText, inputs, weatherRequested, weatherSummary)
	subjectNeedsExternal := shouldDeferSubjectAutofillForExternal(rawText, normalizedText, inputs, bodyNeedsExternal)

	if !hasInputValue(inputs["subject"]) && subjectFromFacts != "" {
		setAutofilledInput(
			inputs,
			"subject",
			subjectFromFacts,
			"external_facts",
			"composed subject from normal-loop facts",
			out,
		)
	}

	if !hasInputValue(inputs["subject"]) {
		if inferred := inferSubjectFromNL(rawText); inferred != "" {
			setAutofilledInput(inputs, "subject", inferred, "nl_heuristic", "inferred subject from natural-language topic phrase", out)
		}
	}

	if !hasInputValue(inputs["subject"]) && !subjectNeedsExternal && hasInputValue(inputs["document_attachments"]) {
		if subject := defaultAttachmentSubject(inputs["document_attachments"]); subject != "" {
			setAutofilledInput(inputs, "subject", subject, "attachment_default", "filled subject from attachment intent", out)
		}
	}

	if !hasInputValue(inputs["subject"]) {
		if weatherRequested {
			subject := "Weather update"
			if weatherLocation != "" {
				subject = fmt.Sprintf("Weather update for %s", weatherLocation)
			}
			if weatherTimeframe == "tomorrow" {
				subject = subject + " (tomorrow)"
			}
			setAutofilledInput(inputs, "subject", subject, "nl_heuristic", "inferred weather-focused subject", out)
		}
	}

	if !hasInputValue(inputs["text_plain"]) {
		if bodyFromFacts != "" {
			setAutofilledInput(
				inputs,
				"text_plain",
				bodyFromFacts,
				"external_facts",
				"composed body from normal-loop facts",
				out,
			)
		}
	}

	if !hasInputValue(inputs["text_plain"]) && !bodyNeedsExternal {
		if inferred := inferBodyFromNL(rawText, asString(inputs["subject"])); inferred != "" {
			setAutofilledInput(inputs, "text_plain", inferred, "nl_heuristic", "inferred body from request phrasing", out)
		}
	}

	if !hasInputValue(inputs["text_plain"]) && !bodyNeedsExternal && hasInputValue(inputs["document_attachments"]) {
		body := "Attached is the requested document."
		if name := defaultAttachmentDisplayName(inputs["document_attachments"]); name != "" {
			body = fmt.Sprintf("Attached is my %s.", strings.ToLower(name))
		}
		setAutofilledInput(inputs, "text_plain", body, "attachment_default", "filled body from attachment intent", out)
	}
}

func buildMissingInputGuidance(
	route RegistryRoute,
	missing []string,
	rawText string,
	normalizedText string,
	inputs map[string]any,
	facts map[string]any,
) []MissingInputGuidance {
	if len(missing) == 0 {
		return nil
	}
	requiredFor := strings.TrimSpace(route.RouteID)
	if requiredFor == "" {
		requiredFor = strings.TrimSpace(route.EntryID)
	}

	out := make([]MissingInputGuidance, 0, len(missing))
	normalized := normalizedText
	if normalized == "" {
		normalized = normalizeFreeText(rawText)
	}

	for _, inputKey := range missing {
		if guidance, ok := buildPolicyMissingInputGuidance(route, inputKey, requiredFor, rawText, normalized, inputs, facts); ok {
			out = append(out, guidance)
			continue
		}

		out = append(out, MissingInputGuidance{
			InputKey:         inputKey,
			RequiredForRoute: requiredFor,
			ResolutionMode:   ResolutionModeAskUser,
			Question:         questionForMissingInput(inputKey),
		})
	}
	return out
}

func buildPolicyMissingInputGuidance(
	route RegistryRoute,
	inputKey string,
	requiredFor string,
	rawText string,
	normalizedText string,
	inputs map[string]any,
	facts map[string]any,
) (MissingInputGuidance, bool) {
	policy, ok := selectEnrichmentPolicy(route.EnrichmentPolicies, inputKey, normalizedText, rawText, inputs, facts)
	if !ok {
		return MissingInputGuidance{}, false
	}
	trimmedInputKey := strings.TrimSpace(inputKey)
	if policy.Sensitive || policy.ResolutionMode == ResolutionModeAskUser {
		return MissingInputGuidance{
			InputKey:         trimmedInputKey,
			RequiredForRoute: requiredFor,
			ResolutionMode:   ResolutionModeAskUser,
			Question:         questionForMissingInput(trimmedInputKey),
		}, true
	}
	if policy.ResolutionMode != ResolutionModeAutoRetryWithFacts {
		return MissingInputGuidance{
			InputKey:         trimmedInputKey,
			RequiredForRoute: requiredFor,
			ResolutionMode:   ResolutionModeAskUser,
			Question:         questionForMissingInput(trimmedInputKey),
		}, true
	}

	factKey := strings.TrimSpace(policy.FactKey)
	if factKey == "" {
		factKey = trimmedInputKey
	}
	factReq := map[string]any{
		"input_key":      trimmedInputKey,
		"fact_key":       factKey,
		"fact_kind":      strings.TrimSpace(policy.FactKind),
		"kind":           strings.TrimSpace(policy.FactKind), // Backward-compatible with existing clients.
		"parallelizable": policy.Parallelizable,
		"batch_group":    resolveBatchGroup(policy.BatchGroup, route.RouteID),
		"request_text":   strings.TrimSpace(rawText),
		"instructions":   resolvePolicyInstructions(policy, trimmedInputKey, factKey),
	}
	if strings.ToLower(strings.TrimSpace(policy.FactKind)) == "weather_forecast" {
		location, timeframe, weatherRequested := detectWeatherIntent(normalizedText)
		if weatherRequested {
			if location != "" {
				factReq["location"] = location
			}
			if timeframe == "" {
				timeframe = "today"
			}
			factReq["timeframe"] = timeframe
		}
	}
	return MissingInputGuidance{
		InputKey:            trimmedInputKey,
		RequiredForRoute:    requiredFor,
		ResolutionMode:      ResolutionModeAutoRetryWithFacts,
		Question:            fmt.Sprintf("Resolve %s and retry route resolve with context.facts.%s.", trimmedInputKey, factKey),
		ExternalFactRequest: factReq,
	}, true
}

func selectEnrichmentPolicy(
	policies []RegistryEnrichmentPolicy,
	inputKey string,
	normalizedText string,
	rawText string,
	inputs map[string]any,
	facts map[string]any,
) (RegistryEnrichmentPolicy, bool) {
	trimmed := strings.TrimSpace(inputKey)
	if len(policies) == 0 || trimmed == "" {
		return RegistryEnrichmentPolicy{}, false
	}
	normalizedInputKey := strings.ToLower(trimmed)
	for _, policy := range policies {
		if strings.ToLower(strings.TrimSpace(policy.InputKey)) != normalizedInputKey {
			continue
		}
		if policyConditionMatches(policy.Condition, normalizedText, rawText, inputs, facts) {
			return policy, true
		}
	}
	return RegistryEnrichmentPolicy{}, false
}

func policyConditionMatches(
	condition string,
	normalizedText string,
	rawText string,
	inputs map[string]any,
	facts map[string]any,
) bool {
	trimmed := strings.TrimSpace(strings.ToLower(condition))
	if trimmed == "" {
		return true
	}
	switch trimmed {
	case "weather_requested":
		_, _, requested := detectWeatherIntent(normalizedText)
		return requested && weatherSummaryFromFacts(facts) == ""
	case "semantic_body_request":
		return shouldDeferBodyAutofillForExternal(rawText, normalizedText, inputs, false, "")
	case "semantic_subject_request":
		bodyNeedsExternal := shouldDeferBodyAutofillForExternal(rawText, normalizedText, inputs, false, "")
		return shouldDeferSubjectAutofillForExternal(rawText, normalizedText, inputs, bodyNeedsExternal)
	default:
		return false
	}
}

func resolveBatchGroup(raw string, routeID string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		return trimmed
	}
	base := strings.TrimSpace(routeID)
	if base == "" {
		return "default"
	}
	return strings.ReplaceAll(strings.ToLower(base), ".", "_") + "_enrichment"
}

func resolvePolicyInstructions(policy RegistryEnrichmentPolicy, inputKey string, factKey string) string {
	if instructions := strings.TrimSpace(policy.Instructions); instructions != "" {
		return instructions
	}
	return fmt.Sprintf("Resolve %s and retry vaultclaw_route_resolve with context.facts.%s.", inputKey, factKey)
}

func hasAskUserGuidance(guidance []MissingInputGuidance) bool {
	for _, item := range guidance {
		if item.ResolutionMode == ResolutionModeAskUser {
			return true
		}
	}
	return false
}

func buildProgressHint(guidance []MissingInputGuidance) *ProgressHint {
	if len(guidance) == 0 {
		return nil
	}
	auto := make([]MissingInputGuidance, 0, len(guidance))
	ask := make([]MissingInputGuidance, 0, len(guidance))
	for _, item := range guidance {
		switch item.ResolutionMode {
		case ResolutionModeAutoRetryWithFacts:
			auto = append(auto, item)
		case ResolutionModeAskUser:
			ask = append(ask, item)
		}
	}

	if len(auto) > 0 && len(ask) == 0 {
		factKeys, batchGroups, parallelizable := progressFactMeta(auto)
		return &ProgressHint{
			Mode:             ProgressHintModeAutoEnrichAndRetry,
			Message:          "Missing inputs can be auto-filled with normal-loop facts. Fetch facts and retry route resolve.",
			NextAction:       "RUN_FACT_TASKS_AND_RETRY_ROUTE_RESOLVE",
			RetryRecommended: true,
			Parallelizable:   parallelizable,
			FactKeys:         factKeys,
			BatchGroups:      batchGroups,
		}
	}
	if len(auto) > 0 && len(ask) > 0 {
		factKeys, batchGroups, parallelizable := progressFactMeta(auto)
		return &ProgressHint{
			Mode:             ProgressHintModePartialAutoThenAskUser,
			Message:          "Some missing inputs can be auto-filled with normal-loop facts; remaining fields require user input.",
			NextAction:       "RUN_FACT_TASKS_THEN_ASK_USER_IF_STILL_MISSING",
			RetryRecommended: true,
			Parallelizable:   parallelizable,
			FactKeys:         factKeys,
			BatchGroups:      batchGroups,
		}
	}
	return &ProgressHint{
		Mode:       ProgressHintModeAskUser,
		Message:    "Missing inputs require user clarification.",
		NextAction: "ASK_USER_FOR_MISSING_INPUTS",
	}
}

func progressFactMeta(guidance []MissingInputGuidance) ([]string, []string, bool) {
	factKeySet := map[string]struct{}{}
	batchSet := map[string]struct{}{}
	parallelizable := len(guidance) > 0
	for _, item := range guidance {
		if key := asString(item.ExternalFactRequest["fact_key"]); key != "" {
			factKeySet[key] = struct{}{}
		}
		if group := asString(item.ExternalFactRequest["batch_group"]); group != "" {
			batchSet[group] = struct{}{}
		}
		flag, ok := item.ExternalFactRequest["parallelizable"].(bool)
		if !ok || !flag {
			parallelizable = false
		}
	}
	factKeys := make([]string, 0, len(factKeySet))
	for key := range factKeySet {
		factKeys = append(factKeys, key)
	}
	sort.Strings(factKeys)
	batchGroups := make([]string, 0, len(batchSet))
	for group := range batchSet {
		batchGroups = append(batchGroups, group)
	}
	sort.Strings(batchGroups)
	return factKeys, batchGroups, parallelizable
}

func setAutofilledInput(inputs map[string]any, inputKey string, value any, source string, reason string, out *[]AutofilledInput) {
	if !hasInputValue(value) {
		return
	}
	if hasInputValue(inputs[inputKey]) {
		return
	}
	inputs[inputKey] = value
	*out = append(*out, AutofilledInput{
		InputKey: strings.TrimSpace(inputKey),
		Value:    value,
		Source:   strings.TrimSpace(source),
		Reason:   strings.TrimSpace(reason),
	})
}

func applyFactInputAutofill(inputs map[string]any, facts map[string]any, allowed map[string]struct{}, out *[]AutofilledInput) {
	if len(facts) == 0 || len(allowed) == 0 {
		return
	}
	for inputKey := range allowed {
		if hasInputValue(inputs[inputKey]) {
			continue
		}
		for _, factKey := range factCandidatesForInput(inputKey) {
			value, ok := facts[factKey]
			if !ok || !hasInputValue(value) {
				continue
			}
			setAutofilledInput(
				inputs,
				inputKey,
				value,
				"external_facts",
				fmt.Sprintf("filled %s from context.facts.%s", inputKey, factKey),
				out,
			)
			break
		}
	}
}

func factCandidatesForInput(inputKey string) []string {
	trimmed := strings.TrimSpace(inputKey)
	if trimmed == "" {
		return nil
	}
	out := []string{trimmed}
	switch trimmed {
	case "subject":
		out = append(out, "email_subject")
	case "text_plain":
		out = append(out, "email_body")
	case "url":
		out = append(out, "http_url")
	case "method":
		out = append(out, "http_method")
	}
	return out
}

func inputKeySet(groups ...[]string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, keys := range groups {
		for _, key := range keys {
			trimmed := strings.TrimSpace(key)
			if trimmed == "" {
				continue
			}
			out[trimmed] = struct{}{}
		}
	}
	return out
}

func isGmailComposeRoute(route RegistryRoute) bool {
	if strings.TrimSpace(strings.ToLower(route.Domain)) != "google.gmail" {
		return false
	}
	switch strings.TrimSpace(route.RouteID) {
	case "google.gmail.send_email.v1", "google.gmail.create_draft.v1", "google.gmail.reply_email.v1":
		return true
	}
	switch strings.TrimSpace(route.EntryID) {
	case "gmail_tpl_plan_send_email_v1", "gmail_tpl_plan_create_draft_v1", "gmail_tpl_plan_reply_in_thread_v1":
		return true
	}
	return false
}

func inferSubjectFromNL(rawText string) string {
	match := aboutPattern.FindStringSubmatch(rawText)
	if len(match) > 1 {
		return normalizeSentenceFragment(match[1], 70)
	}
	return ""
}

func inferBodyFromNL(rawText, subject string) string {
	if phrase := firstMatch(rawText, `(?i)\b(?:saying|that)\s+['"]?([^'"\n]+)['"]?`); phrase != "" {
		return normalizeSentenceFragment(phrase, 220)
	}
	if strings.TrimSpace(subject) != "" {
		return fmt.Sprintf("Following up regarding %s.", strings.TrimSpace(subject))
	}
	return ""
}

func detectWeatherIntent(normalizedText string) (string, string, bool) {
	match := weatherPattern.FindStringSubmatch(normalizedText)
	if len(match) > 0 {
		location := ""
		timeframe := ""
		if len(match) > 2 {
			location = normalizeSentenceFragment(match[2], 60)
		}
		if len(match) > 1 {
			timeframe = normalizeSentenceFragment(match[1], 20)
		}
		if timeframe == "" && len(match) > 3 {
			timeframe = normalizeSentenceFragment(match[3], 20)
		}
		if timeframe == "" {
			timeframe = "today"
		}
		return location, strings.ToLower(timeframe), true
	}
	if strings.Contains(normalizedText, "weather") {
		return "", "today", true
	}
	return "", "", false
}

func composeSubjectFromFacts(facts map[string]any) string {
	if len(facts) == 0 {
		return ""
	}
	if subject := asString(facts["email_subject"]); subject != "" {
		return strings.TrimSpace(subject)
	}
	return ""
}

func composeBodyFromFacts(facts map[string]any, weatherLocation string, weatherTimeframe string) string {
	if len(facts) == 0 {
		return ""
	}
	if body := asString(facts["email_body"]); body != "" {
		return strings.TrimSpace(body)
	}
	weatherSummary := weatherSummaryFromFacts(facts)
	if strings.TrimSpace(weatherSummary) == "" {
		return ""
	}
	return composeWeatherBody(weatherSummary, weatherLocation, weatherTimeframe)
}

func shouldDeferBodyAutofillForExternal(
	rawText string,
	normalizedText string,
	inputs map[string]any,
	weatherRequested bool,
	weatherSummary string,
) bool {
	if hasInputValue(inputs["text_plain"]) {
		return false
	}
	if weatherRequested && strings.TrimSpace(weatherSummary) == "" {
		return true
	}
	if hasSemanticBodyRequest(normalizedText) {
		return true
	}
	return false
}

func shouldDeferSubjectAutofillForExternal(rawText string, normalizedText string, inputs map[string]any, bodyNeedsExternal bool) bool {
	if hasInputValue(inputs["subject"]) {
		return false
	}
	if !bodyNeedsExternal {
		return false
	}
	if hasInputValue(inputs["document_attachments"]) {
		return true
	}
	if hasSemanticSubjectRequest(normalizedText) {
		return true
	}
	return false
}

func hasSemanticBodyRequest(normalizedText string) bool {
	if strings.TrimSpace(normalizedText) == "" {
		return false
	}
	markers := []string{
		" weather ",
		" recipe ",
		" how to ",
		" instructions ",
		" instruction ",
		" summarize ",
		" summary ",
		" explain ",
		" answer ",
		" steps ",
		" step by step ",
	}
	padded := " " + normalizedText + " "
	for _, marker := range markers {
		if strings.Contains(padded, marker) {
			return true
		}
	}
	return false
}

func hasSemanticSubjectRequest(normalizedText string) bool {
	if strings.TrimSpace(normalizedText) == "" {
		return false
	}
	return strings.Contains(normalizedText, "subject as ") ||
		strings.Contains(normalizedText, "subject to be ") ||
		strings.Contains(normalizedText, "subject line")
}

func wantsPassportFieldsWorkflow(tokens map[string]struct{}, normalizedText string) bool {
	if _, ok := tokens["passport"]; !ok {
		return false
	}
	if _, ok := tokens["email"]; !ok {
		if _, gmail := tokens["gmail"]; !gmail {
			return false
		}
	}

	padded := " " + strings.TrimSpace(normalizedText) + " "
	if strings.Contains(padded, " without passport fields ") ||
		strings.Contains(padded, " no passport fields ") ||
		strings.Contains(padded, " without passport details ") ||
		strings.Contains(padded, " no passport details ") {
		return false
	}

	if strings.Contains(padded, " passport fields ") || strings.Contains(padded, " passport details ") {
		return true
	}

	if _, fields := tokens["fields"]; fields {
		if _, body := tokens["body"]; body {
			return true
		}
	}
	if _, details := tokens["details"]; details {
		if _, body := tokens["body"]; body {
			return true
		}
	}
	return false
}

func recipientsFromInputs(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return uniqueStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			value, ok := item.(string)
			if !ok {
				continue
			}
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			out = append(out, trimmed)
		}
		return uniqueStrings(out)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		return []string{trimmed}
	default:
		return nil
	}
}

func weatherSummaryFromFacts(facts map[string]any) string {
	if len(facts) == 0 {
		return ""
	}
	if direct := asString(facts["weather_summary"]); direct != "" {
		return strings.TrimSpace(direct)
	}
	if weather, ok := facts["weather"].(map[string]any); ok {
		if summary := asString(weather["summary"]); summary != "" {
			return strings.TrimSpace(summary)
		}
	}
	return ""
}

func composeWeatherBody(summary string, location string, timeframe string) string {
	trimmed := strings.TrimSpace(summary)
	if trimmed == "" {
		return ""
	}
	if location == "" {
		return trimmed
	}
	if timeframe == "" {
		return fmt.Sprintf("Weather for %s: %s", location, trimmed)
	}
	return fmt.Sprintf("Weather for %s (%s): %s", location, timeframe, trimmed)
}

func defaultAttachmentSubject(raw any) string {
	name := defaultAttachmentDisplayName(raw)
	if name == "" {
		return "Document: Requested document"
	}
	return fmt.Sprintf("Document: %s", name)
}

func defaultAttachmentDisplayName(raw any) string {
	typeID := firstAttachmentTypeID(raw)
	if typeID == "" {
		return ""
	}
	return humanizeTypeID(typeID)
}

func firstAttachmentTypeID(raw any) string {
	switch v := raw.(type) {
	case []map[string]any:
		for _, item := range v {
			if typeID := asString(item["type_id"]); typeID != "" {
				return typeID
			}
		}
	case []any:
		for _, item := range v {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if typeID := asString(obj["type_id"]); typeID != "" {
				return typeID
			}
		}
	}
	return ""
}

func humanizeTypeID(typeID string) string {
	trimmed := strings.TrimSpace(typeID)
	if trimmed == "" {
		return ""
	}
	last := trimmed
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 && idx+1 < len(trimmed) {
		last = trimmed[idx+1:]
	}
	last = strings.ReplaceAll(last, "_", " ")
	last = strings.ReplaceAll(last, "-", " ")
	words := strings.Fields(strings.ToLower(last))
	for i, word := range words {
		if len(word) == 0 {
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}

func normalizeSentenceFragment(input string, maxLen int) string {
	clean := strings.TrimSpace(strings.Join(strings.Fields(input), " "))
	clean = strings.Trim(clean, " .,:;!?\"'")
	if clean == "" {
		return ""
	}
	if maxLen > 0 && len(clean) > maxLen {
		clean = strings.TrimSpace(clean[:maxLen])
	}
	return clean
}

func questionForMissingInput(inputKey string) string {
	switch strings.TrimSpace(inputKey) {
	case "to":
		return "Who should receive this email? Provide at least one recipient email address."
	case "subject":
		return "What subject should be used for this email?"
	case "text_plain":
		return "What should the plain-text email body say?"
	case "thread_id":
		return "Which thread_id should this reply target?"
	case "message_id":
		return "Which message_id should be used?"
	case "url":
		return "What URL should be requested?"
	default:
		return fmt.Sprintf("Please provide %s.", inputKey)
	}
}

func asString(raw any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func extractInputs(rawText string, aliasMap map[string]string) map[string]any {
	inputs := map[string]any{}
	text := strings.TrimSpace(rawText)
	lower := strings.ToLower(text)

	emails := uniqueStrings(emailPattern.FindAllString(text, -1))
	if len(emails) > 0 {
		inputs["to"] = emails
	}
	if subject := firstMatch(text,
		`(?i)\bwith\s+(?:the\s+)?subject\s+['"]?([^'"\n,.;:!?]+?)['"]?\s+and\s+(?:message\s+)?body\b`,
		`(?i)\bsubject\s*(?:is|=|:)\s*['"]([^'"\n]+)['"]`,
		`(?i)\bsubject\s+as\s+['"]?([^'"\n,.;:!?]+)`,
		`(?i)\bsubject\s+to\s+be\s+['"]?([^'"\n,.;:!?]+)`,
		`(?i)\bsubject\s+['"]?([^'"\n,.;:!?]+?)['"]?\s+and\s+(?:message\s+)?body\b`,
		`(?i)\bsubject\s+['"]([^'"\n]+)['"]`,
		`(?i)\bwith\s+(?:the\s+)?subject\s+['"]([^'"\n]+)['"]`,
		`(?i)\bwith\s+(?:the\s+)?subject\s+['"]?([^'"\n,.;:!?]+(?:\s+[^'"\n,.;:!?]+){0,10})`,
	); subject != "" {
		inputs["subject"] = subject
	}
	if body := firstMatch(text,
		`(?i)\b(?:and\s+)?(?:message\s+)?body\s*(?:is|=|:)\s*['"]([^'"\n]+)['"]`,
		`(?i)\b(?:and\s+)?(?:message\s+)?body\s*(?:is|=|:)\s*['"]?([^'"\n,.;:!?]+(?:\s+[^'"\n,.;:!?]+){0,24})`,
		`(?i)\b(?:and\s+)?(?:message\s+)?body\s+to\s+(?:have|say|include)\s+['"]?([^'"\n]+)`,
		`(?i)\b(?:and\s+)?(?:message\s+)?body\s+['"]([^'"\n]+)['"]`,
		`(?i)\b(?:and\s+)?(?:message\s+)?body\s+['"]?([^'"\n,.;:!?]+(?:\s+[^'"\n,.;:!?]+){0,24})`,
		`(?i)\bwith\s+(?:message\s+)?body\s+['"]([^'"\n]+)['"]`,
	); body != "" {
		inputs["text_plain"] = body
	}
	if msgID := firstMatch(text,
		`(?i)\bmessage[_\s-]?id\s*(?:is|=|:)\s*([A-Za-z0-9_\-]+)`,
		`(?i)\bmessage\s+([A-Za-z0-9_\-]{8,})\b`,
	); msgID != "" {
		inputs["message_id"] = msgID
	}
	if threadID := firstMatch(text,
		`(?i)\bthread[_\s-]?id\s*(?:is|=|:)\s*([A-Za-z0-9_\-]+)`,
		`(?i)\bthread\s+([A-Za-z0-9_\-]{8,})\b`,
	); threadID != "" {
		inputs["thread_id"] = threadID
	}
	if label := firstMatch(text,
		`(?i)\blabel\s*(?:is|=|:)\s*['"]?([A-Za-z0-9_\-]+)`,
	); label != "" {
		inputs["label"] = strings.ToLower(label)
	}
	if limitRaw := firstMatch(text,
		`(?i)\b(?:first|top|last)\s+(\d{1,3})\b`,
		`(?i)\b(\d{1,3})\s+(?:emails|messages)\b`,
	); limitRaw != "" {
		if n, err := strconv.Atoi(limitRaw); err == nil && n > 0 {
			inputs["page_limit"] = n
		}
	}

	if method := firstMatch(text, `(?i)\b(GET|POST|PUT|PATCH|DELETE)\b`); method != "" {
		inputs["method"] = strings.ToUpper(method)
	}
	if url := firstMatch(text, `(?i)\b(https?://[^\s'"<>]+)`); url != "" {
		inputs["url"] = url
	}

	if strings.Contains(lower, "inbox") && inputs["label"] == nil {
		inputs["label"] = "inbox"
	}

	attachments := extractDocumentAttachments(lower, aliasMap)
	if len(attachments) > 0 {
		inputs["document_attachments"] = attachments
	}

	return inputs
}

func extractDocumentAttachments(lowerText string, aliases map[string]string) []map[string]any {
	if len(aliases) == 0 {
		return nil
	}
	normalized := normalizeAliasKey(lowerText)
	if normalized == "" {
		return nil
	}
	type pair struct {
		alias  string
		typeID string
	}
	pairs := make([]pair, 0, len(aliases))
	for alias, typeID := range aliases {
		pairs = append(pairs, pair{alias: alias, typeID: typeID})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if len(pairs[i].alias) != len(pairs[j].alias) {
			return len(pairs[i].alias) > len(pairs[j].alias)
		}
		return pairs[i].alias < pairs[j].alias
	})

	seen := map[string]struct{}{}
	out := make([]map[string]any, 0)
	for _, candidate := range pairs {
		if !containsAlias(normalized, candidate.alias) {
			continue
		}
		if _, ok := seen[candidate.typeID]; ok {
			continue
		}
		seen[candidate.typeID] = struct{}{}
		out = append(out, map[string]any{"type_id": candidate.typeID})
	}
	return out
}

func containsAlias(normalizedText, alias string) bool {
	if alias == "" {
		return false
	}
	if normalizedText == alias {
		return true
	}
	if strings.Contains(normalizedText, " "+alias+" ") {
		return true
	}
	if strings.HasPrefix(normalizedText, alias+" ") || strings.HasSuffix(normalizedText, " "+alias) {
		return true
	}
	return strings.Contains(normalizedText, alias)
}

func missingRequiredInputs(required []string, inputs map[string]any) []string {
	if len(required) == 0 {
		return nil
	}
	missing := make([]string, 0, len(required))
	for _, key := range required {
		value, ok := inputs[key]
		if !ok || !hasInputValue(value) {
			missing = append(missing, key)
		}
	}
	return missing
}

func hasInputValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case string:
		return strings.TrimSpace(v) != ""
	case []string:
		return len(v) > 0
	case []any:
		return len(v) > 0
	case []map[string]any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	default:
		return true
	}
}

func strategyFromEntryType(entryType string) ExecutionStrategy {
	v := strings.ToLower(strings.TrimSpace(entryType))
	switch v {
	case "template.plan.v1", "template.verb.v1":
		return StrategyTemplate
	case "recipe.plan.v1", "recipe.verb.v1":
		return StrategyRecipe
	default:
		return ""
	}
}

func strategyToolHint(strategy ExecutionStrategy) string {
	switch strategy {
	case StrategyTemplate:
		return "vaultclaw_template_render"
	case StrategyRecipe:
		return "vaultclaw_recipe_get"
	case StrategyConnectorExecuteJob:
		return "vaultclaw_connector_execute_job"
	case StrategyPlanExecute:
		return "vaultclaw_plan_execute"
	default:
		return ""
	}
}

func inferDomain(candidate SearchCandidate) string {
	verb := strings.ToLower(strings.TrimSpace(candidate.Verb))
	if strings.HasPrefix(verb, "google.gmail.") {
		return "google.gmail"
	}
	if strings.HasPrefix(verb, "generic.http.") {
		return "generic.http"
	}
	if strings.Contains(strings.ToLower(strings.Join(candidate.Tags, " ")), "gmail") {
		return "google.gmail"
	}
	if strings.Contains(strings.ToLower(strings.Join(candidate.Tags, " ")), "http") {
		return "generic.http"
	}
	return ""
}

func confidenceFromScore(score int) Confidence {
	switch {
	case score >= 10:
		return ConfidenceHigh
	case score >= 6:
		return ConfidenceMedium
	default:
		return ConfidenceLow
	}
}

func buildSearchQuery(tokens map[string]struct{}, text string) string {
	for _, preferred := range []string{
		"gmail",
		"calendar",
		"http",
		"webhook",
		"endpoint",
		"email",
		"message",
		"messages",
		"draft",
	} {
		if _, ok := tokens[preferred]; ok {
			return preferred
		}
	}

	best := ""
	for token := range tokens {
		if len(token) < 4 {
			continue
		}
		if len(token) > len(best) {
			best = token
		}
	}
	if best != "" {
		return best
	}
	return text
}

func normalizeFreeText(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	return strings.Join(strings.Fields(lower), " ")
}

func tokenSet(text string) map[string]struct{} {
	set := map[string]struct{}{}
	for _, token := range splitWords(text) {
		if token == "" {
			continue
		}
		set[token] = struct{}{}
	}
	return set
}

func splitWords(text string) []string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= '0' && r <= '9':
			return r
		default:
			return ' '
		}
	}, strings.ToLower(text))
	parts := strings.Fields(clean)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		out = append(out, part)
	}
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, raw := range values {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	return out
}

func firstMatch(text string, patterns ...string) string {
	for _, raw := range patterns {
		re := regexp.MustCompile(raw)
		match := re.FindStringSubmatch(text)
		if len(match) > 1 {
			candidate := strings.TrimSpace(match[1])
			if candidate != "" {
				return candidate
			}
			continue
		}
		if len(match) == 1 {
			candidate := strings.TrimSpace(match[0])
			if candidate != "" {
				return candidate
			}
		}
	}
	return ""
}

func normalizeAliasKey(input string) string {
	lower := strings.ToLower(strings.TrimSpace(input))
	if lower == "" {
		return ""
	}
	clean := aliasCleanupRegex.ReplaceAllString(lower, " ")
	return strings.Join(strings.Fields(clean), " ")
}
