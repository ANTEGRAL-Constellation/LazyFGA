import {
  grantTupleKey,
  isAssignableRelation,
  revokeTupleKey,
  subjectToUser,
  validateGrant,
  validateRevoke,
  type GrantEntry,
  type GrantErrorCode,
  type GrantRequest,
  type GrantSubject,
  type ModelIR,
  type RevokeRequest,
} from "@lazyfga/shared";
import { recordAudit } from "../audit/audit";
import { getCurrentVersion } from "../model/model.repo";
import { gateway, type ReadTuple } from "../../openfga";
import { classifyWriteError, isTransientApiError } from "../../openfga/write-error";

// lazyfga-20: 구조적 권한 grant/revoke. 발행본 모델로 검증 → gateway로 단일 tuple write/delete → audit.

/** grant/revoke 실패를 HTTP 상태로 표면화. */
export class GrantError extends Error {
  constructor(
    public readonly status: 400 | 404 | 502,
    public readonly code: GrantErrorCode,
    public readonly detail: string,
  ) {
    super(detail);
    this.name = "GrantError";
  }
}

/** 발행본 ModelIR + OpenFGA authorization_model_id 해석(lazyfga-9). 미발행 → 404. */
async function publishedModel(): Promise<{ ir: ModelIR; modelId: string }> {
  const cur = await getCurrentVersion();
  if (!cur) throw new GrantError(404, "no_published_model", "no model has been published yet");
  return { ir: cur.irJson, modelId: cur.authorizationModelId };
}

/**
 * OpenFGA write/delete 오류 → 결과 해석(순수, 테스트 가능).
 * - 멱등(duplicate write / missing delete) → no-op(상태 변화 아님).
 * - transient → 502, 그 외 결정적 4xx → 400 backstop(우리 검증을 빠져나간 모델/타입 불일치 등).
 */
export function interpretWriteError(
  e: unknown,
  op: "write" | "delete",
): { noop: true } | { error: GrantError } {
  const { idempotent, transient } = classifyWriteError(e, op);
  if (idempotent) return { noop: true };
  if (transient) return { error: new GrantError(502, "openfga_unavailable", String(e)) };
  return { error: new GrantError(400, "openfga_invalid_input", String(e)) };
}

/** 권한 부여. 새 tuple → {created:true}(감사됨), 이미 존재 → {created:false}(no-op, 미감사). */
export async function grant(req: GrantRequest, actor: string): Promise<{ created: boolean }> {
  const { ir, modelId } = await publishedModel();
  const v = validateGrant(ir, req);
  if (!v.ok) throw new GrantError(400, v.code, v.message);
  try {
    await gateway.write({ writes: [grantTupleKey(req)] }, { authorizationModelId: modelId });
  } catch (e) {
    const r = interpretWriteError(e, "write");
    if ("error" in r) throw r.error;
    return { created: false };
  }
  recordAudit(
    "permission.grant",
    {
      subject: req.subject,
      relation: req.relation,
      resource: req.resource,
      condition: req.condition,
    },
    actor,
  );
  return { created: true };
}

/** 권한 회수. 실제 삭제 → {deleted:true}(감사됨), 이미 없음 → {deleted:false}(no-op, 미감사). */
export async function revoke(req: RevokeRequest, actor: string): Promise<{ deleted: boolean }> {
  const { ir, modelId } = await publishedModel();
  const v = validateRevoke(ir, req);
  if (!v.ok) throw new GrantError(400, v.code, v.message);
  try {
    await gateway.write({ deletes: [revokeTupleKey(req)] }, { authorizationModelId: modelId });
  } catch (e) {
    const r = interpretWriteError(e, "delete");
    if ("error" in r) throw r.error;
    return { deleted: false };
  }
  recordAudit(
    "permission.revoke",
    { subject: req.subject, relation: req.relation, resource: req.resource },
    actor,
  );
  return { deleted: true };
}

// ── 조회(GET) ──────────────────────────────────────────────────────────────────

/** type:id 또는 type:id#relation(신뢰된 read-back) → GrantSubject(관대 분리). */
function splitObject(s: string): { type: string; id: string } {
  const i = s.indexOf(":");
  return i < 0 ? { type: s, id: "" } : { type: s.slice(0, i), id: s.slice(i + 1) };
}
function tupleToEntry(t: ReadTuple): GrantEntry {
  const hash = t.user.indexOf("#");
  const subject: GrantSubject =
    hash >= 0
      ? { ...splitObject(t.user.slice(0, hash)), relation: t.user.slice(hash + 1) }
      : splitObject(t.user);
  const entry: GrantEntry = { subject, relation: t.relation, resource: splitObject(t.object) };
  if (t.condition) entry.condition = t.condition;
  return entry;
}

/** read 실패 분류: transient(5xx/429/네트워크) → 502, 결정적 4xx → 400(§5-2). */
async function readTuples(input: { user?: string; object?: string }): Promise<ReadTuple[]> {
  try {
    const { tuples } = await gateway.read(input);
    return tuples;
  } catch (e) {
    if (isTransientApiError(e)) throw new GrantError(502, "openfga_unavailable", String(e));
    throw new GrantError(400, "openfga_invalid_input", String(e));
  }
}

/** 리소스 위 배정 목록(단일 Read). 배정 가능 relation으로만 필터(parent edge 등 제외). */
export async function listByResource(resource: {
  type: string;
  id: string;
}): Promise<GrantEntry[]> {
  const { ir } = await publishedModel();
  const tuples = await readTuples({ object: `${resource.type}:${resource.id}` });
  return tuples
    .filter((t) => isAssignableRelation(ir, resource.type, t.relation))
    .map(tupleToEntry);
}

/**
 * 주체가 보유한 배정 목록. OpenFGA Read는 user 필터에 object **타입**을 요구하므로:
 * resourceType 지정 → Read 1회, 미지정 → 발행본의 모든 resource/group 타입에 대해 fan-out 후 병합.
 */
export async function listBySubject(
  subject: GrantSubject,
  resourceType?: string,
): Promise<GrantEntry[]> {
  const { ir } = await publishedModel();
  const user = subjectToUser(subject);
  const types = resourceType
    ? [resourceType]
    : [...ir.resources.map((r) => r.name), ...ir.groups.map((g) => g.name)];
  const entries: GrantEntry[] = [];
  for (const t of types) {
    const tuples = await readTuples({ user, object: `${t}:` });
    for (const tup of tuples) {
      if (isAssignableRelation(ir, t, tup.relation)) entries.push(tupleToEntry(tup));
    }
  }
  return entries;
}
