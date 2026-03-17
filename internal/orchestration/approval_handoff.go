package orchestration

import (
	"context"
	"errors"
	"sort"
	"strings"

	"accords-mcp/internal/vault"
)

const (
	ApprovalKindJob     = "JOB"
	ApprovalKindPlanRun = "PLAN_RUN"

	ApprovalStatePending  = "PENDING_APPROVAL"
	ApprovalStateTerminal = "TERMINAL"

	DecisionOutcomePending = "PENDING"
	DecisionOutcomeAllow   = "ALLOW"
	DecisionOutcomeDeny    = "DENY"
	DecisionOutcomeUnknown = "UNKNOWN"
)

var allPendingStates = []string{"WAITING", "READY", "RUNNING", "SUCCEEDED", "DENIED", "FAILED", "EXPIRED"}

type ApprovalHandle struct {
	Kind        string `json:"kind"`
	JobID       string `json:"job_id,omitempty"`
	RunID       string `json:"run_id,omitempty"`
	ChallengeID string `json:"challenge_id,omitempty"`
	PendingID   string `json:"pending_id,omitempty"`
}

type ApprovalState struct {
	Status               string         `json:"status"`
	DecisionOutcome      string         `json:"decision_outcome"`
	PendingApproval      map[string]any `json:"pending_approval,omitempty"`
	PendingExpiresAtUnix int64          `json:"pending_expires_at_unix_ms,omitempty"`
}

type ApprovalHandoff struct {
	Handle ApprovalHandle `json:"handle"`
	State  ApprovalState  `json:"state"`
	Vault  map[string]any `json:"vault,omitempty"`
}

func ExtractExecuteJobApprovalHandoff(result vault.APIResult) (ApprovalHandoff, bool) {
	snapshot, err := vault.DecodeJobSnapshot(result.Body)
	if err != nil {
		return ApprovalHandoff{}, false
	}
	if !strings.EqualFold(snapshot.Status, "PENDING") {
		return ApprovalHandoff{}, false
	}
	if !isApprovalRequiredCode(snapshot.ErrorCode) && len(snapshot.PendingApproval) == 0 {
		return ApprovalHandoff{}, false
	}
	challengeID := challengeIDFromPending(snapshot.PendingApproval)
	handle := ApprovalHandle{
		Kind:        ApprovalKindJob,
		JobID:       snapshot.JobID,
		ChallengeID: challengeID,
		PendingID:   pendingIDFromPending(snapshot.PendingApproval),
	}
	return ApprovalHandoff{
		Handle: handle,
		State: ApprovalState{
			Status:               ApprovalStatePending,
			DecisionOutcome:      DecisionOutcomePending,
			PendingApproval:      snapshot.PendingApproval,
			PendingExpiresAtUnix: snapshot.PendingExpiresAtUnix,
		},
		Vault: map[string]any{
			"request_id":        result.RequestID,
			"vault_http_status": result.StatusCode,
			"vault_code":        snapshot.ErrorCode,
			"response":          result.Body,
		},
	}, true
}

func ExtractPlanExecuteApprovalHandoff(result vault.APIResult) (ApprovalHandoff, bool) {
	runSnap, err := vault.DecodePlanRunSnapshot(result.Body)
	if err == nil {
		runID := firstNonEmpty(runSnap.RunID, strVal(result.Body["run_id"]))
		if runID == "" {
			return ApprovalHandoff{}, false
		}
		jobStatus := strings.ToUpper(strVal(runSnap.Job["status"]))
		jobCode := strings.ToUpper(extractErrorCode(runSnap.Job["error"]))
		required := strings.EqualFold(runSnap.State, "PENDING_APPROVAL") ||
			isApprovalRequiredCode(runSnap.LastErrorCode) ||
			(len(runSnap.PendingApproval) > 0 && (jobStatus == "PENDING" || strings.EqualFold(runSnap.State, "PENDING_APPROVAL"))) ||
			(strings.EqualFold(jobStatus, "PENDING") && isApprovalRequiredCode(jobCode))
		if !required {
			return ApprovalHandoff{}, false
		}
		handle := ApprovalHandle{
			Kind:        ApprovalKindPlanRun,
			RunID:       runID,
			JobID:       runSnap.JobID,
			ChallengeID: firstNonEmpty(challengeIDFromPending(runSnap.PendingApproval), strVal(runSnap.Run["challenge_id"])),
			PendingID:   pendingIDFromPending(runSnap.PendingApproval),
		}
		return ApprovalHandoff{
			Handle: handle,
			State: ApprovalState{
				Status:               ApprovalStatePending,
				DecisionOutcome:      DecisionOutcomePending,
				PendingApproval:      runSnap.PendingApproval,
				PendingExpiresAtUnix: numberToInt64(runSnap.Job["pending_expires_at_unix_ms"]),
			},
			Vault: map[string]any{
				"request_id":        result.RequestID,
				"vault_http_status": result.StatusCode,
				"vault_code":        firstNonEmpty(runSnap.LastErrorCode, jobCode),
				"response":          result.Body,
			},
		}, true
	}

	runID := strings.TrimSpace(strVal(result.Body["run_id"]))
	job, _ := result.Body["job"].(map[string]any)
	if runID == "" || job == nil {
		return ApprovalHandoff{}, false
	}
	pendingApproval, _ := result.Body["pending_approval"].(map[string]any)
	if pendingApproval == nil {
		pendingApproval, _ = job["pending_approval"].(map[string]any)
	}
	jobStatus := strings.ToUpper(strings.TrimSpace(strVal(job["status"])))
	jobCode := strings.ToUpper(strings.TrimSpace(extractErrorCode(job["error"])))
	required := strings.EqualFold(jobStatus, "PENDING") && (isApprovalRequiredCode(jobCode) || len(pendingApproval) > 0)
	if !required {
		return ApprovalHandoff{}, false
	}
	handle := ApprovalHandle{
		Kind:        ApprovalKindPlanRun,
		RunID:       runID,
		JobID:       strings.TrimSpace(strVal(job["job_id"])),
		ChallengeID: challengeIDFromPending(pendingApproval),
		PendingID:   pendingIDFromPending(pendingApproval),
	}
	return ApprovalHandoff{
		Handle: handle,
		State: ApprovalState{
			Status:               ApprovalStatePending,
			DecisionOutcome:      DecisionOutcomePending,
			PendingApproval:      pendingApproval,
			PendingExpiresAtUnix: numberToInt64(job["pending_expires_at_unix_ms"]),
		},
		Vault: map[string]any{
			"request_id":        result.RequestID,
			"vault_http_status": result.StatusCode,
			"vault_code":        jobCode,
			"response":          result.Body,
		},
	}, true
}

func BuildApprovalRequiredError(h ApprovalHandoff) *OrchestrationError {
	handleMap := approvalHandleMap(h.Handle)
	requestID := strings.TrimSpace(strVal(h.Vault["request_id"]))
	vaultStatus := numberToInt64(h.Vault["vault_http_status"])
	attestationURL := remoteAttestationURLFromPending(h.State.PendingApproval)
	approvalDetails := map[string]any{
		"status":                     ApprovalStatePending,
		"kind":                       strings.ToUpper(strings.TrimSpace(h.Handle.Kind)),
		"job_id":                     strings.TrimSpace(h.Handle.JobID),
		"run_id":                     strings.TrimSpace(h.Handle.RunID),
		"challenge_id":               strings.TrimSpace(h.Handle.ChallengeID),
		"pending_id":                 strings.TrimSpace(h.Handle.PendingID),
		"pending_id_resolved":        strings.TrimSpace(h.Handle.PendingID) != "",
		"pending_expires_at_unix_ms": h.State.PendingExpiresAtUnix,
		"pending_approval":           h.State.PendingApproval,
		"decision_outcome":           DecisionOutcomePending,
		"next_action": map[string]any{
			"tool":      "vaultclaw_approval_wait",
			"arguments": map[string]any{"handle": handleMap},
		},
	}
	if attestationURL != "" {
		approvalDetails["remote_attestation_url"] = attestationURL
		approvalDetails["remote_attestation_link_markdown"] = markdownLinkForURL(attestationURL)
	}
	return &OrchestrationError{
		Code:    "MCP_APPROVAL_REQUIRED",
		Message: "approval decision required before execution can continue",
		Details: map[string]any{
			"approval":          approvalDetails,
			"request_id":        requestID,
			"vault_http_status": vaultStatus,
			"vault":             h.Vault,
		},
	}
}

func ResolvePendingIDByChallengeAndJob(ctx context.Context, vc *vault.Client, challengeID, jobID string) (string, bool, error) {
	challengeID = strings.TrimSpace(challengeID)
	jobID = strings.TrimSpace(jobID)
	if vc == nil || challengeID == "" || jobID == "" {
		return "", false, nil
	}
	result, err := vc.Get(ctx, "/v0/connectors/approvals/pending", map[string]string{
		"challenge_id": challengeID,
		"states":       strings.Join(allPendingStates, ","),
		"limit":        "200",
	})
	if err != nil {
		var apiErr *vault.APIError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 401 || apiErr.StatusCode == 403 || strings.EqualFold(strings.TrimSpace(apiErr.Code), "INSUFFICIENT_SCOPE")) {
			return "", false, nil
		}
		return "", false, err
	}
	items := vault.DecodePendingApprovalItems(result.Body)
	matches := make([]vault.PendingApprovalItem, 0)
	for _, item := range items {
		if strings.EqualFold(strings.TrimSpace(item.JobID), jobID) {
			matches = append(matches, item)
		}
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].CreatedSeq != matches[j].CreatedSeq {
			return matches[i].CreatedSeq > matches[j].CreatedSeq
		}
		return matches[i].PendingID < matches[j].PendingID
	})
	pendingID := strings.TrimSpace(matches[0].PendingID)
	return pendingID, pendingID != "", nil
}

func DecisionOutcomeFromJob(job map[string]any) string {
	status := strings.ToUpper(strings.TrimSpace(strVal(job["status"])))
	code := strings.ToUpper(strings.TrimSpace(extractErrorCode(job["error"])))
	pendingApproval, _ := job["pending_approval"].(map[string]any)
	switch status {
	case "PENDING", "RUNNING", "READY":
		if len(pendingApproval) > 0 || isApprovalRequiredCode(code) {
			return DecisionOutcomePending
		}
		return DecisionOutcomeUnknown
	case "SUCCEEDED":
		return DecisionOutcomeAllow
	case "DENIED":
		if isApprovalDenyCode(code) {
			return DecisionOutcomeDeny
		}
		return DecisionOutcomeUnknown
	case "FAILED":
		return DecisionOutcomeUnknown
	default:
		return DecisionOutcomeUnknown
	}
}

func DecisionOutcomeFromPlanRun(run map[string]any, job map[string]any) string {
	state := strings.ToUpper(strings.TrimSpace(strVal(run["state"])))
	code := strings.ToUpper(strings.TrimSpace(firstNonEmpty(strVal(run["last_error_code"]), extractErrorCode(job["error"]))))
	switch state {
	case "PENDING_APPROVAL", "READY", "RUNNING":
		return DecisionOutcomePending
	case "SUCCEEDED":
		return DecisionOutcomeAllow
	case "DENIED":
		if isApprovalDenyCode(code) {
			return DecisionOutcomeDeny
		}
		return DecisionOutcomeUnknown
	case "FAILED":
		return DecisionOutcomeUnknown
	default:
		return DecisionOutcomeUnknown
	}
}

func DecisionOutcomeFromPending(item vault.PendingApprovalItem) string {
	switch item.State {
	case "WAITING", "READY", "RUNNING":
		return DecisionOutcomePending
	case "SUCCEEDED":
		return DecisionOutcomeAllow
	case "DENIED", "EXPIRED":
		if isApprovalDenyCode(item.LastErrorCode) || item.LastErrorCode == "" {
			return DecisionOutcomeDeny
		}
		return DecisionOutcomeUnknown
	case "FAILED":
		return DecisionOutcomeUnknown
	default:
		return DecisionOutcomeUnknown
	}
}

func IsTerminalJobStatus(status string) bool {
	status = strings.ToUpper(strings.TrimSpace(status))
	return status == "SUCCEEDED" || status == "FAILED" || status == "DENIED"
}

func IsTerminalPlanRunState(state string) bool {
	state = strings.ToUpper(strings.TrimSpace(state))
	return state == "SUCCEEDED" || state == "FAILED" || state == "DENIED"
}

func approvalHandleMap(h ApprovalHandle) map[string]any {
	return map[string]any{
		"kind":         strings.ToUpper(strings.TrimSpace(h.Kind)),
		"job_id":       strings.TrimSpace(h.JobID),
		"run_id":       strings.TrimSpace(h.RunID),
		"challenge_id": strings.TrimSpace(h.ChallengeID),
		"pending_id":   strings.TrimSpace(h.PendingID),
	}
}

func challengeIDFromPending(pending map[string]any) string {
	if pending == nil {
		return ""
	}
	challenge, _ := pending["challenge"].(map[string]any)
	return strings.TrimSpace(strVal(challenge["challenge_id"]))
}

func pendingIDFromPending(pending map[string]any) string {
	if pending == nil {
		return ""
	}
	return strings.TrimSpace(strVal(pending["pending_id"]))
}

func remoteAttestationURLFromPending(pending map[string]any) string {
	if pending == nil {
		return ""
	}
	return strings.TrimSpace(strVal(pending["remote_attestation_url"]))
}

func markdownLinkForURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return ""
	}
	return "[" + url + "](" + url + ")"
}

func isApprovalRequiredCode(code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	return code == "CONNECTOR_APPROVAL_DECISION_REQUIRED" || code == "PLAN_APPROVAL_REQUIRED"
}

func isApprovalDenyCode(code string) bool {
	switch strings.ToUpper(strings.TrimSpace(code)) {
	case "CONNECTOR_APPROVAL_RULE_DENIED", "PLAN_APPROVAL_DENIED", "APPROVAL_DECISION_EXPIRED":
		return true
	default:
		return false
	}
}

func extractErrorCode(raw any) string {
	errObj, _ := raw.(map[string]any)
	if errObj == nil {
		return ""
	}
	code := strings.TrimSpace(strVal(errObj["code"]))
	if code != "" {
		return code
	}
	inner, _ := errObj["error"].(map[string]any)
	return strings.TrimSpace(strVal(inner["code"]))
}

func numberToInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		return 0
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		s := strings.TrimSpace(v)
		if s != "" {
			return s
		}
	}
	return ""
}
