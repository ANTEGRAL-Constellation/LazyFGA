import type { GrantEntry } from "@lazyfga/shared";
import { useGrants, type GrantForm } from "./useGrants";

// 회수 가능한 행인가 — id에 :,#,*,공백이 있으면(예: 시드된 user:* 와일드카드) revoke가 400으로
// 막히므로 버튼을 비활성화한다(목록 가시성은 유지하되 죽은 액션은 노출하지 않음, LFGA-20 review).
const FORBIDDEN_IN_ID = /[:#*\s]/;
function isRevocable(e: GrantEntry): boolean {
  return !FORBIDDEN_IN_ID.test(e.subject.id) && !FORBIDDEN_IN_ID.test(e.resource.id);
}

// lazyfga-20: 구조적 권한 grant/revoke/list (admin 토큰 필요).
// 발행본 모델에 정의된 직접 배정 가능 relation에만 동작한다(서버가 권위 검증).
export function GrantsPanel(): JSX.Element {
  const g = useGrants();
  const f = g.form;
  const set = (patch: Partial<GrantForm>): void => g.setForm({ ...f, ...patch });

  const subjectPreview = f.subjectRelation.trim()
    ? `${f.subjectType}:${f.subjectId}#${f.subjectRelation}`
    : `${f.subjectType}:${f.subjectId}`;

  return (
    <section className="lf-grants" data-testid="grants-panel">
      <h2>
        Grants <span className="lf-sub">structured grant / revoke (admin)</span>
      </h2>

      <label>
        admin token
        <input
          type="password"
          value={g.token}
          onChange={(e) => g.setToken(e.target.value)}
          placeholder="admin token"
          data-testid="grants-token"
        />
      </label>

      <fieldset className="lf-grants-form">
        <legend>subject → relation @ resource</legend>
        <div className="lf-row">
          <input
            value={f.subjectType}
            onChange={(e) => set({ subjectType: e.target.value })}
            placeholder="subject type (user)"
            data-testid="grants-subject-type"
          />
          <input
            value={f.subjectId}
            onChange={(e) => set({ subjectId: e.target.value })}
            placeholder="subject id"
            data-testid="grants-subject-id"
          />
          <input
            value={f.subjectRelation}
            onChange={(e) => set({ subjectRelation: e.target.value })}
            placeholder="userset relation (e.g. member) — optional"
            data-testid="grants-subject-relation"
          />
        </div>
        <div className="lf-row">
          <input
            value={f.relation}
            onChange={(e) => set({ relation: e.target.value })}
            placeholder="relation (role, e.g. editor)"
            data-testid="grants-relation"
          />
          <input
            value={f.resourceType}
            onChange={(e) => set({ resourceType: e.target.value })}
            placeholder="resource type (document)"
            data-testid="grants-resource-type"
          />
          <input
            value={f.resourceId}
            onChange={(e) => set({ resourceId: e.target.value })}
            placeholder="resource id"
            data-testid="grants-resource-id"
          />
        </div>
        <div className="lf-row">
          <input
            value={f.conditionName}
            onChange={(e) => set({ conditionName: e.target.value })}
            placeholder="condition name (optional, lazyfga-14)"
            data-testid="grants-condition"
          />
        </div>
        <p className="lf-sub" data-testid="grants-preview">
          <code>
            {f.resourceType}:{f.resourceId}#{f.relation}@{subjectPreview}
            {f.conditionName.trim() ? ` (with ${f.conditionName.trim()})` : ""}
          </code>
        </p>
        <div className="lf-row">
          <button onClick={() => void g.grant()} disabled={g.busy} data-testid="grants-grant">
            Grant
          </button>
          <button onClick={() => void g.revokeForm()} disabled={g.busy} data-testid="grants-revoke">
            Revoke
          </button>
          <button
            onClick={() => void g.listByResource()}
            disabled={g.busy}
            data-testid="grants-list-resource"
          >
            List by resource
          </button>
          <button
            onClick={() => void g.listBySubject()}
            disabled={g.busy}
            data-testid="grants-list-subject"
          >
            List by subject
          </button>
        </div>
      </fieldset>

      {g.status && (
        <p className="lf-status" data-testid="grants-status">
          {g.status}
        </p>
      )}
      {g.error && (
        <p className="lf-parse-error" data-testid="grants-error">
          {g.error}
        </p>
      )}

      {g.entries.length > 0 && (
        <table className="lf-grants-table" data-testid="grants-table">
          <thead>
            <tr>
              <th>subject</th>
              <th>relation</th>
              <th>resource</th>
              <th>condition</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {g.entries.map((e, i) => (
              <tr
                key={`${e.resource.type}:${e.resource.id}#${e.relation}@${e.subject.type}:${e.subject.id}#${e.subject.relation ?? ""}:${i}`}
              >
                <td>
                  <code>
                    {e.subject.type}:{e.subject.id}
                    {e.subject.relation ? `#${e.subject.relation}` : ""}
                  </code>
                </td>
                <td>{e.relation}</td>
                <td>
                  <code>
                    {e.resource.type}:{e.resource.id}
                  </code>
                </td>
                <td>{e.condition?.name ?? ""}</td>
                <td>
                  <button
                    className="lf-mini"
                    onClick={() => void g.revokeEntry(e)}
                    disabled={g.busy || !isRevocable(e)}
                    title={
                      isRevocable(e) ? "revoke" : "not revocable (wildcard/unsupported subject)"
                    }
                    data-testid={`grants-revoke-row-${i}`}
                  >
                    revoke
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}
