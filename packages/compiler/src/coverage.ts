import type { ValidationError } from "@lazyfga/shared";

/**
 * 비주얼이 표현 가능한 subset의 경계 판정 결과(단일 진실).
 * CONCEPT "양방향 sync는 지원 범위 안에서만, 그 밖은 read-only"를 구현한다.
 */
export interface Coverage {
  /**
   * true면 완전 왕복 가능. 다음을 모두 만족할 때만 true:
   * advanced 없음 + 조립된 IR이 validateModelIR 통과 + schema 1.1 + 생성 subset 밖 조건 없음.
   */
  fullyRepresentable: boolean;
  /** subset 밖이라 IR로 표현 못 한 relation 목록. */
  advanced: Array<{ type: string; relation: string; reason: CoverageReason }>;
  /** DSL 구문 오류 시 파서 메시지(이때 ir=null). */
  parseError?: string;
  /** 조립된 IR이 의미 검증을 통과하지 못한 경우의 상세(라운드트립 backstop). */
  validationErrors?: ValidationError[];
  /** 모델 레벨 비표현 사유(예: schema 버전, condition 블록). */
  notes?: string[];
}

export type CoverageReason =
  | "INTERSECTION"
  | "EXCLUSION"
  | "CONDITION"
  | "NON_ROLE_UNION"
  | "CROSS_TYPE_USERSET"
  | "UNCLASSIFIABLE";
