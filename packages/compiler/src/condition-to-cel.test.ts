import type { ConditionDef, ConditionParam, ModelIR } from "@lazyfga/shared";
import { describe, expect, test } from "bun:test";
import { conditionToCel, tryParseCondition } from "./condition-to-cel";
import { compileIrToDsl } from "./ir-to-dsl";
import { parseDslToIr } from "./dsl-to-ir";

const params: ConditionParam[] = [
  { name: "current_time", type: "timestamp" },
  { name: "expiry", type: "timestamp" },
  { name: "user_ip", type: "ipaddress" },
  { name: "tier", type: "string" },
];

describe("conditionToCel", () => {
  test("emits decl + flat AND body (no outer parens)", () => {
    const def: ConditionDef = {
      name: "rule",
      params,
      tree: {
        op: "and",
        children: [
          {
            kind: "time",
            param: "current_time",
            op: "lt",
            rhs: { kind: "param", param: "expiry" },
          },
          { kind: "ip", param: "user_ip", op: "in_cidr", cidr: "10.0.0.0/8" },
        ],
      },
    };
    const { decl, cel } = conditionToCel(def);
    expect(decl).toBe(
      "condition rule(current_time: timestamp, expiry: timestamp, user_ip: ipaddress, tier: string)",
    );
    expect(cel).toBe('current_time < expiry && user_ip.in_cidr("10.0.0.0/8")');
  });

  test("value literals: string quoted, number/bool bare", () => {
    const { cel } = conditionToCel({
      name: "r",
      params,
      tree: { kind: "value", param: "tier", op: "eq", value: "gold" },
    });
    expect(cel).toBe('tier == "gold"');
  });
});

describe("tryParseCondition (reverse, subset only)", () => {
  const roundtrip = (tree: ConditionDef["tree"]): ConditionDef["tree"] | null => {
    const def: ConditionDef = { name: "r", params, tree };
    const { cel } = conditionToCel(def);
    return tryParseCondition("r", params, cel)?.tree ?? null;
  };

  test("round-trips a single AND group", () => {
    const tree: ConditionDef["tree"] = {
      op: "and",
      children: [
        { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
        { kind: "ip", param: "user_ip", op: "in_cidr", cidr: "10.0.0.0/8" },
      ],
    };
    expect(roundtrip(tree)).toEqual(tree);
  });

  test("round-trips a single leaf", () => {
    const tree: ConditionDef["tree"] = { kind: "value", param: "tier", op: "neq", value: "free" };
    expect(roundtrip(tree)).toEqual(tree);
  });

  test("round-trips literal timestamp rhs", () => {
    const tree: ConditionDef["tree"] = {
      kind: "time",
      param: "current_time",
      op: "gte",
      rhs: { kind: "literal", rfc3339: "2026-01-01T09:00:00Z" },
    };
    expect(roundtrip(tree)).toEqual(tree);
  });

  test("round-trips a string value containing a quote (escape-safe split)", () => {
    const tree: ConditionDef["tree"] = {
      op: "and",
      children: [
        { kind: "value", param: "tier", op: "eq", value: 'a"b' },
        { kind: "value", param: "tier", op: "neq", value: "c" },
      ],
    };
    expect(roundtrip(tree)).toEqual(tree);
  });

  test("nested group → null (not in subset)", () => {
    expect(
      tryParseCondition("r", params, '(current_time < expiry) || user_ip.in_cidr("10.0.0.0/8")'),
    ).toBeNull();
  });

  test("mixed && / || → null", () => {
    expect(
      tryParseCondition("r", params, 'current_time < expiry && tier == "x" || tier == "y"'),
    ).toBeNull();
  });

  test("unknown function → null", () => {
    expect(tryParseCondition("r", params, "user_ip.in_range(5)")).toBeNull();
  });
});

describe("IR round-trip with a conditioned role assignment", () => {
  test("compile → parse preserves condition + conditioned assignableBy", () => {
    const ir: ModelIR = {
      schemaVersion: "1.1",
      groups: [],
      resources: [
        {
          name: "document",
          parents: [],
          roles: [{ name: "viewer", assignableBy: [{ kind: "user", condition: "non_expired" }] }],
          permissions: [{ name: "read", grantedByRoles: ["viewer"], inheritFromParents: [] }],
        },
      ],
      conditions: [
        {
          name: "non_expired",
          params: [
            { name: "current_time", type: "timestamp" },
            { name: "expiry", type: "timestamp" },
          ],
          tree: {
            kind: "time",
            param: "current_time",
            op: "lt",
            rhs: { kind: "param", param: "expiry" },
          },
        },
      ],
    };
    const { dsl } = compileIrToDsl(ir);
    expect(dsl).toContain("define viewer: [user with non_expired]");
    expect(dsl).toContain("condition non_expired(current_time: timestamp, expiry: timestamp) {");

    const back = parseDslToIr(dsl);
    expect(back.coverage.fullyRepresentable).toBe(true);
    expect(back.ir).toEqual(ir);
  });
});
