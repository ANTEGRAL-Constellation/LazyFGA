// Package auditread는 감사 로그 조회 측(GET /audit)을 구현한다(LFGA-26, TS modules/audit 읽기 부분 포팅).
// 쓰기 측 Recorder는 LFGA-23 internal/audit이 소유하고, 이 패키지는 keyset 페이지네이션 조회만 담당한다.
package auditread

import (
	"encoding/base64"
	"strings"
	"time"
)

// isoLayout은 JS Date.toISOString()의 `YYYY-MM-DDTHH:mm:ss.sssZ`에 대응한다(UTC·ms 3자리·'Z').
const isoLayout = "2006-01-02T15:04:05.000Z07:00"

// isoMillis는 시각을 JS Date.toISOString()과 바이트 동일한 ms ISO 문자열로 만든다.
func isoMillis(t time.Time) string {
	return t.UTC().Truncate(time.Millisecond).Format(isoLayout)
}

// Cursor는 keyset 커서(occurredAt, id)다.
type Cursor struct {
	OccurredAt time.Time
	ID         string
}

// encodeCursor는 "<ISO>|<id>"를 패딩 없는 base64url로 인코딩한다.
func encodeCursor(iso, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(iso + "|" + id))
}

// decodeCursor는 커서를 해석한다. 무효(디코딩 실패/빈 iso·id/파싱 불가)면 ok=false.
func decodeCursor(s string) (*Cursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, false
	}
	parts := strings.Split(string(raw), "|")
	iso := parts[0]
	id := ""
	if len(parts) >= 2 {
		id = parts[1]
	}
	if iso == "" || id == "" {
		return nil, false
	}
	t, ok := parseFlexibleTime(iso)
	if !ok {
		return nil, false
	}
	return &Cursor{OccurredAt: t, ID: id}, true
}

// parseFlexibleTime은 RFC3339(소수초 유무 무관)와 YYYY-MM-DD(UTC 자정)를 파싱한다(§4.4-3 승인 편차).
// JS Date는 ms 정밀도(초과 소수 자릿수 절단)이므로 동일하게 ms로 절단한다 — 안 하면
// sub-ms 경계에서 keyset/필터 포함 여부가 TS와 어긋난다(LFGA-26 리뷰 반영).
func parseFlexibleTime(s string) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Truncate(time.Millisecond), true
	}
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
