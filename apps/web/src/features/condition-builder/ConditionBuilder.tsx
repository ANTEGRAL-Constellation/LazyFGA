import {
  describeCondition,
  validateConditionDef,
  type ConditionDef,
  type ConditionGroup,
  type ConditionLeaf,
  type ConditionNode,
  type ConditionParam,
  type ConditionParamType,
} from "@lazyfga/shared";
import { useState } from "react";

// lazyfga-13: WAF식 And/Or 조건 빌더. ConditionDef 1개를 value/onChange로 편집한다.
// CEL 컴파일·모델 부착은 lazyfga-14. 여기서는 저작 + 사람이 읽는 미리보기 + 인라인 검증만.

const PARAM_TYPES: ConditionParamType[] = [
  "timestamp",
  "ipaddress",
  "string",
  "int",
  "double",
  "bool",
];
const TIME_OPS = ["lt", "lte", "gt", "gte"] as const;
const VALUE_OPS = ["eq", "neq", "lt", "lte", "gt", "gte"] as const;
const LEAF_KINDS = ["time", "ip", "value"] as const;

const isGroup = (n: ConditionNode): n is ConditionGroup => "children" in n;

/** value 트리의 root를 단일 그룹으로 정규화(bare leaf면 감싼다). */
function rootGroup(tree: ConditionNode): ConditionGroup {
  return isGroup(tree) ? tree : { op: "and", children: [tree] };
}

function defaultLeaf(kind: ConditionLeaf["kind"], params: ConditionParam[]): ConditionLeaf {
  const ofType = (t: ConditionParamType): string => params.find((p) => p.type === t)?.name ?? "";
  if (kind === "time")
    return {
      kind: "time",
      param: ofType("timestamp"),
      op: "lt",
      rhs: { kind: "literal", rfc3339: "2026-01-01T09:00:00Z" },
    };
  if (kind === "ip")
    return { kind: "ip", param: ofType("ipaddress"), op: "in_cidr", cidr: "10.0.0.0/8" };
  const valueParam =
    params.find((p) => ["string", "int", "double", "bool"].includes(p.type))?.name ?? "";
  return { kind: "value", param: valueParam, op: "eq", value: "" };
}

export function ConditionBuilder({
  value,
  onChange,
}: {
  value: ConditionDef;
  onChange(next: ConditionDef): void;
}): JSX.Element {
  const root = rootGroup(value.tree);
  const nested = root.children.some(isGroup);
  const errors = validateConditionDef(value);

  const emit = (patch: Partial<ConditionDef>): void => onChange({ ...value, ...patch });
  const emitTree = (children: ConditionNode[], op: "and" | "or" = root.op): void =>
    emit({ tree: { op, children } });

  // ── params ──
  const addParam = (): void => emit({ params: [...value.params, { name: "", type: "string" }] });
  const updateParam = (i: number, patch: Partial<ConditionParam>): void =>
    emit({ params: value.params.map((p, idx) => (idx === i ? { ...p, ...patch } : p)) });
  const removeParam = (i: number): void =>
    emit({ params: value.params.filter((_, idx) => idx !== i) });

  // ── leaves (root group children; 편집은 nested가 아닐 때만) ──
  const addLeaf = (): void => emitTree([...root.children, defaultLeaf("value", value.params)]);
  const removeLeaf = (i: number): void =>
    emitTree(root.children.filter((_, idx) => idx !== i));
  const setLeaf = (i: number, leaf: ConditionLeaf): void =>
    emitTree(root.children.map((c, idx) => (idx === i ? leaf : c)));

  const paramType = (name: string): ConditionParamType | undefined =>
    value.params.find((p) => p.name === name)?.type;

  return (
    <div className="lf-cond" data-testid="condition-builder">
      {nested && (
        <p className="lf-readonly-badge" data-testid="cond-nested-readonly">
          nested groups are read-only in this builder (edit as DSL)
        </p>
      )}

      <label className="lf-cond-name">
        condition name
        <input
          value={value.name}
          disabled={nested}
          onChange={(e) => emit({ name: e.target.value })}
          data-testid="cond-name"
        />
      </label>

      <div className="lf-cond-params" data-testid="cond-params">
        <div className="lf-cond-sub">parameters</div>
        {value.params.map((p, i) => (
          <span className="lf-row" key={i}>
            <input
              value={p.name}
              placeholder="name"
              disabled={nested}
              onChange={(e) => updateParam(i, { name: e.target.value })}
              data-testid={`cond-param-name-${i}`}
            />
            <select
              value={p.type}
              disabled={nested}
              onChange={(e) => updateParam(i, { type: e.target.value as ConditionParamType })}
              data-testid={`cond-param-type-${i}`}
            >
              {PARAM_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
            <button onClick={() => removeParam(i)} disabled={nested} data-testid={`cond-param-del-${i}`}>
              ✕
            </button>
          </span>
        ))}
        <button onClick={addParam} disabled={nested} data-testid="cond-param-add">
          + param
        </button>
      </div>

      {nested ? null : (
        <div className="lf-cond-rules" data-testid="cond-rules">
          <div className="lf-cond-sub">
            combine with{" "}
            <select
              value={root.op}
              onChange={(e) => emitTree(root.children, e.target.value as "and" | "or")}
              data-testid="cond-combinator"
            >
              <option value="and">AND</option>
              <option value="or">OR</option>
            </select>
          </div>
          {(root.children as ConditionLeaf[]).map((leaf, i) => (
            <LeafRow
              key={i}
              index={i}
              leaf={leaf}
              params={value.params}
              paramType={paramType}
              onChange={(l) => setLeaf(i, l)}
              onRemove={() => removeLeaf(i)}
            />
          ))}
          <button onClick={addLeaf} data-testid="cond-rule-add">
            + rule
          </button>
        </div>
      )}

      <div className="lf-cond-preview" data-testid="cond-preview">
        <span className="lf-cond-sub">preview</span>
        <code>{describeCondition(value.tree)}</code>
      </div>

      {errors.length > 0 && (
        <ul className="lf-cond-errors" data-testid="cond-errors">
          {errors.map((e, i) => (
            <li key={i}>
              <strong>{e.code}</strong> @ {e.path}: {e.message}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function LeafRow({
  index,
  leaf,
  params,
  paramType,
  onChange,
  onRemove,
}: {
  index: number;
  leaf: ConditionLeaf;
  params: ConditionParam[];
  paramType(name: string): ConditionParamType | undefined;
  onChange(leaf: ConditionLeaf): void;
  onRemove(): void;
}): JSX.Element {
  const changeKind = (kind: ConditionLeaf["kind"]): void => onChange(defaultLeaf(kind, params));

  const ParamSelect = (
    <select
      value={leaf.param}
      onChange={(e) => onChange({ ...leaf, param: e.target.value })}
      data-testid={`cond-rule-param-${index}`}
    >
      <option value="">(param)</option>
      {params.map((p) => (
        <option key={p.name} value={p.name}>
          {p.name}
        </option>
      ))}
    </select>
  );

  return (
    <div className="lf-row lf-cond-rule" data-testid={`cond-rule-${index}`}>
      <select
        value={leaf.kind}
        onChange={(e) => changeKind(e.target.value as ConditionLeaf["kind"])}
        data-testid={`cond-rule-kind-${index}`}
      >
        {LEAF_KINDS.map((k) => (
          <option key={k} value={k}>
            {k}
          </option>
        ))}
      </select>
      {ParamSelect}

      {leaf.kind === "time" && (
        <>
          <select
            value={leaf.op}
            onChange={(e) => onChange({ ...leaf, op: e.target.value as (typeof TIME_OPS)[number] })}
            data-testid={`cond-rule-op-${index}`}
          >
            {TIME_OPS.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <select
            value={leaf.rhs.kind}
            onChange={(e) =>
              onChange({
                ...leaf,
                rhs:
                  e.target.value === "param"
                    ? { kind: "param", param: "" }
                    : { kind: "literal", rfc3339: "2026-01-01T09:00:00Z" },
              })
            }
            data-testid={`cond-rule-rhskind-${index}`}
          >
            <option value="literal">literal</option>
            <option value="param">param</option>
          </select>
          {leaf.rhs.kind === "literal" ? (
            <input
              value={leaf.rhs.rfc3339}
              placeholder="RFC3339 time"
              onChange={(e) => onChange({ ...leaf, rhs: { kind: "literal", rfc3339: e.target.value } })}
              data-testid={`cond-rule-rhs-${index}`}
            />
          ) : (
            <select
              value={leaf.rhs.param}
              onChange={(e) => onChange({ ...leaf, rhs: { kind: "param", param: e.target.value } })}
              data-testid={`cond-rule-rhs-${index}`}
            >
              <option value="">(param)</option>
              {params
                .filter((p) => p.type === "timestamp")
                .map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.name}
                  </option>
                ))}
            </select>
          )}
        </>
      )}

      {leaf.kind === "ip" && (
        <>
          <span className="lf-cond-fixedop">in_cidr</span>
          <input
            value={leaf.cidr}
            placeholder="10.0.0.0/8"
            onChange={(e) => onChange({ ...leaf, cidr: e.target.value })}
            data-testid={`cond-rule-cidr-${index}`}
          />
        </>
      )}

      {leaf.kind === "value" && (
        <>
          <select
            value={leaf.op}
            onChange={(e) => onChange({ ...leaf, op: e.target.value as (typeof VALUE_OPS)[number] })}
            data-testid={`cond-rule-op-${index}`}
          >
            {VALUE_OPS.map((o) => (
              <option key={o} value={o}>
                {o}
              </option>
            ))}
          </select>
          <ValueInput
            key={leaf.param}
            index={index}
            value={leaf.value}
            type={paramType(leaf.param)}
            onChange={(v) => onChange({ ...leaf, value: v })}
          />
        </>
      )}

      <button onClick={onRemove} data-testid={`cond-rule-del-${index}`}>
        ✕
      </button>
    </div>
  );
}

function ValueInput({
  index,
  value,
  type,
  onChange,
}: {
  index: number;
  value: string | number | boolean;
  type: ConditionParamType | undefined;
  onChange(v: string | number | boolean): void;
}): JSX.Element {
  if (type === "bool") {
    return (
      <select
        value={String(value)}
        onChange={(e) => onChange(e.target.value === "true")}
        data-testid={`cond-rule-value-${index}`}
      >
        <option value="true">true</option>
        <option value="false">false</option>
      </select>
    );
  }
  return <TextValueInput index={index} value={value} type={type} onChange={onChange} />;
}

/**
 * 텍스트 값 입력. 로컬 텍스트 상태를 유지해 int/double 입력 중 "3." 같은 부분 입력이
 * 컨트롤드 echo로 사라지지 않게 한다(부모에는 가능하면 Number로 강제 전달).
 * 부모에서 param이 바뀌면 key로 remount되어 텍스트가 새 값으로 초기화된다.
 */
function TextValueInput({
  index,
  value,
  type,
  onChange,
}: {
  index: number;
  value: string | number | boolean;
  type: ConditionParamType | undefined;
  onChange(v: string | number | boolean): void;
}): JSX.Element {
  const [text, setText] = useState(String(value));
  return (
    <input
      value={text}
      placeholder="value"
      onChange={(e) => {
        const raw = e.target.value;
        setText(raw);
        const numeric =
          (type === "int" || type === "double") && raw.trim() !== "" && !Number.isNaN(Number(raw));
        onChange(numeric ? Number(raw) : raw);
      }}
      data-testid={`cond-rule-value-${index}`}
    />
  );
}
