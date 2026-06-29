// lazyfga-15: IdP webhook 코어 계약. provider-agnostic.

/** 정규 IdP 이벤트(provider 독립). adapter가 raw payload를 이 형태로 정규화한다. */
export interface IdpEvent {
  type: string; // 정규 이벤트 타입. 예: "user.grant.added"
  subject: { id: string }; // 영향받는 user 식별자(OpenFGA user id로 쓰임)
  attributes: Record<string, string>; // 정규화된 평탄 필드. 예: { projectId: "123" }
}

/** provider별 adapter: 서명 검증 + payload 정규화. */
export interface IdpAdapter {
  provider: string;
  verifySignature(rawBody: Uint8Array, headers: Headers, secret: string): boolean;
  parseEvents(body: unknown, headers: Headers): IdpEvent[];
}

/** 동등 비교 술어: 이벤트의 field 경로 값이 equals와 같아야 매칭. */
export interface MatchPredicate {
  field: string; // "type" | "subject.id" | "attributes.<k>"
  equals: string;
}

/** tuple 템플릿. 각 문자열은 {{path}} placeholder를 이벤트 값으로 치환. */
export interface TupleTemplate {
  user: string;
  relation: string;
  object: string;
}

/** 매핑 규칙(설정형, Q3=B). idp_mapping_rule 행과 1:1. */
export interface MappingRule {
  eventType: string;
  match: MatchPredicate[];
  tupleTemplate: TupleTemplate;
  op: "write" | "delete";
  priority: number;
}

// ── adapter 레지스트리 ─────────────────────────────────────────────────────────
// lazyfga-16(zitadel)·테스트(fake)가 registerAdapter로 등록한다.
const registry = new Map<string, IdpAdapter>();

export function registerAdapter(adapter: IdpAdapter): void {
  registry.set(adapter.provider, adapter);
}
export function getAdapter(provider: string): IdpAdapter | undefined {
  return registry.get(provider);
}
