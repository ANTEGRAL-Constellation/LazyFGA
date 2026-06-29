// lazyfga-19: 자체 완결 데모 오케스트레이터. 라이브 ZITADEL 없이 전체 흐름을 한 번에 시연한다.
// 실행: (api·openfga·postgres 기동 상태에서)
//   ADMIN_TOKEN=... API_BASE=http://localhost:8787 OPENFGA_API_URL=http://localhost:8080 \
//   DATABASE_URL=postgres://lazyfga:lazyfga@localhost:5432/lazyfga \
//   bun run apps/api/scripts/demo/run.ts
//
// 흐름: 모델 발행(조건 1개 포함) → 정책 → IdP 연결+규칙 → 서명 webhook replay(grant→membership)
//       → 구조 tuple 직접 시드(SDK) → evaluate+explain로 ALLOW 경로 시연.
import {
  addCondition,
  setAssignmentCondition,
  type ConditionDef,
  type ModelIR,
} from "@lazyfga/shared";
import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { OpenFgaClient } from "@openfga/sdk";
import { db, sql } from "../../src/db/client";
import { instanceConfig } from "../../src/db/schema";
import { zitadelSignatureHeader } from "../lib/zitadel-sign";

const API = process.env.API_BASE ?? "http://localhost:8787";
const ADMIN = process.env.ADMIN_TOKEN ?? "devtoken";
const SIGNING = process.env.ZITADEL_SIGNING_SECRET ?? "dev-zitadel-signing-secret";
const OPENFGA = process.env.OPENFGA_API_URL ?? "http://localhost:8080";
const adminHeaders = { authorization: `Bearer ${ADMIN}`, "content-type": "application/json" };

const log = (msg: string): void => console.log(`\n▶ ${msg}`);

async function main(): Promise<void> {
  // 0) 스택 선검사.
  const health = await fetch(`${API}/healthz`).catch(() => null);
  if (!health || !health.ok) throw new Error(`api not ready at ${API} (start api + openfga + postgres first)`);

  // 1) 데모 모델 발행: docFolderTeamIR + 조건 1개(non_expired)를 document.owner 부여에 부착.
  //    (조건은 시연 allow 경로(viewer 상속) 밖이라 evaluate에 context가 없어도 ALLOW가 난다.)
  const nonExpired: ConditionDef = {
    name: "non_expired",
    params: [
      { name: "current_time", type: "timestamp" },
      { name: "expiry", type: "timestamp" },
    ],
    tree: { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
  };
  let demoIr: ModelIR = addCondition(docFolderTeamIR, nonExpired);
  demoIr = setAssignmentCondition(demoIr, "document", "owner", 0, "non_expired");
  log("publish demo model (with a non_expired condition on document.owner)");
  const pub = await fetch(`${API}/model`, {
    method: "POST",
    headers: adminHeaders,
    body: JSON.stringify({ ir: demoIr, note: "demo (lazyfga-19)" }),
  });
  if (!pub.ok) throw new Error(`model publish failed: ${pub.status} ${await pub.text()}`);

  // 2) 정책 시드(이미 있으면 409 → 무시).
  log("seed policy can-read-doc (read, document)");
  await fetch(`${API}/policies`, {
    method: "POST",
    headers: adminHeaders,
    body: JSON.stringify({ id: "can-read-doc", permission: "read", resourceType: "document" }),
  });

  // 3) IdP 연결 + 규칙 시드(idempotent). 연결이 있으면 secret을 데모 값으로 PUT(서명 일치 보장).
  log("seed zitadel connection + projectId-based mapping rule");
  let connectionId: string;
  const created = await fetch(`${API}/idp/connections`, {
    method: "POST",
    headers: adminHeaders,
    body: JSON.stringify({ provider: "zitadel", preset: "zitadel", signingSecret: SIGNING }),
  });
  if (created.status === 201) {
    connectionId = ((await created.json()) as { connection: { id: string } }).connection.id;
  } else {
    const list = (await (await fetch(`${API}/idp/connections`, { headers: adminHeaders })).json()) as {
      connections: Array<{ id: string; provider: string }>;
    };
    connectionId = list.connections.find((c) => c.provider === "zitadel")!.id;
    await fetch(`${API}/idp/connections/${connectionId}`, {
      method: "PUT",
      headers: adminHeaders,
      body: JSON.stringify({ signingSecret: SIGNING }),
    });
  }
  const existingRules = (await (
    await fetch(`${API}/idp/connections/${connectionId}/rules`, { headers: adminHeaders })
  ).json()) as { rules: Array<{ id: string }> };
  for (const r of existingRules.rules)
    await fetch(`${API}/idp/rules/${r.id}`, { method: "DELETE", headers: adminHeaders });
  await fetch(`${API}/idp/connections/${connectionId}/rules`, {
    method: "POST",
    headers: adminHeaders,
    body: JSON.stringify({
      eventType: "user.grant.added",
      op: "write",
      tupleTemplate: { user: "user:{{subject}}", relation: "member", object: "team:{{attributes.project}}" },
    }),
  });

  // 4) 서명된 ZITADEL grant 이벤트 replay → user:alice member team:eng (webhook 경로, audited).
  //    실제 ZITADEL shape: usergrant-aggregate → 주체는 event_payload.userId, 타임스탬프는 초.
  log("replay a signed ZITADEL user.grant.added webhook (alice granted project 'eng')");
  const payload = { event_type: "user.grant.added", event_payload: { userId: "alice", projectId: "eng" } };
  const raw = new TextEncoder().encode(JSON.stringify(payload));
  const sig = zitadelSignatureHeader(raw, SIGNING, Math.floor(Date.now() / 1000));
  const wh = await fetch(`${API}/idp/webhook/zitadel`, {
    method: "POST",
    headers: { "ZITADEL-Signature": sig, "content-type": "application/json" },
    body: raw,
  });
  console.log(`   webhook → ${wh.status} ${await wh.text()}`);

  // 5) 구조 tuple 직접 시드(매핑 엔진으로는 userset/parent를 못 쓰므로 OpenFGA SDK로 직접; Q4=A).
  log("seed structural tuples via OpenFGA SDK (team→folder role binding + folder→document parent)");
  const cfg = (await db.select().from(instanceConfig).limit(1))[0];
  if (!cfg) throw new Error("instance_config missing (api not bootstrapped?)");
  const fga = new OpenFgaClient({ apiUrl: OPENFGA, storeId: cfg.openfgaStoreId });
  const structural = [
    { user: "team:eng#member", relation: "viewer", object: "folder:reports" },
    { user: "folder:reports", relation: "parent", object: "document:report1" },
  ];
  for (const t of structural) {
    try {
      await fga.write({ writes: [t] });
    } catch (e) {
      const msg = String(e);
      // 멱등(이미 존재)만 조용히 넘기고, 실제 오류(연결 거부/모델 오류 등)는 표면화한다.
      if (!msg.includes("already exists") && !msg.includes("write_failed_due_to_invalid_input"))
        console.warn(`   ! structural tuple write failed: ${JSON.stringify(t)} → ${msg}`);
    }
  }

  // 6) evaluate + explain: alice가 document:report1을 read할 수 있나? (grant→team→상속 경로)
  log("evaluate: can user:alice read document:report1 ? (path: grant → team → folder → document)");
  const evalRes = await fetch(`${API}/access/v1/evaluation`, {
    method: "POST",
    headers: adminHeaders,
    body: JSON.stringify({
      subject: { type: "user", id: "alice" },
      action: { name: "read" },
      resource: { type: "document", id: "report1" },
      options: { reason: true },
    }),
  });
  const decision = (await evalRes.json()) as { decision: boolean; context?: { reason?: { text: string } } };
  console.log(`   decision: ${decision.decision ? "ALLOW" : "DENY"}`);
  console.log(`   reason:   ${decision.context?.reason?.text ?? "(none)"}`);

  console.log("\n✔ demo complete. Explore in the web studio (canvas / conditions / playground / audit).");
}

try {
  await main();
} finally {
  await sql.end();
}
