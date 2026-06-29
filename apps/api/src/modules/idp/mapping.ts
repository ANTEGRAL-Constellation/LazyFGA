import type { IdpEvent, MappingRule, TupleTemplate } from "./types";

// lazyfga-15: 매핑 엔진(순수/주입형). 정규 이벤트 → 규칙 매칭 → 안전한 템플릿 렌더 → tuple.
// 실제 write는 ApplyDeps로 주입(테스트는 fake, 라우트는 gateway.write 개별 호출).

/** type:id (id에는 :, #, *, 공백 금지) — 주입 가드. */
const TYPE_ID_RE = /^[A-Za-z0-9_]+:[^\s:#*]+$/;
const RELATION_RE = /^[a-zA-Z0-9_]+$/;
const FORBIDDEN_IN_VALUE = /[:#*\s]/;
/** 템플릿의 type 접두는 리터럴이어야 한다(예: `team:{{x}}` ✅, `{{x}}:{{y}}` ❌). */
const LITERAL_TYPE_PREFIX = /^[A-Za-z0-9_]+:/;

function fieldValue(ev: IdpEvent, path: string): string | undefined {
  if (path === "type") return ev.type;
  if (path === "subject.id") return ev.subject.id;
  if (path.startsWith("attributes.")) return ev.attributes[path.slice("attributes.".length)];
  return undefined;
}

export function matchRule(rule: MappingRule, ev: IdpEvent): boolean {
  if (rule.eventType !== ev.type) return false;
  return rule.match.every((m) => fieldValue(ev, m.field) === m.equals);
}

export interface RenderedTuple {
  user: string;
  relation: string;
  object: string;
}

/** 템플릿 렌더 + 주입 가드. 미해결 placeholder/형식 위반이면 error. */
export function renderTuple(
  t: TupleTemplate,
  ev: IdpEvent,
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
      const val = fieldValue(ev, path);
      if (val === undefined) return { error: `unresolved {{${path}}}` };
      if (FORBIDDEN_IN_VALUE.test(val))
        return { error: `value for {{${path}}} contains forbidden char (:#* or space)` };
      out = out.split(`{{${path}}}`).join(val);
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
      const { tuple, error } = renderTuple(rule.tupleTemplate, ev);
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
  return { applied, skipped, failed };
}
