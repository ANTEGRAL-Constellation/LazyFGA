// Package contract는 packages/shared(백엔드 소비 부분집합)의 Go 포트다: ModelIR·조건·authzen·
// policy·reason·audit·grant 타입과 정적 검증기. TS 계약과 바이트 호환 JSON(필드명/optionality/
// 판별 유니온)을 목표로 하며, 검증 코드/경로 문자열은 TS와 바이트 단위로 동일하다(LFGA-24).
package contract

import "regexp"

// IDENT_RE: OpenFGA 식별자 규칙(보수적) — 영숫자/언더스코어.
var identRE = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

// isIdent는 IDENT_RE.test(name) 대응.
func isIdent(name string) bool { return identRE.MatchString(name) }

// RESERVED_WORDS: DSL 키워드 + 예약 식별자(이름 충돌 금지).
var reservedWords = map[string]struct{}{
	"this": {}, "self": {}, "type": {}, "relation": {}, "relations": {},
	"define": {}, "model": {}, "schema": {}, "from": {}, "or": {}, "and": {},
	"but": {}, "not": {}, "with": {}, "module": {}, "extend": {}, "condition": {},
}

// CEL_RESERVED: CEL 예약 식별자(LFGA-14). condition/param 이름이 CEL 식으로 흘러가므로
// 추가로 금지한다(리터럴 상수식 유발 / 타입 토큰 문법 파손 방지).
var celReserved = map[string]struct{}{
	// CEL 리터럴 → 상수식 유발.
	"true": {}, "false": {}, "null": {},
	// condition 선언의 타입 토큰 → 문법 파손.
	"int": {}, "uint": {}, "double": {}, "bool": {}, "bytes": {}, "string": {},
	"timestamp": {}, "duration": {}, "ipaddress": {}, "list": {}, "map": {}, "any": {},
	// CEL 키워드(방어적).
	"in": {}, "as": {}, "break": {}, "const": {}, "continue": {}, "else": {},
	"for": {}, "function": {}, "if": {}, "import": {}, "let": {}, "loop": {},
	"package": {}, "namespace": {}, "return": {}, "var": {}, "void": {}, "while": {},
}

func isReservedWord(name string) bool {
	_, ok := reservedWords[name]
	return ok
}

func isCelReserved(name string) bool {
	_, ok := celReserved[name]
	return ok
}
