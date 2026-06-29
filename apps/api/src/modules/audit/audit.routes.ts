import { Hono } from "hono";
import { requireRole, type AppEnv } from "../../middleware/auth";
import { decodeCursor, queryAudit, type AuditCursor } from "./audit.repo";

export const auditRoutes = new Hono<AppEnv>();

// 감사 조회는 admin 전용.
auditRoutes.use("*", requireRole("admin"));

auditRoutes.get("/", async (c) => {
  const q = c.req.query();
  // trunc 먼저: 소수 limit(예: 1.5)이 .limit()로 흘러가 Postgres bigint 오류(500) 나는 것 방지.
  const limit = Math.min(Math.max(Math.trunc(Number(q.limit)) || 50, 1), 200);

  let cursor: AuditCursor | undefined;
  if (q.cursor) {
    cursor = decodeCursor(q.cursor) ?? undefined;
    if (!cursor) return c.json({ error: "invalid cursor" }, 400);
  }
  const parseDate = (s: string | undefined): Date | null | undefined => {
    if (s === undefined) return undefined;
    const d = new Date(s);
    return Number.isNaN(d.getTime()) ? null : d;
  };
  const from = parseDate(q.from);
  const to = parseDate(q.to);
  if (from === null || to === null) return c.json({ error: "invalid from/to (use ISO 8601)" }, 400);

  const result = await queryAudit({
    action: q.action || undefined,
    actor: q.actor || undefined,
    from: from ?? undefined,
    to: to ?? undefined,
    limit,
    cursor,
  });
  return c.json(result);
});
