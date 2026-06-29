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
  getRulesByProvider,
  listConnections,
  listRulesByConnection,
  updateConnection,
  updateRule,
} from "./idp.repo";
import { applyEvents, WriteError, type ApplyDeps, type RenderedTuple } from "./mapping";
import { getAdapter, type MatchPredicate, type TupleTemplate } from "./types";

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

// ── 웹훅(서명 인증, 토큰 불요) ─────────────────────────────────────────────────
idpRoutes.post("/webhook/:provider", async (c) => {
  const provider = c.req.param("provider");
  const conn = await getConnectionByProvider(provider);
  if (!conn) return c.json({ error: "unknown provider" }, 404);
  if (!conn.enabled) return c.json({ error: "connection disabled" }, 403);
  const adapter = getAdapter(provider);
  if (!adapter) return c.json({ error: "no adapter registered for provider" }, 501);

  const raw = new Uint8Array(await c.req.arrayBuffer());
  if (!adapter.verifySignature(raw, c.req.raw.headers, conn.signingSecret)) {
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

  const events = adapter.parseEvents(body, c.req.raw.headers);
  // 인식 못 한/필드 부재 payload는 빈 배열로 정규화된다 → 감사 흔적만 남기고 200 no-op.
  if (events.length === 0) recordAudit("idp.webhook.no_events", { provider }, `idp:${provider}`);
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
    const result = await applyEvents(events, rules, deps);
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
  try {
    const connection = await createConnection({
      provider: b.provider,
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
  const connection = await updateConnection(id, {
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
  if (!(await getConnectionById(id))) return c.json({ error: "connection not found" }, 404);
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
  const rule = await createRule(id, {
    eventType: b.eventType,
    match: (b.match as MatchPredicate[] | undefined) ?? [],
    tupleTemplate: { user: tt.user, relation: tt.relation, object: tt.object },
    op: b.op,
    priority: typeof b.priority === "number" ? b.priority : undefined,
  });
  recordAudit("idp.rule.create", { id: rule.id, connectionId: id }, principalActor(c.get("principal")));
  return c.json({ rule }, 201);
});

idpRoutes.put("/rules/:ruleId", async (c) => {
  const ruleId = c.req.param("ruleId");
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
  const rule = await updateRule(ruleId, {
    eventType: typeof b?.eventType === "string" ? b.eventType : undefined,
    match: b?.match !== undefined ? (b.match as MatchPredicate[]) : undefined,
    tupleTemplate: tt !== undefined ? { user: tt.user, relation: tt.relation, object: tt.object } : undefined,
    op: b?.op === "write" || b?.op === "delete" ? b.op : undefined,
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
