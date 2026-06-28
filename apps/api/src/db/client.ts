import { drizzle } from "drizzle-orm/postgres-js";
import postgres from "postgres";
import { config } from "../config";
import * as schema from "./schema";

/** 저수준 postgres 연결(헬스체크 `select 1` 등에 직접 사용). */
export const sql = postgres(config.databaseUrl, {
  max: 10,
  // 멱등 마이그레이션의 "already exists" 류 NOTICE는 무시(부팅 로그 정리).
  onnotice: () => {},
});

/** Drizzle ORM 클라이언트(스키마 바인딩). */
export const db = drizzle(sql, { schema });

export type Db = typeof db;

/** Postgres 연결 헬스: `select 1` 성공 여부. */
export async function pingDb(): Promise<boolean> {
  try {
    await sql`select 1`;
    return true;
  } catch {
    return false;
  }
}
