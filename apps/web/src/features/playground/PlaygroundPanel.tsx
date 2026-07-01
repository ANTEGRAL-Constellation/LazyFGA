import { useExplain } from "../explain/useExplain";
import { usePlayground, type TestCase } from "./usePlayground";

// lazyfga-18: 인가 어서션을 그 자리에서 여러 건 테스트. 발행본 모델/정책 기준 평가(읽기 전용).
// tuple 데이터는 IdP 동기화/시드로 채운다(Q4=A). 케이스별 explain은 lazyfga-12를 재사용.

const expectedToSelect = (e: boolean | undefined): string =>
  e === undefined ? "" : e ? "allow" : "deny";
const selectToExpected = (s: string): boolean | undefined => (s === "" ? undefined : s === "allow");

export function PlaygroundPanel(): JSX.Element {
  const pg = usePlayground();
  const explain = useExplain();

  // 토큰은 playground·explain이 공유(둘 다 evaluate 호출).
  const setToken = (t: string): void => {
    pg.setToken(t);
    explain.setToken(t);
  };

  const update = (i: number, patch: Partial<TestCase>): void =>
    pg.setCases(pg.cases.map((c, idx) => (idx === i ? { ...c, ...patch } : c)));

  const addCase = (): void =>
    pg.setCases([
      ...pg.cases,
      { subject: { type: "user", id: "" }, action: { name: "" }, resource: { type: "", id: "" } },
    ]);
  const removeCase = (i: number): void => pg.setCases(pg.cases.filter((_, idx) => idx !== i));

  return (
    <section className="lf-pg" data-testid="playground-panel">
      <h2>
        Playground <span className="lf-sub">test assertions (published model)</span>
      </h2>

      <label className="lf-pg-token">
        token
        <input
          type="password"
          value={pg.token}
          onChange={(e) => setToken(e.target.value)}
          onBlur={() => void pg.loadPolicyOptions()}
          placeholder="service or admin token"
          data-testid="pg-token"
        />
      </label>

      <datalist id="pg-actions">
        {pg.policyOptions.actions.map((a) => (
          <option key={a} value={a} />
        ))}
      </datalist>
      <datalist id="pg-restypes">
        {pg.policyOptions.resourceTypes.map((t) => (
          <option key={t} value={t} />
        ))}
      </datalist>

      <div className="lf-pg-cases" data-testid="pg-cases">
        {pg.cases.map((c, i) => (
          <div className="lf-pg-case lf-row" key={i} data-testid={`pg-case-${i}`}>
            <input
              value={c.subject.type}
              placeholder="subj type"
              onChange={(e) => update(i, { subject: { ...c.subject, type: e.target.value } })}
              data-testid={`pg-subjtype-${i}`}
            />
            <input
              value={c.subject.id}
              placeholder="subject id"
              onChange={(e) => update(i, { subject: { ...c.subject, id: e.target.value } })}
              data-testid={`pg-subject-${i}`}
            />
            <input
              value={c.action.name}
              list="pg-actions"
              placeholder="action"
              onChange={(e) => update(i, { action: { name: e.target.value } })}
              data-testid={`pg-action-${i}`}
            />
            <input
              value={c.resource.type}
              list="pg-restypes"
              placeholder="res type"
              onChange={(e) => update(i, { resource: { ...c.resource, type: e.target.value } })}
              data-testid={`pg-restype-${i}`}
            />
            <input
              value={c.resource.id}
              placeholder="res id"
              onChange={(e) => update(i, { resource: { ...c.resource, id: e.target.value } })}
              data-testid={`pg-resid-${i}`}
            />
            <select
              value={expectedToSelect(c.expected)}
              onChange={(e) => update(i, { expected: selectToExpected(e.target.value) })}
              data-testid={`pg-expected-${i}`}
            >
              <option value="">expect: any</option>
              <option value="allow">allow</option>
              <option value="deny">deny</option>
            </select>
            <span className="lf-pg-result" data-testid={`pg-result-${i}`}>
              {pg.results[i]?.error
                ? `err`
                : pg.results[i]?.decision === undefined
                  ? ""
                  : pg.results[i]!.decision
                    ? "ALLOW"
                    : "DENY"}
              {pg.results[i]?.pass === true && " ✓"}
              {pg.results[i]?.pass === false && " ✗"}
            </span>
            <button
              disabled={!pg.token}
              onClick={() =>
                void explain.run({
                  subject: c.subject,
                  action: c.action,
                  resource: c.resource,
                  context: c.context,
                })
              }
              data-testid={`pg-explain-${i}`}
              title="explain on canvas"
            >
              ?
            </button>
            <button onClick={() => removeCase(i)} data-testid={`pg-del-${i}`}>
              ✕
            </button>
          </div>
        ))}
        <div className="lf-row">
          <button onClick={addCase} data-testid="pg-add">
            + case
          </button>
          <button
            onClick={() => void pg.runAll()}
            disabled={pg.running || !pg.token}
            data-testid="pg-run"
          >
            {pg.running ? "running..." : "Run all"}
          </button>
          {!pg.token && (
            <span className="lf-sub" data-testid="pg-token-hint">
              enter a service/admin token to run
            </span>
          )}
        </div>
      </div>

      <p className="lf-sub lf-pg-note">
        Evaluates the published model. Tuples come from IdP sync or seed scripts; with no tuples
        every case is DENY (expected). Explain highlights the current canvas, which may differ from
        the published model that was evaluated.
      </p>

      {explain.error && <p className="lf-parse-error">{explain.error}</p>}
      {explain.result && (
        <div className="lf-pg-explain" data-testid="pg-explain-result">
          <span className={`lf-decision ${explain.result.decision ? "allow" : "deny"}`}>
            {explain.result.decision ? "ALLOW" : "DENY"}
          </span>
          <span className="lf-explain-text"> {explain.result.text}</span>
        </div>
      )}
    </section>
  );
}
