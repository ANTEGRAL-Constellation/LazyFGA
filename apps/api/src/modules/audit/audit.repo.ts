import type { AuditEntry } from "@lazyfga/shared";
import { and, desc, eq, gte, like, lt, lte, or, type SQL } from "drizzle-orm";
import { db } from "../../db/client";
import { auditLog, type AuditLogRow } from "../../db/schema";

const toEntry = (r: AuditLogRow): AuditEntry => ({
  id: r.id,
  occurredAt: r.occurredAt.toISOString(),
  actor: r.actor,
  action: r.action,
  data: r.data,
});

export interface AuditCursor {
  occurredAt: Date;
  id: string;
}

export function encodeCursor(occurredAt: string, id: string): string {
  return Buffer.from(`${occurredAt}|${id}`, "utf8").toString("base64url");
}

export function decodeCursor(s: string): AuditCursor | null {
  try {
    const [iso, id] = Buffer.from(s, "base64url").toString("utf8").split("|");
    if (!iso || !id) return null;
    const occurredAt = new Date(iso);
    if (Number.isNaN(occurredAt.getTime())) return null;
    return { occurredAt, id };
  } catch {
    return null;
  }
}

export interface AuditQuery {
  action?: string;
  actor?: string;
  from?: Date;
  to?: Date;
  limit: number;
  cursor?: AuditCursor;
}

/** 최신순(occurred_at desc, id desc) keyset 페이지네이션. */
export async function queryAudit(
  q: AuditQuery,
): Promise<{ entries: AuditEntry[]; nextCursor?: string }> {
  const conds: SQL[] = [];
  if (q.action) {
    // 정확 일치, 또는 trailing `*` → 접두 일치(LIKE; 와일드카드 문자는 이스케이프).
    if (q.action.endsWith("*")) {
      const prefix = q.action.slice(0, -1).replace(/[\\%_]/g, (m) => `\\${m}`);
      conds.push(like(auditLog.action, `${prefix}%`));
    } else {
      conds.push(eq(auditLog.action, q.action));
    }
  }
  if (q.actor) conds.push(eq(auditLog.actor, q.actor));
  if (q.from) conds.push(gte(auditLog.occurredAt, q.from));
  if (q.to) conds.push(lte(auditLog.occurredAt, q.to));
  if (q.cursor) {
    // desc 정렬에서 cursor 다음 행: (occurred_at < c.at) OR (occurred_at = c.at AND id < c.id)
    conds.push(
      or(
        lt(auditLog.occurredAt, q.cursor.occurredAt),
        and(eq(auditLog.occurredAt, q.cursor.occurredAt), lt(auditLog.id, q.cursor.id)),
      )!,
    );
  }
  const rows = await db
    .select()
    .from(auditLog)
    .where(conds.length ? and(...conds) : undefined)
    .orderBy(desc(auditLog.occurredAt), desc(auditLog.id))
    .limit(q.limit + 1);

  const hasMore = rows.length > q.limit;
  const page = hasMore ? rows.slice(0, q.limit) : rows;
  const entries = page.map(toEntry);
  const last = page[page.length - 1];
  const nextCursor =
    hasMore && last ? encodeCursor(last.occurredAt.toISOString(), last.id) : undefined;
  return { entries, nextCursor };
}
