import { isValidConditionName, type ConditionDef, type SubjectRef } from "@lazyfga/shared";
import { useState } from "react";
import { useModelStore } from "../../store/modelStore";
import { ConditionBuilder } from "./ConditionBuilder";

// lazyfga-14: 모델에 연결된 조건 패널. 모델 단위 조건을 관리하고(추가/편집/삭제),
// 역할 부여(assignableBy)에 조건을 부착한다 → DSL 미리보기(DslPanel)에 즉시 반영된다.
// 조건 트리 저작 자체는 lazyfga-13의 ConditionBuilder 컴포넌트를 재사용한다.

const newConditionDef = (name: string): ConditionDef => ({
  name,
  params: [
    { name: "current_time", type: "timestamp" },
    { name: "expiry", type: "timestamp" },
  ],
  tree: { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
});

const subjectLabel = (s: SubjectRef): string => {
  const base = s.kind === "user" ? "user" : `${s.group}#member`;
  return s.condition !== undefined ? `${base} [${s.condition}]` : base;
};

// 안정 참조: 셀렉터에서 `?? []`로 새 배열을 만들면 useSyncExternalStore 무한 루프가 난다.
const NO_CONDITIONS: ConditionDef[] = [];

export function ConditionBuilderPanel(): JSX.Element {
  const conditions = useModelStore((s) => s.ir.conditions) ?? NO_CONDITIONS;
  const resources = useModelStore((s) => s.ir.resources);
  const readOnly = useModelStore((s) => s.readOnly);
  const addCondition = useModelStore((s) => s.addCondition);
  const updateCondition = useModelStore((s) => s.updateCondition);
  const renameCondition = useModelStore((s) => s.renameCondition);
  const removeCondition = useModelStore((s) => s.removeCondition);
  const setAssignmentCondition = useModelStore((s) => s.setAssignmentCondition);

  const [selected, setSelected] = useState<string | null>(null);
  const selectedDef = conditions.find((c) => c.name === selected) ?? null;

  const add = (): void => {
    const names = new Set(conditions.map((c) => c.name));
    let n = conditions.length + 1;
    let name = `condition_${n}`;
    while (names.has(name)) name = `condition_${++n}`;
    addCondition(newConditionDef(name));
    setSelected(name);
  };

  const onChange = (next: ConditionDef): void => {
    if (selected === null) return;
    const renaming = next.name !== selected;
    // 충돌하거나 유효하지 않은 이름(빈 문자열/예약어/CEL 예약어)으로의 리네임은 거부.
    const reject =
      renaming && (conditions.some((c) => c.name === next.name) || !isValidConditionName(next.name));
    if (renaming && !reject) {
      renameCondition(selected, next.name);
      setSelected(next.name);
      updateCondition(next.name, next);
    } else {
      // 이름 미변경이거나 거부: 원래 이름 유지하고 본문/params만 갱신.
      updateCondition(selected, { ...next, name: selected });
    }
  };

  return (
    <section className="lf-cond-panel" data-testid="condition-panel">
      <h2>
        Conditions <span className="lf-sub">attribute rules → CEL</span>
      </h2>

      {readOnly && (
        <p className="lf-sub" data-testid="cond-readonly">
          model is read-only (advanced DSL); condition editing disabled
        </p>
      )}

      <div className="lf-cond-list" data-testid="cond-list">
        {conditions.map((c) => (
          <button
            key={c.name}
            className={c.name === selected ? "lf-cond-chip sel" : "lf-cond-chip"}
            onClick={() => setSelected(c.name)}
            data-testid={`cond-chip-${c.name}`}
          >
            {c.name}
          </button>
        ))}
        {!readOnly && (
          <button onClick={add} data-testid="cond-add">
            + condition
          </button>
        )}
      </div>

      {selectedDef && !readOnly && (
        <>
          <ConditionBuilder value={selectedDef} onChange={onChange} />
          <button
            className="lf-cond-removebtn"
            onClick={() => {
              removeCondition(selectedDef.name);
              setSelected(null);
            }}
            data-testid="cond-remove"
          >
            remove condition
          </button>
          <AttachForm
            resources={resources}
            conditionName={selectedDef.name}
            onAttach={setAssignmentCondition}
          />
        </>
      )}
    </section>
  );
}

function AttachForm({
  resources,
  conditionName,
  onAttach,
}: {
  resources: { name: string; roles: { name: string; assignableBy: SubjectRef[] }[] }[];
  conditionName: string;
  onAttach(type: string, role: string, subjectIndex: number, condition: string | null): void;
}): JSX.Element {
  const [type, setType] = useState(resources[0]?.name ?? "");
  const res = resources.find((r) => r.name === type) ?? resources[0];
  const [role, setRole] = useState(res?.roles[0]?.name ?? "");
  const roleObj = res?.roles.find((r) => r.name === role) ?? res?.roles[0];
  const [idx, setIdx] = useState(0);
  const subjects = roleObj?.assignableBy ?? [];
  const safeIdx = idx < subjects.length ? idx : 0;

  return (
    <div className="lf-cond-attach" data-testid="cond-attach">
      <div className="lf-cond-sub">
        attach &ldquo;{conditionName}&rdquo; to a role assignment
      </div>
      <span className="lf-row">
        <select
          value={res?.name ?? ""}
          onChange={(e) => {
            setType(e.target.value);
            setRole("");
            setIdx(0);
          }}
          data-testid="cond-attach-type"
        >
          {resources.map((r) => (
            <option key={r.name} value={r.name}>
              {r.name}
            </option>
          ))}
        </select>
        <select
          value={roleObj?.name ?? ""}
          onChange={(e) => {
            setRole(e.target.value);
            setIdx(0);
          }}
          data-testid="cond-attach-role"
        >
          {res?.roles.map((r) => (
            <option key={r.name} value={r.name}>
              {r.name}
            </option>
          ))}
        </select>
        <select
          value={String(safeIdx)}
          onChange={(e) => setIdx(Number(e.target.value))}
          data-testid="cond-attach-subject"
        >
          {subjects.map((s, i) => (
            <option key={i} value={i}>
              {subjectLabel(s)}
            </option>
          ))}
        </select>
      </span>
      <span className="lf-row">
        <button
          disabled={!res || !roleObj || subjects.length === 0}
          onClick={() => res && roleObj && onAttach(res.name, roleObj.name, safeIdx, conditionName)}
          data-testid="cond-attach-set"
        >
          attach
        </button>
        <button
          disabled={!res || !roleObj || subjects.length === 0}
          onClick={() => res && roleObj && onAttach(res.name, roleObj.name, safeIdx, null)}
          data-testid="cond-attach-clear"
        >
          clear
        </button>
      </span>
    </div>
  );
}
