import { FgaApiInternalError, FgaApiRateLimitExceededError } from "@openfga/sdk";
import { Hono } from "hono";
import { requireRole, type AppEnv } from "../../middleware/auth";
import { recordAudit } from "../audit/audit";
import { gateway } from "../../openfga";
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

const TRANSIENT_CODES = new Set(["ECONNREFUSED", "ENOTFOUND", "ECONNRESET", "ETIMEDOUT", "EAI_AGAIN"]);

/** OpenFGA write 오류 분류: 멱등 no-op / 일시적(→502) / 결정적(카운트). */
function classifyWriteError(e: unknown, op: "write" | "delete"): { idempotent: boolean; transient: boolean } {
  if (e instanceof FgaApiInternalError || e instanceof FgaApiRateLimitExceededError)
    return { idempotent: false, transient: true };
  const code = (e as { code?: string } | null)?.code;
  if (code && TRANSIENT_CODES.has(code)) return { idempotent: false, transient: true };
  const msg = String((e as { message?: string } | null)?.message ?? e).toLowerCase();
  if (
    msg.includes("fetch failed") ||
    msg.includes("network") ||
    msg.includes("timeout") ||
    msg.includes("econnrefused")
  )
    return { idempotent: false, transient: true };
  // 멱등 흡수는 invalid-input 신호(코드/메시지) + op별 패턴이 함께 맞을 때만(과잉 흡수 방지).
  const apiCode = (e as { responseData?: { code?: string } } | null)?.responseData?.code;
  const isInvalidInput =
    apiCode === "write_failed_due_to_invalid_input" ||
    msg.includes("write_failed_due_to_invalid_input");
  const opPattern =
    op === "write"
      ? msg.includes("already exists") || msg.includes("duplicate")
      : msg.includes("not found") || msg.includes("cannot delete") || msg.includes("does not exist");
  return { idempotent: isInvalidInput && opPattern, transient: false };
}

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
    recordAudit("idp.webhook.unauthorized", { provider });
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
  if (events.length === 0) recordAudit("idp.webhook.no_events", { provider });
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
    audit: (action, data) => recordAudit(action, { provider, ...data }),
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
  return connection ? c.json({ connection }) : c.json({ error: "connection not found" }, 404);
});

idpRoutes.delete("/connections/:id", async (c) => {
  const ok = await deleteConnection(c.req.param("id"));
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
  return rule ? c.json({ rule }) : c.json({ error: "rule not found" }, 404);
});

idpRoutes.delete("/rules/:ruleId", async (c) => {
  const ok = await deleteRule(c.req.param("ruleId"));
  return ok ? c.body(null, 204) : c.json({ error: "rule not found" }, 404);
});
