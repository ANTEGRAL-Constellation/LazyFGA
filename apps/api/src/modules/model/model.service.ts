import { validateModelIR, type ModelIR, type ValidationError } from "@lazyfga/shared";
import { CompileError, compileIrToDsl } from "@lazyfga/compiler";
import type { WriteAuthorizationModelRequest } from "@openfga/sdk";
import { eq } from "drizzle-orm";
import { db } from "../../db/client";
import { instanceConfig, modelVersion } from "../../db/schema";
import { gateway } from "../../openfga";
import { recordAudit } from "../audit/audit";

/** 발행 단계별 실패를 HTTP 상태로 표면화하기 위한 에러. */
export class PublishError extends Error {
  constructor(
    public readonly status: 422 | 502 | 500,
    public readonly detail: unknown,
  ) {
    super(`publish failed (${status})`);
    this.name = "PublishError";
  }
}

export interface PublishedVersion {
  id: string;
  authorizationModelId: string;
  createdAt: Date;
}

/**
 * 발행 절차(lazyfga-7 §4.3):
 * 1) validateModelIR → 위반 시 422
 * 2) compileIrToDsl → 실패 시 422
 * 3) OpenFGA writeAuthorizationModel → 실패 시 502
 * 4) model_version INSERT + current 포인터 갱신(같은 트랜잭션) → 실패 시 500(고아 모델 가능)
 * 5) audit
 */
export async function publishModel(
  ir: ModelIR,
  note: string | undefined,
  createdBy: string,
): Promise<PublishedVersion> {
  const errors: ValidationError[] = validateModelIR(ir);
  if (errors.length > 0) throw new PublishError(422, { validation: errors });

  let dsl: string;
  let model: WriteAuthorizationModelRequest;
  try {
    const compiled = compileIrToDsl(ir);
    dsl = compiled.dsl;
    // AuthModelJSON(= Omit<AuthorizationModel,"id">)은 WriteAuthorizationModelRequest와 구조 동일.
    model = compiled.model as unknown as WriteAuthorizationModelRequest;
  } catch (e) {
    if (e instanceof CompileError)
      throw new PublishError(422, { compile: e.reason, detail: e.detail });
    throw e;
  }

  let authorizationModelId: string;
  try {
    ({ authorizationModelId } = await gateway.writeAuthorizationModel(model));
  } catch (e) {
    throw new PublishError(502, { openfga: String(e) });
  }

  try {
    const version = await db.transaction(async (tx) => {
      const inserted = await tx
        .insert(modelVersion)
        .values({ authorizationModelId, irJson: ir, dsl, note: note ?? null, createdBy })
        .returning({
          id: modelVersion.id,
          authorizationModelId: modelVersion.authorizationModelId,
          createdAt: modelVersion.createdAt,
        });
      const row = inserted[0];
      if (!row) throw new Error("insert returned no row");
      await tx
        .update(instanceConfig)
        .set({ currentModelVersionId: row.id, updatedAt: new Date() })
        .where(eq(instanceConfig.id, "singleton"));
      return row;
    });
    recordAudit("model.publish", { versionId: version.id, authorizationModelId }, createdBy);
    return version;
  } catch (e) {
    // OpenFGA write는 성공했으나 DB 기록 실패 → 고아 모델 가능(운영 문서: ReadAuthorizationModels로 복구).
    recordAudit("model.publish.db_failure", { authorizationModelId, error: String(e) }, createdBy);
    throw new PublishError(500, { db: String(e), orphanModelId: authorizationModelId });
  }
}
