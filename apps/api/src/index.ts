import { Hono } from "hono";
import { config } from "./config";
import { db, pingDb } from "./db/client";
import { runMigrations } from "./db/migrate";
import { instanceConfig } from "./db/schema";
import type { AppEnv } from "./middleware/auth";
import { auditRoutes } from "./modules/audit/audit.routes";
import { tokenRoutes } from "./modules/auth/auth.routes";
import "./modules/idp/adapters"; // lazyfga-16: 빌트인 adapter(zitadel) 레지스트리 등록(side-effect)
import { idpRoutes } from "./modules/idp/idp.routes";
import { modelRoutes } from "./modules/model/model.routes";
import { pdpRoutes } from "./modules/pdp/pdp.routes";
import { permissionRoutes } from "./modules/permission/permission.routes";
import { policyRoutes } from "./modules/policy/policy.routes";
import { gateway } from "./openfga";

/** lazyFGA control-plane API 버전. */
const VERSION = "0.0.0";

let storeReady = false;

const app = new Hono<AppEnv>();

// lazyfga-1: openfga·db 헬스 + 부트스트랩된 storeId 노출. 의존성 다운 시 503.
app.get("/healthz", async (c) => {
  const [dbUp, openfgaUp] = await Promise.all([pingDb(), gateway.ping()]);
  const ok = dbUp && openfgaUp && storeReady;
  // 라이브니스/레디니스는 미인증 공개(오케스트레이터용). storeId 등 인프라 식별자는 노출하지 않음.
  return c.json(
    {
      status: ok ? "ok" : "degraded",
      version: VERSION,
      db: dbUp ? "up" : "down",
      openfga: openfgaUp ? "up" : "down",
      storeReady,
    },
    ok ? 200 : 503,
  );
});

// lazyfga-7: 모델 발행/버전/diff (admin). lazyfga-10: 토큰(admin). lazyfga-8: 정책(admin).
// lazyfga-9: PDP evaluate (service|admin).
app.route("/model", modelRoutes);
app.route("/tokens", tokenRoutes);
app.route("/policies", policyRoutes);
app.route("/access/v1", pdpRoutes);
// lazyfga-20: 구조적 권한 grant/revoke/list (admin).
app.route("/grants", permissionRoutes);
// lazyfga-15: IdP webhook(서명 인증) + 설정 CRUD(admin). 어댑터는 lazyfga-16(zitadel)이 등록.
app.route("/idp", idpRoutes);
// lazyfga-17: 변경 감사 조회(admin).
app.route("/audit", auditRoutes);

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
  if (!config.adminToken) {
    console.warn(
      "[startup] ADMIN_TOKEN is empty — the control plane (model/policy/token) is unreachable. Set ADMIN_TOKEN.",
    );
  }
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
