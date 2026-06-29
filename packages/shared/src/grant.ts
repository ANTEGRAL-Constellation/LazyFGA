// lazyfga-20: 권한 grant/revoke 계약 + 순수 검증기(isomorphic).
// "구조적 권한 배정" — admin이 모델에 정의된 *직접 배정 가능* relation에 한해
// (subject, relation, resource) tuple을 write/delete 한다. raw tuple 편집이 아니다.
// validateGrant/validateRevoke는 OpenFGA 없이 단위 테스트 가능한 조기 UX 게이트이며,
// OpenFGA가 write 시점의 최종 권위자다(validateModelIR와 같은 철학, lazyfga-5).
import { z } from "zod";
import { IDENT_RE } from "./ident";
import type { ModelIR, SubjectRef } from "./model";

/**
 * 배정 대상 주체. relation 없으면 concrete user(`user:<id>`),
 * 있으면 userset(`<group>:<id>#<relation>`, 모델상 group의 member). (lazyfga-20 §5)
 */
export interface GrantSubject {
  type: string;
  id: string;
  /** userset일 때만. 모델상 group member 관계명("member"). */
  relation?: string;
}

export interface GrantRequest {
  subject: GrantSubject;
  /** resource.type 위에서 직접 배정 가능해야 한다(role 또는 group member). */
  relation: string;
  resource: { type: string; id: string };
  /** lazyfga-14 조건(선택). name은 모델에 존재 + 해당 subject type restriction에 부착돼 있어야 함. */
  condition?: { name: string; context?: Record<string, unknown> };
}

/** revoke는 (user, relation, object) 키로만 삭제한다 — 조건은 tuple 정체성의 일부가 아니다. */
export type RevokeRequest = Omit<GrantRequest, "condition">;

export type GrantErrorCode =
  // validateGrant/validateRevoke 가 반환:
  | "malformed_request"
  | "relation_not_assignable"
  | "subject_type_not_allowed"
  | "unknown_condition"
  | "condition_not_permitted"
  | "condition_required"
  // service/route 가 반환:
  | "no_published_model"
  | "openfga_invalid_input"
  | "openfga_unavailable";

export type GrantValidation = { ok: true } | { ok: false; code: GrantErrorCode; message: string };

// ── 런타임 스키마(zod) — 신뢰 못 할 요청 본문 파싱용(형태만, 의미는 validate*) ──────────
const grantSubjectSchema = z.object({
  type: z.string(),
  id: z.string(),
  relation: z.string().optional(),
});

export const grantRequestSchema: z.ZodType<GrantRequest> = z.object({
  subject: grantSubjectSchema,
  relation: z.string(),
  resource: z.object({ type: z.string(), id: z.string() }),
  condition: z
    .object({ name: z.string(), context: z.record(z.unknown()).optional() })
    .optional(),
});

export const revokeRequestSchema: z.ZodType<RevokeRequest> = z.object({
  subject: grantSubjectSchema,
  relation: z.string(),
  resource: z.object({ type: z.string(), id: z.string() }),
});

// ── tuple 키 빌더(순수) ────────────────────────────────────────────────────────
// shared는 @openfga/sdk에 의존하지 않으므로 plain shape를 반환한다(구조적으로 TupleKey 호환).

/** subject → OpenFGA user 문자열. user:alice 또는 team:eng#member. */
export function subjectToUser(s: GrantSubject): string {
  return s.relation ? `${s.type}:${s.id}#${s.relation}` : `${s.type}:${s.id}`;
}

export interface GrantTupleKey {
  user: string;
  relation: string;
  object: string;
  condition?: { name: string; context?: Record<string, unknown> };
}

export function grantTupleKey(req: GrantRequest): GrantTupleKey {
  const key: GrantTupleKey = {
    user: subjectToUser(req.subject),
    relation: req.relation,
    object: `${req.resource.type}:${req.resource.id}`,
  };
  if (req.condition) {
    key.condition = req.condition.context
      ? { name: req.condition.name, context: req.condition.context }
      : { name: req.condition.name };
  }
  return key;
}

export function revokeTupleKey(req: RevokeRequest): { user: string; relation: string; object: string } {
  return {
    user: subjectToUser(req.subject),
    relation: req.relation,
    object: `${req.resource.type}:${req.resource.id}`,
  };
}

// ── 검증 로직 ──────────────────────────────────────────────────────────────────
// type:id 의 id에 :,#,*,공백 금지(tuple 구조/OpenFGA 의미 파손 방지 — mapping.ts와 동일 규칙).
const FORBIDDEN_IN_ID = /[:#*\s]/;

const fail = (
  code: GrantErrorCode,
  message: string,
): { ok: false; code: GrantErrorCode; message: string } => ({ ok: false, code, message });

/**
 * (resource.type, relation)이 *직접 배정 가능*하면 허용 SubjectRef[]를 반환, 아니면 null.
 * lazyfga의 모델에서 직접 배정 가능 relation은 정확히 두 종류뿐이다:
 *   1) ResourceType.roles[].name        → assignableBy (user | <group>#member)
 *   2) GroupType.member                 → memberTypes  (user | <group>#member)
 * permission(can_*)·parent relation은 제외된다(전자는 계산식, 후자의 주체는 user/userset이
 * 아닌 object 참조라 §3.1의 subject 범위[user|userset] 밖이며 "상속은 모델로 표현"한다 §3.2).
 */
function assignableSubjects(model: ModelIR, type: string, relation: string): SubjectRef[] | null {
  const res = model.resources.find((r) => r.name === type);
  if (res) {
    const role = res.roles.find((r) => r.name === relation);
    return role ? role.assignableBy : null;
  }
  const grp = model.groups.find((g) => g.name === type);
  if (grp) {
    return relation === "member" ? grp.memberTypes : null;
  }
  return null;
}

/** subject가 이 SubjectRef(허용 항목)와 type/userset 형태가 일치하는가(조건은 별도 검사). */
function subjectMatchesRef(ref: SubjectRef, s: GrantSubject): boolean {
  if (ref.kind === "user") {
    // 비-userset 직접 주체는 user 베이스 타입뿐이다.
    return s.relation === undefined && s.type === "user";
  }
  // group member userset: 같은 group + relation === "member".
  return s.relation === ref.relation && s.type === ref.group;
}

/** 형태(식별자/금지문자) + 배정 가능성 검사. 매칭된 SubjectRef 후보를 함께 돌려준다. */
function structural(
  model: ModelIR,
  req: RevokeRequest,
): { ok: true; candidates: SubjectRef[] } | { ok: false; code: GrantErrorCode; message: string } {
  const { subject, relation, resource } = req;
  if (!IDENT_RE.test(subject.type)) return fail("malformed_request", `invalid subject.type "${subject.type}"`);
  if (subject.id === "" || FORBIDDEN_IN_ID.test(subject.id))
    return fail("malformed_request", `invalid subject.id "${subject.id}"`);
  if (subject.relation !== undefined && !IDENT_RE.test(subject.relation))
    return fail("malformed_request", `invalid subject.relation "${subject.relation}"`);
  if (!IDENT_RE.test(relation)) return fail("malformed_request", `invalid relation "${relation}"`);
  if (!IDENT_RE.test(resource.type)) return fail("malformed_request", `invalid resource.type "${resource.type}"`);
  if (resource.id === "" || FORBIDDEN_IN_ID.test(resource.id))
    return fail("malformed_request", `invalid resource.id "${resource.id}"`);

  const allowed = assignableSubjects(model, resource.type, relation);
  if (allowed === null)
    return fail(
      "relation_not_assignable",
      `relation "${relation}" is not a directly-assignable role/membership on type "${resource.type}"`,
    );
  const candidates = allowed.filter((ref) => subjectMatchesRef(ref, subject));
  if (candidates.length === 0)
    return fail(
      "subject_type_not_allowed",
      `subject "${subjectToUser(subject)}" type is not allowed for relation "${relation}" on "${resource.type}"`,
    );
  return { ok: true, candidates };
}

/**
 * grant 검증: 구조(배정 가능성) + 조건 규칙(lazyfga-14).
 * - condition 있음: name이 모델에 존재 + 매칭 SubjectRef 중 하나가 그 조건을 부착하고 있어야 함.
 * - condition 없음: 매칭 SubjectRef 중 조건 없는 항목이 하나는 있어야 함(전부 조건부면 거부 —
 *   OpenFGA가 `[user with cond]`만인 type restriction에 무조건 write를 거부하기 때문).
 */
export function validateGrant(model: ModelIR, req: GrantRequest): GrantValidation {
  const s = structural(model, req);
  if (!s.ok) return s;

  if (req.condition) {
    const name = req.condition.name;
    if (!IDENT_RE.test(name)) return fail("malformed_request", `invalid condition name "${name}"`);
    const known = (model.conditions ?? []).some((c) => c.name === name);
    if (!known) return fail("unknown_condition", `unknown condition "${name}"`);
    const permitted = s.candidates.some((ref) => ref.condition === name);
    if (!permitted)
      return fail(
        "condition_not_permitted",
        `condition "${name}" is not attached to relation "${req.relation}" for this subject type`,
      );
    return { ok: true };
  }

  const hasConditionless = s.candidates.some((ref) => ref.condition === undefined);
  if (!hasConditionless)
    return fail(
      "condition_required",
      `relation "${req.relation}" requires a condition for this subject type`,
    );
  return { ok: true };
}

/** revoke 검증: 구조만(삭제는 조건 무관 — (user, relation, object) 키로만 동작). */
export function validateRevoke(model: ModelIR, req: RevokeRequest): GrantValidation {
  const s = structural(model, req);
  return s.ok ? { ok: true } : s;
}

/** (type, relation)이 직접 배정 가능 relation인가(role 또는 group member). GET 필터용. */
export function isAssignableRelation(model: ModelIR, type: string, relation: string): boolean {
  return assignableSubjects(model, type, relation) !== null;
}

// ── 조회용 표현 + 쿼리 파서 ─────────────────────────────────────────────────────

/** 현재 배정 1건(GET /grants 응답 원소). */
export interface GrantEntry {
  subject: GrantSubject;
  relation: string;
  resource: { type: string; id: string };
  condition?: { name: string; context?: Record<string, unknown> };
}

/** `type:id` 파싱(신뢰 못 할 쿼리 입력용 — 식별자/금지문자 엄격 검사). 실패 시 null. */
export function parseResourceRef(s: string): { type: string; id: string } | null {
  const i = s.indexOf(":");
  if (i <= 0) return null;
  const type = s.slice(0, i);
  const id = s.slice(i + 1);
  if (!IDENT_RE.test(type) || id === "" || FORBIDDEN_IN_ID.test(id)) return null;
  return { type, id };
}

/** `type:id` 또는 `type:id#relation`(userset) 파싱(엄격). 실패 시 null. */
export function parseGrantSubject(s: string): GrantSubject | null {
  const hash = s.indexOf("#");
  if (hash >= 0) {
    const obj = parseResourceRef(s.slice(0, hash));
    const relation = s.slice(hash + 1);
    if (!obj || !IDENT_RE.test(relation)) return null;
    return { ...obj, relation };
  }
  const obj = parseResourceRef(s);
  return obj ? { type: obj.type, id: obj.id } : null;
}
