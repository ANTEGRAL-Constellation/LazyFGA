import { useAudit } from "./useAudit";

// lazyfga-17: 감사 로그 뷰(admin 토큰 필요 — explain의 service 토큰으로는 403).
export function AuditPanel(): JSX.Element {
  const a = useAudit();
  return (
    <section className="lf-audit" data-testid="audit-panel">
      <h2>
        Audit <span className="lf-sub">control-plane changes (admin)</span>
      </h2>
      <div className="lf-audit-form">
        <label>
          admin token
          <input
            type="password"
            value={a.token}
            onChange={(e) => a.setToken(e.target.value)}
            placeholder="admin token"
            data-testid="audit-token"
          />
        </label>
        <span className="lf-row">
          <input
            value={a.action}
            onChange={(e) => a.setAction(e.target.value)}
            placeholder="action filter"
            data-testid="audit-action"
          />
          <input
            value={a.actor}
            onChange={(e) => a.setActor(e.target.value)}
            placeholder="actor filter"
            data-testid="audit-actor"
          />
        </span>
        <button onClick={() => void a.load(false)} disabled={a.loading} data-testid="audit-load">
          {a.loading ? "loading..." : "Load audit"}
        </button>
      </div>

      {a.error && (
        <p className="lf-parse-error" data-testid="audit-error">
          {a.error}
        </p>
      )}

      {a.entries.length > 0 && (
        <table className="lf-audit-table" data-testid="audit-table">
          <thead>
            <tr>
              <th>time</th>
              <th>actor</th>
              <th>action</th>
              <th>data</th>
            </tr>
          </thead>
          <tbody>
            {a.entries.map((e) => (
              <tr key={e.id}>
                <td>{new Date(e.occurredAt).toLocaleString()}</td>
                <td>{e.actor}</td>
                <td>{e.action}</td>
                <td>
                  <code>{JSON.stringify(e.data)}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {a.hasMore && (
        <button onClick={() => void a.load(true)} disabled={a.loading} data-testid="audit-more">
          Load more
        </button>
      )}
    </section>
  );
}
