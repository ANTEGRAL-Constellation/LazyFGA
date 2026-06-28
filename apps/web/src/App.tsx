import { DslPanel } from "./features/model-canvas/DslPanel";
import { ModelCanvas } from "./features/model-canvas/ModelCanvas";
import { MatrixPanel } from "./features/permission-matrix/MatrixPanel";

/** lazyfga-5/6: 모델 스튜디오 — 노드 캔버스 + 실시간 DSL + role×permission 행렬. */
export function App() {
  return (
    <div className="lf-app">
      <header className="lf-header">
        <strong>lazyFGA</strong>
        <span className="lf-sub">model studio — draw the model, get OpenFGA</span>
      </header>
      <div className="lf-main">
        <ModelCanvas />
        <DslPanel />
      </div>
      <MatrixPanel />
    </div>
  );
}
