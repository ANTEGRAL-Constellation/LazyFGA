import type { IdpEvent, MappingRule, TupleTemplate } from "./types";

// lazyfga-15/21: 매핑 엔진(순수/주입형). 정규 이벤트 → 규칙 매칭 → 안전한 템플릿 렌더 → tuple.
// lazyfga-21: 배열 fan-out(roleKeys 등) + 위든된 attribute(string|string[]) 지원.
// 실제 write는 ApplyDeps로 주입(테스트는 fake, 라우트는 gateway.write 개별 호출).

/** type:id (id에는 :, #, *, 공백 금지) — 주입 가드. */
const TYPE_ID_RE = /^[A-Za-z0-9_]+:[^\s:#*]+$/;
const RELATION_RE = /^[a-zA-Z0-9_]+$/;
const FORBIDDEN_IN_VALUE = /[:#*\s]/;
/** 템플릿의 type 접두는 리터럴이어야 한다(예: `team:{{x}}` ✅, `{{x}}:{{y}}` ❌). */
const LITERAL_TYPE_PREFIX = /^[A-Za-z0-9_]+:/;

/**
 * placeholder 경로 → 스칼라 값. 배열 attribute는 스칼라 슬롯에서 에러(fan-out의 {{item}}으로만 사용).
 * 지원: {{subject}}|{{subject.id}} → subject.id, {{subject.type}} → subject.type, {{type}} → 이벤트 타입,
 *       {{attributes.<k>}} → 스칼라 attribute, {{item}} → fan-out 원소(item 제공 시).
 */
function scalarValue(
  ev: IdpEvent,
  path: string,
  item: string | undefined,
): { value?: string; error?: string } {
  if (path === "item") {
    if (item === undefined) return { error: `{{item}} used without fan-out` };
    return { value: item };
  }
  if (path === "type") return { value: ev.type };
  if (path === "subject" || path === "subject.id") return { value: ev.subject.id };
  if (path === "subject.type") return { value: ev.subject.type };
  if (path.startsWith("attributes.")) {
    const v = ev.attributes[path.slice("attributes.".length)];
    if (v === undefined) return { error: `unresolved {{${path}}}` };
    if (Array.isArray(v)) return { error: `attribute {{${path}}} is an array; use fan-out ({{item}})` };
    return { value: v };
  }
  return { error: `unresolved {{${path}}}` };
}

/** match 술어용 필드 값(스칼라만; 배열/미해결은 undefined → 매칭 실패). */
function matchValue(ev: IdpEvent, path: string): string | undefined {
  const r = scalarValue(ev, path, undefined);
  return r.value;
}

export function matchRule(rule: MappingRule, ev: IdpEvent): boolean {
  if (rule.eventType !== ev.type) return false;
  if (!rule.match.every((m) => matchValue(ev, m.field) === m.equals)) return false;
  // fan-out 규칙은 그 배열 attribute가 비어있지 않을 때만 매칭(펼칠 원소가 있어야 함).
  if (rule.fanOut !== undefined) {
    const arr = ev.attributes[rule.fanOut];
    if (!Array.isArray(arr) || arr.length === 0) return false;
  }
  return true;
}

export interface RenderedTuple {
  user: string;
  relation: string;
  object: string;
}

/** 템플릿 렌더 + 주입 가드. 미해결 placeholder/형식 위반이면 error. item은 fan-out 원소(선택). */
export function renderTuple(
  t: TupleTemplate,
  ev: IdpEvent,
  item?: string,
): { tuple?: RenderedTuple; error?: string } {
  // 타입 접두는 템플릿 리터럴이어야 한다(이벤트 필드가 객체/주체 타입을 정하지 못하게).
  if (!LITERAL_TYPE_PREFIX.test(t.user))
    return { error: `user template must start with a literal type: prefix` };
  if (!LITERAL_TYPE_PREFIX.test(t.object))
    return { error: `object template must start with a literal type: prefix` };
  const sub = (s: string): { value?: string; error?: string } => {
    const placeholders = [...s.matchAll(/\{\{([^}]+)\}\}/g)].map((m) => m[1]!);
    let out = s;
    for (const path of placeholders) {
      const { value, error } = scalarValue(ev, path, item);
      if (error !== undefined || value === undefined) return { error: error ?? `unresolved {{${path}}}` };
      if (FORBIDDEN_IN_VALUE.test(value))
        return { error: `value for {{${path}}} contains forbidden char (:#* or space)` };
      out = out.split(`{{${path}}}`).join(value);
    }
    return { value: out };
  };

  const u = sub(t.user);
  const r = sub(t.relation);
  const o = sub(t.object);
  const err = u.error ?? r.error ?? o.error;
  if (err) return { error: err };
  const tuple: RenderedTuple = { user: u.value!, relation: r.value!, object: o.value! };
  if (!TYPE_ID_RE.test(tuple.user)) return { error: `invalid user "${tuple.user}" (need type:id)` };
  if (!TYPE_ID_RE.test(tuple.object))
    return { error: `invalid object "${tuple.object}" (need type:id)` };
  if (!RELATION_RE.test(tuple.relation)) return { error: `invalid relation "${tuple.relation}"` };
  return { tuple };
}

/** write 실패 분류: transient면 502로 올려 IdP 재전송 유도, 아니면 결정적 실패로 카운트. */
export class WriteError extends Error {
  constructor(
    public readonly transient: boolean,
    message: string,
  ) {
    super(message);
    this.name = "WriteError";
  }
}

export interface ApplyDeps {
  /** "applied" | "skipped"(멱등 no-op). 실패 시 WriteError throw. */
  writeTuple(op: "write" | "delete", tuple: RenderedTuple): Promise<"applied" | "skipped">;
  audit(action: string, data: Record<string, unknown>): void;
}

export interface ApplyResult {
  applied: number;
  skipped: number;
  failed: number;
}

/** rule이 fan-out이면 배열 원소 목록, 아니면 [undefined](단일 렌더). */
function fanOutItems(rule: MappingRule, ev: IdpEvent): (string | undefined)[] {
  if (rule.fanOut === undefined) return [undefined];
  const arr = ev.attributes[rule.fanOut];
  return Array.isArray(arr) ? arr : [];
}

/** 이벤트들에 규칙을 적용(개별·멱등). transient write 오류는 위로 던진다(→ 502). */
export async function applyEvents(
  events: IdpEvent[],
  rules: MappingRule[],
  deps: ApplyDeps,
): Promise<ApplyResult> {
  let applied = 0;
  let skipped = 0;
  let failed = 0;
  const sorted = [...rules].sort((a, b) => a.priority - b.priority);

  for (const ev of events) {
    const matched = sorted.filter((r) => matchRule(r, ev));
    if (matched.length === 0) {
      skipped++;
      deps.audit("idp.tuple.skip", { event: ev.type, reason: "no matching rule" });
      continue;
    }
    for (const rule of matched) {
      // fan-out: 배열 원소별 1 tuple. 각 원소를 개별 렌더 + 가드(나쁜 원소만 failed, 나머지 진행).
      for (const item of fanOutItems(rule, ev)) {
        const { tuple, error } = renderTuple(rule.tupleTemplate, ev, item);
        if (error || !tuple) {
          failed++;
          deps.audit("idp.tuple.error", { event: ev.type, error });
          continue;
        }
        try {
          const result = await deps.writeTuple(rule.op, tuple);
          if (result === "applied") {
            applied++;
            deps.audit(`idp.tuple.${rule.op}`, { tuple });
          } else {
            skipped++;
          }
        } catch (e) {
          if (e instanceof WriteError && e.transient) throw e; // → 502, IdP가 재전송
          failed++;
          deps.audit("idp.tuple.error", { tuple, error: String(e) });
        }
      }
    }
  }
  return { applied, skipped, failed };
}
