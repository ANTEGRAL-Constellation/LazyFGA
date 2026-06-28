import { desc, eq } from "drizzle-orm";
import { db } from "../../db/client";
import { instanceConfig, modelVersion, type ModelVersionRow } from "../../db/schema";

/** 현재 발행 버전(instance_config.current_model_version_id → model_version). */
export async function getCurrentVersion(): Promise<ModelVersionRow | null> {
  const cfg = await db
    .select({ cur: instanceConfig.currentModelVersionId })
    .from(instanceConfig)
    .limit(1);
  const id = cfg[0]?.cur ?? null;
  if (!id) return null;
  const rows = await db.select().from(modelVersion).where(eq(modelVersion.id, id)).limit(1);
  return rows[0] ?? null;
}

export async function listVersions(): Promise<ModelVersionRow[]> {
  return db.select().from(modelVersion).orderBy(desc(modelVersion.createdAt));
}

export async function getVersion(id: string): Promise<ModelVersionRow | null> {
  const rows = await db.select().from(modelVersion).where(eq(modelVersion.id, id)).limit(1);
  return rows[0] ?? null;
}
