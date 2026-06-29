// lazyfga-15/21: IdP webhook 코어 계약. provider-agnostic.
// lazyfga-21: per-provider adapter 코드를 제거하고, 서명/추출을 선언적 spec(WebhookSignatureSpec /
// ProviderPreset, signature.ts·extraction.ts)으로 구성한다. 이 파일엔 매핑 엔진이 쓰는 정규 타입만 남는다.

/** 정규 IdP 이벤트(provider 독립). extraction 엔진이 raw payload를 이 형태로 정규화한다. */
export interface IdpEvent {
  type: string; // 정규 이벤트 타입. 예: "user.grant.added"
  /** 영향받는 주체. id는 OpenFGA user id로 쓰임. type은 추출 규칙이 정한 주체 타입(예: "user"). */
  subject: { type: string; id: string };
  /** 정규화된 평탄 필드. 스칼라(string) 또는 배열(string[], fan-out 소스). 예: { project: "123", roleKeys: ["a","b"] } */
  attributes: Record<string, string | string[]>;
}

/** 동등 비교 술어: 이벤트의 field 경로 값이 equals와 같아야 매칭. */
export interface MatchPredicate {
  field: string; // "type" | "subject" | "attributes.<k>"
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
  /**
   * lazyfga-21 배열 fan-out: 지정 시 그 이름의 배열 attribute를 원소별 1 tuple로 펼친다
   * (원소는 템플릿의 `{{item}}`에 바인딩). 미지정이면 단일 tuple(기존 동작).
   */
  fanOut?: string;
}
