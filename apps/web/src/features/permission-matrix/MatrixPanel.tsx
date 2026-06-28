import type { SubjectRef } from "@lazyfga/shared";
import { useModelStore } from "../../store/modelStore";
import { useMatrix } from "./useMatrix";

function buildRefs(hasUser: boolean, groups: string[]): SubjectRef[] {
  const refs: SubjectRef[] = [];
  if (hasUser) refs.push({ kind: "user" });
  for (const g of groups) refs.push({ kind: "group", group: g, relation: "member" });
  return refs;
}

export function MatrixPanel() {
  const selectedType = useModelStore((s) => s.selectedType);
  const close = useModelStore((s) => s.setSelected);
  if (!selectedType) return null;
  return <MatrixPanelInner typeName={selectedType} onClose={() => close(null)} />;
}

function MatrixPanelInner({ typeName, onClose }: { typeName: string; onClose: () => void }) {
  const m = useMatrix(typeName);

  const groupsOf = (role: string): string[] => {
    const r = m.roles.find((x) => x.name === role);
    return (r?.assignableBy ?? [])
      .filter((ref): ref is Extract<SubjectRef, { kind: "group" }> => ref.kind === "group")
      .map((ref) => ref.group);
  };
  const hasUser = (role: string): boolean =>
    !!m.roles.find((x) => x.name === role)?.assignableBy.some((ref) => ref.kind === "user");

  const toggleUser = (role: string) => {
    m.setRoleAssignableBy(role, buildRefs(!hasUser(role), groupsOf(role)));
  };
  const toggleGroup = (role: string, g: string) => {
    const cur = groupsOf(role);
    const next = cur.includes(g) ? cur.filter((x) => x !== g) : [...cur, g];
    m.setRoleAssignableBy(role, buildRefs(hasUser(role), next));
  };

  return (
    <div className="lf-matrix" data-testid="matrix-panel">
      <div className="lf-matrix-head">
        <h2>
          <code>{typeName}</code> — roles × permissions
        </h2>
        <button data-testid="close-matrix" onClick={onClose}>
          close
        </button>
      </div>

      {m.readOnly && <p className="lf-readonly-badge">read-only model</p>}

      {m.errors.length > 0 && (
        <ul className="lf-errors" data-testid="matrix-errors">
          {m.errors.map((e, i) => (
            <li key={i}>
              <code>{e.code}</code> {e.message}
            </li>
          ))}
        </ul>
      )}

      <table className="lf-matrix-table">
        <thead>
          <tr>
            <th>permission \ role</th>
            {m.roles.map((r) => (
              <th key={r.name}>{r.name}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {m.permissions.map((p) => (
            <tr key={p.name}>
              <th>
                can_{p.name}
                <button
                  className="lf-mini"
                  data-testid={`rename-perm-${p.name}`}
                  disabled={m.readOnly}
                  onClick={() => {
                    const to = window.prompt("Rename permission to", p.name)?.trim();
                    if (to && to !== p.name) m.renamePermission(p.name, to);
                  }}
                >
                  ✎
                </button>
                <button
                  className="lf-mini"
                  data-testid={`remove-perm-${p.name}`}
                  disabled={m.readOnly}
                  onClick={() => m.removePermission(p.name)}
                >
                  ×
                </button>
              </th>
              {m.roles.map((r) => (
                <td key={r.name}>
                  <input
                    type="checkbox"
                    data-testid={`cell-${p.name}-${r.name}`}
                    checked={p.grantedByRoles.includes(r.name)}
                    disabled={m.readOnly}
                    onChange={() => m.toggleCell(p.name, r.name)}
                  />
                </td>
              ))}
            </tr>
          ))}
          {m.permissions.length === 0 && (
            <tr>
              <td colSpan={m.roles.length + 1}>no permissions yet</td>
            </tr>
          )}
        </tbody>
      </table>

      <div className="lf-matrix-actions">
        <button
          data-testid="add-role"
          disabled={m.readOnly}
          onClick={() => {
            const n = window.prompt("New role name")?.trim();
            if (n) m.addRole(n);
          }}
        >
          + role
        </button>
        <button
          data-testid="add-permission"
          disabled={m.readOnly}
          onClick={() => {
            const n = window.prompt("New permission (action) name")?.trim();
            if (n) m.addPermission(n);
          }}
        >
          + permission
        </button>
      </div>

      <section className="lf-roles-editor">
        <h3>Roles — assignable by</h3>
        {m.roles.map((r) => (
          <div key={r.name} className="lf-role-row" data-testid={`role-${r.name}`}>
            <strong>{r.name}</strong>
            <label>
              <input
                type="checkbox"
                checked={hasUser(r.name)}
                disabled={m.readOnly}
                onChange={() => toggleUser(r.name)}
              />
              user
            </label>
            {m.groups.map((g) => (
              <label key={g}>
                <input
                  type="checkbox"
                  checked={groupsOf(r.name).includes(g)}
                  disabled={m.readOnly}
                  onChange={() => toggleGroup(r.name, g)}
                />
                {g}#member
              </label>
            ))}
            <button
              className="lf-mini"
              disabled={m.readOnly}
              onClick={() => {
                const to = window.prompt("Rename role to", r.name)?.trim();
                if (to && to !== r.name) m.renameRole(r.name, to);
              }}
            >
              rename
            </button>
            <button
              className="lf-mini"
              data-testid={`remove-role-${r.name}`}
              disabled={m.readOnly}
              onClick={() => m.removeRole(r.name)}
            >
              remove
            </button>
          </div>
        ))}
      </section>

      {m.parents.length > 0 && (
        <section className="lf-inherit-editor">
          <h3>Inherit permission from parents</h3>
          {m.permissions.map((p) => (
            <div key={p.name} className="lf-inherit-row">
              <span>can_{p.name}</span>
              {m.parents.map((par) => (
                <label key={par.relationName}>
                  <input
                    type="checkbox"
                    data-testid={`inherit-${p.name}-${par.relationName}`}
                    checked={p.inheritFromParents.includes(par.relationName)}
                    disabled={m.readOnly}
                    onChange={() => m.toggleInherit(p.name, par.relationName)}
                  />
                  from {par.relationName} [{par.parentTypes.join(", ")}]
                </label>
              ))}
            </div>
          ))}
        </section>
      )}
    </div>
  );
}
