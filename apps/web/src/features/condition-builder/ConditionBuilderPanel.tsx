import { type ConditionDef } from "@lazyfga/shared";
import { useState } from "react";
import { ConditionBuilder } from "./ConditionBuilder";

// lazyfga-13: 조건 빌더의 독립 패널(사용 예시). CONCEPT 플래그십 예시(업무시간 AND 사내 IP)로 시드한다.
// lazyfga-14에서 이 빌더가 역할 부여(assignableBy) 편집에 결합되어 모델에 부착된다.
const SEED: ConditionDef = {
  name: "business_hours_internal",
  params: [
    { name: "current_time", type: "timestamp" },
    { name: "expiry", type: "timestamp" },
    { name: "user_ip", type: "ipaddress" },
  ],
  tree: {
    op: "and",
    children: [
      { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
      { kind: "ip", param: "user_ip", op: "in_cidr", cidr: "10.0.0.0/8" },
    ],
  },
};

export function ConditionBuilderPanel(): JSX.Element {
  const [def, setDef] = useState<ConditionDef>(SEED);
  return (
    <section className="lf-cond-panel" data-testid="condition-panel">
      <h2>
        Conditions <span className="lf-sub">attribute rules (WAF-style)</span>
      </h2>
      <ConditionBuilder value={def} onChange={setDef} />
    </section>
  );
}
