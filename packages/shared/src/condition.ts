// 조건(ABAC/CEL) 트리 계약 (lazyfga-13).
// WAF식 And/Or 블록 빌더가 만들고, lazyfga-14가 OpenFGA CEL로 컴파일한다.
// 지원 피연산자는 전부 OpenFGA 네이티브 condition 파라미터 타입에 대응한다 — lazyFGA는
// 조건을 직접 평가하지 않고, CEL 생성·선언만 하며 평가는 OpenFGA가 Check 시 수행한다.
import { z } from "zod";
import { CEL_RESERVED, IDENT_RE, RESERVED_WORDS } from "./ident";

/** 조건 파라미터 타입. OpenFGA 네이티브 condition 파라미터 타입의 부분집합(MVP). */
export type ConditionParamType = "timestamp" | "ipaddress" | "string" | "int" | "double" | "bool";

export interface ConditionParam {
  /** CEL 파라미터 이름. 예: "current_time", "user_ip" */
  name: string;
  type: ConditionParamType;
}

/** 시간 비교의 우변: 고정 시각(literal) 또는 다른 timestamp 파라미터. */
export type TimeRhs =
  | { kind: "literal"; rfc3339: string }
  | { kind: "param"; param: string };

/** 단일 비교(leaf 술어). */
export type ConditionLeaf =
  | { kind: "time"; param: string; op: "lt" | "lte" | "gt" | "gte"; rhs: TimeRhs }
  | { kind: "ip"; param: string; op: "in_cidr"; cidr: string }
  | {
      kind: "value";
      param: string;
      op: "eq" | "neq" | "lt" | "lte" | "gt" | "gte";
      value: string | number | boolean;
    };

/** AND/OR 그룹(WAF식). children는 leaf 또는 중첩 그룹. */
export interface ConditionGroup {
  op: "and" | "or";
  children: ConditionNode[];
}
export type ConditionNode = ConditionGroup | ConditionLeaf;

/** 이름 붙은 재사용 조건. OpenFGA `condition <name>(<params>) { <CEL> }` 한 블록에 대응. */
export interface ConditionDef {
  name: string;
  params: ConditionParam[];
  tree: ConditionNode;
}

// ── 런타임 스키마(zod) ────────────────────────────────────────────────────────

const conditionParamTypeSchema = z.enum([
  "timestamp",
  "ipaddress",
  "string",
  "int",
  "double",
  "bool",
]);

const conditionParamSchema: z.ZodType<ConditionParam> = z.object({
  name: z.string(),
  type: conditionParamTypeSchema,
});

const timeRhsSchema: z.ZodType<TimeRhs> = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("literal"), rfc3339: z.string() }),
  z.object({ kind: z.literal("param"), param: z.string() }),
]);

const conditionLeafSchema: z.ZodType<ConditionLeaf> = z.discriminatedUnion("kind", [
  z.object({
    kind: z.literal("time"),
    param: z.string(),
    op: z.enum(["lt", "lte", "gt", "gte"]),
    rhs: timeRhsSchema,
  }),
  z.object({
    kind: z.literal("ip"),
    param: z.string(),
    op: z.literal("in_cidr"),
    cidr: z.string(),
  }),
  z.object({
    kind: z.literal("value"),
    param: z.string(),
    op: z.enum(["eq", "neq", "lt", "lte", "gt", "gte"]),
    value: z.union([z.string(), z.number(), z.boolean()]),
  }),
]);

const conditionNodeSchema: z.ZodType<ConditionNode> = z.lazy(() =>
  z.union([
    z.object({ op: z.enum(["and", "or"]), children: z.array(conditionNodeSchema) }),
    conditionLeafSchema,
  ]),
);

export const conditionDefSchema: z.ZodType<ConditionDef> = z.object({
  name: z.string(),
  params: z.array(conditionParamSchema),
  tree: conditionNodeSchema,
});

// ── 사람이 읽는 미리보기(describeCondition) ──────────────────────────────────────

const isGroup = (n: ConditionNode): n is ConditionGroup => "children" in n;

const TIME_OP: Record<"lt" | "lte" | "gt" | "gte", string> = {
  lt: "<",
  lte: "<=",
  gt: ">",
  gte: ">=",
};
const VALUE_OP: Record<"eq" | "neq" | "lt" | "lte" | "gt" | "gte", string> = {
  eq: "==",
  neq: "!=",
  lt: "<",
  lte: "<=",
  gt: ">",
  gte: ">=",
};

function describeLeaf(leaf: ConditionLeaf): string {
  if (leaf.kind === "time") {
    const rhs = leaf.rhs.kind === "literal" ? leaf.rhs.rfc3339 : leaf.rhs.param;
    return `${leaf.param} ${TIME_OP[leaf.op]} ${rhs}`;
  }
  if (leaf.kind === "ip") return `${leaf.param} in ${leaf.cidr}`;
  const v = typeof leaf.value === "string" ? `"${leaf.value}"` : String(leaf.value);
  return `${leaf.param} ${VALUE_OP[leaf.op]} ${v}`;
}

/**
 * 조건 트리를 사람이 읽는 한 줄로 렌더(순수, 결정적).
 * child가 2개 이상인 그룹은 괄호로 감싸 and/or 우선순위 모호성을 제거한다.
 */
export function describeCondition(node: ConditionNode): string {
  if (!isGroup(node)) return describeLeaf(node);
  if (node.children.length === 0) return "(empty)";
  if (node.children.length === 1) return describeCondition(node.children[0]!);
  const sep = node.op === "and" ? " AND " : " OR ";
  return `(${node.children.map(describeCondition).join(sep)})`;
}

// ── 정적 검증(validateConditionDef) ───────────────────────────────────────────

export type ConditionErrorCode =
  | "BAD_NAME"
  | "DUP_PARAM"
  | "UNKNOWN_PARAM"
  | "TYPE_MISMATCH"
  | "BAD_CIDR"
  | "BAD_TIMESTAMP"
  | "EMPTY_GROUP";

export interface ConditionError {
  code: ConditionErrorCode;
  path: string;
  message: string;
}

const RFC3339_RE = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;

/** IPv4/IPv6 CIDR(addr/prefix) 형식 검사(보수적). */
function isCidr(s: string): boolean {
  const slash = s.lastIndexOf("/");
  if (slash < 0) return false;
  const addr = s.slice(0, slash);
  const prefixStr = s.slice(slash + 1);
  if (!/^\d+$/.test(prefixStr)) return false;
  const prefix = Number(prefixStr);
  if (addr.includes(":")) {
    // IPv6(보수적): hex 그룹 + 콜론(IPv4-mapped 위해 점 허용), prefix 0~128.
    return prefix >= 0 && prefix <= 128 && /^[0-9a-fA-F:.]+$/.test(addr) && addr.length > 0;
  }
  const octets = addr.split(".");
  if (octets.length !== 4) return false;
  if (!octets.every((o) => /^\d{1,3}$/.test(o) && Number(o) <= 255)) return false;
  return prefix >= 0 && prefix <= 32;
}

const VALUE_TYPES: ReadonlySet<ConditionParamType> = new Set([
  "string",
  "int",
  "double",
  "bool",
]);
const ORDER_OPS: ReadonlySet<string> = new Set(["lt", "lte", "gt", "gte"]);

/** 조건 정의의 정적 검증(빈 배열 = 유효, 예외 없음). lazyfga-13 §4.3 규칙 1~7. */
export function validateConditionDef(def: ConditionDef): ConditionError[] {
  const errors: ConditionError[] = [];
  const add = (code: ConditionErrorCode, path: string, message: string): void => {
    errors.push({ code, path, message });
  };
  // condition/param 이름은 CEL 식으로 흘러가므로 CEL 예약어도 금지(상수식/문법파손 방지, lazyfga-14 hardening).
  const badIdent = (name: string): boolean =>
    !IDENT_RE.test(name) || RESERVED_WORDS.has(name) || CEL_RESERVED.has(name);

  // rule 1: 조건 이름.
  if (badIdent(def.name)) add("BAD_NAME", "name", `invalid condition name: "${def.name}"`);

  // params: rule 1(이름) + rule 2(유일).
  const paramType = new Map<string, ConditionParamType>();
  const seen = new Set<string>();
  def.params.forEach((p, i) => {
    if (badIdent(p.name)) add("BAD_NAME", `params[${i}].name`, `invalid param name: "${p.name}"`);
    if (seen.has(p.name)) add("DUP_PARAM", `params[${i}].name`, `duplicate param: "${p.name}"`);
    seen.add(p.name);
    paramType.set(p.name, p.type);
  });

  const expectParam = (param: string, path: string, wanted: ConditionParamType[]): void => {
    if (!paramType.has(param)) {
      add("UNKNOWN_PARAM", `${path}.param`, `unknown param: "${param}"`);
      return;
    }
    const t = paramType.get(param)!;
    if (!wanted.includes(t)) {
      add("TYPE_MISMATCH", `${path}.param`, `param "${param}" is ${t}, expected ${wanted.join("|")}`);
    }
  };

  const visitLeaf = (leaf: ConditionLeaf, path: string): void => {
    if (leaf.kind === "time") {
      expectParam(leaf.param, path, ["timestamp"]);
      if (leaf.rhs.kind === "literal") {
        if (!RFC3339_RE.test(leaf.rhs.rfc3339)) {
          add("BAD_TIMESTAMP", `${path}.rhs.rfc3339`, `invalid RFC3339: "${leaf.rhs.rfc3339}"`);
        }
      } else if (!paramType.has(leaf.rhs.param)) {
        add("UNKNOWN_PARAM", `${path}.rhs.param`, `unknown param: "${leaf.rhs.param}"`);
      } else if (paramType.get(leaf.rhs.param) !== "timestamp") {
        add("TYPE_MISMATCH", `${path}.rhs.param`, `param "${leaf.rhs.param}" must be timestamp`);
      }
    } else if (leaf.kind === "ip") {
      expectParam(leaf.param, path, ["ipaddress"]);
      if (!isCidr(leaf.cidr)) add("BAD_CIDR", `${path}.cidr`, `invalid CIDR: "${leaf.cidr}"`);
    } else {
      expectParam(leaf.param, path, ["string", "int", "double", "bool"]);
      const t = paramType.get(leaf.param);
      if (t !== undefined && VALUE_TYPES.has(t)) {
        const v = leaf.value;
        const ok =
          (t === "string" && typeof v === "string") ||
          (t === "int" && typeof v === "number" && Number.isSafeInteger(v)) ||
          (t === "double" && typeof v === "number" && Number.isFinite(v)) ||
          (t === "bool" && typeof v === "boolean");
        if (!ok) {
          add("TYPE_MISMATCH", `${path}.value`, `value ${JSON.stringify(v)} does not match param type ${t}`);
        }
        if (t === "bool" && ORDER_OPS.has(leaf.op)) {
          add("TYPE_MISMATCH", `${path}.op`, `ordering op "${leaf.op}" not allowed on bool param`);
        }
      }
    }
  };

  const visit = (node: ConditionNode, path: string): void => {
    if (isGroup(node)) {
      if (node.children.length === 0) add("EMPTY_GROUP", path, "group must have >= 1 child");
      node.children.forEach((c, i) => visit(c, `${path}.children[${i}]`));
      return;
    }
    visitLeaf(node, path);
  };

  visit(def.tree, "tree");
  return errors;
}
