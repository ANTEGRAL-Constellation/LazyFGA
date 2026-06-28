import { migrate } from "drizzle-orm/postgres-js/migrator";
import { db, sql } from "./client";

const migrationsFolder = new URL("./migrations", import.meta.url).pathname;

/** Drizzle 마이그레이션 적용(부팅 시 1회, 멱등). */
export async function runMigrations(): Promise<void> {
  await migrate(db, { migrationsFolder });
}

// `bun run src/db/migrate.ts` 로 단독 실행 가능.
if (import.meta.main) {
  await runMigrations();
  await sql.end();
  console.log("migrations applied");
}
