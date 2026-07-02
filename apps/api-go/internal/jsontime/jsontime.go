// Package jsontime은 노출 타임스탬프를 JS `Date.toISOString()`과 바이트 동일하게
// 직렬화하는 Time 타입을 제공한다. TS 백엔드가 모든 timestamp를 이 형식(UTC,
// 밀리초 3자리 고정, 'Z' 접미)으로 응답했으므로 LFGA-25/26의 모든 DTO가 이를 공유한다.
package jsontime

import (
	"encoding/json"
	"time"
)

// isoLayout은 JS Date.toISOString()의 `YYYY-MM-DDTHH:mm:ss.sssZ`에 대응한다.
// UTC로 강제한 시각에 적용하면 오프셋 자리가 'Z'로 렌더된다.
const isoLayout = "2006-01-02T15:04:05.000Z07:00"

// Time은 time.Time을 감싸 JS Date.toISOString()과 동일한 JSON 표현을 갖는다.
type Time struct {
	time.Time
}

// New는 임의의 time.Time을 감싼다.
func New(t time.Time) Time {
	return Time{Time: t}
}

// NowUTC는 현재 시각을 UTC로 감싸 반환한다(주로 테스트/부수 용도).
func NowUTC() Time {
	return Time{Time: time.Now().UTC()}
}

// MarshalJSON은 UTC·밀리초 3자리·'Z' 접미의 따옴표 문자열로 직렬화한다.
// JS Date는 ms 정밀도뿐이므로 ms로 절삭해 반올림 모호성을 제거한다.
func (t Time) MarshalJSON() ([]byte, error) {
	b := make([]byte, 0, len(isoLayout)+2)
	b = append(b, '"')
	b = t.Time.UTC().Truncate(time.Millisecond).AppendFormat(b, isoLayout)
	b = append(b, '"')
	return b, nil
}

// UnmarshalJSON은 RFC3339(나노초 허용) 문자열 또는 null을 파싱한다.
func (t *Time) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}
