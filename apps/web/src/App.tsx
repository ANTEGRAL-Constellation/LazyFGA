import { DslPanel } from "./features/model-canvas/DslPanel";
import { ModelCanvas } from "./features/model-canvas/ModelCanvas";
import { ExplainPanel } from "./features/explain/ExplainPanel";
import { MatrixPanel } from "./features/permission-matrix/MatrixPanel";
import { ConditionBuilderPanel } from "./features/condition-builder/ConditionBuilderPanel";

/** lazyfga-5/6/12/13: 모델 스튜디오 — 노드 캔버스 + 실시간 DSL + 행렬 + explain + 조건 빌더. */
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
        </aside>
      </div>
      <MatrixPanel />
    </div>
  );
}
