import type { ModelIR, ResourceType, Role, SubjectRef } from "@lazyfga/shared";

export type DiffChange =
  | { kind: "TYPE_ADDED" | "TYPE_REMOVED"; type: string }
  | { kind: "ROLE_ADDED" | "ROLE_REMOVED"; type: string; role: string }
  | { kind: "PERMISSION_ADDED" | "PERMISSION_REMOVED"; type: string; permission: string }
  | { kind: "GRANT_CHANGED"; type: string; permission: string; added: string[]; removed: string[] }
  | {
      kind: "ROLE_ASSIGNABLE_CHANGED";
      type: string;
      role: string;
      added: string[];
      removed: string[];
    }
  | {
      kind: "PERMISSION_INHERIT_CHANGED";
      type: string;
      permission: string;
      added: string[];
      removed: string[];
    }
  | {
      kind: "PARENT_ADDED" | "PARENT_REMOVED";
      type: string;
      relationName: string;
      parentType: string;
    };

const typeNames = (ir: ModelIR): string[] => [
  ...ir.groups.map((g) => g.name),
  ...ir.resources.map((r) => r.name),
];

const resourceByName = (ir: ModelIR): Map<string, ResourceType> =>
  new Map(ir.resources.map((r) => [r.name, r]));

const subjectKey = (ref: SubjectRef): string =>
  ref.kind === "group" ? `${ref.group}#member` : "user";

const subjectKeys = (role: Role): Set<string> => new Set(role.assignableBy.map(subjectKey));

// 식별자에 절대 나타날 수 없는 NUL 구분자(이름 충돌/스페이스 무관).
const SEP = "\u0000";
const parentPairs = (r: ResourceType): Set<string> => {
  const s = new Set<string>();
  for (const p of r.parents) for (const pt of p.parentTypes) s.add(`${p.relationName}${SEP}${pt}`);
  return s;
};

const setDiff = (from: Set<string>, to: Set<string>): { added: string[]; removed: string[] } => ({
  added: [...to].filter((x) => !from.has(x)).sort(),
  removed: [...from].filter((x) => !to.has(x)).sort(),
});

/** 두 IR의 구조 diff(결정적, 바이트 정렬). 의미: from → to 변경. */
export function diffModels(from: ModelIR, to: ModelIR): DiffChange[] {
  const changes: DiffChange[] = [];
  const fromTypes = new Set(typeNames(from));
  const toTypes = new Set(typeNames(to));

  for (const t of toTypes) if (!fromTypes.has(t)) changes.push({ kind: "TYPE_ADDED", type: t });
  for (const t of fromTypes) if (!toTypes.has(t)) changes.push({ kind: "TYPE_REMOVED", type: t });

  const fromRes = resourceByName(from);
  const toRes = resourceByName(to);

  for (const [name, tr] of toRes) {
    const fr = fromRes.get(name);
    if (!fr) continue; // 새 타입은 TYPE_ADDED로 표기

    const frRoles = new Map(fr.roles.map((r) => [r.name, r]));
    const trRoles = new Map(tr.roles.map((r) => [r.name, r]));
    for (const [rn, tRole] of trRoles) {
      const fRole = frRoles.get(rn);
      if (!fRole) {
        changes.push({ kind: "ROLE_ADDED", type: name, role: rn });
        continue;
      }
      const { added, removed } = setDiff(subjectKeys(fRole), subjectKeys(tRole));
      if (added.length || removed.length) {
        changes.push({ kind: "ROLE_ASSIGNABLE_CHANGED", type: name, role: rn, added, removed });
      }
    }
    for (const rn of frRoles.keys()) {
      if (!trRoles.has(rn)) changes.push({ kind: "ROLE_REMOVED", type: name, role: rn });
    }

    const frPerms = new Map(fr.permissions.map((p) => [p.name, p]));
    const trPerms = new Map(tr.permissions.map((p) => [p.name, p]));
    for (const [pname, tp] of trPerms) {
      const fp = frPerms.get(pname);
      if (!fp) {
        changes.push({ kind: "PERMISSION_ADDED", type: name, permission: pname });
        continue;
      }
      const grant = setDiff(new Set(fp.grantedByRoles), new Set(tp.grantedByRoles));
      if (grant.added.length || grant.removed.length) {
        changes.push({ kind: "GRANT_CHANGED", type: name, permission: pname, ...grant });
      }
      const inh = setDiff(new Set(fp.inheritFromParents), new Set(tp.inheritFromParents));
      if (inh.added.length || inh.removed.length) {
        changes.push({ kind: "PERMISSION_INHERIT_CHANGED", type: name, permission: pname, ...inh });
      }
    }
    for (const pname of frPerms.keys()) {
      if (!trPerms.has(pname))
        changes.push({ kind: "PERMISSION_REMOVED", type: name, permission: pname });
    }

    const fp = parentPairs(fr);
    const tp = parentPairs(tr);
    for (const pair of tp)
      if (!fp.has(pair)) {
        const [relationName, parentType] = pair.split(SEP);
        changes.push({
          kind: "PARENT_ADDED",
          type: name,
          relationName: relationName!,
          parentType: parentType!,
        });
      }
    for (const pair of fp)
      if (!tp.has(pair)) {
        const [relationName, parentType] = pair.split(SEP);
        changes.push({
          kind: "PARENT_REMOVED",
          type: name,
          relationName: relationName!,
          parentType: parentType!,
        });
      }
  }

  // 결정적·로케일 비의존 정렬(바이트 비교).
  return changes
    .map((c) => ({ c, k: JSON.stringify(c) }))
    .sort((a, b) => (a.k < b.k ? -1 : a.k > b.k ? 1 : 0))
    .map((x) => x.c);
}
