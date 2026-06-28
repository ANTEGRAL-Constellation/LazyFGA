import { Hono } from "hono";
import { config } from "./config";
import { db, pingDb } from "./db/client";
import { runMigrations } from "./db/migrate";
import { instanceConfig } from "./db/schema";
import { createOpenFgaGateway } from "./openfga";

/** lazyFGA control-plane API 버전. */
const VERSION = "0.0.0";

const gateway = createOpenFgaGateway({ apiUrl: config.openfgaApiUrl });
let storeReady = false;

const app = new Hono();

// lazyfga-1: openfga·db 헬스 + 부트스트랩된 storeId 노출. 의존성 다운 시 503.
app.get("/healthz", async (c) => {
  const [dbUp, openfgaUp] = await Promise.all([pingDb(), gateway.ping()]);
  const ok = dbUp && openfgaUp && storeReady;
  return c.json(
    {
      status: ok ? "ok" : "degraded",
      version: VERSION,
      db: dbUp ? "up" : "down",
      openfga: openfgaUp ? "up" : "down",
      storeId: storeReady ? gateway.getStoreId() : null,
    },
    ok ? 200 : 503,
  );
});

/** lazyFGA DB(instance_config)에서 저장된 store id 로드. */
async function loadStoredStoreId(): Promise<string | null> {
  const rows = await db
    .select({ storeId: instanceConfig.openfgaStoreId })
    .from(instanceConfig)
    .limit(1);
  return rows[0]?.storeId ?? null;
}

/** 확정된 store id를 싱글턴 행에 upsert. */
async function persistStoreId(storeId: string): Promise<void> {
  await db
    .insert(instanceConfig)
    .values({ id: "singleton", openfgaStoreId: storeId })
    .onConflictDoUpdate({
      target: instanceConfig.id,
      set: { openfgaStoreId: storeId, updatedAt: new Date() },
    });
}

const sleep = (ms: number) => new Promise<void>((resolve) => setTimeout(resolve, ms));

/**
 * 일시적(의존성 미기동) 오류인지 판별. true면 재시도/degraded, false면 설정·권한 등
 * 비복구 오류 → fatal로 분류해 하드 종료한다.
 */
function isTransient(err: unknown): boolean {
  const code = (err as { code?: string } | null)?.code;
  if (
    code === "ECONNREFUSED" ||
    code === "ENOTFOUND" ||
    code === "ECONNRESET" ||
    code === "ETIMEDOUT" ||
    code === "EAI_AGAIN"
  ) {
    return true;
  }
  const msg = String((err as { message?: string } | null)?.message ?? err).toLowerCase();
  return (
    msg.includes("econnrefused") ||
    msg.includes("connect") ||
    msg.includes("fetch failed") ||
    msg.includes("network") ||
    msg.includes("timeout") ||
    msg.includes("getaddrinfo")
  );
}

/** 일시적 오류에만 backoff 재시도. 비일시적(fatal) 오류는 즉시 throw. */
async function withRetry<T>(fn: () => Promise<T>, label: string, attempts = 8): Promise<T> {
  let lastErr: unknown;
  for (let i = 1; i <= attempts; i++) {
    try {
      return await fn();
    } catch (err) {
      if (!isTransient(err)) throw err; // fatal: 재시도 무의미
      lastErr = err;
      console.warn(`[startup] ${label} attempt ${i}/${attempts} failed (transient); retrying...`);
      await sleep(Math.min(1000 * i, 5000));
    }
  }
  throw lastErr;
}

async function startup(): Promise<void> {
  await withRetry(() => runMigrations(), "db migrate");
  const { storeId } = await withRetry(
    () =>
      gateway.bootstrap({
        envStoreId: config.storeId,
        loadStoredStoreId,
        persistStoreId,
      }),
    "openfga bootstrap",
  );
  storeReady = true;
  console.log(`[startup] lazyfga-api ready: store=${storeId} port=${config.port}`);
}

// 서버는 즉시 리스닝하고(아래 default export), 부트스트랩은 백그라운드로 진행한다.
// → 의존성 준비 전에도 /healthz가 503으로 즉시 관측 가능.
// 일시적 의존성 다운: retry 후 degraded 유지. 비복구 오류: 로그 + exit(1)로 하드 실패.
void startup().catch((err) => {
  if (isTransient(err)) {
    console.error(
      "[startup] dependencies unavailable after retries; serving in degraded mode (/healthz 503):",
      err,
    );
    return;
  }
  console.error("[startup] fatal bootstrap error; exiting:", err);
  process.exit(1);
});

export default {
  port: config.port,
  fetch: app.fetch,
};
