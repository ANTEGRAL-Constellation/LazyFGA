// lazyfga-19: 데모 상태 초기화. 정책·IdP 설정·데모 tuple을 정리한다.
// (OpenFGA store/authorization model 자체는 남긴다 — 재발행은 run.ts가 한다.)
import { OpenFgaClient } from "@openfga/sdk";
import { db, sql } from "../../src/db/client";
import { instanceConfig } from "../../src/db/schema";

const API = process.env.API_BASE ?? "http://localhost:8787";
const ADMIN = process.env.ADMIN_TOKEN ?? "devtoken";
const OPENFGA = process.env.OPENFGA_API_URL ?? "http://localhost:8080";
const adminHeaders = { authorization: `Bearer ${ADMIN}`, "content-type": "application/json" };

async function main(): Promise<void> {
  // 정책 삭제(없으면 404 무시).
  await fetch(`${API}/policies/can-read-doc`, { method: "DELETE", headers: adminHeaders });

  // IdP zitadel 연결 삭제(규칙 cascade).
  const list = await fetch(`${API}/idp/connections`, { headers: adminHeaders });
  if (list.ok) {
    const { connections } = (await list.json()) as {
      connections: Array<{ id: string; provider: string }>;
    };
    const zit = connections.find((c) => c.provider === "zitadel");
    if (zit)
      await fetch(`${API}/idp/connections/${zit.id}`, { method: "DELETE", headers: adminHeaders });
  }

  // 데모 tuple 삭제(SDK; 없으면 무시).
  const cfg = (await db.select().from(instanceConfig).limit(1))[0];
  if (cfg) {
    const fga = new OpenFgaClient({ apiUrl: OPENFGA, storeId: cfg.openfgaStoreId });
    const deletes = [
      { user: "user:alice", relation: "member", object: "team:eng" },
      { user: "team:eng#member", relation: "viewer", object: "folder:reports" },
      { user: "folder:reports", relation: "parent", object: "document:report1" },
    ];
    for (const t of deletes) {
      try {
        await fga.write({ deletes: [t] });
      } catch {
        /* not found: ignore */
      }
    }
  }
  console.log("demo state reset (policy + idp config + demo tuples cleared)");
}

try {
  await main();
} finally {
  await sql.end();
}
