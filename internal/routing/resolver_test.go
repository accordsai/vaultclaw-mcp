package routing

import (
	"context"
	"strings"
	"testing"
)

func TestResolveSendEmailExecutable(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send an email to skl83@cornell.edu with subject 'hello' and body 'testing from vault'",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)

	if result.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status, got %s (%v)", result.Status, result)
	}
	if result.Route.EntryID != "gmail_tpl_plan_send_email_v1" {
		t.Fatalf("unexpected route entry id: %s", result.Route.EntryID)
	}
	if result.Execution.Strategy != StrategyTemplate {
		t.Fatalf("unexpected strategy: %s", result.Execution.Strategy)
	}
	if result.Inputs["subject"] != "hello" {
		t.Fatalf("expected extracted subject, got: %v", result.Inputs["subject"])
	}
	if len(result.AutofilledInputs) != 0 {
		t.Fatalf("expected no autofilled inputs for explicit payload, got: %+v", result.AutofilledInputs)
	}
	if result.ProgressHint != nil {
		t.Fatalf("expected nil progress hint for executable route, got %+v", result.ProgressHint)
	}
	to, _ := result.Inputs["to"].([]string)
	if len(to) != 1 || to[0] != "skl83@cornell.edu" {
		t.Fatalf("unexpected to extraction: %v", result.Inputs["to"])
	}
}

func TestResolveSendEmailExtractsSubjectAndMessageBodyVariants(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send an email to skl83@cornell.edu with the subject hi and message body hello",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)

	if result.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status, got %s (%v)", result.Status, result)
	}
	if got := asString(result.Inputs["subject"]); got != "hi" {
		t.Fatalf("expected extracted subject 'hi', got %q", got)
	}
	if got := asString(result.Inputs["text_plain"]); got != "hello" {
		t.Fatalf("expected extracted text_plain 'hello', got %q", got)
	}
}

func TestResolveSendEmailAutofillsFromNL(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send email to skl83@cornell.edu about project kickoff notes",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)

	if result.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status, got %s (%v)", result.Status, result)
	}
	if result.Inputs["subject"] == nil || result.Inputs["text_plain"] == nil {
		t.Fatalf("expected subject/text_plain autofill, got inputs=%v", result.Inputs)
	}
	if len(result.AutofilledInputs) < 2 {
		t.Fatalf("expected at least two autofilled inputs, got %+v", result.AutofilledInputs)
	}
}

func TestResolveSendEmailAttachmentDefaults(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send email with my passport to skl83@cornell.edu",
		Options:     ResolveOptions{AllowSearchFallback: false},
	}, nil)

	if result.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status, got %s (%v)", result.Status, result)
	}
	if got := strings.TrimSpace(string(result.Execution.Strategy)); got != string(StrategyTemplate) {
		t.Fatalf("expected TEMPLATE strategy for attachment-only flow, got %q", got)
	}
	if got := strings.TrimSpace(result.Execution.Tool); got != "vaultclaw_template_render" {
		t.Fatalf("expected template render tool for attachment-only flow, got %q", got)
	}
	if got := result.Inputs["subject"]; got == nil || got == "" {
		t.Fatalf("expected default subject for attachment flow, got %v", got)
	}
	if got := result.Inputs["text_plain"]; got == nil || got == "" {
		t.Fatalf("expected default text_plain for attachment flow, got %v", got)
	}
	if len(result.AutofilledInputs) < 2 {
		t.Fatalf("expected attachment defaults to be tracked as autofills, got %+v", result.AutofilledInputs)
	}
}

func TestResolvePassportFieldsInBodyRoutesWorkflowTool(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send an email to skl83@cornell.edu with a copy of my passport and the passport fields in the body",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)

	if result.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status, got %s (%v)", result.Status, result)
	}
	if got := strings.TrimSpace(string(result.Execution.Strategy)); got != string(StrategyToolInvoke) {
		t.Fatalf("expected TOOL_INVOKE strategy, got %q", got)
	}
	if got := strings.TrimSpace(result.Execution.Tool); got != "vaultclaw_passport_email_workflow" {
		t.Fatalf("expected passport workflow tool, got %q", got)
	}
	if got := strings.TrimSpace(asString(result.Inputs["recipient_email"])); got != "skl83@cornell.edu" {
		t.Fatalf("expected recipient_email from prompt, got %q", got)
	}
	if got := strings.TrimSpace(asString(result.Inputs["request_text"])); got == "" {
		t.Fatalf("expected request_text to be forwarded for workflow context")
	}
}

func TestResolvePassportFieldsInBodyMissingRecipient(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send an email with a copy of my passport and include passport fields in the body",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)

	if result.Status != StatusResolvedMissing {
		t.Fatalf("expected missing-input status, got %s (%v)", result.Status, result)
	}
	if len(result.MissingInputs) != 1 || result.MissingInputs[0] != "to" {
		t.Fatalf("expected missing input 'to', got %v", result.MissingInputs)
	}
	if got := strings.TrimSpace(string(result.Execution.Strategy)); got != string(StrategyToolInvoke) {
		t.Fatalf("expected TOOL_INVOKE strategy for missing recipient workflow intent, got %q", got)
	}
	if got := strings.TrimSpace(result.Execution.Tool); got != "vaultclaw_passport_email_workflow" {
		t.Fatalf("expected passport workflow tool, got %q", got)
	}
}

func TestResolveSendEmailMissingInputs(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send email to skl83@cornell.edu",
		Options:     ResolveOptions{AllowSearchFallback: false},
	}, nil)

	if result.Status != StatusResolvedMissing {
		t.Fatalf("expected missing-input status, got %s (%v)", result.Status, result)
	}
	if len(result.MissingInputs) == 0 {
		t.Fatalf("expected missing inputs, got none")
	}
	if len(result.MissingInputGuidance) == 0 {
		t.Fatalf("expected missing input guidance, got none: %+v", result)
	}
	if result.NeedsClarification {
		t.Fatalf("expected needs_clarification=false when missing inputs are policy-auto-enriched")
	}
	if result.ProgressHint == nil || result.ProgressHint.Mode != ProgressHintModeAutoEnrichAndRetry {
		t.Fatalf("expected AUTO_ENRICH_AND_RETRY progress hint for unresolved route, got %+v", result.ProgressHint)
	}
}

func TestResolveMissingRecipientDoesNotUseSearchFallback(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	searchCalled := false
	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send an email about project kickoff notes",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, func(_ context.Context, _ SearchFilter) ([]SearchCandidate, error) {
		searchCalled = true
		return []SearchCandidate{
			{CookbookID: "other", Version: "1.0.0", EntryID: "irrelevant", EntryType: "recipe.verb.v1"},
		}, nil
	})

	if searchCalled {
		t.Fatalf("search fallback should not run when a deterministic route was matched")
	}
	if result.Status != StatusResolvedMissing {
		t.Fatalf("expected missing status, got %s (%v)", result.Status, result)
	}
}

func TestResolveNotVaultEligible(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "tell me a joke about lobsters",
		Options:     ResolveOptions{AllowSearchFallback: false},
	}, nil)
	if result.Status != StatusNotVaultEligible {
		t.Fatalf("expected not-vault-eligible, got %s", result.Status)
	}
}

func TestResolveControlledSearchFallback(t *testing.T) {
	resolver := NewResolver(Registry{
		Version:             "1.0.0",
		DocumentTypeAliases: map[string]string{},
		Routes: []RegistryRoute{
			{
				RouteID:        "gmail.only",
				Domain:         "google.gmail",
				CookbookID:     "google.workspace",
				Version:        "1.3.0",
				EntryID:        "gmail_recipe_labels_list_v1",
				EntryType:      "recipe.verb.v1",
				Strategy:       StrategyRecipe,
				Keywords:       []string{"gmail", "labels"},
				RequiredInputs: []string{},
			},
		},
	})

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "schedule a meeting in calendar",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, func(_ context.Context, _ SearchFilter) ([]SearchCandidate, error) {
		return []SearchCandidate{
			{
				CookbookID:  "google.workspace",
				Version:     "1.3.0",
				EntryID:     "calendar_recipe_events_list_v1",
				EntryType:   "recipe.verb.v1",
				Title:       "List Calendar Events",
				ConnectorID: "google",
				Verb:        "google.calendar.events.list",
				Tags:        []string{"calendar", "events", "list"},
			},
		}, nil
	})

	if result.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable fallback status, got %s (%v)", result.Status, result)
	}
	if result.Route.Source != "search" {
		t.Fatalf("expected search source, got %s", result.Route.Source)
	}
}

func TestResolveAmbiguousRegistry(t *testing.T) {
	resolver := NewResolver(Registry{
		Version:             "1.0.0",
		DocumentTypeAliases: map[string]string{},
		Routes: []RegistryRoute{
			{
				RouteID:    "route.one",
				Domain:     "google.gmail",
				EntryID:    "one",
				EntryType:  "recipe.verb.v1",
				CookbookID: "google.workspace",
				Version:    "1.3.0",
				Strategy:   StrategyRecipe,
				Keywords:   []string{"gmail", "messages", "list"},
			},
			{
				RouteID:    "route.two",
				Domain:     "google.gmail",
				EntryID:    "two",
				EntryType:  "recipe.verb.v1",
				CookbookID: "google.workspace",
				Version:    "1.3.0",
				Strategy:   StrategyRecipe,
				Keywords:   []string{"gmail", "messages", "list"},
			},
		},
	})

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "list gmail messages",
		Options:     ResolveOptions{AllowSearchFallback: false},
	}, nil)

	if result.Status != StatusAmbiguous {
		t.Fatalf("expected ambiguous status, got %s (%v)", result.Status, result)
	}
	if !result.NeedsClarification {
		t.Fatalf("expected needs clarification for ambiguous route")
	}
}

func TestResolveWeatherGuidanceAndFactsRetry(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	initial := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send email to skl83@cornell.edu about weather in boston tomorrow",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)
	if initial.Status != StatusResolvedMissing {
		t.Fatalf("expected missing status before facts, got %s (%v)", initial.Status, initial)
	}
	if initial.NeedsClarification {
		t.Fatalf("expected needs_clarification=false for external fact recovery path")
	}
	foundFactGuidance := false
	for _, g := range initial.MissingInputGuidance {
		if g.InputKey == "text_plain" && g.ResolutionMode == ResolutionModeAutoRetryWithFacts {
			if kind := asString(g.ExternalFactRequest["fact_kind"]); kind != "weather_forecast" {
				t.Fatalf("expected fact_kind=weather_forecast, got %q", kind)
			}
			foundFactGuidance = true
		}
	}
	if !foundFactGuidance {
		t.Fatalf("expected AUTO_RETRY_WITH_FACTS guidance for weather flow, got %+v", initial.MissingInputGuidance)
	}
	if initial.ProgressHint == nil || initial.ProgressHint.Mode != ProgressHintModeAutoEnrichAndRetry {
		t.Fatalf("expected AUTO_ENRICH_AND_RETRY progress hint for weather flow, got %+v", initial.ProgressHint)
	}

	retried := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send email to skl83@cornell.edu about weather in boston tomorrow",
		Options:     ResolveOptions{AllowSearchFallback: true},
		Facts: map[string]any{
			"weather_summary": "Tomorrow in Boston: High 58F, low 43F, light rain in the morning.",
		},
	}, nil)
	if retried.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status after facts, got %s (%v)", retried.Status, retried)
	}
	if got := retried.Inputs["text_plain"]; got == nil || got == "" {
		t.Fatalf("expected text_plain from weather facts, got %v", got)
	}
}

func TestResolveWeatherGuidanceHandlesPossessiveTimeframe(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	request := "send an email to skl83@cornell.edu that has my passport and tomorrow's weather in santa rosa california"
	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: request,
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)
	if result.Status != StatusResolvedMissing {
		t.Fatalf("expected missing status before weather facts, got %s (%v)", result.Status, result)
	}
	if result.ProgressHint == nil || result.ProgressHint.Mode != ProgressHintModeAutoEnrichAndRetry {
		t.Fatalf("expected AUTO_ENRICH_AND_RETRY progress hint, got %+v", result.ProgressHint)
	}

	foundWeather := false
	for _, item := range result.MissingInputGuidance {
		if item.InputKey != "text_plain" || item.ResolutionMode != ResolutionModeAutoRetryWithFacts {
			continue
		}
		if asString(item.ExternalFactRequest["fact_key"]) != "weather_summary" {
			continue
		}
		if got := strings.ToLower(asString(item.ExternalFactRequest["timeframe"])); got != "tomorrow" {
			t.Fatalf("expected timeframe=tomorrow for possessive weather phrasing, got %q guidance=%+v", got, item)
		}
		if got := strings.ToLower(asString(item.ExternalFactRequest["location"])); got != "santa rosa california" {
			t.Fatalf("expected location extraction for possessive weather phrasing, got %q guidance=%+v", got, item)
		}
		foundWeather = true
	}
	if !foundWeather {
		t.Fatalf("expected weather AUTO_RETRY_WITH_FACTS guidance, got %+v", result.MissingInputGuidance)
	}
}

func TestResolveAttachmentWithSemanticContentGuidanceAndParallelRetry(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	request := "send an email to skl83@cornell.edu with my passport and send him a recipe on how to make teriyaki sauce as well"
	initial := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: request,
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)
	if initial.Status != StatusResolvedMissing {
		t.Fatalf("expected missing status before external compose facts, got %s (%v)", initial.Status, initial)
	}
	if initial.NeedsClarification {
		t.Fatalf("expected needs_clarification=false for parallel external fact path")
	}

	hasSubject := false
	hasBody := false
	for _, g := range initial.MissingInputGuidance {
		if g.ResolutionMode != ResolutionModeAutoRetryWithFacts {
			continue
		}
		factKey := strings.TrimSpace(asString(g.ExternalFactRequest["fact_key"]))
		parallelizable, _ := g.ExternalFactRequest["parallelizable"].(bool)
		if !parallelizable {
			t.Fatalf("expected guidance to mark external fact request parallelizable: %+v", g)
		}
		switch g.InputKey {
		case "subject":
			if factKey != "email_subject" {
				t.Fatalf("expected subject fact_key=email_subject, got %q guidance=%+v", factKey, g)
			}
			hasSubject = true
		case "text_plain":
			if factKey != "email_body" {
				t.Fatalf("expected text_plain fact_key=email_body, got %q guidance=%+v", factKey, g)
			}
			hasBody = true
		}
	}
	if !hasSubject || !hasBody {
		t.Fatalf("expected parallel AUTO_RETRY_WITH_FACTS guidance for subject + text_plain, got %+v", initial.MissingInputGuidance)
	}

	retried := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: request,
		Options:     ResolveOptions{AllowSearchFallback: true},
		Facts: map[string]any{
			"email_subject": "Passport + Teriyaki Sauce Recipe",
			"email_body":    "Attached is my passport.\n\nTeriyaki sauce: whisk soy sauce, mirin, brown sugar, garlic, and ginger; simmer 3-5 minutes.",
		},
	}, nil)
	if retried.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status after external compose facts, got %s (%v)", retried.Status, retried)
	}
	if got := asString(retried.Inputs["subject"]); got != "Passport + Teriyaki Sauce Recipe" {
		t.Fatalf("expected subject from external facts, got %q", got)
	}
	if got := asString(retried.Inputs["text_plain"]); !strings.Contains(got, "Teriyaki sauce") {
		t.Fatalf("expected text_plain from external facts, got %q", got)
	}
	if !hasInputValue(retried.Inputs["document_attachments"]) {
		t.Fatalf("expected attachment to remain present after retry, got %v", retried.Inputs["document_attachments"])
	}
}

func TestResolveGenericHTTPGuidanceAndFactsRetry(t *testing.T) {
	resolver, err := NewDefaultResolver()
	if err != nil {
		t.Fatalf("NewDefaultResolver err: %v", err)
	}

	initial := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send this webhook request to the partner endpoint",
		Options:     ResolveOptions{AllowSearchFallback: true},
	}, nil)
	if initial.Status != StatusResolvedMissing {
		t.Fatalf("expected missing status before url facts, got %s (%v)", initial.Status, initial)
	}
	if initial.NeedsClarification {
		t.Fatalf("expected needs_clarification=false for generic.http external fact recovery")
	}
	foundURLGuidance := false
	for _, g := range initial.MissingInputGuidance {
		if g.InputKey != "url" || g.ResolutionMode != ResolutionModeAutoRetryWithFacts {
			continue
		}
		if key := asString(g.ExternalFactRequest["fact_key"]); key != "url" {
			t.Fatalf("expected fact_key=url for generic.http url guidance, got %q", key)
		}
		if kind := asString(g.ExternalFactRequest["fact_kind"]); kind != "connector_input_generation" {
			t.Fatalf("expected fact_kind=connector_input_generation for generic.http url guidance, got %q", kind)
		}
		parallelizable, _ := g.ExternalFactRequest["parallelizable"].(bool)
		if !parallelizable {
			t.Fatalf("expected generic.http fact guidance to be parallelizable: %+v", g)
		}
		foundURLGuidance = true
	}
	if !foundURLGuidance {
		t.Fatalf("expected AUTO_RETRY_WITH_FACTS guidance for generic.http url, got %+v", initial.MissingInputGuidance)
	}

	retried := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "send this webhook request to the partner endpoint",
		Options:     ResolveOptions{AllowSearchFallback: true},
		Facts: map[string]any{
			"url":    "https://partner.example.com/webhook",
			"method": "POST",
		},
	}, nil)
	if retried.Status != StatusResolvedExecutable {
		t.Fatalf("expected executable status after url facts, got %s (%v)", retried.Status, retried)
	}
	if got := asString(retried.Inputs["url"]); got != "https://partner.example.com/webhook" {
		t.Fatalf("expected url from facts, got %q", got)
	}
}

func TestResolveSensitivePolicyAlwaysAsksUser(t *testing.T) {
	resolver := NewResolver(Registry{
		Version:             "1.1.0",
		DocumentTypeAliases: map[string]string{},
		Routes: []RegistryRoute{
			{
				RouteID:        "sensitive.token.route",
				Domain:         "test.domain",
				CookbookID:     "test.cookbook",
				Version:        "1.0.0",
				EntryID:        "entry",
				EntryType:      "recipe.verb.v1",
				Strategy:       StrategyRecipe,
				Keywords:       []string{"token"},
				RequiredInputs: []string{"api_token"},
				EnrichmentPolicies: []RegistryEnrichmentPolicy{
					{
						InputKey:       "api_token",
						ResolutionMode: ResolutionModeAutoRetryWithFacts,
						FactKey:        "api_token",
						FactKind:       "text_field_generation",
						Sensitive:      true,
						Parallelizable: true,
					},
				},
			},
		},
	})

	result := resolver.Resolve(context.Background(), ResolveRequest{
		RequestText: "use token flow",
		Options:     ResolveOptions{AllowSearchFallback: false},
	}, nil)

	if result.Status != StatusResolvedMissing {
		t.Fatalf("expected missing status, got %s (%v)", result.Status, result)
	}
	if len(result.MissingInputGuidance) != 1 {
		t.Fatalf("expected one guidance item, got %+v", result.MissingInputGuidance)
	}
	if result.MissingInputGuidance[0].ResolutionMode != ResolutionModeAskUser {
		t.Fatalf("expected sensitive field to be ASK_USER, got %+v", result.MissingInputGuidance[0])
	}
}
