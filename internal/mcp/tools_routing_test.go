package mcp

import (
	"context"
	"io"
	"strings"
	"testing"

	"accords-mcp/internal/routing"
)

func TestRouteResolveValidation(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	resp, err := s.handleRouteResolve(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected validation failure, got success: %v", resp)
	}
	errObj, _ := resp["error"].(map[string]any)
	if code := strVal(errObj["code"]); code != "MCP_VALIDATION_ERROR" {
		t.Fatalf("unexpected code: %s", code)
	}
}

func TestRouteResolveGmailSendExecutable(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	resp, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": "send an email to skl83@cornell.edu with subject 'hello' and body 'test body'",
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success, got: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if got := strVal(data["status"]); got != "RESOLVED_EXECUTABLE" {
		t.Fatalf("unexpected status: %s data=%v", got, data)
	}
	route, _ := data["route"].(routing.RouteRef)
	if route.EntryID != "gmail_tpl_plan_send_email_v1" {
		t.Fatalf("unexpected entry_id: %s route=%+v", route.EntryID, route)
	}
	execSpec, _ := data["execution"].(routing.ExecutionSpec)
	if execSpec.Strategy != routing.StrategyTemplate {
		t.Fatalf("unexpected strategy: %s exec=%+v", execSpec.Strategy, execSpec)
	}
	autofilled, _ := data["autofilled_inputs"].([]routing.AutofilledInput)
	if len(autofilled) != 0 {
		t.Fatalf("expected no autofilled inputs for explicit prompt, got %+v", autofilled)
	}
}

func TestRouteResolveMissingInputs(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	resp, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": "send email to skl83@cornell.edu",
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if got := strVal(data["status"]); got != "RESOLVED_MISSING_INPUTS" {
		t.Fatalf("unexpected status: %s data=%v", got, data)
	}
	missing, _ := data["missing_inputs"].([]string)
	if len(missing) == 0 {
		t.Fatalf("expected non-empty missing_inputs: %v", data)
	}
	guidance, _ := data["missing_input_guidance"].([]routing.MissingInputGuidance)
	if len(guidance) == 0 {
		t.Fatalf("expected missing_input_guidance for unresolved route: %v", data)
	}
	if needs, _ := data["needs_clarification"].(bool); !needs {
		t.Fatalf("expected needs_clarification=true for unresolved user input: %v", data)
	}
	progressHint, _ := data["progress_hint"].(*routing.ProgressHint)
	if progressHint == nil || progressHint.Mode != routing.ProgressHintModeAskUser {
		t.Fatalf("expected ASK_USER progress_hint for unresolved user input: %v", data["progress_hint"])
	}
}

func TestRouteResolveSearchFallback(t *testing.T) {
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)
	bundle := map[string]any{
		"type":        "accords.cookbook.bundle.v1",
		"cookbook_id": "google.workspace",
		"version":     "2.0.0",
		"title":       "Calendar",
		"entries": []any{
			map[string]any{
				"entry_id":     "calendar_recipe_events_list_v1",
				"entry_type":   "recipe.verb.v1",
				"title":        "List Calendar Events",
				"connector_id": "google",
				"verb":         "google.calendar.events.list",
				"tags":         []any{"calendar", "events", "list"},
				"request":      map[string]any{"calendar_id": "primary"},
			},
		},
	}
	upsertResp, upsertErr := s.handleCookbookUpsert(context.Background(), map[string]any{"bundle": bundle})
	if upsertErr != nil {
		t.Fatalf("handleCookbookUpsert err: %v", upsertErr)
	}
	if ok, _ := upsertResp["ok"].(bool); !ok {
		t.Fatalf("expected upsert success, got: %v", upsertResp)
	}

	resp, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": "list my calendar events",
		"options": map[string]any{
			"allow_search_fallback": true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if got := strVal(data["status"]); got != "RESOLVED_EXECUTABLE" {
		t.Fatalf("unexpected status: %s data=%v", got, data)
	}
	route, _ := data["route"].(routing.RouteRef)
	if route.Source != "search" {
		t.Fatalf("expected search source, got %s route=%+v", route.Source, route)
	}
}

func TestRouteResolveWeatherGuidanceAndFactsRetry(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	baseArgs := map[string]any{
		"request_text": "send email to skl83@cornell.edu about weather in boston tomorrow",
	}

	initial, err := s.handleRouteResolve(context.Background(), baseArgs)
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := initial["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", initial)
	}
	initialData, _ := initial["data"].(map[string]any)
	if got := strVal(initialData["status"]); got != "RESOLVED_MISSING_INPUTS" {
		t.Fatalf("unexpected status before facts: %s data=%v", got, initialData)
	}
	if needs, _ := initialData["needs_clarification"].(bool); needs {
		t.Fatalf("expected needs_clarification=false for external fact guidance path: %v", initialData)
	}
	guidance, _ := initialData["missing_input_guidance"].([]routing.MissingInputGuidance)
	foundFactGuidance := false
	for _, item := range guidance {
		if item.InputKey == "text_plain" && item.ResolutionMode == routing.ResolutionModeAutoRetryWithFacts {
			foundFactGuidance = true
		}
	}
	if !foundFactGuidance {
		t.Fatalf("expected AUTO_RETRY_WITH_FACTS guidance, got %+v", guidance)
	}
	progressHint, _ := initialData["progress_hint"].(*routing.ProgressHint)
	if progressHint == nil || progressHint.Mode != routing.ProgressHintModeAutoEnrichAndRetry {
		t.Fatalf("expected AUTO_ENRICH_AND_RETRY progress_hint for weather flow: %v", initialData["progress_hint"])
	}

	retryArgs := map[string]any{
		"request_text": "send email to skl83@cornell.edu about weather in boston tomorrow",
		"context": map[string]any{
			"facts": map[string]any{
				"weather_summary": "Tomorrow in Boston: 58F with morning showers.",
			},
		},
	}
	retried, err := s.handleRouteResolve(context.Background(), retryArgs)
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := retried["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", retried)
	}
	retryData, _ := retried["data"].(map[string]any)
	if got := strVal(retryData["status"]); got != "RESOLVED_EXECUTABLE" {
		t.Fatalf("unexpected status after facts: %s data=%v", got, retryData)
	}
}

func TestRouteResolveWeatherGuidanceHandlesPossessiveTimeframe(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	request := "send an email to skl83@cornell.edu that has my passport and tomorrow's weather in santa rosa california"

	resp, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", resp)
	}
	data, _ := resp["data"].(map[string]any)
	if got := strVal(data["status"]); got != "RESOLVED_MISSING_INPUTS" {
		t.Fatalf("unexpected status: %s data=%v", got, data)
	}

	guidance, _ := data["missing_input_guidance"].([]routing.MissingInputGuidance)
	foundWeather := false
	for _, item := range guidance {
		if item.InputKey != "text_plain" || item.ResolutionMode != routing.ResolutionModeAutoRetryWithFacts {
			continue
		}
		if strVal(item.ExternalFactRequest["fact_key"]) != "weather_summary" {
			continue
		}
		if got := strings.ToLower(strVal(item.ExternalFactRequest["timeframe"])); got != "tomorrow" {
			t.Fatalf("expected timeframe=tomorrow for possessive weather phrasing, got %q guidance=%+v", got, item)
		}
		if got := strings.ToLower(strVal(item.ExternalFactRequest["location"])); got != "santa rosa california" {
			t.Fatalf("expected location extraction for possessive weather phrasing, got %q guidance=%+v", got, item)
		}
		foundWeather = true
	}
	if !foundWeather {
		t.Fatalf("expected weather external fact guidance, got %+v", guidance)
	}
}

func TestRouteResolveAttachmentSemanticContentParallelGuidanceAndRetry(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	request := "send an email to skl83@cornell.edu with my passport and send him a recipe on how to make teriyaki sauce as well"

	initial, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := initial["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", initial)
	}
	initialData, _ := initial["data"].(map[string]any)
	if got := strVal(initialData["status"]); got != "RESOLVED_MISSING_INPUTS" {
		t.Fatalf("unexpected status before compose facts: %s data=%v", got, initialData)
	}
	if needs, _ := initialData["needs_clarification"].(bool); needs {
		t.Fatalf("expected needs_clarification=false for external compose fact guidance: %v", initialData)
	}

	guidance, _ := initialData["missing_input_guidance"].([]routing.MissingInputGuidance)
	subjectGuidance := false
	bodyGuidance := false
	for _, item := range guidance {
		if item.ResolutionMode != routing.ResolutionModeAutoRetryWithFacts {
			continue
		}
		parallelizable, _ := item.ExternalFactRequest["parallelizable"].(bool)
		if !parallelizable {
			t.Fatalf("expected parallelizable=true in external_fact_request: %+v", item)
		}
		factKey := strVal(item.ExternalFactRequest["fact_key"])
		switch item.InputKey {
		case "subject":
			if factKey != "email_subject" {
				t.Fatalf("expected subject fact_key=email_subject, got %q guidance=%+v", factKey, item)
			}
			subjectGuidance = true
		case "text_plain":
			if factKey != "email_body" {
				t.Fatalf("expected text_plain fact_key=email_body, got %q guidance=%+v", factKey, item)
			}
			bodyGuidance = true
		}
	}
	if !subjectGuidance || !bodyGuidance {
		t.Fatalf("expected subject+text_plain AUTO_RETRY guidance, got %+v", guidance)
	}

	retried, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
		"context": map[string]any{
			"facts": map[string]any{
				"email_subject": "Passport + Teriyaki Sauce Recipe",
				"email_body":    "Attached is my passport.\n\nTeriyaki sauce recipe: combine soy sauce, mirin, sugar, garlic, and ginger; simmer briefly.",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := retried["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", retried)
	}
	retryData, _ := retried["data"].(map[string]any)
	if got := strVal(retryData["status"]); got != "RESOLVED_EXECUTABLE" {
		t.Fatalf("unexpected status after compose facts: %s data=%v", got, retryData)
	}
	inputs, _ := retryData["inputs"].(map[string]any)
	if got := strVal(inputs["subject"]); got != "Passport + Teriyaki Sauce Recipe" {
		t.Fatalf("expected subject from compose facts, got %q inputs=%v", got, inputs)
	}
	if body := strVal(inputs["text_plain"]); !strings.Contains(body, "Teriyaki sauce recipe") {
		t.Fatalf("expected text_plain from compose facts, got %q", body)
	}
}

func TestRouteResolveGenericHTTPGuidanceAndFactsRetry(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	request := "send this webhook request to the partner endpoint"

	initial, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := initial["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", initial)
	}
	initialData, _ := initial["data"].(map[string]any)
	if got := strVal(initialData["status"]); got != "RESOLVED_MISSING_INPUTS" {
		t.Fatalf("unexpected status before generic.http facts: %s data=%v", got, initialData)
	}
	if needs, _ := initialData["needs_clarification"].(bool); needs {
		t.Fatalf("expected needs_clarification=false for generic.http external facts: %v", initialData)
	}
	guidance, _ := initialData["missing_input_guidance"].([]routing.MissingInputGuidance)
	foundURL := false
	for _, item := range guidance {
		if item.InputKey != "url" || item.ResolutionMode != routing.ResolutionModeAutoRetryWithFacts {
			continue
		}
		if key := strVal(item.ExternalFactRequest["fact_key"]); key != "url" {
			t.Fatalf("expected fact_key=url for generic.http url guidance, got %q guidance=%+v", key, item)
		}
		parallelizable, _ := item.ExternalFactRequest["parallelizable"].(bool)
		if !parallelizable {
			t.Fatalf("expected generic.http guidance parallelizable=true, got %+v", item)
		}
		foundURL = true
	}
	if !foundURL {
		t.Fatalf("expected generic.http url AUTO_RETRY guidance, got %+v", guidance)
	}
	progressHint, _ := initialData["progress_hint"].(*routing.ProgressHint)
	if progressHint == nil || progressHint.Mode != routing.ProgressHintModeAutoEnrichAndRetry {
		t.Fatalf("expected AUTO_ENRICH_AND_RETRY progress_hint for generic.http flow: %v", initialData["progress_hint"])
	}

	retried, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
		"context": map[string]any{
			"facts": map[string]any{
				"url": "https://partner.example.com/webhook",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := retried["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", retried)
	}
	retryData, _ := retried["data"].(map[string]any)
	if got := strVal(retryData["status"]); got != "RESOLVED_EXECUTABLE" {
		t.Fatalf("unexpected status after generic.http facts: %s data=%v", got, retryData)
	}
}

func TestRouteResolveSearchTemplateRequiredInputsGuidanceAndRetry(t *testing.T) {
	t.Setenv("ACCORDS_MCP_CATALOG_DIR", t.TempDir())
	s := NewServer(strings.NewReader(""), io.Discard)
	bundle := map[string]any{
		"type":        "accords.cookbook.bundle.v1",
		"cookbook_id": "partner.ops",
		"version":     "1.0.0",
		"title":       "Partner Ops",
		"entries": []any{
			map[string]any{
				"entry_id":     "partner_tpl_sync_request_v1",
				"entry_type":   "template.verb.v1",
				"title":        "Partner Sync Request",
				"connector_id": "generic.http",
				"verb":         "generic.http.request.v1",
				"tags":         []any{"partner", "sync"},
				"base_request": map[string]any{
					"connector_id": "generic.http",
					"verb":         "generic.http.request.v1",
					"request": map[string]any{
						"method":  "POST",
						"url":     "",
						"headers": map[string]any{},
					},
				},
				"bindings": []any{
					map[string]any{"target_path": "/request/url", "input_key": "url", "required": true},
					map[string]any{"target_path": "/request/headers/X-API-Key", "input_key": "api_key", "required": true},
				},
			},
		},
	}
	upsertResp, upsertErr := s.handleCookbookUpsert(context.Background(), map[string]any{"bundle": bundle})
	if upsertErr != nil {
		t.Fatalf("handleCookbookUpsert err: %v", upsertErr)
	}
	if ok, _ := upsertResp["ok"].(bool); !ok {
		t.Fatalf("expected upsert success, got: %v", upsertResp)
	}

	request := "run the partner sync now"
	initial, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
		"options": map[string]any{
			"allow_search_fallback": true,
		},
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := initial["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", initial)
	}
	initialData, _ := initial["data"].(map[string]any)
	if got := strVal(initialData["status"]); got != "RESOLVED_MISSING_INPUTS" {
		t.Fatalf("unexpected status before search-template facts: %s data=%v", got, initialData)
	}
	route, _ := initialData["route"].(routing.RouteRef)
	if route.Source != "search" {
		t.Fatalf("expected search source, got %s route=%+v", route.Source, route)
	}
	guidance, _ := initialData["missing_input_guidance"].([]routing.MissingInputGuidance)
	hasURL := false
	hasAPIKey := false
	for _, item := range guidance {
		if item.ResolutionMode != routing.ResolutionModeAutoRetryWithFacts {
			continue
		}
		switch item.InputKey {
		case "url":
			hasURL = true
		case "api_key":
			hasAPIKey = true
		}
		parallelizable, _ := item.ExternalFactRequest["parallelizable"].(bool)
		if !parallelizable {
			t.Fatalf("expected parallelizable=true for search-template missing input guidance: %+v", item)
		}
	}
	if !hasURL || !hasAPIKey {
		t.Fatalf("expected AUTO_RETRY guidance for url+api_key, got %+v", guidance)
	}
	if needs, _ := initialData["needs_clarification"].(bool); needs {
		t.Fatalf("expected needs_clarification=false for generic.http search-template external facts: %v", initialData)
	}

	retried, err := s.handleRouteResolve(context.Background(), map[string]any{
		"request_text": request,
		"options": map[string]any{
			"allow_search_fallback": true,
		},
		"context": map[string]any{
			"facts": map[string]any{
				"url":     "https://api.partner.example.com/sync",
				"api_key": "partner_sync_token",
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected handler err: %v", err)
	}
	if ok, _ := retried["ok"].(bool); !ok {
		t.Fatalf("expected success envelope, got: %v", retried)
	}
	retryData, _ := retried["data"].(map[string]any)
	if got := strVal(retryData["status"]); got != "RESOLVED_EXECUTABLE" {
		t.Fatalf("unexpected status after search-template facts: %s data=%v", got, retryData)
	}
}
