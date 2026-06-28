import { useState } from "react";
import { useModelStore } from "../../store/modelStore";

export function DslPanel() {
  const dsl = useModelStore((s) => s.dsl);
  const errors = useModelStore((s) => s.errors);
  const coverage = useModelStore((s) => s.coverage);
  const compileError = useModelStore((s) => s.compileError);
  const importError = useModelStore((s) => s.importError);
  const loadFromDsl = useModelStore((s) => s.loadFromDsl);
  const [draft, setDraft] = useState("");

  return (
    <aside className="lf-dsl">
      <h2>
        OpenFGA DSL <span className="lf-sub">(live preview)</span>
      </h2>
      <pre className="lf-dsl-pre" data-testid="dsl-preview">
        {dsl}
      </pre>

      {compileError && (
        <div className="lf-errors" data-testid="compile-error">
          <h3>Compile error</h3>
          <p>{compileError}</p>
        </div>
      )}

      {errors.length > 0 && (
        <div className="lf-errors" data-testid="validation-errors">
          <h3>Validation ({errors.length})</h3>
          <ul>
            {errors.map((e, i) => (
              <li key={i}>
                <code>{e.code}</code> <span className="lf-path">{e.path}</span> — {e.message}
              </li>
            ))}
          </ul>
        </div>
      )}

      {coverage && !coverage.fullyRepresentable && (
        <div className="lf-coverage" data-testid="coverage-advanced">
          <h3>Not fully representable</h3>
          {coverage.parseError && <p className="lf-parse-error">{coverage.parseError}</p>}
          {coverage.advanced.length > 0 && (
            <ul>
              {coverage.advanced.map((a, i) => (
                <li key={i}>
                  {a.type}.<strong>{a.relation}</strong>: {a.reason}
                </li>
              ))}
            </ul>
          )}
          {coverage.notes?.map((n, i) => (
            <p key={i} className="lf-note">
              {n}
            </p>
          ))}
        </div>
      )}

      <div className="lf-import">
        <h3>Import DSL</h3>
        <textarea
          data-testid="dsl-import"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          placeholder="paste an OpenFGA .fga model..."
          rows={6}
        />
        <button data-testid="load-dsl" onClick={() => loadFromDsl(draft)} disabled={!draft.trim()}>
          Load DSL
        </button>
        {importError && (
          <p className="lf-parse-error" data-testid="import-error">
            {importError}
          </p>
        )}
      </div>
    </aside>
  );
}
