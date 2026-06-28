import type { ModelIR, ValidationError } from "@lazyfga/shared";
import type { Coverage } from "@lazyfga/compiler";
import type { Connection, Edge, NodeChange } from "@xyflow/react";
import { useMemo } from "react";
import { useModelStore } from "../../store/modelStore";
import {
  advancedByType,
  buildEdges,
  buildNodes,
  type ParentEdgeData,
  type TypeNode,
} from "./graph";

/** ModelIR ⇄ React Flow 노드/엣지 파생 및 변형 액션(lazyfga-5). */
export function useModelGraph() {
  const ir = useModelStore((s) => s.ir);
  const positions = useModelStore((s) => s.positions);
  const coverage = useModelStore((s) => s.coverage);
  const readOnly = useModelStore((s) => s.readOnly);
  const dsl = useModelStore((s) => s.dsl);
  const errors = useModelStore((s) => s.errors);

  const addResource = useModelStore((s) => s.addResource);
  const addGroup = useModelStore((s) => s.addGroup);
  const removeType = useModelStore((s) => s.removeType);
  const connectParent = useModelStore((s) => s.connectParent);
  const disconnectParent = useModelStore((s) => s.disconnectParent);
  const setPosition = useModelStore((s) => s.setPosition);
  const setSelected = useModelStore((s) => s.setSelected);

  const advanced = useMemo(() => advancedByType(coverage), [coverage]);
  const nodes = useMemo(
    () => buildNodes(ir, positions, advanced, readOnly),
    [ir, positions, advanced, readOnly],
  );
  const edges = useMemo(() => buildEdges(ir), [ir]);

  const onNodesChange = (changes: NodeChange<TypeNode>[]): void => {
    for (const ch of changes) {
      if (ch.type === "position" && ch.position) {
        if (!readOnly) setPosition(ch.id, ch.position);
      } else if (ch.type === "remove") {
        removeType(ch.id); // store가 readOnly를 가드
      }
    }
  };

  // 엣지 식별을 id 파싱이 아니라 edge.data/source/target에서 읽어 이름에 '|'가 있어도 안전.
  const onEdgesDelete = (deleted: Edge<ParentEdgeData>[]): void => {
    for (const e of deleted) {
      disconnectParent(e.source, e.data?.relationName ?? "parent", e.target);
    }
  };

  const onConnect = (c: Connection): void => {
    if (c.source && c.target) connectParent(c.source, c.target);
  };

  return {
    ir,
    nodes,
    edges,
    coverage,
    readOnly,
    dsl,
    errors,
    addResource,
    addGroup,
    removeType,
    connectParent,
    disconnectParent,
    onNodesChange,
    onEdgesDelete,
    onConnect,
    selectType: setSelected,
  } satisfies {
    ir: ModelIR;
    nodes: TypeNode[];
    edges: Edge<ParentEdgeData>[];
    coverage: Coverage | null;
    readOnly: boolean;
    dsl: string;
    errors: ValidationError[];
    [k: string]: unknown;
  };
}
