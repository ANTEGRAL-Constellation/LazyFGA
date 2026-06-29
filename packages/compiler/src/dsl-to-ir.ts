import {
  validateModelIR,
  type ConditionDef,
  type ConditionParam,
  type ConditionParamType,
  type GroupType,
  type ModelIR,
  type ParentRef,
  type Permission,
  type ResourceType,
  type Role,
  type SubjectRef,
  type ValidationError,
} from "@lazyfga/shared";
import { transformer } from "@openfga/syntax-transformer";
import { tryParseCondition } from "./condition-to-cel";
import type { Coverage, CoverageReason } from "./coverage";

// ── 순회용 OpenFGA JSON 형태(정확한 필드명, 부분) ───────────────────────────────
interface RelationRefJSON {
  type: string;
  relation?: string;
  wildcard?: object;
  condition?: string;
}
interface ComputedUsersetJSON {
  relation?: string;
  object?: string;
}
interface TupleToUsersetJSON {
  tupleset?: { relation?: string; object?: string };
  computedUserset?: ComputedUsersetJSON;
}
interface UsersetJSON {
  this?: object;
  computedUserset?: ComputedUsersetJSON;
  tupleToUserset?: TupleToUsersetJSON;
  union?: { child: UsersetJSON[] };
  intersection?: { child: UsersetJSON[] };
  difference?: object;
}
interface TypeDefJSON {
  type: string;
  relations?: Record<string, UsersetJSON>;
  metadata?: { relations?: Record<string, { directly_related_user_types?: RelationRefJSON[] }> };
}
interface ConditionJSON {
  name?: string;
  expression?: string;
  parameters?: Record<string, { type_name?: string }>;
}
interface AuthModelLike {
  schema_version?: string;
  type_definitions?: TypeDefJSON[];
  conditions?: Record<string, ConditionJSON>;
}

// ── ref 분류 헬퍼 ──────────────────────────────────────────────────────────────
/** OpenFGA condition 파라미터 type_name → ConditionParamType. */
const PARAM_FROM_TYPE_NAME: Record<string, ConditionParamType | undefined> = {
  TYPE_NAME_TIMESTAMP: "timestamp",
  TYPE_NAME_IPADDRESS: "ipaddress",
  TYPE_NAME_STRING: "string",
  TYPE_NAME_INT: "int",
  TYPE_NAME_DOUBLE: "double",
  TYPE_NAME_BOOL: "bool",
};

/** user 또는 <group>#member 형태의 주체 참조인가(조건 유무 무관, wildcard 제외). */
const isSubjectRef = (r: RelationRefJSON): boolean =>
  r.wildcard === undefined &&
  ((r.relation === undefined && r.type === "user") || r.relation === "member");

/** 다른 resource 타입을 가리키는 bare 참조(상속 부모 후보)인가(조건/와일드카드 없음). */
const isResourceTypeRef = (r: RelationRefJSON): boolean =>
  r.wildcard === undefined &&
  r.condition === undefined &&
  r.relation === undefined &&
  r.type !== "user";

const toSubjectRef = (r: RelationRefJSON): SubjectRef => {
  const base: SubjectRef =
    r.relation === "member"
      ? { kind: "group", group: r.type, relation: "member" }
      : { kind: "user" };
  return r.condition !== undefined ? { ...base, condition: r.condition } : base;
};

/** OpenFGA conditions JSON → ConditionDef[]. 생성 subset만 복원, 나머지는 unrepresentable. */
function parseConditions(model: AuthModelLike): {
  conditions: ConditionDef[];
  representable: Set<string>;
  unrepresentable: string[];
} {
  const conditions: ConditionDef[] = [];
  const representable = new Set<string>();
  const unrepresentable: string[] = [];
  for (const [name, cond] of Object.entries(model.conditions ?? {})) {
    const params: ConditionParam[] = [];
    let ok = true;
    for (const [pn, pinfo] of Object.entries(cond.parameters ?? {})) {
      const t = PARAM_FROM_TYPE_NAME[pinfo.type_name ?? ""];
      if (!t) {
        ok = false;
        break;
      }
      params.push({ name: pn, type: t });
    }
    const parsed =
      ok && cond.expression !== undefined ? tryParseCondition(name, params, cond.expression) : null;
    if (parsed) {
      conditions.push(parsed);
      representable.add(name);
    } else {
      unrepresentable.push(name);
    }
  }
  return { conditions, representable, unrepresentable };
}

const isDirectOnly = (u: UsersetJSON): boolean =>
  u.this !== undefined &&
  u.union === undefined &&
  u.intersection === undefined &&
  u.difference === undefined &&
  u.computedUserset === undefined &&
  u.tupleToUserset === undefined;

type DirectClass =
  | { kind: "role"; role: Role }
  | { kind: "parent"; parent: ParentRef }
  | { kind: "advanced"; reason: CoverageReason };

/** direct-only relation을 role | parent | advanced로 분류. */
function classifyDirect(
  relName: string,
  refs: RelationRefJSON[],
  representableConds: Set<string>,
): DirectClass {
  // 생성 subset 밖 CEL 조건이 붙은 ref → advanced.
  if (refs.some((r) => r.condition !== undefined && !representableConds.has(r.condition)))
    return { kind: "advanced", reason: "CONDITION" };
  if (refs.length > 0 && refs.every(isSubjectRef)) {
    return { kind: "role", role: { name: relName, assignableBy: refs.map(toSubjectRef) } };
  }
  if (refs.length > 0 && refs.every(isResourceTypeRef)) {
    return { kind: "parent", parent: { relationName: relName, parentTypes: refs.map((r) => r.type) } };
  }
  return { kind: "advanced", reason: "UNCLASSIFIABLE" };
}

type PermClass =
  | { kind: "permission"; permission: Permission }
  | { kind: "advanced"; reason: CoverageReason };

/** rewrite relation(can_<perm>)을 permission | advanced로 분류. */
function classifyPermission(
  relName: string,
  u: UsersetJSON,
  roleNames: Set<string>,
  parentRels: Set<string>,
): PermClass {
  if (u.intersection !== undefined) return { kind: "advanced", reason: "INTERSECTION" };
  if (u.difference !== undefined) return { kind: "advanced", reason: "EXCLUSION" };
  if (!relName.startsWith("can_")) return { kind: "advanced", reason: "NON_ROLE_UNION" };

  const children: UsersetJSON[] = u.union
    ? u.union.child
    : u.computedUserset
      ? [{ computedUserset: u.computedUserset }]
      : u.tupleToUserset
        ? [{ tupleToUserset: u.tupleToUserset }]
        : [];

  const grantedByRoles: string[] = [];
  const inheritFromParents: string[] = [];

  for (const child of children) {
    if (child.this !== undefined) return { kind: "advanced", reason: "UNCLASSIFIABLE" };
    if (child.computedUserset) {
      const cu = child.computedUserset;
      if (cu.object) return { kind: "advanced", reason: "CROSS_TYPE_USERSET" };
      if (cu.relation && roleNames.has(cu.relation)) grantedByRoles.push(cu.relation);
      else return { kind: "advanced", reason: "NON_ROLE_UNION" };
    } else if (child.tupleToUserset) {
      const tsRel = child.tupleToUserset.tupleset?.relation;
      const cuRel = child.tupleToUserset.computedUserset?.relation;
      if (tsRel && parentRels.has(tsRel) && cuRel === relName) inheritFromParents.push(tsRel);
      else return { kind: "advanced", reason: "CROSS_TYPE_USERSET" };
    } else {
      return { kind: "advanced", reason: "UNCLASSIFIABLE" };
    }
  }

  // grantedByRoles가 비면 IR(validateModelIR EMPTY_GRANT)로 표현 불가 → advanced.
  if (grantedByRoles.length === 0) return { kind: "advanced", reason: "NON_ROLE_UNION" };
  return { kind: "permission", permission: { name: relName.slice(4), grantedByRoles, inheritFromParents } };
}

/**
 * DSL을 IR로 역변환하고 비주얼 표현 가능 범위를 판정한다.
 * - ir: subset으로 표현된 모델(부분 매핑). 파싱 실패 시에만 null.
 * - coverage: 무엇이 advanced인지, 완전 왕복 가능한지.
 */
export function parseDslToIr(dsl: string): { ir: ModelIR | null; coverage: Coverage } {
  let model: AuthModelLike;
  try {
    model = transformer.transformDSLToJSONObject(dsl) as unknown as AuthModelLike;
  } catch (cause) {
    return {
      ir: null,
      coverage: { fullyRepresentable: false, advanced: [], parseError: String(cause) },
    };
  }

  const {
    conditions: parsedConditions,
    representable: representableConds,
    unrepresentable: unrepConds,
  } = parseConditions(model);

  const advanced: Coverage["advanced"] = [];
  const groups: GroupType[] = [];
  const resources: ResourceType[] = [];

  for (const td of model.type_definitions ?? []) {
    const relations = td.relations ?? {};
    const relNames = Object.keys(relations);

    // base type `user`: subset에서 관계가 없어야 한다. 관계가 있으면 표현 불가로 표시.
    if (td.type === "user") {
      for (const rel of relNames) {
        advanced.push({ type: "user", relation: rel, reason: "UNCLASSIFIABLE" });
      }
      continue;
    }
    const refsOf = (rel: string): RelationRefJSON[] =>
      td.metadata?.relations?.[rel]?.directly_related_user_types ?? [];

    // group type: member 단일 relation.
    if (relNames.length === 1 && relNames[0] === "member") {
      const u = relations["member"]!;
      const refs = refsOf("member");
      const unrepCond = refs.some(
        (r) => r.condition !== undefined && !representableConds.has(r.condition),
      );
      if (isDirectOnly(u) && refs.length > 0 && refs.every(isSubjectRef) && !unrepCond) {
        groups.push({ name: td.type, memberTypes: refs.map(toSubjectRef) });
      } else {
        advanced.push({
          type: td.type,
          relation: "member",
          reason: unrepCond ? "CONDITION" : "UNCLASSIFIABLE",
        });
      }
      continue;
    }

    // resource type: pass1(role/parent) → pass2(permission).
    const parents: ParentRef[] = [];
    const roles: Role[] = [];
    const permissions: Permission[] = [];
    const deferred: Array<{ name: string; userset: UsersetJSON }> = [];

    for (const rel of relNames) {
      const u = relations[rel]!;
      if (isDirectOnly(u)) {
        const c = classifyDirect(rel, refsOf(rel), representableConds);
        if (c.kind === "role") roles.push(c.role);
        else if (c.kind === "parent") parents.push(c.parent);
        else advanced.push({ type: td.type, relation: rel, reason: c.reason });
      } else {
        deferred.push({ name: rel, userset: u });
      }
    }

    const roleNames = new Set(roles.map((r) => r.name));
    const parentRels = new Set(parents.map((p) => p.relationName));
    for (const d of deferred) {
      const c = classifyPermission(d.name, d.userset, roleNames, parentRels);
      if (c.kind === "permission") permissions.push(c.permission);
      else advanced.push({ type: td.type, relation: d.name, reason: c.reason });
    }

    resources.push({ name: td.type, parents, roles, permissions });
  }

  const ir: ModelIR = { schemaVersion: "1.1", groups, resources };
  if (parsedConditions.length > 0) ir.conditions = parsedConditions;
  const notes: string[] = [];

  // 모델 레벨: schema 버전(IR은 1.1 고정) + 생성 subset 밖 condition.
  const schemaVersion = model.schema_version ?? "1.1";
  if (schemaVersion !== "1.1") {
    notes.push(`schema version "${schemaVersion}" is not representable (IR is fixed to 1.1)`);
  }
  if (unrepConds.length > 0) {
    notes.push(`condition(s) not representable: ${unrepConds.join(", ")} (kept as advanced)`);
  }

  // backstop: 분류는 성공해도 조립된 IR이 의미적으로 무효일 수 있다(예: 다중 관계 그룹을
  // #member로 참조). validateModelIR로 걸러 라운드트립 불변식을 보장한다.
  const validationErrors: ValidationError[] = validateModelIR(ir);
  for (const entry of validationErrorsToAdvanced(validationErrors, ir)) {
    if (!advanced.some((a) => a.type === entry.type && a.relation === entry.relation)) {
      advanced.push(entry);
    }
  }

  // validationErrors까지 포함: tryParseCondition은 shape만 보므로(예: 잘못된 CIDR도 복원),
  // 의미 검증 실패가 있으면 완전 표현 불가로 본다(round-trip 불변식 유지).
  const fullyRepresentable =
    advanced.length === 0 && notes.length === 0 && validationErrors.length === 0;
  const coverage: Coverage = { fullyRepresentable, advanced };
  if (validationErrors.length > 0) coverage.validationErrors = validationErrors;
  if (notes.length > 0) coverage.notes = notes;
  return { ir, coverage };
}

/** validateModelIR 오류 경로(path)에서 type/relation을 복원해 advanced 항목으로 변환. */
function validationErrorsToAdvanced(
  errors: ValidationError[],
  ir: ModelIR,
): Array<{ type: string; relation: string; reason: CoverageReason }> {
  const out: Array<{ type: string; relation: string; reason: CoverageReason }> = [];
  for (const err of errors) {
    const rm = /^resources\[(\d+)\]/.exec(err.path);
    if (rm) {
      const r = ir.resources[Number(rm[1])];
      if (!r) continue;
      let relation = "";
      const role = /\.roles\[(\d+)\]/.exec(err.path);
      const perm = /\.permissions\[(\d+)\]/.exec(err.path);
      const par = /\.parents\[(\d+)\]/.exec(err.path);
      if (role) relation = r.roles[Number(role[1])]?.name ?? "";
      else if (perm) {
        const p = r.permissions[Number(perm[1])];
        relation = p ? `can_${p.name}` : "";
      } else if (par) relation = r.parents[Number(par[1])]?.relationName ?? "";
      out.push({ type: r.name, relation, reason: "UNCLASSIFIABLE" });
      continue;
    }
    const gm = /^groups\[(\d+)\]/.exec(err.path);
    if (gm) {
      const g = ir.groups[Number(gm[1])];
      if (g) out.push({ type: g.name, relation: "member", reason: "UNCLASSIFIABLE" });
    }
  }
  return out;
}
