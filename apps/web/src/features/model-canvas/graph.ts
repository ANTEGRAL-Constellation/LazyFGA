import type { ModelIR } from "@lazyfga/shared";
import type { Coverage } from "@lazyfga/compiler";
import type { Edge, Node } from "@xyflow/react";
import { MarkerType } from "@xyflow/react";
import type { XY } from "../../store/modelStore";

export interface AdvancedTag {
  relation: string;
  reason: string;
}

export interface TypeNodeData extends Record<string, unknown> {
  label: string;
  kind: "group" | "resource";
  roles: number;
  permissions: number;
  members: number;
  advanced: AdvancedTag[];
  readOnly: boolean;
}

export type TypeNode = Node<TypeNodeData>;

/** coverage.advanced 를 type별로 묶는다. */
export function advancedByType(coverage: Coverage | null): Record<string, AdvancedTag[]> {
  const map: Record<string, AdvancedTag[]> = {};
  for (const a of coverage?.advanced ?? []) {
    (map[a.type] ??= []).push({ relation: a.relation, reason: a.reason });
  }
  return map;
}

export function buildNodes(
  ir: ModelIR,
  positions: Record<string, XY>,
  advanced: Record<string, AdvancedTag[]>,
  readOnly: boolean,
): TypeNode[] {
  const nodes: TypeNode[] = [];
  for (const g of ir.groups) {
    nodes.push({
      id: g.name,
      type: "lazyfgaType",
      position: positions[g.name] ?? { x: 0, y: 0 },
      data: {
        label: g.name,
        kind: "group",
        roles: 0,
        permissions: 0,
        members: g.memberTypes.length,
        advanced: advanced[g.name] ?? [],
        readOnly,
      },
    });
  }
  for (const r of ir.resources) {
    nodes.push({
      id: r.name,
      type: "lazyfgaType",
      position: positions[r.name] ?? { x: 0, y: 0 },
      data: {
        label: r.name,
        kind: "resource",
        roles: r.roles.length,
        permissions: r.permissions.length,
        members: 0,
        advanced: advanced[r.name] ?? [],
        readOnly,
      },
    });
  }
  return nodes;
}

export interface ParentEdgeData extends Record<string, unknown> {
  relationName: string;
}

/** 상속 엣지: child(resource) → parentType, relationName 라벨. */
export function buildEdges(ir: ModelIR): Edge<ParentEdgeData>[] {
  const edges: Edge<ParentEdgeData>[] = [];
  for (const r of ir.resources) {
    for (const p of r.parents) {
      for (const pt of p.parentTypes) {
        edges.push({
          id: `${r.name}|${p.relationName}|${pt}`,
          source: r.name,
          target: pt,
          label: p.relationName,
          data: { relationName: p.relationName },
          markerEnd: { type: MarkerType.ArrowClosed },
        });
      }
    }
  }
  return edges;
}
