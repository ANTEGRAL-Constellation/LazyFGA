import type {
  ConditionDef,
  ConditionLeaf,
  ConditionNode,
  ConditionParam,
  ConditionParamType,
} from "@lazyfga/shared";

// lazyfga-14: ConditionDef ↔ OpenFGA CEL.
// 정방향(conditionToCel)은 임의 트리를 CEL로 emit한다. 역방향(tryParseCondition)은
// "우리가 생성한 제한 subset"(시간/IP/값 leaf의 단일 and/or 그룹)만 복원하고,
// 그 밖(중첩 그룹·혼합 연산자·미인식 leaf)은 null을 돌려 advanced(read-only)로 떨어뜨린다.

const isGroup = (n: ConditionNode): n is { op: "and" | "or"; children: ConditionNode[] } =>
  "children" in n;

const TIME_OP: Record<"lt" | "lte" | "gt" | "gte", string> = { lt: "<", lte: "<=", gt: ">", gte: ">=" };
const VALUE_OP: Record<"eq" | "neq" | "lt" | "lte" | "gt" | "gte", string> = {
  eq: "==",
  neq: "!=",
  lt: "<",
  lte: "<=",
  gt: ">",
  gte: ">=",
};
// OpenFGA DSL condition 파라미터 타입(우리 subset은 1:1).
const TYPE_NAME: Record<ConditionParamType, string> = {
  timestamp: "timestamp",
  ipaddress: "ipaddress",
  string: "string",
  int: "int",
  double: "double",
  bool: "bool",
};

function celLiteral(v: string | number | boolean): string {
  return typeof v === "string" ? JSON.stringify(v) : String(v);
}

function leafToCel(leaf: ConditionLeaf): string {
  if (leaf.kind === "time") {
    const rhs =
      leaf.rhs.kind === "literal" ? `timestamp(${JSON.stringify(leaf.rhs.rfc3339)})` : leaf.rhs.param;
    return `${leaf.param} ${TIME_OP[leaf.op]} ${rhs}`;
  }
  if (leaf.kind === "ip") return `${leaf.param}.in_cidr(${JSON.stringify(leaf.cidr)})`;
  return `${leaf.param} ${VALUE_OP[leaf.op]} ${celLiteral(leaf.value)}`;
}

/** node → CEL. top(=root)은 괄호 없이 평탄, 중첩 그룹만 괄호로 감싼다. */
function nodeToCel(node: ConditionNode, top: boolean): string {
  if (!isGroup(node)) return leafToCel(node);
  if (node.children.length === 0) return "true"; // 검증에서 막히지만 방어적.
  if (node.children.length === 1) return nodeToCel(node.children[0]!, top);
  const sep = node.op === "and" ? " && " : " || ";
  const inner = node.children.map((c) => nodeToCel(c, false)).join(sep);
  return top ? inner : `(${inner})`;
}

/** ConditionDef → OpenFGA condition 선언/본문(결정적). */
export function conditionToCel(def: ConditionDef): { decl: string; cel: string } {
  const params = def.params.map((p) => `${p.name}: ${TYPE_NAME[p.type]}`).join(", ");
  return { decl: `condition ${def.name}(${params})`, cel: nodeToCel(def.tree, true) };
}

// ── 역방향(제한 subset만) ────────────────────────────────────────────────────

const TIME_FROM_SYM: Record<string, "lt" | "lte" | "gt" | "gte" | undefined> = {
  "<": "lt",
  "<=": "lte",
  ">": "gt",
  ">=": "gte",
};
const VALUE_FROM_SYM: Record<string, "eq" | "neq" | "lt" | "lte" | "gt" | "gte"> = {
  "==": "eq",
  "!=": "neq",
  "<": "lt",
  "<=": "lte",
  ">": "gt",
  ">=": "gte",
};

/** top-level &&/|| 분리(괄호·따옴표 무시). 혼합 연산자면 null. */
function topLevelSplit(body: string): { op: "and" | "or"; parts: string[] } | null {
  const parts: string[] = [];
  let depth = 0;
  let inQuote = false;
  let start = 0;
  let op: "and" | "or" | null = null;
  for (let i = 0; i < body.length; i++) {
    const ch = body[i]!;
    if (inQuote) {
      if (ch === "\\") {
        i++; // 이스케이프된 다음 문자 건너뜀(예: \" 가 따옴표를 조기 종료하지 않도록).
        continue;
      }
      if (ch === '"') inQuote = false;
      continue;
    }
    if (ch === '"') inQuote = true;
    else if (ch === "(") depth++;
    else if (ch === ")") depth--;
    else if (depth === 0 && (ch === "&" || ch === "|") && body[i + 1] === ch) {
      const found = ch === "&" ? "and" : "or";
      if (op !== null && op !== found) return null; // 혼합 → 표현 불가
      op = found;
      parts.push(body.slice(start, i).trim());
      i++;
      start = i + 1;
    }
  }
  parts.push(body.slice(start).trim());
  return { op: op ?? "and", parts };
}

function paramTypeOf(params: ConditionParam[], name: string): ConditionParamType | undefined {
  return params.find((p) => p.name === name)?.type;
}

function parseCelLiteral(
  rhs: string,
  t: ConditionParamType,
): string | number | boolean | undefined {
  if (t === "string") {
    if (/^".*"$/.test(rhs)) {
      try {
        return JSON.parse(rhs) as string;
      } catch {
        return undefined;
      }
    }
    return undefined;
  }
  if (t === "bool") return rhs === "true" ? true : rhs === "false" ? false : undefined;
  if (t === "int") return /^-?\d+$/.test(rhs) ? Number(rhs) : undefined;
  if (t === "double")
    return /^-?\d+(\.\d+)?([eE][+-]?\d+)?$/.test(rhs) && Number.isFinite(Number(rhs))
      ? Number(rhs)
      : undefined;
  return undefined;
}

function parseLeaf(s: string, params: ConditionParam[]): ConditionLeaf | null {
  const ipm = /^(\w+)\.in_cidr\("([^"]+)"\)$/.exec(s);
  if (ipm) {
    if (paramTypeOf(params, ipm[1]!) !== "ipaddress") return null;
    return { kind: "ip", param: ipm[1]!, op: "in_cidr", cidr: ipm[2]! };
  }
  const m = /^(\w+)\s*(==|!=|<=|>=|<|>)\s*(.+)$/.exec(s);
  if (!m) return null;
  const lhs = m[1]!;
  const sym = m[2]!;
  const rhs = m[3]!.trim();
  const t = paramTypeOf(params, lhs);
  if (t === "timestamp") {
    const op = TIME_FROM_SYM[sym];
    if (!op) return null; // ==/!= 는 시간 subset 아님.
    const lit = /^timestamp\("([^"]+)"\)$/.exec(rhs);
    if (lit) return { kind: "time", param: lhs, op, rhs: { kind: "literal", rfc3339: lit[1]! } };
    if (/^\w+$/.test(rhs) && paramTypeOf(params, rhs) === "timestamp")
      return { kind: "time", param: lhs, op, rhs: { kind: "param", param: rhs } };
    return null;
  }
  if (t === "string" || t === "int" || t === "double" || t === "bool") {
    const op = VALUE_FROM_SYM[sym];
    if (!op) return null;
    const val = parseCelLiteral(rhs, t);
    if (val === undefined) return null;
    return { kind: "value", param: lhs, op, value: val };
  }
  return null;
}

/**
 * 우리가 생성한 제한 subset의 CEL만 ConditionDef로 복원. 그 밖은 null(=advanced).
 * @param celBody OpenFGA condition expression 본문(중괄호 제외).
 */
export function tryParseCondition(
  name: string,
  params: ConditionParam[],
  celBody: string,
): ConditionDef | null {
  const split = topLevelSplit(celBody.trim());
  if (!split) return null;
  const leaves: ConditionLeaf[] = [];
  for (const part of split.parts) {
    const leaf = parseLeaf(part, params);
    if (!leaf) return null;
    leaves.push(leaf);
  }
  const tree: ConditionNode =
    leaves.length === 1 ? leaves[0]! : { op: split.op, children: leaves };
  return { name, params, tree };
}
