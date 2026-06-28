import { DslPanel } from "./features/model-canvas/DslPanel";
import { ModelCanvas } from "./features/model-canvas/ModelCanvas";
import { ExplainPanel } from "./features/explain/ExplainPanel";
import { MatrixPanel } from "./features/permission-matrix/MatrixPanel";

/** lazyfga-5/6/12: 모델 스튜디오 — 노드 캔버스 + 실시간 DSL + 행렬 + explain. */
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
          <ExplainPanel />
        </aside>
      </div>
      <MatrixPanel />
    </div>
  );
}
