// named policy 계약(lazyfga-8): 정책 1개 = (permission, resourceType) 단일 질문 템플릿.
import type { ConditionParam } from "./condition";
import type { ModelIR } from "./model";

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

/**
 * 정책 (permission, resourceType)을 평가할 때 OpenFGA가 필요로 하는 context 파라미터를
 * 모델에서 파생한다(lazyfga-14): can_<permission>을 부여하는 role(+상속 부모, 깊이 한도)의
 * assignableBy 중 조건이 붙은 것들의 params 합집합(name 기준 dedup). playground/PEP가
 * "이 정책엔 어떤 context를 넣어야 하나"를 알 수 있게 한다. 결정 자체는 OpenFGA Check가 한다.
 */
export function policyContextParams(
  ir: ModelIR,
  policy: Pick<Policy, "permission" | "resourceType">,
): ConditionParam[] {
  const condByName = new Map((ir.conditions ?? []).map((c) => [c.name, c]));
  const condNames = new Set<string>();
  const visited = new Set<string>();

  const walk = (resourceType: string, permission: string, depth: number): void => {
    const key = `${resourceType}:${permission}`;
    if (depth > 8 || visited.has(key)) return;
    visited.add(key);
    const r = ir.resources.find((x) => x.name === resourceType);
    const perm = r?.permissions.find((p) => p.name === permission);
    if (!r || !perm) return;
    for (const roleName of perm.grantedByRoles) {
      const role = r.roles.find((rl) => rl.name === roleName);
      role?.assignableBy.forEach((ref) => {
        if (ref.condition !== undefined) condNames.add(ref.condition);
      });
    }
    for (const relName of perm.inheritFromParents) {
      const parent = r.parents.find((p) => p.relationName === relName);
      parent?.parentTypes.forEach((pt) => walk(pt, permission, depth + 1));
    }
  };
  walk(policy.resourceType, policy.permission, 0);

  // name 기준 dedup(first-wins). 한 정책 경로의 여러 조건이 같은 param 이름을 서로 다른
  // 타입으로 쓰면 런타임 context를 동시에 만족시킬 수 없으므로, 모델 저작 시 이름→타입을
  // 일관되게 두는 것을 전제한다(여기서는 첫 정의를 채택).
  const params: ConditionParam[] = [];
  const seen = new Set<string>();
  for (const name of condNames) {
    condByName.get(name)?.params.forEach((p) => {
      if (!seen.has(p.name)) {
        seen.add(p.name);
        params.push(p);
      }
    });
  }
  return params;
}
