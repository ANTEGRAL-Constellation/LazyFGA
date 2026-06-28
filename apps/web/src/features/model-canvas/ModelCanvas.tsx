import { Background, Controls, ReactFlow, type Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { useModelStore } from "../../store/modelStore";
import type { TypeNodeData } from "./graph";
import { nodeTypes } from "./nodes";
import { useModelGraph } from "./useModelGraph";

export function ModelCanvas() {
  const g = useModelGraph();
  const resetTo = useModelStore((s) => s.resetTo);

  const promptAdd = (kind: "resource" | "group") => {
    const name = window.prompt(`New ${kind} type name`)?.trim();
    if (!name) return;
    if (kind === "resource") g.addResource(name);
    else g.addGroup(name);
  };

  return (
    <div className="lf-canvas" data-testid="model-canvas">
      <div className="lf-toolbar">
        <button data-testid="add-resource" disabled={g.readOnly} onClick={() => promptAdd("resource")}>
          + Resource
        </button>
        <button data-testid="add-group" disabled={g.readOnly} onClick={() => promptAdd("group")}>
          + Group
        </button>
        <button data-testid="load-sample" onClick={() => resetTo(docFolderTeamIR)}>
          Load sample
        </button>
        {g.readOnly && (
          <span className="lf-readonly-badge" data-testid="readonly-badge">
            read-only (advanced model)
          </span>
        )}
      </div>
      <ReactFlow
        nodes={g.nodes}
        edges={g.edges}
        nodeTypes={nodeTypes}
        onNodesChange={g.onNodesChange}
        onEdgesDelete={g.onEdgesDelete}
        onConnect={g.onConnect}
        nodesConnectable={!g.readOnly}
        nodesDraggable={!g.readOnly}
        deleteKeyCode={g.readOnly ? null : ["Backspace", "Delete"]}
        onNodeDoubleClick={(_, node: Node<TypeNodeData>) => {
          if (node.data.kind === "resource") g.selectType(node.id);
        }}
        fitView
      >
        <Background />
        <Controls />
      </ReactFlow>
    </div>
  );
}
