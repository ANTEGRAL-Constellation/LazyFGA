import { pgTable, text, timestamp } from "drizzle-orm/pg-core";

/**
 * 단일 인스턴스 설정(싱글턴 1행, id="singleton").
 * store 부트스트랩 결과를 영속한다. 후속 명세가 컬럼을 추가한다:
 *  - lazyfga-7: current_model_version_id (현재 발행 모델 포인터)
 */
export const instanceConfig = pgTable("instance_config", {
  id: text("id").primaryKey().default("singleton"),
  openfgaStoreId: text("openfga_store_id").notNull(),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
});

export type InstanceConfig = typeof instanceConfig.$inferSelect;
