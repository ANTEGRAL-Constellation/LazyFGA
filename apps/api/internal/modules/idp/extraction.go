package idp

import (
	"strconv"
	"strings"

	"github.com/antegral-constellation/lazyfga/api/internal/jsutil"
)

// EventExtractionRule은 preset이 든 event-type별 추출 규칙이다(TS extraction.ts 포팅).
type EventExtractionRule struct {
	// Match는 이 규칙이 적용되는 이벤트 타입들.
	Match []string
	// SubjectType은 주체 타입(예: "user").
	SubjectType string
	// SubjectIDPath는 매칭된 이벤트 내 주체 id 경로.
	SubjectIDPath string
	// AttributePaths는 정규 attribute 이름 → payload 경로. 순서 보존(에러 메시지의 known 목록
	// 순서 parity를 위해 Go map 대신 정렬된 슬라이스로 둔다).
	AttributePaths []AttributePath
}

// AttributePath는 정규 attribute 이름과 payload 경로 쌍이다.
type AttributePath struct {
	Name string
	Path string
}

// ProviderPreset은 서명 spec + 이벤트 타입 경로 + event-type별 추출 규칙이다.
type ProviderPreset struct {
	Signature WebhookSignatureSpec
	// TypePath는 이벤트 타입을 읽는 경로. 예: "event_type".
	TypePath   string
	Extraction []EventExtractionRule
}

// dangerousKeys는 prototype-pollution 방지용 거부 세그먼트다. Go엔 프로토타입 체인이 없지만
// 동일 payload가 동일 결과를 내도록 spec parity로 유지한다.
var dangerousKeys = map[string]bool{"__proto__": true, "constructor": true, "prototype": true}

// getPath는 dotted-path 값 조회다(예: "event_payload.userId"). 중간이 객체/배열이 아니면 nil.
// 위험 세그먼트를 거부하고, 객체는 own-key만, 배열은 유효 숫자 인덱스로만 순회한다
// (TS hasOwnProperty 의미론 — LFGA-22 §4.3 승인 편차: 숫자 인덱스만 지원).
func getPath(obj any, path string) any {
	cur := obj
	for _, key := range strings.Split(path, ".") {
		if dangerousKeys[key] {
			return nil
		}
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[key]
			if !ok {
				return nil
			}
			cur = v
		case []any:
			// JS 배열의 own property: 유효 인덱스 + "length"(hasOwnProperty가 true) 둘 다 해석된다.
			if key == "length" {
				cur = float64(len(node))
				continue
			}
			idx, ok := arrayIndex(key, len(node))
			if !ok {
				return nil
			}
			cur = node[idx]
		default:
			return nil
		}
	}
	return cur
}

// arrayIndex는 세그먼트가 배열의 유효 own 인덱스(정규 비음수 정수, 범위 내)인지 검사한다.
// leading-zero("00")·부호·소수는 배열의 own 프로퍼티가 아니므로 거부한다.
func arrayIndex(key string, length int) (int, bool) {
	if key == "" {
		return 0, false
	}
	if key != "0" && key[0] == '0' {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	idx, err := strconv.Atoi(key)
	if err != nil || idx < 0 || idx >= length {
		return 0, false
	}
	return idx, true
}

// readEventType은 이벤트 타입만 읽는다(추출 실패 시 audit 관측용). 비-객체/빈 문자열 → ok=false.
func readEventType(preset ProviderPreset, body any) (string, bool) {
	if _, ok := body.(map[string]any); !ok {
		return "", false
	}
	s, ok := getPath(body, preset.TypePath).(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// attributeNamesForEvent는 이벤트 타입에 매칭되는 규칙들이 생성하는 attribute 이름 집합을
// 삽입 순서 보존·중복 제거해 반환한다(fanOut 검증 + 에러 메시지 순서 parity).
func attributeNamesForEvent(preset ProviderPreset, eventType string) []string {
	var names []string
	seen := map[string]bool{}
	for _, rule := range preset.Extraction {
		if !contains(rule.Match, eventType) {
			continue
		}
		for _, ap := range rule.AttributePaths {
			if !seen[ap.Name] {
				seen[ap.Name] = true
				names = append(names, ap.Name)
			}
		}
	}
	return names
}

// contains는 문자열 슬라이스 멤버십이다.
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// coerceScalar는 스칼라(string/number/bool)를 string으로 강제한다. 그 외 → ok=false.
// number는 JS String(number)과 바이트 동일하게 포맷한다(jsutil.NumberString).
func coerceScalar(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		if x {
			return "true", true
		}
		return "false", true
	case float64:
		return jsutil.NumberString(x), true
	default:
		return "", false
	}
}

// ExtractEvent는 raw payload를 정규 IdpEvent로 정규화한다. 매핑 대상 아니면 nil.
//   - 타입을 preset.TypePath로 읽고, Match에 그 타입을 포함하는 첫 규칙을 고른다(없으면 nil).
//   - 주체 id는 비어있지 않은 string이어야 한다(강제 변환 안 함 — lazyfga-16 하드닝).
//   - attributes: 스칼라(→string) 또는 스칼라 배열(→[]string, fan-out 소스). 빈 배열도 보존.
func ExtractEvent(preset ProviderPreset, body any) *IdpEvent {
	m, ok := body.(map[string]any)
	if !ok {
		return nil
	}
	typ, ok := getPath(m, preset.TypePath).(string)
	if !ok || typ == "" {
		return nil
	}
	var rule *EventExtractionRule
	for i := range preset.Extraction {
		if contains(preset.Extraction[i].Match, typ) {
			rule = &preset.Extraction[i]
			break
		}
	}
	if rule == nil {
		return nil
	}
	// 주체 id: 비어있지 않은 string만(숫자/객체 강제 변환 금지).
	sid, ok := getPath(m, rule.SubjectIDPath).(string)
	if !ok || sid == "" {
		return nil
	}
	attrs := map[string]any{}
	for _, ap := range rule.AttributePaths {
		raw := getPath(m, ap.Path)
		if arr, isArr := raw.([]any); isArr {
			out := make([]string, 0, len(arr))
			for _, el := range arr {
				if s, ok := coerceScalar(el); ok {
					out = append(out, s)
				}
			}
			attrs[ap.Name] = out // 빈 배열도 그대로(fan-out에서 0 tuple로 처리).
		} else if s, ok := coerceScalar(raw); ok {
			attrs[ap.Name] = s
		}
	}
	return &IdpEvent{Type: typ, Subject: Subject{Type: rule.SubjectType, ID: sid}, Attributes: attrs}
}
