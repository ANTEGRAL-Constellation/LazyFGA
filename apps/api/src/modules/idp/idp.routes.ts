import { Hono } from "hono";
import { bodyLimit } from "hono/body-limit";
import { requireRole, type AppEnv } from "../../middleware/auth";
import { principalActor, recordAudit } from "../audit/audit";
import { gateway } from "../../openfga";
import { classifyWriteError } from "../../openfga/write-error";
import {
  createConnection,
  createRule,
  deleteConnection,
  deleteRule,
  getConnectionById,
  getConnectionByProvider,
  getRuleById,
  getRulesByProvider,
  listConnections,
  listRulesByConnection,
  updateConnection,
  updateRule,
  type PublicConnection,
} from "./idp.repo";
import { applyEvents, WriteError, type ApplyDeps, type RenderedTuple } from "./mapping";
import { attributeNamesForEvent, extractEvent, readEventType } from "./extraction";
import { PRESETS } from "./presets";
import { verifyWebhookSignature } from "./signature";
import type { MatchPredicate, TupleTemplate } from "./types";

export const idpRoutes = new Hono<AppEnv>();

// 설정 CRUD만 admin 가드. 웹훅(/webhook/:provider)은 가드 없이 서명으로만 인증한다.
idpRoutes.use("/connections", requireRole("admin"));
idpRoutes.use("/connections/*", requireRole("admin"));
idpRoutes.use("/rules/*", requireRole("admin"));
// 웹훅은 서명 검증 전 raw body를 전부 버퍼링하므로 크기를 제한한다(미인증 메모리 DoS 방지).
idpRoutes.use(
  "/webhook/*",
  bodyLimit({ maxSize: 256 * 1024, onError: (c) => c.json({ error: "payload too large" }, 413) }),
);

// lazyfga-15 → openfga/write-error.ts로 이전(permission 모듈과 공유). 기존 import 경로 유지를 위해 재노출.
export { classifyWriteError } from "../../openfga/write-error";

const isValidMatch = (m: unknown): m is MatchPredicate[] =>
  Array.isArray(m) &&
  m.every(
    (x) =>
      x !== null &&
      typeof x === "object" &&
      typeof (x as MatchPredicate).field === "string" &&
      typeof (x as MatchPredicate).equals === "string",
  );
const isValidPriority = (p: unknown): boolean =>
  p === undefined || (typeof p === "number" && Number.isInteger(p));

const usesItem = (tt: TupleTemplate): boolean =>
  [tt.user, tt.relation, tt.object].some((s) => s.includes("{{item}}"));

/**
 * fan-out 검증(lazyfga-21, review 반영). 지정 시:
 *  - 비어있지 않은 문자열이어야 하고,
 *  - 템플릿이 `{{item}}`을 참조해야 의미가 있으며(안 그러면 같은 tuple을 원소 수만큼 반복),
 *  - 연결 preset의 그 eventType 매칭 추출 규칙이 **생성하는 attribute**여야 한다(오타/스칼라 지정 방지).
 * fanOut이 없으면, 템플릿이 `{{item}}`을 참조하면 안 된다(고아 placeholder → 항상 렌더 실패).
 * preset을 알 수 없으면 attribute 교차검증은 생략(webhook 시 500으로 잡힘).
 */
const fanOutError = (
  conn: PublicConnection,
  eventType: string,
  fanOut: string | null | undefined,
  tt: TupleTemplate,
): string | null => {
  if (fanOut === undefined || fanOut === null || fanOut === "") {
    return usesItem(tt) ? "tuple template references {{item}} but no fanOut is set" : null;
  }
  if (typeof fanOut !== "string") return "fanOut must be a non-empty string";
  if (!usesItem(tt)) return "fanOut requires the tuple template to reference {{item}}";
  const preset = PRESETS[conn.preset ?? conn.provider];
  if (preset) {
    const known = attributeNamesForEvent(preset, eventType);
    if (!known.has(fanOut))
      return `fanOut "${fanOut}" is not an attribute produced by preset for event "${eventType}" (known: ${[...known].join(", ") || "none"})`;
  }
  return null;
};

// ── 웹훅(서명 인증, 토큰 불요) ─────────────────────────────────────────────────
idpRoutes.post("/webhook/:provider", async (c) => {
  const provider = c.req.param("provider");
  const conn = await getConnectionByProvider(provider);
  if (!conn) return c.json({ error: "unknown provider" }, 404);
  if (!conn.enabled) return c.json({ error: "connection disabled" }, 403);
  // preset 키 = 연결에 저장된 키, 없으면 provider 이름으로 폴백(기존 zitadel 연결 하위호환).
  const presetKey = conn.preset ?? provider;
  const preset = PRESETS[presetKey];
  if (!preset) {
    // 서버 설정 오류(클라이언트 입력 아님) → 500. 미인증 단계 이전이라 DB audit엔 쓰지 않는다.
    console.error(`[idp] connection ${conn.id} references unknown preset "${presetKey}"`);
    return c.json({ error: "connection misconfigured (unknown preset)" }, 500);
  }

  const raw = new Uint8Array(await c.req.arrayBuffer());
  if (!verifyWebhookSignature(preset.signature, raw, c.req.raw.headers, conn.signingSecret)) {
    // 미인증 요청은 DB audit에 쓰지 않는다(공격자가 audit_log를 무한 적재하는 amplification 방지).
    // 보안 신호는 앱 로그로만 남긴다(로그는 자체 로테이션이 있고 DB/디스크 증식 벡터가 아님).
    console.warn(`[idp] unauthorized webhook for provider="${provider}" (signature verification failed)`);
    return c.json({ error: "invalid signature" }, 401);
  }

  let body: unknown;
  try {
    body = JSON.parse(new TextDecoder().decode(raw));
  } catch {
    return c.json({ error: "invalid json body" }, 400);
  }

  // 매핑 대상 아닌 이벤트(타입 미스 또는 주체 부재) → null → 감사 흔적만 남기고 200 no-op.
  // 관측성: 이벤트 타입을 함께 남겨 "무시된 타입"과 "매칭됐으나 주체 부재(잘못된 payload)"를 구분 가능하게 한다.
  const ev = extractEvent(preset, body);
  if (!ev) {
    recordAudit("idp.webhook.no_events", { provider, eventType: readEventType(preset, body) }, `idp:${provider}`);
    return c.json({ applied: 0, skipped: 0, failed: 0 }, 200);
  }
  const rules = await getRulesByProvider(provider);
  const deps: ApplyDeps = {
    writeTuple: async (op: "write" | "delete", tuple: RenderedTuple) => {
      try {
        await gateway.write(op === "write" ? { writes: [tuple] } : { deletes: [tuple] });
        return "applied";
      } catch (e) {
        const { idempotent, transient } = classifyWriteError(e, op);
        if (idempotent) return "skipped";
        throw new WriteError(transient, String(e));
      }
    },
    audit: (action, data) => recordAudit(action, data, `idp:${provider}`),
  };

  try {
    const result = await applyEvents([ev], rules, deps);
    return c.json(result, 200);
  } catch (e) {
    if (e instanceof WriteError && e.transient) return c.json({ error: "upstream unavailable" }, 502);
    throw e;
  }
});

// ── 설정 CRUD(admin) ──────────────────────────────────────────────────────────
idpRoutes.post("/connections", async (c) => {
  const b = await c.req.json().catch(() => null);
  if (
    !b ||
    typeof b.provider !== "string" ||
    b.provider.trim() === "" ||
    typeof b.signingSecret !== "string" ||
    b.signingSecret === ""
  ) {
    return c.json({ error: "non-empty provider and signingSecret are required" }, 422);
  }
  // preset이 주어지면 알려진 키여야 한다(미지정이면 webhook 시 provider 이름으로 폴백).
  if (b.preset !== undefined && (typeof b.preset !== "string" || !PRESETS[b.preset])) {
    return c.json({ error: `unknown preset; known: ${Object.keys(PRESETS).join(", ")}` }, 422);
  }
  try {
    const connection = await createConnection({
      provider: b.provider,
      preset: typeof b.preset === "string" ? b.preset : undefined,
      signingSecret: b.signingSecret,
      enabled: typeof b.enabled === "boolean" ? b.enabled : undefined,
    });
    recordAudit("idp.connection.create", { id: connection.id, provider: connection.provider }, principalActor(c.get("principal")));
    return c.json({ connection }, 201);
  } catch (e) {
    if (String(e).includes("duplicate") || String(e).includes("unique"))
      return c.json({ error: "provider already exists" }, 409);
    throw e;
  }
});

idpRoutes.get("/connections", async (c) => c.json({ connections: await listConnections() }));

idpRoutes.put("/connections/:id", async (c) => {
  const id = c.req.param("id");
  if (!(await getConnectionById(id))) return c.json({ error: "connection not found" }, 404);
  const b = await c.req.json().catch(() => ({}));
  if (b?.signingSecret !== undefined && (typeof b.signingSecret !== "string" || b.signingSecret === ""))
    return c.json({ error: "signingSecret must be a non-empty string" }, 422);
  if (b?.enabled !== undefined && typeof b.enabled !== "boolean")
    return c.json({ error: "enabled must be a boolean" }, 422);
  if (b?.preset !== undefined && (typeof b.preset !== "string" || !PRESETS[b.preset]))
    return c.json({ error: `unknown preset; known: ${Object.keys(PRESETS).join(", ")}` }, 422);
  const connection = await updateConnection(id, {
    preset: typeof b?.preset === "string" ? b.preset : undefined,
    signingSecret: typeof b?.signingSecret === "string" ? b.signingSecret : undefined,
    enabled: typeof b?.enabled === "boolean" ? b.enabled : undefined,
  });
  if (connection) recordAudit("idp.connection.update", { id }, principalActor(c.get("principal")));
  return connection ? c.json({ connection }) : c.json({ error: "connection not found" }, 404);
});

idpRoutes.delete("/connections/:id", async (c) => {
  const id = c.req.param("id");
  const ok = await deleteConnection(id);
  if (ok) recordAudit("idp.connection.delete", { id }, principalActor(c.get("principal")));
  return ok ? c.body(null, 204) : c.json({ error: "connection not found" }, 404);
});

idpRoutes.get("/connections/:id/rules", async (c) => {
  const id = c.req.param("id");
  if (!(await getConnectionById(id))) return c.json({ error: "connection not found" }, 404);
  return c.json({ rules: await listRulesByConnection(id) });
});

idpRoutes.post("/connections/:id/rules", async (c) => {
  const id = c.req.param("id");
  const conn = await getConnectionById(id);
  if (!conn) return c.json({ error: "connection not found" }, 404);
  const b = await c.req.json().catch(() => null);
  const tt = b?.tupleTemplate as TupleTemplate | undefined;
  if (
    !b ||
    typeof b.eventType !== "string" ||
    (b.op !== "write" && b.op !== "delete") ||
    !tt ||
    typeof tt.user !== "string" ||
    typeof tt.relation !== "string" ||
    typeof tt.object !== "string" ||
    !isValidMatch(b.match ?? []) ||
    !isValidPriority(b.priority)
  ) {
    return c.json(
      { error: "eventType, op(write|delete), tupleTemplate{user,relation,object}, match[], integer priority" },
      422,
    );
  }
  const tmpl = { user: tt.user, relation: tt.relation, object: tt.object };
  const ferr = fanOutError(conn, b.eventType, b.fanOut, tmpl);
  if (ferr) return c.json({ error: ferr }, 422);
  const rule = await createRule(id, {
    eventType: b.eventType,
    match: (b.match as MatchPredicate[] | undefined) ?? [],
    tupleTemplate: { user: tt.user, relation: tt.relation, object: tt.object },
    op: b.op,
    fanOut: typeof b.fanOut === "string" ? b.fanOut : undefined,
    priority: typeof b.priority === "number" ? b.priority : undefined,
  });
  recordAudit("idp.rule.create", { id: rule.id, connectionId: id }, principalActor(c.get("principal")));
  return c.json({ rule }, 201);
});

idpRoutes.put("/rules/:ruleId", async (c) => {
  const ruleId = c.req.param("ruleId");
  // 기존 규칙을 먼저 로드해 병합본(eventType/template/fanOut)을 검증한다(부분 수정의 고아 {{item}} 방지).
  const existing = await getRuleById(ruleId);
  if (!existing) return c.json({ error: "rule not found" }, 404);
  const conn = await getConnectionById(existing.connectionId);
  const b = await c.req.json().catch(() => ({}));
  if (b?.op !== undefined && b.op !== "write" && b.op !== "delete")
    return c.json({ error: "op must be write|delete" }, 422);
  if (b?.match !== undefined && !isValidMatch(b.match)) return c.json({ error: "invalid match[]" }, 422);
  if (!isValidPriority(b?.priority)) return c.json({ error: "priority must be an integer" }, 422);
  const tt = b?.tupleTemplate as TupleTemplate | undefined;
  if (
    tt !== undefined &&
    (typeof tt.user !== "string" || typeof tt.relation !== "string" || typeof tt.object !== "string")
  ) {
    return c.json({ error: "invalid tupleTemplate" }, 422);
  }
  if (b?.fanOut !== undefined && b.fanOut !== null && (typeof b.fanOut !== "string" || b.fanOut.trim() === ""))
    return c.json({ error: "fanOut must be a non-empty string or null" }, 422);
  // 병합본으로 fanOut↔template↔preset 정합성을 검증(template만 바꿔도, fanOut만 비워도 잡힌다).
  const mergedTemplate = tt !== undefined ? { user: tt.user, relation: tt.relation, object: tt.object } : existing.tupleTemplate;
  const mergedEventType = typeof b?.eventType === "string" ? b.eventType : existing.eventType;
  const mergedFanOut = b?.fanOut === null ? undefined : typeof b?.fanOut === "string" ? b.fanOut : existing.fanOut;
  if (conn) {
    const ferr = fanOutError(conn, mergedEventType, mergedFanOut, mergedTemplate);
    if (ferr) return c.json({ error: ferr }, 422);
  }
  const rule = await updateRule(ruleId, {
    eventType: typeof b?.eventType === "string" ? b.eventType : undefined,
    match: b?.match !== undefined ? (b.match as MatchPredicate[]) : undefined,
    tupleTemplate: tt !== undefined ? { user: tt.user, relation: tt.relation, object: tt.object } : undefined,
    op: b?.op === "write" || b?.op === "delete" ? b.op : undefined,
    fanOut: b?.fanOut === null ? null : typeof b?.fanOut === "string" ? b.fanOut : undefined,
    priority: typeof b?.priority === "number" ? b.priority : undefined,
  });
  if (rule) recordAudit("idp.rule.update", { ruleId }, principalActor(c.get("principal")));
  return rule ? c.json({ rule }) : c.json({ error: "rule not found" }, 404);
});

idpRoutes.delete("/rules/:ruleId", async (c) => {
  const ruleId = c.req.param("ruleId");
  const ok = await deleteRule(ruleId);
  if (ok) recordAudit("idp.rule.delete", { ruleId }, principalActor(c.get("principal")));
  return ok ? c.body(null, 204) : c.json({ error: "rule not found" }, 404);
});
