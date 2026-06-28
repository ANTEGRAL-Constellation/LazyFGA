import type { ReasonResult } from "@lazyfga/shared";
import { useState } from "react";
import { useExplain } from "./useExplain";

function ResultView({ result }: { result: ReasonResult }) {
  return (
    <div className="lf-explain-result" data-testid="ex-result">
      <div className="lf-explain-head">
        <span
          className={`lf-decision ${result.decision ? "allow" : "deny"}`}
          data-testid="ex-decision"
        >
          {result.decision ? "ALLOW" : "DENY"}
        </span>
        {result.truncated && (
          <span className="lf-readonly-badge" data-testid="ex-truncated">
            partial path
          </span>
        )}
      </div>
      <p className="lf-explain-text">{result.text}</p>

      {result.path && result.path.length > 0 && (
        <ol className="lf-path" data-testid="ex-path">
          {result.path.map((s, i) => (
            <li key={i}>
              {s.via === "role"
                ? `role ${s.role} on ${s.on}${s.direct ? " (direct)" : s.group ? ` via ${s.group} group` : ""}`
                : `inherited via ${s.relation} from ${s.parent}`}
            </li>
          ))}
        </ol>
      )}

      {result.missingLinks && result.missingLinks.length > 0 && (
        <ul className="lf-missing" data-testid="ex-missing">
          {result.missingLinks.map((l, i) => (
            <li key={i}>
              {l.kind === "role"
                ? `need one of [${l.anyOf.join(", ")}] on ${l.on}`
                : `need ${l.needs} via parent "${l.relation}"`}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function ExplainPanel() {
  const ex = useExplain();
  const [subjectType, setSubjectType] = useState("user");
  const [subjectId, setSubjectId] = useState("alice");
  const [action, setAction] = useState("read");
  const [resourceType, setResourceType] = useState("document");
  const [resourceId, setResourceId] = useState("123");

  const submit = () =>
    void ex.run({
      subject: { type: subjectType, id: subjectId },
      action: { name: action },
      resource: { type: resourceType, id: resourceId },
    });

  return (
    <section className="lf-explain" data-testid="explain-panel">
      <h2>
        Explain <span className="lf-sub">why allow / deny</span>
      </h2>
      <div className="lf-explain-form">
        <label>
          subject
          <span className="lf-row">
            <input value={subjectType} onChange={(e) => setSubjectType(e.target.value)} data-testid="ex-subject-type" />
            <input value={subjectId} onChange={(e) => setSubjectId(e.target.value)} data-testid="ex-subject-id" />
          </span>
        </label>
        <label>
          action
          <input value={action} onChange={(e) => setAction(e.target.value)} data-testid="ex-action" />
        </label>
        <label>
          resource
          <span className="lf-row">
            <input value={resourceType} onChange={(e) => setResourceType(e.target.value)} data-testid="ex-resource-type" />
            <input value={resourceId} onChange={(e) => setResourceId(e.target.value)} data-testid="ex-resource-id" />
          </span>
        </label>
        <label>
          token
          <input
            type="password"
            value={ex.token}
            onChange={(e) => ex.setToken(e.target.value)}
            placeholder="service or admin token"
            data-testid="ex-token"
          />
        </label>
        <button data-testid="ex-run" onClick={submit} disabled={ex.loading}>
          {ex.loading ? "evaluating..." : "Evaluate + explain"}
        </button>
      </div>

      {ex.error && (
        <p className="lf-parse-error" data-testid="ex-error">
          {ex.error}
        </p>
      )}
      {ex.result && <ResultView result={ex.result} />}
    </section>
  );
}
