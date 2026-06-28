import type { ModelIR } from "@lazyfga/shared";
import { jsonb, pgTable, text, timestamp, uuid } from "drizzle-orm/pg-core";

/**
 * 발행된 모델 버전 메타(lazyfga-7). OpenFGA는 immutable/versioned이므로
 * 발행 = 새 버전 생성. 어떤 IR이 어떤 OpenFGA model id가 됐는지 추적한다.
 */
export const modelVersion = pgTable("model_version", {
  id: uuid("id").primaryKey().defaultRandom(),
  authorizationModelId: text("authorization_model_id").notNull(),
  irJson: jsonb("ir_json").$type<ModelIR>().notNull(),
  dsl: text("dsl").notNull(),
  note: text("note"),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  createdBy: text("created_by").notNull(),
});

/**
 * 단일 인스턴스 설정(싱글턴 1행, id="singleton").
 * store 부트스트랩 결과 + 현재 발행 모델 포인터를 영속한다.
 */
export const instanceConfig = pgTable("instance_config", {
  id: text("id").primaryKey().default("singleton"),
  openfgaStoreId: text("openfga_store_id").notNull(),
  /** 최신 발행본 포인터(lazyfga-7). */
  currentModelVersionId: uuid("current_model_version_id").references(() => modelVersion.id),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
});

export type InstanceConfig = typeof instanceConfig.$inferSelect;
export type ModelVersionRow = typeof modelVersion.$inferSelect;
