import type { ModelIR, ResourceType, SubjectRef } from "./model";

// IR은 순수 JSON 직렬화 가능 → 안전한 deep clone(브라우저·Bun 공통, lib 비의존).
const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v)) as T;

const findResource = (ir: ModelIR, name: string): ResourceType | undefined =>
  ir.resources.find((r) => r.name === name);

const DEFAULT_PARENT_RELATION = "parent";

/** 모든 IR 편집 연산은 순수하다: 입력을 변형하지 않고 새 IR을 반환한다. */

export function addResource(ir: ModelIR, name: string): ModelIR {
  if (ir.groups.some((g) => g.name === name) || ir.resources.some((r) => r.name === name)) return ir;
  const next = clone(ir);
  next.resources.push({ name, parents: [], roles: [], permissions: [] });
  return next;
}

export function addGroup(ir: ModelIR, name: string): ModelIR {
  if (ir.groups.some((g) => g.name === name) || ir.resources.some((r) => r.name === name)) return ir;
  const next = clone(ir);
  next.groups.push({ name, memberTypes: [{ kind: "user" }] });
  return next;
}

/** 타입 삭제 + 이를 parentType/group으로 참조하던 모든 ParentRef·SubjectRef 정리(고아 금지). */
export function removeType(ir: ModelIR, name: string): ModelIR {
  const next = clone(ir);
  next.groups = next.groups.filter((g) => g.name !== name);
  next.resources = next.resources.filter((r) => r.name !== name);

  const stripGroupRefs = (refs: SubjectRef[]): SubjectRef[] =>
    refs.filter((ref) => !(ref.kind === "group" && ref.group === name));

  for (const g of next.groups) {
    g.memberTypes = stripGroupRefs(g.memberTypes);
  }
  for (const r of next.resources) {
    // 부모로 참조하던 엣지 정리: parentTypes에서 name 제거, 비면 ParentRef 삭제.
    const removedRelations: string[] = [];
    r.parents = r.parents.flatMap((p) => {
      const parentTypes = p.parentTypes.filter((t) => t !== name);
      if (parentTypes.length === 0) {
        removedRelations.push(p.relationName);
        return [];
      }
      return [{ ...p, parentTypes }];
    });
    for (const role of r.roles) {
      role.assignableBy = stripGroupRefs(role.assignableBy);
    }
    for (const perm of r.permissions) {
      perm.inheritFromParents = perm.inheritFromParents.filter((rel) => !removedRelations.includes(rel));
    }
  }
  return next;
}

/** ParentRef 추가/병합(relationName 기준). 부모/자식 모두 resource여야 한다. */
export function connectParent(
  ir: ModelIR,
  childType: string,
  parentType: string,
  relationName: string = DEFAULT_PARENT_RELATION,
): ModelIR {
  if (!findResource(ir, childType) || !findResource(ir, parentType)) return ir;
  const next = clone(ir);
  const child = findResource(next, childType)!;
  const existing = child.parents.find((p) => p.relationName === relationName);
  if (existing) {
    if (!existing.parentTypes.includes(parentType)) existing.parentTypes.push(parentType);
  } else {
    child.parents.push({ relationName, parentTypes: [parentType] });
  }
  return next;
}

/**
 * 상속 엣지 삭제.
 * - parentType 지정: 그 타입만 parentTypes에서 제거(비면 ParentRef 삭제).
 * - parentType 생략: relationName의 ParentRef 전체 삭제.
 * 어느 경우든 ParentRef가 사라지면 이를 참조하던 permission.inheritFromParents도 정리.
 */
export function disconnectParent(
  ir: ModelIR,
  childType: string,
  relationName: string,
  parentType?: string,
): ModelIR {
  const child = findResource(ir, childType);
  if (!child) return ir;
  const next = clone(ir);
  const c = findResource(next, childType)!;
  let relationRemoved = false;

  c.parents = c.parents.flatMap((p) => {
    if (p.relationName !== relationName) return [p];
    if (parentType === undefined) {
      relationRemoved = true;
      return [];
    }
    const parentTypes = p.parentTypes.filter((t) => t !== parentType);
    if (parentTypes.length === 0) {
      relationRemoved = true;
      return [];
    }
    return [{ ...p, parentTypes }];
  });

  if (relationRemoved) {
    for (const perm of c.permissions) {
      perm.inheritFromParents = perm.inheritFromParents.filter((rel) => rel !== relationName);
    }
  }
  return next;
}

// ── 행렬(matrix) 편집 ──────────────────────────────────────────────────────────

/** 셀 토글: permission.grantedByRoles ∋ role 를 켜고/끈다. */
export function toggleCell(ir: ModelIR, typeName: string, permission: string, role: string): ModelIR {
  const next = clone(ir);
  const perm = findResource(next, typeName)?.permissions.find((p) => p.name === permission);
  if (!perm) return ir;
  perm.grantedByRoles = perm.grantedByRoles.includes(role)
    ? perm.grantedByRoles.filter((r) => r !== role)
    : [...perm.grantedByRoles, role];
  return next;
}

export function addRole(ir: ModelIR, typeName: string, name: string): ModelIR {
  const r = findResource(ir, typeName);
  if (!r || r.roles.some((role) => role.name === name)) return ir;
  const next = clone(ir);
  findResource(next, typeName)!.roles.push({ name, assignableBy: [{ kind: "user" }] });
  return next;
}

/** role 삭제 + 이를 참조하던 permission.grantedByRoles 정리. */
export function removeRole(ir: ModelIR, typeName: string, name: string): ModelIR {
  const r = findResource(ir, typeName);
  if (!r) return ir;
  const next = clone(ir);
  const res = findResource(next, typeName)!;
  res.roles = res.roles.filter((role) => role.name !== name);
  for (const perm of res.permissions) {
    perm.grantedByRoles = perm.grantedByRoles.filter((rn) => rn !== name);
  }
  return next;
}

export function renameRole(ir: ModelIR, typeName: string, from: string, to: string): ModelIR {
  const r = findResource(ir, typeName);
  if (!r || !r.roles.some((role) => role.name === from)) return ir;
  const next = clone(ir);
  const res = findResource(next, typeName)!;
  for (const role of res.roles) if (role.name === from) role.name = to;
  for (const perm of res.permissions) {
    perm.grantedByRoles = perm.grantedByRoles.map((rn) => (rn === from ? to : rn));
  }
  return next;
}

export function setRoleAssignableBy(
  ir: ModelIR,
  typeName: string,
  role: string,
  refs: SubjectRef[],
): ModelIR {
  const r = findResource(ir, typeName);
  if (!r) return ir;
  const next = clone(ir);
  const target = findResource(next, typeName)!.roles.find((rl) => rl.name === role);
  if (!target) return ir;
  target.assignableBy = clone(refs);
  return next;
}

export function addPermission(ir: ModelIR, typeName: string, name: string): ModelIR {
  const r = findResource(ir, typeName);
  if (!r || r.permissions.some((p) => p.name === name)) return ir;
  const next = clone(ir);
  findResource(next, typeName)!.permissions.push({
    name,
    grantedByRoles: [],
    inheritFromParents: [],
  });
  return next;
}

export function removePermission(ir: ModelIR, typeName: string, name: string): ModelIR {
  const r = findResource(ir, typeName);
  if (!r) return ir;
  const next = clone(ir);
  const res = findResource(next, typeName)!;
  res.permissions = res.permissions.filter((p) => p.name !== name);
  return next;
}

/** permission 이름변경. permission 이름은 내부에서 참조되지 않으므로 순수 필드 변경. */
export function renamePermission(ir: ModelIR, typeName: string, from: string, to: string): ModelIR {
  const r = findResource(ir, typeName);
  if (!r || !r.permissions.some((p) => p.name === from)) return ir;
  const next = clone(ir);
  for (const p of findResource(next, typeName)!.permissions) {
    if (p.name === from) p.name = to;
  }
  return next;
}

/** permission의 부모 상속(inheritFromParents)에서 parentRelation 토글. */
export function toggleInherit(
  ir: ModelIR,
  typeName: string,
  permission: string,
  parentRelation: string,
): ModelIR {
  const next = clone(ir);
  const perm = findResource(next, typeName)?.permissions.find((p) => p.name === permission);
  if (!perm) return ir;
  perm.inheritFromParents = perm.inheritFromParents.includes(parentRelation)
    ? perm.inheritFromParents.filter((rel) => rel !== parentRelation)
    : [...perm.inheritFromParents, parentRelation];
  return next;
}
