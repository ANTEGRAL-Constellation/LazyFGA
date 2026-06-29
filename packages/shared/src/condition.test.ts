import { describe, expect, test } from "bun:test";
import {
  conditionDefSchema,
  describeCondition,
  isValidConditionName,
  validateConditionDef,
  type ConditionDef,
  type ConditionNode,
} from "./condition";

// CONCEPT 플래그십 예시: "업무시간 AND 사내 IP".
const businessHoursAndInternalIp: ConditionDef = {
  name: "business_hours_internal",
  params: [
    { name: "current_time", type: "timestamp" },
    { name: "expiry", type: "timestamp" },
    { name: "user_ip", type: "ipaddress" },
  ],
  tree: {
    op: "and",
    children: [
      { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
      { kind: "ip", param: "user_ip", op: "in_cidr", cidr: "10.0.0.0/8" },
    ],
  },
};

describe("conditionDefSchema (zod shape)", () => {
  test("accepts a valid def", () => {
    expect(conditionDefSchema.safeParse(businessHoursAndInternalIp).success).toBe(true);
  });
  test("rejects unknown leaf kind", () => {
    const bad = { name: "x", params: [], tree: { kind: "regex", param: "p" } };
    expect(conditionDefSchema.safeParse(bad).success).toBe(false);
  });
  test("accepts nested groups", () => {
    const nested: ConditionDef = {
      name: "n",
      params: [{ name: "t", type: "timestamp" }],
      tree: {
        op: "or",
        children: [
          { op: "and", children: [{ kind: "time", param: "t", op: "gt", rhs: { kind: "literal", rfc3339: "2026-01-01T00:00:00Z" } }] },
        ],
      },
    };
    expect(conditionDefSchema.safeParse(nested).success).toBe(true);
  });
});

describe("describeCondition", () => {
  test("renders 'AND' of two leaves with parens", () => {
    expect(describeCondition(businessHoursAndInternalIp.tree)).toBe(
      "(current_time < expiry AND user_ip in 10.0.0.0/8)",
    );
  });
  test("single-child group has no parens", () => {
    const node: ConditionNode = { op: "and", children: [{ kind: "value", param: "tier", op: "eq", value: "gold" }] };
    expect(describeCondition(node)).toBe('tier == "gold"');
  });
  test("empty group → (empty)", () => {
    expect(describeCondition({ op: "and", children: [] })).toBe("(empty)");
  });
  test("nested group parenthesized", () => {
    const node: ConditionNode = {
      op: "or",
      children: [
        { kind: "value", param: "a", op: "eq", value: 1 },
        { op: "and", children: [
          { kind: "value", param: "b", op: "gt", value: 2 },
          { kind: "value", param: "c", op: "lt", value: 3 },
        ] },
      ],
    };
    expect(describeCondition(node)).toBe("(a == 1 OR (b > 2 AND c < 3))");
  });
});

describe("validateConditionDef", () => {
  test("valid def → no errors", () => {
    expect(validateConditionDef(businessHoursAndInternalIp)).toEqual([]);
  });

  test("BAD_NAME on reserved condition name / param", () => {
    const errs = validateConditionDef({
      name: "with", // reserved word
      params: [{ name: "bad-name", type: "string" }], // hyphen → invalid ident
      tree: { kind: "value", param: "bad-name", op: "eq", value: "x" },
    });
    expect(errs.some((e) => e.code === "BAD_NAME" && e.path === "name")).toBe(true);
    expect(errs.some((e) => e.code === "BAD_NAME" && e.path.startsWith("params["))).toBe(true);
  });

  test("DUP_PARAM", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [
        { name: "p", type: "string" },
        { name: "p", type: "int" },
      ],
      tree: { kind: "value", param: "p", op: "eq", value: "x" },
    });
    expect(errs.some((e) => e.code === "DUP_PARAM")).toBe(true);
  });

  test("UNKNOWN_PARAM when leaf references undeclared param", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [],
      tree: { kind: "value", param: "ghost", op: "eq", value: "x" },
    });
    expect(errs.some((e) => e.code === "UNKNOWN_PARAM")).toBe(true);
  });

  test("TYPE_MISMATCH: time leaf on non-timestamp param", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [{ name: "p", type: "string" }],
      tree: { kind: "time", param: "p", op: "lt", rhs: { kind: "literal", rfc3339: "2026-01-01T00:00:00Z" } },
    });
    expect(errs.some((e) => e.code === "TYPE_MISMATCH")).toBe(true);
  });

  test("TYPE_MISMATCH: value literal does not match int param", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [{ name: "n", type: "int" }],
      tree: { kind: "value", param: "n", op: "eq", value: "not-a-number" },
    });
    expect(errs.some((e) => e.code === "TYPE_MISMATCH" && e.path.endsWith(".value"))).toBe(true);
  });

  test("TYPE_MISMATCH: ordering op on bool param", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [{ name: "b", type: "bool" }],
      tree: { kind: "value", param: "b", op: "gt", value: true },
    });
    expect(errs.some((e) => e.code === "TYPE_MISMATCH" && e.path.endsWith(".op"))).toBe(true);
  });

  test("BAD_CIDR", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [{ name: "ip", type: "ipaddress" }],
      tree: { kind: "ip", param: "ip", op: "in_cidr", cidr: "999.0.0.0/8" },
    });
    expect(errs.some((e) => e.code === "BAD_CIDR")).toBe(true);
  });

  test("valid IPv4 and IPv6 CIDR pass", () => {
    const mk = (cidr: string): ConditionDef => ({
      name: "c",
      params: [{ name: "ip", type: "ipaddress" }],
      tree: { kind: "ip", param: "ip", op: "in_cidr", cidr },
    });
    expect(validateConditionDef(mk("192.168.1.0/24"))).toEqual([]);
    expect(validateConditionDef(mk("2001:db8::/32"))).toEqual([]);
    expect(validateConditionDef(mk("::ffff:192.168.0.0/96"))).toEqual([]); // IPv4-mapped IPv6
  });

  test("BAD_TIMESTAMP on malformed literal", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [{ name: "t", type: "timestamp" }],
      tree: { kind: "time", param: "t", op: "lt", rhs: { kind: "literal", rfc3339: "yesterday" } },
    });
    expect(errs.some((e) => e.code === "BAD_TIMESTAMP")).toBe(true);
  });

  test("time rhs param must be a declared timestamp", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [
        { name: "t", type: "timestamp" },
        { name: "s", type: "string" },
      ],
      tree: { kind: "time", param: "t", op: "lt", rhs: { kind: "param", param: "s" } },
    });
    expect(errs.some((e) => e.code === "TYPE_MISMATCH" && e.path.endsWith(".rhs.param"))).toBe(true);
  });

  test("EMPTY_GROUP", () => {
    const errs = validateConditionDef({ name: "c", params: [], tree: { op: "and", children: [] } });
    expect(errs.some((e) => e.code === "EMPTY_GROUP")).toBe(true);
  });

  test("rejects CEL-reserved names (true/false/null + type tokens) — would emit constant/broken CEL", () => {
    const bad = (name: string, type: ConditionDef["params"][number]["type"]): boolean =>
      validateConditionDef({
        name: "c",
        params: [{ name, type }],
        tree: { kind: "value", param: name, op: "eq", value: type === "bool" ? true : "x" },
      }).some((e) => e.code === "BAD_NAME");
    expect(bad("true", "bool")).toBe(true); // would emit `true == true`
    expect(bad("int", "int")).toBe(true); // type token breaks declaration grammar
    expect(bad("string", "string")).toBe(true);
    // condition NAME equal to a type token is rejected too
    expect(
      validateConditionDef({
        name: "timestamp",
        params: [{ name: "t", type: "timestamp" }],
        tree: { kind: "time", param: "t", op: "lt", rhs: { kind: "literal", rfc3339: "2026-01-01T00:00:00Z" } },
      }).some((e) => e.code === "BAD_NAME"),
    ).toBe(true);
  });

  test("isValidConditionName (rename guard)", () => {
    expect(isValidConditionName("non_expired")).toBe(true);
    expect(isValidConditionName("true")).toBe(false);
    expect(isValidConditionName("int")).toBe(false);
    expect(isValidConditionName("")).toBe(false);
    expect(isValidConditionName("bad-name")).toBe(false);
  });

  test("rejects out-of-safe-range int literal (round-trip safety)", () => {
    const errs = validateConditionDef({
      name: "c",
      params: [{ name: "n", type: "int" }],
      tree: { kind: "value", param: "n", op: "eq", value: 1e21 },
    });
    expect(errs.some((e) => e.code === "TYPE_MISMATCH")).toBe(true);
  });
});
