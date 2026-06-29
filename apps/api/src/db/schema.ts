import type { ModelIR } from "@lazyfga/shared";
import { boolean, integer, jsonb, pgTable, text, timestamp, unique, uuid } from "drizzle-orm/pg-core";
import type { MatchPredicate, TupleTemplate } from "../modules/idp/types";

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

/** PDP 호출 통제용 service token(lazyfga-10). 평문 미저장(sha256만). */
export const serviceToken = pgTable("service_token", {
  id: uuid("id").primaryKey().defaultRandom(),
  name: text("name").notNull(),
  tokenHash: text("token_hash").notNull(),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  lastUsedAt: timestamp("last_used_at", { withTimezone: true }),
  revokedAt: timestamp("revoked_at", { withTimezone: true }),
});

/**
 * named policy(lazyfga-8): 정책 1개 = (permission, resourceType) 단일 질문 템플릿.
 * (permission, resource_type)는 evaluate 조회 키이므로 전역 유일.
 */
export const policy = pgTable(
  "policy",
  {
    id: text("id").primaryKey(), // slug
    permission: text("permission").notNull(),
    resourceType: text("resource_type").notNull(),
    description: text("description"),
    conditionRef: text("condition_ref"), // 예약(lazyfga-14)
    createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
    updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
  },
  (t) => ({
    permResourceUnq: unique("policy_perm_resource_unq").on(t.permission, t.resourceType),
  }),
);

/**
 * IdP 연결(lazyfga-15/16): provider별 webhook 서명 시크릿 + 활성화.
 * signing_secret은 HMAC 검증용 raw 시크릿이며 GET 응답에 노출하지 않는다(write-only).
 */
export const idpConnection = pgTable("idp_connection", {
  id: uuid("id").primaryKey().defaultRandom(),
  provider: text("provider").notNull().unique(),
  signingSecret: text("signing_secret").notNull(),
  enabled: boolean("enabled").notNull().default(true),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
});

/** IdP 이벤트→tuple 매핑 규칙(설정형, Q3=B). 연결 삭제 시 cascade. */
export const idpMappingRule = pgTable("idp_mapping_rule", {
  id: uuid("id").primaryKey().defaultRandom(),
  connectionId: uuid("connection_id")
    .notNull()
    .references(() => idpConnection.id, { onDelete: "cascade" }),
  eventType: text("event_type").notNull(),
  match: jsonb("match").$type<MatchPredicate[]>().notNull().default([]),
  tupleTemplate: jsonb("tuple_template").$type<TupleTemplate>().notNull(),
  op: text("op").notNull(), // "write" | "delete"
  priority: integer("priority").notNull().default(0),
  createdAt: timestamp("created_at", { withTimezone: true }).notNull().defaultNow(),
  updatedAt: timestamp("updated_at", { withTimezone: true }).notNull().defaultNow(),
});

export type InstanceConfig = typeof instanceConfig.$inferSelect;
export type ModelVersionRow = typeof modelVersion.$inferSelect;
export type ServiceTokenRow = typeof serviceToken.$inferSelect;
export type PolicyRow = typeof policy.$inferSelect;
export type IdpConnectionRow = typeof idpConnection.$inferSelect;
export type IdpMappingRuleRow = typeof idpMappingRule.$inferSelect;
