package mcp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"accords-mcp/internal/vault"
)

const passportEmailDefaultSubject = "Passport Copy and Details"

var (
	passportEmailRecipientPattern = regexp.MustCompile(`(?i)\b[A-Z0-9._%+\-]+@[A-Z0-9.\-]+\.[A-Z]{2,}\b`)
	passportEmailSubjectPatterns  = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bsubject\s*(?:is|=|:)\s*['"]([^'"\n]+)['"]`),
		regexp.MustCompile(`(?i)\bsubject\s+['"]([^'"\n]+)['"]`),
		regexp.MustCompile(`(?i)\bsubject\s+['"]?([^'"\n,.;:!?]+(?:\s+[^'"\n,.;:!?]+){0,10})`),
	}
	passportEmailRequiredSlots = []string{
		"given_name",
		"family_name",
		"passport_number",
		"passport_expiry_date",
		"passport_issuing_country",
	}
	passportEmailFieldLabels = map[string]string{
		"given_name":               "First Name",
		"family_name":              "Last Name",
		"passport_number":          "Passport Number",
		"passport_expiry_date":     "Expiry Date",
		"passport_issuing_country": "Issuing Country",
	}
)

type passportEmailIntent struct {
	RequestText    string
	RecipientEmail string
	Subject        string
	BodyStyle      string
}

func (s *Server) handlePassportEmailWorkflow(ctx context.Context, args map[string]any) (map[string]any, error) {
	intent := parsePassportEmailIntent(args)
	if !passportEmailRecipientPattern.MatchString(intent.RecipientEmail) {
		return envelopeFailure(
			"MCP_RECIPIENT_REQUIRED",
			"validation",
			"Recipient email is required. Provide recipient_email or include one in request_text.",
			false,
			"",
			map[string]any{
				"next_action": "ASK_USER_FOR_RECIPIENT_EMAIL",
			},
		), nil
	}

	c, _, fail, ok := s.configuredClient(ctx)
	if !ok {
		return fail, nil
	}

	documentID, docFailure := s.resolvePassportDocumentID(ctx, c, args, intent)
	if docFailure != nil {
		return docFailure, nil
	}

	manualFields := normalizePassportManualFields(mapArg(args, "manual_fields"))
	bodyFromExisting := composePassportEmailBodyTemplate(intent.BodyStyle, "read_existing_profile")
	bodyAfterExtraction := composePassportEmailBodyTemplate(intent.BodyStyle, "read_after_extract_profile")
	plan, planInput := buildPassportEmailPlan(
		documentID,
		intent.RecipientEmail,
		intent.Subject,
		bodyFromExisting,
		bodyAfterExtraction,
		manualFields,
	)
	summary := buildPassportEmailSummary(intent.RecipientEmail, intent.Subject, manualFields)

	output := map[string]any{
		"status":            "READY_TO_SEND",
		"pre_send_summary":  summary,
		"resolved_fields":   manualFields,
		"composed_text":     bodyFromExisting,
		"composed_text_alt": bodyAfterExtraction,
		"plan":              plan,
		"plan_input":        planInput,
		"document_type_id":  "identity.passport",
		"document_subject":  "self",
		"required_slot_set": append([]string(nil), passportEmailRequiredSlots...),
	}

	if !boolArg(args, "execute", true) {
		return envelopeSuccess(output, nil), nil
	}

	execResp, err := s.handlePlanExecute(ctx, map[string]any{
		"plan":       plan,
		"plan_input": planInput,
		"orchestration": map[string]any{
			"unbounded_profiles": false,
		},
	})
	if err != nil {
		return envelopeFailure(
			"MCP_GMAIL_DRAFT_SEND_FAILED",
			"validation",
			"I couldn't create and send the Gmail draft. Please retry.",
			false,
			"",
			map[string]any{
				"pre_send_summary": summary,
				"cause":            err.Error(),
			},
		), nil
	}
	if ok, _ := execResp["ok"].(bool); !ok {
		errObj, _ := execResp["error"].(map[string]any)
		if strings.EqualFold(strings.TrimSpace(strVal(errObj["code"])), "MCP_APPROVAL_REQUIRED") {
			details, _ := errObj["details"].(map[string]any)
			if details == nil {
				details = map[string]any{}
				errObj["details"] = details
			}
			details["pre_send_summary"] = summary
			return execResp, nil
		}
		return envelopeFailure(
			"MCP_GMAIL_DRAFT_SEND_FAILED",
			"validation",
			"I couldn't create and send the Gmail draft. Please retry.",
			false,
			"",
			map[string]any{
				"pre_send_summary": summary,
				"upstream_error":   errObj,
			},
		), nil
	}

	output["status"] = "SENT"
	output["execution"] = execResp["data"]
	meta, _ := execResp["meta"].(map[string]any)
	return envelopeSuccess(output, meta), nil
}

func parsePassportEmailIntent(args map[string]any) passportEmailIntent {
	requestText := strings.TrimSpace(strArg(args, "request_text"))
	if requestText == "" {
		requestText = strings.TrimSpace(strArg(args, "request"))
	}
	if requestText == "" {
		requestText = strings.TrimSpace(strArg(args, "text"))
	}

	recipient := strings.TrimSpace(strArg(args, "recipient_email"))
	if recipient == "" {
		if match := passportEmailRecipientPattern.FindString(requestText); strings.TrimSpace(match) != "" {
			recipient = strings.TrimSpace(match)
		}
	}

	subject := strings.TrimSpace(strArg(args, "subject"))
	if subject == "" {
		subject = extractPassportEmailSubject(requestText)
	}
	if subject == "" {
		subject = passportEmailDefaultSubject
	}

	bodyStyle := strings.TrimSpace(strings.ToLower(strArg(args, "body_style")))
	if bodyStyle == "" {
		bodyStyle = detectPassportBodyStyle(requestText)
	}

	return passportEmailIntent{
		RequestText:    requestText,
		RecipientEmail: recipient,
		Subject:        subject,
		BodyStyle:      bodyStyle,
	}
}

func extractPassportEmailSubject(requestText string) string {
	for _, pattern := range passportEmailSubjectPatterns {
		matches := pattern.FindStringSubmatch(requestText)
		if len(matches) < 2 {
			continue
		}
		candidate := strings.TrimSpace(matches[1])
		candidate = strings.Trim(candidate, " .,:;!?\"'")
		if candidate == "" {
			continue
		}
		return candidate
	}
	return ""
}

func detectPassportBodyStyle(requestText string) string {
	lower := strings.ToLower(strings.TrimSpace(requestText))
	switch {
	case strings.Contains(lower, "formal"):
		return "formal"
	case strings.Contains(lower, "casual"):
		return "casual"
	case strings.Contains(lower, "concise"), strings.Contains(lower, "brief"):
		return "concise"
	case strings.Contains(lower, "detailed"), strings.Contains(lower, "detail"):
		return "detailed"
	default:
		return ""
	}
}

func (s *Server) resolvePassportDocumentID(
	ctx context.Context,
	c *vault.Client,
	args map[string]any,
	intent passportEmailIntent,
) (string, map[string]any) {
	documentID := strings.TrimSpace(strArg(args, "passport_document_id"))
	if documentID != "" {
		return documentID, nil
	}

	res, err := c.Get(ctx, "/v0/docs/types/latest", map[string]string{
		"type_id":    "identity.passport",
		"subject_id": "self",
	})
	if err != nil {
		var apiErr *vault.APIError
		if errors.As(err, &apiErr) && strings.EqualFold(strings.TrimSpace(apiErr.Code), "DOCUMENT_SLOT_UNRESOLVED") {
			return "", envelopeFailure(
				"MCP_DOCUMENT_UPLOAD_REQUIRED",
				"validation",
				"I couldn't find a passport document on file. Upload your passport to continue.",
				false,
				strings.TrimSpace(apiErr.Code),
				map[string]any{
					"action": map[string]any{
						"type":  "UPLOAD_DOCUMENT",
						"title": "Upload Passport",
						"message": "Upload a document of type identity.passport for subject self, " +
							"then retry this workflow.",
						"document_requirement": map[string]any{
							"type_id":    "identity.passport",
							"subject_id": "self",
						},
						"resume_arguments": map[string]any{
							"recipient_email": intent.RecipientEmail,
							"subject":         intent.Subject,
							"body_style":      intent.BodyStyle,
						},
					},
				},
			)
		}
		return "", envelopeFailure(
			"MCP_DOCUMENT_RESOLUTION_FAILED",
			"validation",
			"I couldn't resolve your passport document. Please try again.",
			false,
			"",
			map[string]any{
				"upstream_error": err.Error(),
			},
		)
	}

	documentID = strings.TrimSpace(strVal(res.Body["document_id"]))
	if documentID == "" {
		item, _ := res.Body["item"].(map[string]any)
		documentID = strings.TrimSpace(strVal(item["document_id"]))
	}
	if documentID != "" {
		return documentID, nil
	}

	return "", envelopeFailure(
		"MCP_DOCUMENT_UPLOAD_REQUIRED",
		"validation",
		"I couldn't find a passport document on file. Upload your passport to continue.",
		false,
		"",
		map[string]any{
			"action": map[string]any{
				"type":  "UPLOAD_DOCUMENT",
				"title": "Upload Passport",
				"message": "Upload a document of type identity.passport for subject self, " +
					"then retry this workflow.",
				"document_requirement": map[string]any{
					"type_id":    "identity.passport",
					"subject_id": "self",
				},
				"resume_arguments": map[string]any{
					"recipient_email": intent.RecipientEmail,
					"subject":         intent.Subject,
					"body_style":      intent.BodyStyle,
				},
			},
		},
	)
}

func (s *Server) resolvePassportFields(
	ctx context.Context,
	c *vault.Client,
	documentID string,
	args map[string]any,
	intent passportEmailIntent,
) (map[string]string, map[string]any) {
	slots := make([]any, 0, len(passportEmailRequiredSlots))
	for _, slot := range passportEmailRequiredSlots {
		slots = append(slots, slot)
	}

	manual := normalizePassportManualFields(mapArg(args, "manual_fields"))

	extractedBefore, readBeforeErr := s.readPassportProfileFields(ctx, c, slots)
	if readBeforeErr == nil {
		preExtract := mergePassportFields(manual, extractedBefore, nil)
		if len(missingPassportSlots(preExtract)) == 0 {
			return preExtract, nil
		}
	}

	extractReq := map[string]any{
		"connector_id":   "identity",
		"verb":           "identity.extraction.run.v1",
		"policy_version": "1",
		"request": map[string]any{
			"profile_kind": "KYC",
			"provider":     "aws.textract.v1",
			"mode":         "ANALYZE_AND_SAVE",
			"document_id":  documentID,
			"slots":        slots,
		},
	}
	if _, err := c.Post(ctx, "/v0/connectors/execute", extractReq, true); err != nil {
		if approvalResp := s.passportExtractionApprovalResponse(ctx, extractReq, err); approvalResp != nil {
			return nil, approvalResp
		}
		if isPassportExtractionConflict(err) {
			// If extraction data is already persisted for this document/profile, the
			// extraction run may return CONFLICT. Continue with profile read.
		} else {
			return nil, envelopeFailure(
				"MCP_PASSPORT_EXTRACTION_FAILED",
				"validation",
				"I couldn't extract passport fields automatically. Please retry or provide fields manually.",
				false,
				"",
				map[string]any{
					"document_id": documentID,
					"upstream":    err.Error(),
				},
			)
		}
	}

	extractedAfter, readAfterErr := s.readPassportProfileFields(ctx, c, slots)
	if readAfterErr != nil {
		if readBeforeErr != nil {
			return nil, envelopeFailure(
				"MCP_PASSPORT_PROFILE_READ_FAILED",
				"validation",
				"I couldn't read the extracted passport fields. Please retry or provide fields manually.",
				false,
				"",
				map[string]any{
					"document_id": documentID,
					"upstream":    readAfterErr.Error(),
				},
			)
		}
		extractedAfter = extractedBefore
	}

	final := mergePassportFields(manual, extractedAfter, extractedBefore)
	missing := missingPassportSlots(final)
	if len(missing) == 0 {
		return final, nil
	}

	missingFields := make([]map[string]any, 0, len(missing))
	for _, slot := range missing {
		missingFields = append(missingFields, map[string]any{
			"key":   slot,
			"label": passportLabelForSlot(slot),
		})
	}
	sort.SliceStable(missingFields, func(i, j int) bool {
		return strings.Compare(strVal(missingFields[i]["key"]), strVal(missingFields[j]["key"])) < 0
	})

	return nil, envelopeFailure(
		"MCP_MANUAL_FIELDS_REQUIRED",
		"validation",
		"I still need a few passport fields before sending. Provide only the missing fields and retry.",
		false,
		"",
		map[string]any{
			"action": map[string]any{
				"type":           "COLLECT_PASSPORT_FIELDS",
				"title":          "Provide Missing Passport Fields",
				"missing_fields": missingFields,
				"resume_arguments": map[string]any{
					"recipient_email":      intent.RecipientEmail,
					"subject":              intent.Subject,
					"body_style":           intent.BodyStyle,
					"passport_document_id": documentID,
				},
			},
			"resolved_fields": final,
		},
	)
}

func (s *Server) readPassportProfileFields(
	ctx context.Context,
	c *vault.Client,
	slots []any,
) (map[string]string, error) {
	readReq := map[string]any{
		"connector_id":   "identity",
		"verb":           "identity.profile.read.v1",
		"policy_version": "1",
		"request": map[string]any{
			"profile_kind": "KYC",
			"slots":        slots,
		},
	}
	readRes, err := c.Post(ctx, "/v0/connectors/execute", readReq, true)
	if err != nil {
		return nil, err
	}
	extracted := map[string]string{}
	collectPassportSlotValues(readRes.Body, extracted)
	return extracted, nil
}

func mergePassportFields(
	manual map[string]string,
	primary map[string]string,
	fallback map[string]string,
) map[string]string {
	final := map[string]string{}
	for _, slot := range passportEmailRequiredSlots {
		if v := strings.TrimSpace(fallback[slot]); v != "" {
			final[slot] = v
		}
		if v := strings.TrimSpace(primary[slot]); v != "" {
			final[slot] = v
		}
		if v := strings.TrimSpace(manual[slot]); v != "" {
			final[slot] = v
		}
	}
	return final
}

func isPassportExtractionConflict(err error) bool {
	var apiErr *vault.APIError
	if !errors.As(err, &apiErr) || apiErr == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(apiErr.Code), "CONFLICT")
}

func (s *Server) passportExtractionApprovalResponse(
	ctx context.Context,
	extractReq map[string]any,
	extractErr error,
) map[string]any {
	norm := vault.NormalizeError(extractErr)
	if !strings.EqualFold(strings.TrimSpace(norm.Code), "MCP_APPROVAL_REQUIRED") {
		return nil
	}

	execJobResp, err := s.handleConnectorExecuteJob(ctx, map[string]any{
		"request": extractReq,
		"orchestration": map[string]any{
			"unbounded_profiles": false,
		},
	})
	if err != nil {
		return nil
	}
	if ok, _ := execJobResp["ok"].(bool); ok {
		return nil
	}

	errObj, _ := execJobResp["error"].(map[string]any)
	if strings.EqualFold(strings.TrimSpace(strVal(errObj["code"])), "MCP_APPROVAL_REQUIRED") {
		return execJobResp
	}
	return nil
}

func collectPassportSlotValues(raw any, out map[string]string) {
	switch node := raw.(type) {
	case map[string]any:
		for key, value := range node {
			trimmedKey := strings.TrimSpace(key)
			if _, ok := passportEmailFieldLabels[trimmedKey]; ok {
				if rendered := passportSlotString(value); rendered != "" {
					out[trimmedKey] = rendered
				}
			}
			collectPassportSlotValues(value, out)
		}
	case []any:
		for _, value := range node {
			collectPassportSlotValues(value, out)
		}
	}
}

func passportSlotString(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return strings.TrimSpace(strVal(raw))
	}
}

func normalizePassportManualFields(raw map[string]any) map[string]string {
	out := map[string]string{}
	if raw == nil {
		return out
	}
	for _, slot := range passportEmailRequiredSlots {
		out[slot] = strings.TrimSpace(strVal(raw[slot]))
	}
	return out
}

func missingPassportSlots(fields map[string]string) []string {
	missing := make([]string, 0)
	for _, slot := range passportEmailRequiredSlots {
		if strings.TrimSpace(fields[slot]) == "" {
			missing = append(missing, slot)
		}
	}
	return missing
}

func composePassportEmailBody(fields map[string]string, style string) string {
	normalizedStyle := strings.TrimSpace(strings.ToLower(style))
	lines := make([]string, 0, 12)
	switch normalizedStyle {
	case "formal":
		lines = append(lines, "Hello,")
		lines = append(lines, "")
		lines = append(lines, "Please find attached a copy of my passport and the requested passport details below.")
		lines = append(lines, "")
	case "casual":
		lines = append(lines, "Hi,")
		lines = append(lines, "")
		lines = append(lines, "I've attached a copy of my passport and included the details below.")
		lines = append(lines, "")
	case "concise":
		// Keep the concise style to labels only.
	default:
		lines = append(lines, "Attached is a copy of my passport and the requested details.")
		lines = append(lines, "")
	}
	lines = append(lines, formatPassportFieldLine("given_name", fields))
	lines = append(lines, formatPassportFieldLine("family_name", fields))
	lines = append(lines, formatPassportFieldLine("passport_number", fields))
	lines = append(lines, formatPassportFieldLine("passport_issuing_country", fields))
	lines = append(lines, formatPassportFieldLine("passport_expiry_date", fields))
	return strings.Join(lines, "\n")
}

func composePassportEmailBodyTemplate(style string, readStepID string) string {
	normalizedStyle := strings.TrimSpace(strings.ToLower(style))
	lines := make([]string, 0, 12)
	switch normalizedStyle {
	case "formal":
		lines = append(lines, "Hello,")
		lines = append(lines, "")
		lines = append(lines, "Please find attached a copy of my passport and the requested passport details below.")
		lines = append(lines, "")
	case "casual":
		lines = append(lines, "Hi,")
		lines = append(lines, "")
		lines = append(lines, "I've attached a copy of my passport and included the details below.")
		lines = append(lines, "")
	case "concise":
		// Keep the concise style to labels only.
	default:
		lines = append(lines, "Attached is a copy of my passport and the requested details.")
		lines = append(lines, "")
	}
	lines = append(lines, formatPassportFieldTemplateLine("given_name", readStepID))
	lines = append(lines, formatPassportFieldTemplateLine("family_name", readStepID))
	lines = append(lines, formatPassportFieldTemplateLine("passport_number", readStepID))
	lines = append(lines, formatPassportFieldTemplateLine("passport_issuing_country", readStepID))
	lines = append(lines, formatPassportFieldTemplateLine("passport_expiry_date", readStepID))
	return strings.Join(lines, "\n")
}

func formatPassportFieldLine(slot string, fields map[string]string) string {
	return passportLabelForSlot(slot) + ": " + strings.TrimSpace(fields[slot])
}

func formatPassportFieldTemplateLine(slot string, readStepID string) string {
	return passportLabelForSlot(slot) + ": " + passportTemplateRef(readStepID, slot)
}

func passportTemplateRef(readStepID string, slot string) string {
	return "{{step_output:" + strings.TrimSpace(readStepID) + ":/values/" + strings.TrimSpace(slot) + "}}"
}

func passportLabelForSlot(slot string) string {
	if label := strings.TrimSpace(passportEmailFieldLabels[slot]); label != "" {
		return label
	}
	return slot
}

func buildPassportEmailPlan(
	documentID string,
	recipientEmail string,
	subject string,
	bodyFromExisting string,
	bodyAfterExtraction string,
	manualFields map[string]string,
) (map[string]any, map[string]any) {
	slotValues := make([]any, 0, len(passportEmailRequiredSlots))
	for _, slot := range passportEmailRequiredSlots {
		slotValues = append(slotValues, slot)
	}
	manualFieldValues := map[string]any{}
	for _, slot := range passportEmailRequiredSlots {
		if v := strings.TrimSpace(manualFields[slot]); v != "" {
			manualFieldValues[slot] = v
		}
	}

	documentAttachments := []any{
		map[string]any{
			"type_id": "identity.passport",
		},
	}
	planInput := map[string]any{
		"passport": map[string]any{
			"subject_ref":   "self",
			"document_id":   documentID,
			"slots":         slotValues,
			"manual_fields": manualFieldValues,
		},
		"email": map[string]any{
			"to":                   []any{recipientEmail},
			"subject":              subject,
			"document_attachments": documentAttachments,
		},
	}

	readExistingStepID := "read_existing_profile"
	readAfterExtractStepID := "read_after_extract_profile"
	createFromExistingStepID := "create_draft_from_existing"
	createAfterExtractStepID := "create_draft_after_extract"

	plan := map[string]any{
		"type":          "connector.execution.plan.v1",
		"start_step_id": readExistingStepID,
		"steps": []any{
			map[string]any{
				"step_id":                      readExistingStepID,
				"connector_id":                 "identity",
				"verb":                         "identity.profile.read.v1",
				"policy_version":               "1",
				"default_success_next_step_id": "extract_passport_fields",
				"success_branches": []any{
					map[string]any{
						"when":         passportAllSlotsExistPredicate(readExistingStepID),
						"next_step_id": createFromExistingStepID,
					},
				},
				"request_base": map[string]any{
					"subject_ref":  "self",
					"profile_kind": "KYC",
					"slots":        []any{},
				},
				"request_bindings": []any{
					map[string]any{
						"path": "/slots",
						"ref": map[string]any{
							"source": "plan_input",
							"path":   "/passport/slots",
						},
					},
				},
			},
			map[string]any{
				"step_id":                      "extract_passport_fields",
				"connector_id":                 "identity",
				"verb":                         "identity.extraction.run.v1",
				"policy_version":               "1",
				"default_success_next_step_id": readAfterExtractStepID,
				"request_base": map[string]any{
					"subject_ref":     "self",
					"profile_kind":     "KYC",
					"provider":         "aws.textract.v1",
					"mode":             "ANALYZE_AND_SAVE",
					"document_id":      "passport_doc_placeholder",
					"slots":            []any{},
					"extracted_fields": manualFieldValues,
				},
				"request_bindings": []any{
					map[string]any{
						"path": "/document_id",
						"ref": map[string]any{
							"source": "plan_input",
							"path":   "/passport/document_id",
						},
					},
					map[string]any{
						"path": "/slots",
						"ref": map[string]any{
							"source": "plan_input",
							"path":   "/passport/slots",
						},
					},
				},
			},
			map[string]any{
				"step_id":                      readAfterExtractStepID,
				"connector_id":                 "identity",
				"verb":                         "identity.profile.read.v1",
				"policy_version":               "1",
				"default_success_next_step_id": createAfterExtractStepID,
				"request_base": map[string]any{
					"subject_ref":  "self",
					"profile_kind": "KYC",
					"slots":        []any{},
				},
				"request_bindings": []any{
					map[string]any{
						"path": "/slots",
						"ref": map[string]any{
							"source": "plan_input",
							"path":   "/passport/slots",
						},
					},
				},
			},
			buildPassportDraftCreateStep(createFromExistingStepID, "send_draft_from_existing", bodyFromExisting),
			buildPassportDraftSendStep("send_draft_from_existing", createFromExistingStepID),
			buildPassportDraftCreateStep(createAfterExtractStepID, "send_draft_after_extract", bodyAfterExtraction),
			buildPassportDraftSendStep("send_draft_after_extract", createAfterExtractStepID),
		},
	}
	return plan, planInput
}

func passportAllSlotsExistPredicate(stepID string) map[string]any {
	args := make([]any, 0, len(passportEmailRequiredSlots))
	for _, slot := range passportEmailRequiredSlots {
		args = append(args, map[string]any{
			"op": "exists",
			"ref": map[string]any{
				"source":  "step_output",
				"step_id": stepID,
				"path":    "/values/" + slot,
			},
		})
	}
	return map[string]any{
		"op":   "and",
		"args": args,
	}
}

func buildPassportDraftCreateStep(stepID string, nextStepID string, bodyTemplate string) map[string]any {
	return map[string]any{
		"step_id":                      stepID,
		"connector_id":                 "google",
		"verb":                         "google.gmail.drafts.create",
		"policy_version":               "1",
		"default_success_next_step_id": nextStepID,
		"request_base": map[string]any{
			"user_id":              "me",
			"to":                   []any{},
			"subject":              "",
			"text_plain":           bodyTemplate,
			"document_attachments": []any{},
		},
		"request_bindings": []any{
			map[string]any{
				"path": "/to",
				"ref": map[string]any{
					"source": "plan_input",
					"path":   "/email/to",
				},
			},
			map[string]any{
				"path": "/subject",
				"ref": map[string]any{
					"source": "plan_input",
					"path":   "/email/subject",
				},
			},
			map[string]any{
				"path": "/document_attachments",
				"ref": map[string]any{
					"source": "plan_input",
					"path":   "/email/document_attachments",
				},
			},
		},
	}
}

func buildPassportDraftSendStep(stepID string, draftCreateStepID string) map[string]any {
	return map[string]any{
		"step_id":        stepID,
		"connector_id":   "google",
		"verb":           "google.gmail.drafts.send",
		"policy_version": "1",
		"request_base": map[string]any{
			"user_id":  "me",
			"draft_id": "draft_placeholder",
		},
		"request_bindings": []any{
			map[string]any{
				"path": "/draft_id",
				"ref": map[string]any{
					"source":  "step_output",
					"step_id": draftCreateStepID,
					"path":    "/draft_id",
				},
			},
		},
	}
}

func buildPassportEmailSummary(recipient string, subject string, fields map[string]string) map[string]any {
	fieldList := make([]map[string]any, 0, len(passportEmailRequiredSlots))
	for _, slot := range passportEmailRequiredSlots {
		fieldList = append(fieldList, map[string]any{
			"slot":  slot,
			"label": passportLabelForSlot(slot),
			"value": strings.TrimSpace(fields[slot]),
		})
	}
	return map[string]any{
		"recipient": recipient,
		"subject":   subject,
		"fields":    fieldList,
		"attachment": map[string]any{
			"type_id": "identity.passport",
		},
		"note": "A passport document of type identity.passport will be attached.",
	}
}
