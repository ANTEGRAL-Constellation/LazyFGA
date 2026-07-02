package idp

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// jsSpaceClass는 JS 정규식 \s와 동일한 문자 집합이다(RE2 \s는 \v·유니코드 공백을 빼므로
// 명시 클래스로 맞춘다). 주입 가드가 JS와 최소 동일하게 엄격하도록 보장한다.
const jsSpaceClass = `\t\n\x0b\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var (
	// typeIDRe: type:id (id에는 :, #, *, JS \s 공백 금지) — 주입 가드.
	typeIDRe = regexp.MustCompile(`^[A-Za-z0-9_]+:[^` + jsSpaceClass + `:#*]+$`)
	// relationRe: 관계 이름.
	relationRe = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	// forbiddenInValue: 치환 값에 금지되는 문자(:#* + JS \s 공백).
	forbiddenInValue = regexp.MustCompile(`[:#*` + jsSpaceClass + `]`)
	// literalTypePrefix: 템플릿의 type 접두는 리터럴이어야 한다(예: `team:{{x}}` ✅).
	literalTypePrefix = regexp.MustCompile(`^[A-Za-z0-9_]+:`)
	// placeholderRe: {{path}} placeholder.
	placeholderRe = regexp.MustCompile(`\{\{([^}]+)\}\}`)
)

// scalarValue는 placeholder 경로를 스칼라 값으로 해석한다. 배열 attribute는 스칼라 슬롯에서
// 에러(fan-out의 {{item}}으로만 사용). errMsg가 ""가 아니면 실패. item은 fan-out 원소(nil=미제공).
func scalarValue(ev *IdpEvent, path string, item *string) (value string, errMsg string) {
	switch {
	case path == "item":
		if item == nil {
			return "", "{{item}} used without fan-out"
		}
		return *item, ""
	case path == "type":
		return ev.Type, ""
	case path == "subject" || path == "subject.id":
		return ev.Subject.ID, ""
	case path == "subject.type":
		return ev.Subject.Type, ""
	case strings.HasPrefix(path, "attributes."):
		key := path[len("attributes."):]
		v, ok := ev.Attributes[key]
		if !ok {
			return "", "unresolved {{" + path + "}}"
		}
		switch val := v.(type) {
		case []string:
			return "", "attribute {{" + path + "}} is an array; use fan-out ({{item}})"
		case string:
			return val, ""
		default:
			return "", "unresolved {{" + path + "}}"
		}
	default:
		return "", "unresolved {{" + path + "}}"
	}
}

// matchValue는 match 술어용 필드 값이다(스칼라만; 배열/미해결이면 ok=false → 매칭 실패).
func matchValue(ev *IdpEvent, path string) (string, bool) {
	v, errMsg := scalarValue(ev, path, nil)
	if errMsg != "" {
		return "", false
	}
	return v, true
}

// matchRule은 규칙이 이벤트에 매칭되는지 판정한다.
func matchRule(rule MappingRule, ev *IdpEvent) bool {
	if rule.EventType != ev.Type {
		return false
	}
	for _, m := range rule.Match {
		v, ok := matchValue(ev, m.Field)
		if !ok || v != m.Equals {
			return false
		}
	}
	// fan-out 규칙은 그 배열 attribute가 비어있지 않을 때만 매칭.
	if rule.FanOut != nil {
		arr, ok := ev.Attributes[*rule.FanOut].([]string)
		if !ok || len(arr) == 0 {
			return false
		}
	}
	return true
}

// RenderedTuple은 렌더된 tuple이다.
type RenderedTuple struct {
	User     string `json:"user"`
	Relation string `json:"relation"`
	Object   string `json:"object"`
}

// renderTuple은 템플릿 렌더 + 주입 가드다. 미해결 placeholder/형식 위반이면 errMsg. item은 fan-out 원소(선택).
func renderTuple(t TupleTemplate, ev *IdpEvent, item *string) (*RenderedTuple, string) {
	// 타입 접두는 템플릿 리터럴이어야 한다(이벤트 필드가 객체/주체 타입을 정하지 못하게).
	if !literalTypePrefix.MatchString(t.User) {
		return nil, "user template must start with a literal type: prefix"
	}
	if !literalTypePrefix.MatchString(t.Object) {
		return nil, "object template must start with a literal type: prefix"
	}
	sub := func(s string) (string, string) {
		out := s
		for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
			path := m[1]
			value, errMsg := scalarValue(ev, path, item)
			if errMsg != "" {
				return "", errMsg
			}
			if forbiddenInValue.MatchString(value) {
				return "", "value for {{" + path + "}} contains forbidden char (:#* or space)"
			}
			out = strings.ReplaceAll(out, "{{"+path+"}}", value)
		}
		return out, ""
	}

	u, ue := sub(t.User)
	r, re := sub(t.Relation)
	o, oe := sub(t.Object)
	if err := firstErr(ue, re, oe); err != "" {
		return nil, err
	}
	tuple := &RenderedTuple{User: u, Relation: r, Object: o}
	if !typeIDRe.MatchString(tuple.User) {
		return nil, fmt.Sprintf(`invalid user "%s" (need type:id)`, tuple.User)
	}
	if !typeIDRe.MatchString(tuple.Object) {
		return nil, fmt.Sprintf(`invalid object "%s" (need type:id)`, tuple.Object)
	}
	if !relationRe.MatchString(tuple.Relation) {
		return nil, fmt.Sprintf(`invalid relation "%s"`, tuple.Relation)
	}
	return tuple, ""
}

// firstErr는 첫 비어있지 않은 오류 문자열을 반환한다(TS `?? ?? ` 우선순위 대응).
func firstErr(errs ...string) string {
	for _, e := range errs {
		if e != "" {
			return e
		}
	}
	return ""
}

// WriteError는 write 실패 분류다. Transient면 502로 올려 IdP 재전송을 유도한다.
type WriteError struct {
	Transient bool
	Msg       string
}

// Error는 error 인터페이스를 만족한다.
func (e *WriteError) Error() string { return e.Msg }

// ApplyDeps는 매핑 적용의 주입 의존성이다(테스트는 fake, 라우트는 gateway.Write 개별 호출).
type ApplyDeps struct {
	// WriteTuple은 "applied" | "skipped"(멱등 no-op)을 반환한다. 실패 시 error(transient는 *WriteError).
	WriteTuple func(op string, tuple RenderedTuple) (string, error)
	Audit      func(action string, data map[string]any)
}

// ApplyResult는 적용 결과 카운터다.
type ApplyResult struct {
	Applied int `json:"applied"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// fanOutItems는 rule이 fan-out이면 배열 원소 목록(각 원소 포인터), 아니면 [nil](단일 렌더)이다.
func fanOutItems(rule MappingRule, ev *IdpEvent) []*string {
	if rule.FanOut == nil {
		return []*string{nil}
	}
	arr, ok := ev.Attributes[*rule.FanOut].([]string)
	if !ok {
		return nil
	}
	items := make([]*string, 0, len(arr))
	for i := range arr {
		s := arr[i]
		items = append(items, &s)
	}
	return items
}

// ApplyEvents는 이벤트들에 규칙을 적용한다(개별·멱등). transient write 오류는 위로 던진다(→ 502).
// priority 오름차순 stable 정렬(JS 안정 정렬 parity), 렌더 실패는 failed로 세고 계속한다.
func ApplyEvents(events []IdpEvent, rules []MappingRule, deps ApplyDeps) (ApplyResult, error) {
	var res ApplyResult
	sorted := make([]MappingRule, len(rules))
	copy(sorted, rules)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Priority < sorted[j].Priority })

	for i := range events {
		ev := &events[i]
		var matched []MappingRule
		for _, rule := range sorted {
			if matchRule(rule, ev) {
				matched = append(matched, rule)
			}
		}
		if len(matched) == 0 {
			res.Skipped++
			deps.Audit("idp.tuple.skip", map[string]any{"event": ev.Type, "reason": "no matching rule"})
			continue
		}
		for _, rule := range matched {
			// fan-out: 배열 원소별 1 tuple. 나쁜 원소만 failed, 나머지 진행.
			for _, item := range fanOutItems(rule, ev) {
				tuple, errMsg := renderTuple(rule.TupleTemplate, ev, item)
				if errMsg != "" || tuple == nil {
					res.Failed++
					deps.Audit("idp.tuple.error", map[string]any{"event": ev.Type, "error": errMsg})
					continue
				}
				result, err := deps.WriteTuple(rule.Op, *tuple)
				if err != nil {
					var we *WriteError
					if errors.As(err, &we) && we.Transient {
						return ApplyResult{}, err // → 502, IdP가 재전송.
					}
					res.Failed++
					deps.Audit("idp.tuple.error", map[string]any{"tuple": *tuple, "error": err.Error()})
					continue
				}
				if result == "applied" {
					res.Applied++
					deps.Audit("idp.tuple."+rule.Op, map[string]any{"tuple": *tuple})
				} else {
					res.Skipped++
				}
			}
		}
	}
	return res, nil
}
