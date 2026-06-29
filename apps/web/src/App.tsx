import { DslPanel } from "./features/model-canvas/DslPanel";
import { ModelCanvas } from "./features/model-canvas/ModelCanvas";
import { ExplainPanel } from "./features/explain/ExplainPanel";
import { MatrixPanel } from "./features/permission-matrix/MatrixPanel";
import { ConditionBuilderPanel } from "./features/condition-builder/ConditionBuilderPanel";
import { AuditPanel } from "./features/audit/AuditPanel";
import { PlaygroundPanel } from "./features/playground/PlaygroundPanel";

/** lazyfga-5/6/12/13/17/18: 캔버스 + DSL + 행렬 + explain + 조건 + playground + audit. */
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
          <PlaygroundPanel />
          <ExplainPanel />
          <AuditPanel />
        </aside>
      </div>
      <MatrixPanel />
    </div>
  );
}
