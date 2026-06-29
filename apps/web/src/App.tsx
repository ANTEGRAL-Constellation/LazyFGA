import { DslPanel } from "./features/model-canvas/DslPanel";
import { ModelCanvas } from "./features/model-canvas/ModelCanvas";
import { ExplainPanel } from "./features/explain/ExplainPanel";
import { MatrixPanel } from "./features/permission-matrix/MatrixPanel";
import { ConditionBuilderPanel } from "./features/condition-builder/ConditionBuilderPanel";
import { AuditPanel } from "./features/audit/AuditPanel";

/** lazyfga-5/6/12/13/17: 모델 스튜디오 — 캔버스 + DSL + 행렬 + explain + 조건 + audit. */
export function App() {
  return (
    <div className="lf-app">
      <header className="lf-header">
        <strong>lazyFGA</strong>
        <span className="lf-sub">model studio — draw the model, get OpenFGA</span>
      </header>
      <div className="lf-main">
        <ModelCanvas />
        <aside className="lf-sidebar">
          <DslPanel />
          <ConditionBuilderPanel />
          <ExplainPanel />
          <AuditPanel />
        </aside>
      </div>
      <MatrixPanel />
    </div>
  );
}
