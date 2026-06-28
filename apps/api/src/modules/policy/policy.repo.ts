import type { Policy } from "@lazyfga/shared";
import { and, desc, eq } from "drizzle-orm";
import { db } from "../../db/client";
import { policy, type PolicyRow } from "../../db/schema";

const toPolicy = (r: PolicyRow): Policy => ({
  id: r.id,
  permission: r.permission,
  resourceType: r.resourceType,
  description: r.description ?? undefined,
  conditionRef: r.conditionRef ?? undefined,
});

export async function findById(id: string): Promise<Policy | null> {
  const rows = await db.select().from(policy).where(eq(policy.id, id)).limit(1);
  return rows[0] ? toPolicy(rows[0]) : null;
}

/** evaluate 조회 키: (permission, resourceType). */
export async function findByActionResource(
  permission: string,
  resourceType: string,
): Promise<Policy | null> {
  const rows = await db
    .select()
    .from(policy)
    .where(and(eq(policy.permission, permission), eq(policy.resourceType, resourceType)))
    .limit(1);
  return rows[0] ? toPolicy(rows[0]) : null;
}

export async function listPolicies(): Promise<Policy[]> {
  const rows = await db.select().from(policy).orderBy(desc(policy.createdAt));
  return rows.map(toPolicy);
}

export async function insertPolicy(p: Policy): Promise<Policy> {
  const rows = await db
    .insert(policy)
    .values({
      id: p.id,
      permission: p.permission,
      resourceType: p.resourceType,
      description: p.description ?? null,
      conditionRef: p.conditionRef ?? null,
    })
    .returning();
  return toPolicy(rows[0]!);
}

export async function updatePolicy(
  id: string,
  patch: { permission: string; resourceType: string; description?: string },
): Promise<Policy | null> {
  const rows = await db
    .update(policy)
    .set({
      permission: patch.permission,
      resourceType: patch.resourceType,
      description: patch.description ?? null,
      updatedAt: new Date(),
    })
    .where(eq(policy.id, id))
    .returning();
  return rows[0] ? toPolicy(rows[0]) : null;
}

export async function deletePolicy(id: string): Promise<boolean> {
  const rows = await db.delete(policy).where(eq(policy.id, id)).returning({ id: policy.id });
  return rows.length > 0;
}
