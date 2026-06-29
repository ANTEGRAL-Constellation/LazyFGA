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
