import { Handle, Position, type NodeProps } from "@xyflow/react";
import type { TypeNode } from "./graph";

/** resource/group 공용 타입 노드. 상단=target 핸들(부모로 받기), 하단=source 핸들(자식으로 연결). */
export function TypeNodeView({ data }: NodeProps<TypeNode>) {
  const isGroup = data.kind === "group";
  const hasAdvanced = data.advanced.length > 0;
  return (
    <div
      className={`lf-node ${isGroup ? "lf-node-group" : "lf-node-resource"}${
        hasAdvanced ? " lf-node-advanced" : ""
      }`}
      data-testid={`node-${data.label}`}
    >
      <Handle type="target" position={Position.Top} />
      <div className="lf-node-title">
        <span className="lf-node-kind">{isGroup ? "group" : "resource"}</span>
        <strong>{data.label}</strong>
      </div>
      <div className="lf-node-meta">
        {isGroup ? (
          <span>{data.members} member type(s)</span>
        ) : (
          <span>
            {data.roles} role(s) · {data.permissions} perm(s)
          </span>
        )}
      </div>
      {hasAdvanced && (
        <div
          className="lf-node-badge"
          title={data.advanced.map((a) => `${a.relation}: ${a.reason}`).join("\n")}
        >
          advanced
        </div>
      )}
      <Handle type="source" position={Position.Bottom} />
    </div>
  );
}

export const nodeTypes = { lazyfgaType: TypeNodeView };
