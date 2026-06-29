// OpenFGA 식별자 규칙 + 예약어. model.ts와 condition.ts가 공유한다
// (별도 모듈로 둬서 model↔condition 순환 import를 끊는다 — lazyfga-14).

/** OpenFGA 식별자 규칙(보수적): 영숫자/언더스코어. */
export const IDENT_RE = /^[a-zA-Z0-9_]+$/;

/** DSL 키워드 + 예약 식별자(이름 충돌 금지). */
export const RESERVED_WORDS = new Set([
  "this",
  "self",
  "type",
  "relation",
  "relations",
  "define",
  "model",
  "schema",
  "from",
  "or",
  "and",
  "but",
  "not",
  "with",
  "module",
  "extend",
  "condition",
]);

/**
 * CEL 예약 식별자(lazyfga-14). condition 이름·파라미터 이름은 CEL 식으로 흘러가므로 이 집합을
 * 추가로 금지한다. 안 그러면: (1) `true`/`false`/`null` 이름 → `true == true` 같은 상수식이 되어
 * 파라미터를 무시(인가 무력화), (2) 타입 토큰(int/string/timestamp …) 이름 → condition 선언
 * 문법을 깨 컴파일 크래시. (모델 type/relation 이름은 CEL로 안 가므로 적용 안 함.)
 */
export const CEL_RESERVED = new Set([
  // CEL 리터럴 → 상수식 유발.
  "true",
  "false",
  "null",
  // condition 선언의 타입 토큰 → 문법 파손.
  "int",
  "uint",
  "double",
  "bool",
  "bytes",
  "string",
  "timestamp",
  "duration",
  "ipaddress",
  "list",
  "map",
  "any",
  // CEL 키워드(방어적).
  "in",
  "as",
  "break",
  "const",
  "continue",
  "else",
  "for",
  "function",
  "if",
  "import",
  "let",
  "loop",
  "package",
  "namespace",
  "return",
  "var",
  "void",
  "while",
]);
