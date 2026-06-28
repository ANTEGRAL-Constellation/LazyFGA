import { z } from "zod";

// ── 5-primitive ModelIR ──────────────────────────────────────────────────────
// CONCEPT.md의 5 primitive(Resource·Role·Permission·Hierarchy·Group)를 그대로
// 데이터 구조로 옮긴 단일 계약. 각 필드는 OpenFGA 구문과 1:1로 대응한다.

/** 주체 참조: 직접 user이거나, 그룹 멤버 userset. DSL: `user` | `<group>#member`. */
export type SubjectRef =
  | { kind: "user" }
  | { kind: "group"; group: string; relation: "member" };

/** 예약(= lazyfga-14, 배치 재검토). MVP에선 항상 undefined. */
export interface ConditionRef {
  name: string;
}

/**
 * 주체 그룹. DSL: `type <name> { relations { define member: [<memberTypes>] } }`
 */
export interface GroupType {
  name: string;
  /** member 관계의 type restriction. 예: [user, team#member] */
  memberTypes: SubjectRef[];
}

/**
 * 상속 엣지. DSL: `define <relationName>: [<parentTypes...>]`,
 * 권한이 `... or can_<perm> from <relationName>`로 참조.
 */
export interface ParentRef {
  /** ResourceType 내 relation 네임스페이스(role/permission/parent) 전역 유일. */
  relationName: string;
  /** 모두 resources 내 존재. 같은 relationName은 단일 ParentRef로 병합. */
  parentTypes: string[];
}

/** 부여 가능한 역할. DSL: `define <name>: [<assignableBy>]` */
export interface Role {
  name: string;
  assignableBy: SubjectRef[];
}

/**
 * 검사용 권한(액션). 관계 이름은 `can_<name>`.
 * DSL: `define can_<name>: <grantedByRoles를 or> [or can_<name> from <parent>]`
 */
export interface Permission {
  name: string;
  /** 이 권한을 주는 역할 목록(행렬의 열). 같은 ResourceType.roles[].name 중 하나 이상. */
  grantedByRoles: string[];
  /** 상속받을 ParentRef.relationName 목록(없으면 빈 배열). */
  inheritFromParents: string[];
  /** 예약(lazyfga-14). MVP에선 항상 undefined. */
  condition?: ConditionRef;
}

export interface ResourceType {
  name: string;
  parents: ParentRef[];
  roles: Role[];
  permissions: Permission[];
}

export interface ModelIR {
  /** OpenFGA schema 버전 고정. */
  schemaVersion: "1.1";
  groups: GroupType[];
  resources: ResourceType[];
}

// ── 런타임 스키마(zod) ────────────────────────────────────────────────────────
// 구조(shape) 검증용. 신뢰할 수 없는 JSON(api 요청 등)을 ModelIR로 파싱할 때 사용.
// 의미 검증(참조 무결성 등)은 validateModelIR가 담당한다.

const subjectRefSchema: z.ZodType<SubjectRef> = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("user") }),
  z.object({ kind: z.literal("group"), group: z.string(), relation: z.literal("member") }),
]);

const conditionRefSchema: z.ZodType<ConditionRef> = z.object({ name: z.string() });

export const modelIrSchema: z.ZodType<ModelIR> = z.object({
  schemaVersion: z.literal("1.1"),
  groups: z.array(
    z.object({
      name: z.string(),
      memberTypes: z.array(subjectRefSchema),
    }),
  ),
  resources: z.array(
    z.object({
      name: z.string(),
      parents: z.array(
        z.object({
          relationName: z.string(),
          parentTypes: z.array(z.string()),
        }),
      ),
      roles: z.array(
        z.object({
          name: z.string(),
          assignableBy: z.array(subjectRefSchema),
        }),
      ),
      permissions: z.array(
        z.object({
          name: z.string(),
          grantedByRoles: z.array(z.string()),
          inheritFromParents: z.array(z.string()),
          condition: conditionRefSchema.optional(),
        }),
      ),
    }),
  ),
});

// ── 의미 검증(validateModelIR) ────────────────────────────────────────────────

export type ValidationErrorCode =
  | "BAD_NAME"
  | "DUP_TYPE"
  | "DUP_RELATION"
  | "UNKNOWN_PARENT"
  | "UNKNOWN_ROLE"
  | "UNKNOWN_GROUP"
  | "EMPTY_GRANT"
  | "RESERVED_USER"
  | "PARENT_MISSING_PERMISSION"
  | "DUP_PARENT_RELATION"
  // 아래 두 코드는 M1 교차리뷰에서 추가(빈 subject 목록 → 무효 DSL / 예약 condition 누수 방지).
  | "EMPTY_SUBJECTS"
  | "CONDITION_RESERVED";

export interface ValidationError {
  code: ValidationErrorCode;
  /** 예: "resources[1].permissions[0].grantedByRoles[2]" */
  path: string;
  message: string;
}

/** OpenFGA 식별자 규칙(보수적): 영숫자/언더스코어. */
const IDENT_RE = /^[a-zA-Z0-9_]+$/;
/** DSL 키워드 + 예약 식별자(이름 충돌 금지). */
const RESERVED_WORDS = new Set([
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

const subjectGroup = (ref: SubjectRef): string | null =>
  ref.kind === "group" ? ref.group : null;

/**
 * IR 정적 검증(§4.3 규칙 1~8). 변환(compiler) 이전에 항상 통과해야 한다.
 * 예외를 던지지 않고 위반을 모두 수집해 반환한다(빈 배열 = 유효).
 */
export function validateModelIR(ir: ModelIR): ValidationError[] {
  const errors: ValidationError[] = [];
  const add = (code: ValidationErrorCode, path: string, message: string) =>
    errors.push({ code, path, message });

  // rule 1: 이름 식별자 규칙 + 예약어 충돌.
  const checkName = (name: string, path: string): void => {
    if (!IDENT_RE.test(name) || RESERVED_WORDS.has(name)) {
      add("BAD_NAME", path, `invalid identifier: "${name}"`);
    }
  };

  // rule 2: type 이름 전역 유일 + user 예약.
  const seenType = new Set<string>();
  const checkTypeName = (name: string, path: string): void => {
    if (name === "user") {
      add("RESERVED_USER", path, `"user" is a reserved base type and cannot be redefined`);
    } else {
      checkName(name, path);
    }
    if (seenType.has(name)) add("DUP_TYPE", path, `duplicate type name: "${name}"`);
    seenType.add(name);
  };

  ir.groups.forEach((g, gi) => checkTypeName(g.name, `groups[${gi}].name`));
  ir.resources.forEach((r, ri) => checkTypeName(r.name, `resources[${ri}].name`));

  const groupNameSet = new Set(ir.groups.map((g) => g.name));
  const resourceNameSet = new Set(ir.resources.map((r) => r.name));
  // 리소스명 → 보유 permission 이름 집합(rule 7 검사용).
  const resourcePerms = new Map<string, Set<string>>(
    ir.resources.map((r) => [r.name, new Set(r.permissions.map((p) => p.name))]),
  );

  // rule 6: 그룹 멤버 type restriction의 group 참조 존재 + 빈 목록 금지.
  ir.groups.forEach((g, gi) => {
    if (g.memberTypes.length === 0) {
      add("EMPTY_SUBJECTS", `groups[${gi}].memberTypes`, `group member list must be non-empty`);
    }
    g.memberTypes.forEach((ref, mi) => {
      const grp = subjectGroup(ref);
      if (grp !== null && !groupNameSet.has(grp)) {
        add("UNKNOWN_GROUP", `groups[${gi}].memberTypes[${mi}].group`, `unknown group: "${grp}"`);
      }
    });
  });

  ir.resources.forEach((r, ri) => {
    const base = `resources[${ri}]`;
    const roleNameSet = new Set(r.roles.map((role) => role.name));

    // rule 3: relation 네임스페이스(role | can_<perm> | parent.relationName) 전역 유일.
    const relationNamespace = new Map<string, string>(); // effectiveName -> origin path
    const claimRelation = (effectiveName: string, path: string): void => {
      if (relationNamespace.has(effectiveName)) {
        add(
          "DUP_RELATION",
          path,
          `relation name "${effectiveName}" collides with ${relationNamespace.get(effectiveName)}`,
        );
      } else {
        relationNamespace.set(effectiveName, path);
      }
    };

    // parents: rule 8 (relationName 유일, parentTypes 비어있지 않고 중복 없음) + rule 4 (parentTypes 존재).
    const parentRelSeen = new Set<string>();
    r.parents.forEach((p, pi) => {
      const ppath = `${base}.parents[${pi}]`;
      checkName(p.relationName, `${ppath}.relationName`);
      if (parentRelSeen.has(p.relationName)) {
        add(
          "DUP_PARENT_RELATION",
          `${ppath}.relationName`,
          `duplicate parent relation: "${p.relationName}" (merge into one ParentRef)`,
        );
      } else {
        // 첫 등장만 네임스페이스를 점유(중복 parent를 DUP_RELATION으로 이중보고하지 않음).
        parentRelSeen.add(p.relationName);
        claimRelation(p.relationName, `${ppath}.relationName`);
      }

      if (p.parentTypes.length === 0) {
        add("UNKNOWN_PARENT", `${ppath}.parentTypes`, `parentTypes must be non-empty`);
      }
      const seenPt = new Set<string>();
      p.parentTypes.forEach((pt, pti) => {
        if (seenPt.has(pt)) {
          add(
            "DUP_PARENT_RELATION",
            `${ppath}.parentTypes[${pti}]`,
            `duplicate parent type "${pt}" in relation "${p.relationName}"`,
          );
        }
        seenPt.add(pt);
        if (!resourceNameSet.has(pt)) {
          add("UNKNOWN_PARENT", `${ppath}.parentTypes[${pti}]`, `unknown parent type: "${pt}"`);
        }
      });
    });

    // roles: rule 1 + namespace + rule 6 (assignableBy group 존재).
    r.roles.forEach((role, roi) => {
      const rolePath = `${base}.roles[${roi}]`;
      checkName(role.name, `${rolePath}.name`);
      claimRelation(role.name, `${rolePath}.name`);
      if (role.assignableBy.length === 0) {
        add("EMPTY_SUBJECTS", `${rolePath}.assignableBy`, `role "${role.name}" must be assignable by >= 1 subject`);
      }
      role.assignableBy.forEach((ref, ai) => {
        const grp = subjectGroup(ref);
        if (grp !== null && !groupNameSet.has(grp)) {
          add("UNKNOWN_GROUP", `${rolePath}.assignableBy[${ai}].group`, `unknown group: "${grp}"`);
        }
      });
    });

    // permissions: rule 1 + namespace(can_<name>) + rule 5 + rule 4(inherit) + rule 7.
    r.permissions.forEach((perm, pei) => {
      const ppath = `${base}.permissions[${pei}]`;
      checkName(perm.name, `${ppath}.name`);
      claimRelation(`can_${perm.name}`, `${ppath}.name`);

      // condition은 예약(lazyfga-14). MVP에선 정의되면 안 됨(컴파일러가 조용히 버림 방지).
      if (perm.condition !== undefined) {
        add("CONDITION_RESERVED", `${ppath}.condition`, `permission conditions are not supported yet`);
      }

      // rule 5: grantedByRoles 비어있지 않고 각 값이 같은 type의 role.
      if (perm.grantedByRoles.length === 0) {
        add("EMPTY_GRANT", `${ppath}.grantedByRoles`, `permission must be granted by >= 1 role`);
      }
      perm.grantedByRoles.forEach((roleName, gi) => {
        if (!roleNameSet.has(roleName)) {
          add("UNKNOWN_ROLE", `${ppath}.grantedByRoles[${gi}]`, `unknown role: "${roleName}"`);
        }
      });

      // rule 4(b): inheritFromParents 각 값이 같은 type의 parent.relationName.
      perm.inheritFromParents.forEach((rel, ii) => {
        if (!parentRelSeen.has(rel)) {
          add(
            "UNKNOWN_PARENT",
            `${ppath}.inheritFromParents[${ii}]`,
            `unknown parent relation: "${rel}"`,
          );
          return;
        }
        // rule 7: 상속 부모의 모든 parentTypes가 동명 permission을 가져야 함.
        const parentRef = r.parents.find((p) => p.relationName === rel);
        parentRef?.parentTypes.forEach((pt) => {
          const perms = resourcePerms.get(pt);
          if (perms && !perms.has(perm.name)) {
            add(
              "PARENT_MISSING_PERMISSION",
              `${ppath}.inheritFromParents[${ii}]`,
              `parent type "${pt}" (via "${rel}") has no permission "${perm.name}"; ` +
                `"can_${perm.name} from ${rel}" would be invalid in OpenFGA`,
            );
          }
        });
      });
    });
  });

  return errors;
}
