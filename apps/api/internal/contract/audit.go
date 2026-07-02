package contract

// audit 엔트리 계약(LFGA-17, web·api 공유).
type AuditEntry struct {
	ID string `json:"id"`
	// ISO 8601(삽입 시각, 이벤트 시각 근사).
	OccurredAt string         `json:"occurredAt"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	Data       map[string]any `json:"data"`
}
