import { asc, eq } from "drizzle-orm";
import { db } from "../../db/client";
import {
  idpConnection,
  idpMappingRule,
  type IdpConnectionRow,
  type IdpMappingRuleRow,
} from "../../db/schema";
import type { MappingRule, MatchPredicate, TupleTemplate } from "./types";

/** 응답용(시크릿 제외). signing_secret은 절대 노출하지 않는다. */
export interface PublicConnection {
  id: string;
  provider: string;
  preset: string | null;
  enabled: boolean;
}
const toPublic = (r: IdpConnectionRow): PublicConnection => ({
  id: r.id,
  provider: r.provider,
  preset: r.preset,
  enabled: r.enabled,
});

export interface StoredRule extends MappingRule {
  id: string;
  connectionId: string;
}
const toRule = (r: IdpMappingRuleRow): StoredRule => ({
  id: r.id,
  connectionId: r.connectionId,
  eventType: r.eventType,
  match: r.match,
  tupleTemplate: r.tupleTemplate,
  op: r.op === "delete" ? "delete" : "write",
  priority: r.priority,
  ...(r.fanOut !== null ? { fanOut: r.fanOut } : {}),
});

// ── connections ──
export async function listConnections(): Promise<PublicConnection[]> {
  const rows = await db.select().from(idpConnection).orderBy(asc(idpConnection.provider));
  return rows.map(toPublic);
}
export async function getConnectionById(id: string): Promise<PublicConnection | null> {
  const rows = await db.select().from(idpConnection).where(eq(idpConnection.id, id)).limit(1);
  return rows[0] ? toPublic(rows[0]) : null;
}
/** 웹훅 서명 검증용(시크릿 포함). 라우트 외부로 새지 않게 주의. */
export async function getConnectionByProvider(provider: string): Promise<IdpConnectionRow | null> {
  const rows = await db
    .select()
    .from(idpConnection)
    .where(eq(idpConnection.provider, provider))
    .limit(1);
  return rows[0] ?? null;
}
export async function createConnection(input: {
  provider: string;
  preset?: string;
  signingSecret: string;
  enabled?: boolean;
}): Promise<PublicConnection> {
  const rows = await db
    .insert(idpConnection)
    .values({
      provider: input.provider,
      preset: input.preset ?? null,
      signingSecret: input.signingSecret,
      enabled: input.enabled ?? true,
    })
    .returning();
  return toPublic(rows[0]!);
}
export async function updateConnection(
  id: string,
  patch: { preset?: string; signingSecret?: string; enabled?: boolean },
): Promise<PublicConnection | null> {
  const set: Partial<typeof idpConnection.$inferInsert> = { updatedAt: new Date() };
  if (patch.preset !== undefined) set.preset = patch.preset;
  if (patch.signingSecret !== undefined) set.signingSecret = patch.signingSecret;
  if (patch.enabled !== undefined) set.enabled = patch.enabled;
  const rows = await db.update(idpConnection).set(set).where(eq(idpConnection.id, id)).returning();
  return rows[0] ? toPublic(rows[0]) : null;
}
export async function deleteConnection(id: string): Promise<boolean> {
  const rows = await db
    .delete(idpConnection)
    .where(eq(idpConnection.id, id))
    .returning({ id: idpConnection.id });
  return rows.length > 0;
}

// ── rules ──
export async function listRulesByConnection(connectionId: string): Promise<StoredRule[]> {
  const rows = await db
    .select()
    .from(idpMappingRule)
    .where(eq(idpMappingRule.connectionId, connectionId))
    .orderBy(asc(idpMappingRule.priority));
  return rows.map(toRule);
}
export async function getRuleById(ruleId: string): Promise<StoredRule | null> {
  const rows = await db.select().from(idpMappingRule).where(eq(idpMappingRule.id, ruleId)).limit(1);
  return rows[0] ? toRule(rows[0]) : null;
}
/** 웹훅 처리용: provider의 모든 규칙(우선순위 순). */
export async function getRulesByProvider(provider: string): Promise<MappingRule[]> {
  const rows = await db
    .select({ rule: idpMappingRule })
    .from(idpMappingRule)
    .innerJoin(idpConnection, eq(idpMappingRule.connectionId, idpConnection.id))
    .where(eq(idpConnection.provider, provider))
    .orderBy(asc(idpMappingRule.priority));
  return rows.map((x) => toRule(x.rule));
}
export async function createRule(
  connectionId: string,
  input: {
    eventType: string;
    match: MatchPredicate[];
    tupleTemplate: TupleTemplate;
    op: "write" | "delete";
    fanOut?: string;
    priority?: number;
  },
): Promise<StoredRule> {
  const rows = await db
    .insert(idpMappingRule)
    .values({
      connectionId,
      eventType: input.eventType,
      match: input.match,
      tupleTemplate: input.tupleTemplate,
      op: input.op,
      fanOut: input.fanOut ?? null,
      priority: input.priority ?? 0,
    })
    .returning();
  return toRule(rows[0]!);
}
export async function updateRule(
  ruleId: string,
  patch: {
    eventType?: string;
    match?: MatchPredicate[];
    tupleTemplate?: TupleTemplate;
    op?: "write" | "delete";
    fanOut?: string | null;
    priority?: number;
  },
): Promise<StoredRule | null> {
  const set: Partial<typeof idpMappingRule.$inferInsert> = { updatedAt: new Date() };
  if (patch.eventType !== undefined) set.eventType = patch.eventType;
  if (patch.match !== undefined) set.match = patch.match;
  if (patch.tupleTemplate !== undefined) set.tupleTemplate = patch.tupleTemplate;
  if (patch.op !== undefined) set.op = patch.op;
  if (patch.fanOut !== undefined) set.fanOut = patch.fanOut;
  if (patch.priority !== undefined) set.priority = patch.priority;
  const rows = await db
    .update(idpMappingRule)
    .set(set)
    .where(eq(idpMappingRule.id, ruleId))
    .returning();
  return rows[0] ? toRule(rows[0]) : null;
}

export async function deleteRule(ruleId: string): Promise<boolean> {
  const rows = await db
    .delete(idpMappingRule)
    .where(eq(idpMappingRule.id, ruleId))
    .returning({ id: idpMappingRule.id });
  return rows.length > 0;
}
