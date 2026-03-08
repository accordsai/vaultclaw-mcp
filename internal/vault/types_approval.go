package vault

import (
	"fmt"
	"sort"
	"strings"
)

type JobSnapshot struct {
	Raw                  map[string]any
	JobID                string
	Status               string
	ErrorCode            string
	PendingApproval      map[string]any
	PendingExpiresAtUnix int64
}

type PlanRunSnapshot struct {
	Raw             map[string]any
	Run             map[string]any
	Job             map[string]any
	PendingApproval map[string]any
	RunID           string
	JobID           string
	State           string
	LastErrorCode   string
}

type PendingApprovalItem struct {
	Raw             map[string]any
	PendingApproval map[string]any
	PendingID       string
	JobID           string
	ChallengeID     string
	State           string
	LastErrorCode   string
	CreatedSeq      int64
}

func DecodeJobSnapshot(body map[string]any) (JobSnapshot, error) {
	job, ok := body["job"].(map[string]any)
	if !ok || job == nil {
		return JobSnapshot{}, fmt.Errorf("job response missing job object")
	}
	pendingApproval, _ := job["pending_approval"].(map[string]any)
	return JobSnapshot{
		Raw:                  job,
		JobID:                String(job, "job_id"),
		Status:               strings.ToUpper(String(job, "status")),
		ErrorCode:            extractErrorCode(job["error"]),
		PendingApproval:      pendingApproval,
		PendingExpiresAtUnix: numberToInt64(job["pending_expires_at_unix_ms"]),
	}, nil
}

func DecodePlanRunSnapshot(body map[string]any) (PlanRunSnapshot, error) {
	run, ok := body["run"].(map[string]any)
	if !ok || run == nil {
		return PlanRunSnapshot{}, fmt.Errorf("plan run response missing run object")
	}
	job, _ := body["job"].(map[string]any)
	pendingApproval, _ := body["pending_approval"].(map[string]any)
	if pendingApproval == nil {
		pendingApproval, _ = job["pending_approval"].(map[string]any)
	}
	return PlanRunSnapshot{
		Raw:             body,
		Run:             run,
		Job:             job,
		PendingApproval: pendingApproval,
		RunID:           String(run, "run_id"),
		JobID:           firstNonEmpty(String(run, "job_id"), String(job, "job_id")),
		State:           strings.ToUpper(String(run, "state")),
		LastErrorCode:   strings.ToUpper(firstNonEmpty(String(run, "last_error_code"), extractErrorCode(job["error"]))),
	}, nil
}

func DecodePendingApprovalItems(body map[string]any) []PendingApprovalItem {
	rows := ExtractItems(body)
	out := make([]PendingApprovalItem, 0, len(rows))
	for _, row := range rows {
		pendingApproval, _ := row["pending_approval"].(map[string]any)
		out = append(out, PendingApprovalItem{
			Raw:             row,
			PendingApproval: pendingApproval,
			PendingID:       String(row, "pending_id"),
			JobID:           String(row, "job_id"),
			ChallengeID:     String(row, "challenge_id"),
			State:           strings.ToUpper(String(row, "state")),
			LastErrorCode:   strings.ToUpper(String(row, "last_error_code")),
			CreatedSeq:      numberToInt64(row["created_seq"]),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedSeq != out[j].CreatedSeq {
			return out[i].CreatedSeq > out[j].CreatedSeq
		}
		return out[i].PendingID < out[j].PendingID
	})
	return out
}

func extractErrorCode(raw any) string {
	errObj, _ := raw.(map[string]any)
	if errObj == nil {
		return ""
	}
	code := strings.TrimSpace(stringAny(errObj["code"]))
	if code != "" {
		return strings.ToUpper(code)
	}
	inner, _ := errObj["error"].(map[string]any)
	return strings.ToUpper(strings.TrimSpace(stringAny(inner["code"])))
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

func stringAny(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}
