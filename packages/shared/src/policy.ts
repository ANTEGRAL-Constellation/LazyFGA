// named policy 계약(lazyfga-8): 정책 1개 = (permission, resourceType) 단일 질문 템플릿.

export interface Policy {
  /** slug. 예: "can-read-document" */
  id: string;
  /** 예: "read" → OpenFGA relation `can_read` */
  permission: string;
  /** 예: "document" */
  resourceType: string;
  description?: string;
  /** 예약(lazyfga-14). */
  conditionRef?: string;
}

/** evaluate가 사용하는 서버 내부 조회 계약. */
export interface PolicyRepo {
  findById(id: string): Promise<Policy | null>;
  findByActionResource(permission: string, resourceType: string): Promise<Policy | null>;
}
