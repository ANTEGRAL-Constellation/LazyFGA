import type { MissingLink, ModelIR, ReasonResult, ReasonStep } from "@lazyfga/shared";
import { gateway } from "../../openfga";
import { getCurrentVersion } from "../model/model.repo";

const MAX_DEPTH = 8;

/** explain이 의존하는 OpenFGA 연산(테스트 시 주입 가능). */
export interface ReasonDeps {
  check(
    input: { user: string; relation: string; object: string; context?: Record<string, unknown> },
    opts?: { authorizationModelId?: string },
  ): Promise<{ allowed: boolean }>;
  read(input: {
    user?: string;
    relation?: string;
    object?: string;
  }): Promise<{ tuples: { user: string; relation: string; object: string }[] }>;
}

const gatewayDeps: ReasonDeps = {
  check: (i, o) => gateway.check(i, o),
  read: (i) => gateway.read(i),
};

/** 결정과 reason이 같은 모델 버전을 쓰도록 evaluate가 핀을 전달한다. */
export interface ReasonPin {
  decision: boolean;
  authorizationModelId: string;
  ir: ModelIR;
}

interface ExplainCtx {
  authorizationModelId: string;
  ir: ModelIR;
  context?: Record<string, unknown>;
  deps: ReasonDeps;
  /** (permission|object) 방문 집합: cycle·재수렴 그래프에서 재방문/조합폭발 방지. */
  visited: Set<string>;
}

function splitObject(object: string): { type: string; id: string } | null {
  const i = object.indexOf(":");
  if (i < 0) return null;
  return { type: object.slice(0, i), id: object.slice(i + 1) };
}

/**
 * (object, role) tuple로 직접/그룹 경유를 판별. Check는 true였는데 직접도 확인된 그룹도
 * 아니면(와일드카드/비-member userset/페이지 밖 등) incomplete=true로 정직하게 표기.
 */
async function classifyRoleStep(
  user: string,
  role: string,
  object: string,
  onType: string,
  ctx: ExplainCtx,
): Promise<{ step: ReasonStep; incomplete: boolean }> {
  const { tuples } = await ctx.deps.read({ object, relation: role });
  if (tuples.some((t) => t.user === user)) {
    return { step: { via: "role", role, on: onType, direct: true }, incomplete: false };
  }
  for (const t of tuples) {
    const hash = t.user.indexOf("#");
    if (hash > 0 && t.user.slice(hash + 1) === "member") {
      const groupObject = t.user.slice(0, hash); // 예: team:eng
      const { allowed } = await ctx.deps.check(
        { user, relation: "member", object: groupObject, context: ctx.context },
        { authorizationModelId: ctx.authorizationModelId },
      );
      if (allowed) {
        return {
          step: {
            via: "role",
            role,
            on: onType,
            direct: false,
            group: splitObject(groupObject)?.type ?? groupObject,
            groupObject, // 인스턴스 수준 설명용 구체 id(예: team:eng)
          },
          incomplete: false,
        };
      }
    }
  }
  return { step: { via: "role", role, on: onType, direct: false }, incomplete: true };
}

interface WitnessResult {
  found: boolean;
  path?: ReasonStep[];
  /** 깊이/페이지 한도 또는 role-step 분류 실패로 설명이 불완전한지. */
  truncated: boolean;
}

/** 존재 증명(bounded): role 직접/그룹 → parent 상속 재귀 중 최초 성립 경로. */
async function findWitness(
  user: string,
  permission: string,
  object: string,
  ctx: ExplainCtx,
  depth: number,
): Promise<WitnessResult> {
  if (depth > MAX_DEPTH) return { found: false, truncated: true };
  const visitKey = `${permission}|${object}`;
  if (ctx.visited.has(visitKey)) return { found: false, truncated: false }; // cycle/재수렴 가드
  ctx.visited.add(visitKey);
  const parsed = splitObject(object);
  if (!parsed) return { found: false, truncated: false };
  const resource = ctx.ir.resources.find((r) => r.name === parsed.type);
  const perm = resource?.permissions.find((p) => p.name === permission);
  if (!perm) return { found: false, truncated: false };

  for (const role of perm.grantedByRoles) {
    const { allowed } = await ctx.deps.check(
      { user, relation: role, object, context: ctx.context },
      { authorizationModelId: ctx.authorizationModelId },
    );
    if (allowed) {
      const { step, incomplete } = await classifyRoleStep(user, role, object, parsed.type, ctx);
      return { found: true, path: [step], truncated: incomplete };
    }
  }

  // 형제 parent들을 끝까지 탐색: 한 가지가 깊이 초과여도 다른 깨끗한 witness를 놓치지 않는다.
  // read는 store-level tuple 목록이라 모델 버전과 무관(Check만 모델 핀이 필요).
  let sawTruncation = false;
  for (const rel of perm.inheritFromParents) {
    const { tuples } = await ctx.deps.read({ object, relation: rel });
    for (const t of tuples) {
      const child = await findWitness(user, permission, t.user, ctx, depth + 1);
      if (child.found && child.path) {
        const parentType = splitObject(t.user)?.type ?? rel;
        return {
          found: true,
          path: [
            { via: "parent", relation: rel, parent: parentType, parentObject: t.user },
            ...child.path,
          ],
          truncated: child.truncated,
        };
      }
      if (child.truncated) sawTruncation = true;
    }
  }
  return { found: false, truncated: sawTruncation };
}

function describePath(user: string, permission: string, object: string, path: ReasonStep[]): string {
  const parts = path.map((s) =>
    s.via === "role"
      ? `role ${s.role}${s.direct ? " (direct)" : s.groupObject ? ` (via ${s.groupObject} membership)` : ""}`
      : `inherited via ${s.relation} from ${s.parentObject ?? s.parent}`,
  );
  return `${user} can ${permission} ${object}: ${parts.join(" → ")}`;
}

function describeMissing(object: string, links: MissingLink[]): string {
  const bits = links.map((l) =>
    l.kind === "role" ? `one of [${l.anyOf.join(", ")}] on ${object}` : `${l.needs} via parent (${l.relation})`,
  );
  return `denied: needs ${bits.length ? bits.join(", or ") : "a grant that does not exist in the model"}`;
}

function denyLinks(ir: ModelIR, onType: string, permission: string): MissingLink[] {
  const perm = ir.resources.find((r) => r.name === onType)?.permissions.find((p) => p.name === permission);
  const links: MissingLink[] = [];
  if (perm) {
    if (perm.grantedByRoles.length > 0) links.push({ kind: "role", anyOf: perm.grantedByRoles, on: onType });
    for (const rel of perm.inheritFromParents) links.push({ kind: "parent", relation: rel, needs: `can_${permission}` });
  }
  return links;
}

/**
 * 결정에 대한 사람이 읽는 reason. 허용=witnessing path, 거부=missing links(비대칭).
 * pin이 주어지면 그 모델 버전/decision을 그대로 사용(evaluate와 동일 버전 보장, DB 재조회 없음).
 */
export async function explain(
  user: string,
  permission: string,
  object: string,
  context?: Record<string, unknown>,
  pin?: ReasonPin,
  deps: ReasonDeps = gatewayDeps,
): Promise<ReasonResult> {
  let authorizationModelId: string;
  let ir: ModelIR;
  let decision: boolean;

  if (pin) {
    ({ authorizationModelId, ir, decision } = pin);
  } else {
    const current = await getCurrentVersion();
    if (!current) return { decision: false, text: "model not published" };
    authorizationModelId = current.authorizationModelId;
    ir = current.irJson;
    const { allowed } = await deps.check(
      { user, relation: `can_${permission}`, object, context },
      { authorizationModelId },
    );
    decision = allowed;
  }

  const ctx: ExplainCtx = { authorizationModelId, ir, context, deps, visited: new Set() };
  const onType = splitObject(object)?.type ?? object;

  if (decision) {
    const w = await findWitness(user, permission, object, ctx, 0);
    if (w.found && w.path) {
      const text = describePath(user, permission, object, w.path);
      return w.truncated
        ? { decision: true, path: w.path, truncated: true, text: `${text} (partial)` }
        : { decision: true, path: w.path, text };
    }
    return {
      decision: true,
      truncated: true,
      text: `allowed via can_${permission} (path reconstruction incomplete)`,
    };
  }

  const missingLinks = denyLinks(ir, onType, permission);
  return { decision: false, missingLinks, text: describeMissing(object, missingLinks) };
}
