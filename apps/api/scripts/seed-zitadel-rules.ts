// lazyfga-21: 데모용 ZITADEL 매핑 규칙 시드(idempotent). project 단위(추가/삭제 대칭).
// 실행: DATABASE_URL=... bun run apps/api/scripts/seed-zitadel-rules.ts
// 전제: team#member를 가진 모델이 먼저 발행돼 있어야 grant write가 성공한다(미발행 시
// type_not_found로 결정적 실패). 모델 발행은 lazyfga-19 데모 오케스트레이터가 담당한다.
//
// 매핑은 추출 엔진이 만든 **정규 attribute** `attributes.project`(= event_payload.projectId)에 키잉한다
// (raw payload 경로가 아님 — mapping.ts는 attributes.*/subject/type만 해석). 주체는 `{{subject}}`.
// 데모 모델엔 `project` 타입이 없으므로 ZITADEL project → OpenFGA `team` 그룹 멤버십으로 매핑한다.
import { sql } from "../src/db/client";
import {
  createConnection,
  createRule,
  deleteRule,
  getConnectionByProvider,
  listRulesByConnection,
} from "../src/modules/idp/idp.repo";
import type { TupleTemplate } from "../src/modules/idp/types";

const SIGNING_SECRET = process.env.ZITADEL_SIGNING_SECRET ?? "dev-zitadel-signing-secret";
// 프로젝트 grant → OpenFGA team 멤버십(원 컨셉의 '직관적 그룹'). attributes.project로 키잉(삭제 신뢰).
const TEAM_MEMBERSHIP: TupleTemplate = {
  user: "user:{{subject}}",
  relation: "member",
  object: "team:{{attributes.project}}",
};

async function main(): Promise<void> {
  const existing = await getConnectionByProvider("zitadel");
  const connectionId = existing
    ? existing.id
    : (
        await createConnection({
          provider: "zitadel",
          preset: "zitadel",
          signingSecret: SIGNING_SECRET,
        })
      ).id;
  console.log(existing ? "zitadel connection exists" : "created zitadel connection");

  // clear-then-insert로 멱등화(규칙 테이블엔 unique 키가 없음).
  for (const r of await listRulesByConnection(connectionId)) await deleteRule(r.id);
  await createRule(connectionId, {
    eventType: "user.grant.added",
    match: [],
    tupleTemplate: TEAM_MEMBERSHIP,
    op: "write",
  });
  await createRule(connectionId, {
    eventType: "user.grant.removed",
    match: [],
    tupleTemplate: TEAM_MEMBERSHIP,
    op: "delete",
  });
  console.log("seeded 2 zitadel mapping rules (project-based: added → write, removed → delete)");
}

try {
  await main();
} finally {
  await sql.end();
}
