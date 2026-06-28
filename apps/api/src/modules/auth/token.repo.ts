import { and, desc, eq, isNull } from "drizzle-orm";
import { db } from "../../db/client";
import { serviceToken, type ServiceTokenRow } from "../../db/schema";

export async function createToken(name: string, tokenHash: string): Promise<ServiceTokenRow> {
  const rows = await db.insert(serviceToken).values({ name, tokenHash }).returning();
  if (!rows[0]) throw new Error("token insert returned no row");
  return rows[0];
}

export async function listTokens(): Promise<ServiceTokenRow[]> {
  return db.select().from(serviceToken).orderBy(desc(serviceToken.createdAt));
}

/** 폐기 처리. 활성 토큰이 없으면 false(404). */
export async function revokeToken(id: string): Promise<boolean> {
  const rows = await db
    .update(serviceToken)
    .set({ revokedAt: new Date() })
    .where(and(eq(serviceToken.id, id), isNull(serviceToken.revokedAt)))
    .returning({ id: serviceToken.id });
  return rows.length > 0;
}

export async function findActiveByHash(hash: string): Promise<ServiceTokenRow | null> {
  const rows = await db
    .select()
    .from(serviceToken)
    .where(and(eq(serviceToken.tokenHash, hash), isNull(serviceToken.revokedAt)))
    .limit(1);
  return rows[0] ?? null;
}

export async function touchLastUsed(id: string): Promise<void> {
  await db.update(serviceToken).set({ lastUsedAt: new Date() }).where(eq(serviceToken.id, id));
}
