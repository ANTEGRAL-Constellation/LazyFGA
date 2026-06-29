import { describe, expect, test } from "bun:test";
import fixtureJson from "./__fixtures__/doc-folder-team.ir.json";
import { modelIrSchema, validateModelIR, type ModelIR } from "./model";
import type { ConditionDef } from "./condition";

const fixture = fixtureJson as unknown as ModelIR;
const clone = (): ModelIR => structuredClone(fixture);
const codes = (ir: ModelIR): string[] => validateModelIR(ir).map((e) => e.code);

describe("modelIrSchema (shape)", () => {
  test("reference fixture parses", () => {
    expect(() => modelIrSchema.parse(fixtureJson)).not.toThrow();
  });
});

describe("validateModelIR", () => {
  test("reference fixture is valid", () => {
    expect(validateModelIR(fixture)).toEqual([]);
  });

  test("rule 2: duplicate type name", () => {
    const ir = clone();
    ir.resources[1]!.name = "folder"; // document -> folder (dup)
    expect(codes(ir)).toContain("DUP_TYPE");
  });

  test("rule 2: user is reserved", () => {
    const ir = clone();
    ir.resources[0]!.name = "user";
    expect(codes(ir)).toContain("RESERVED_USER");
  });

  test("rule 1: bad identifier", () => {
    const ir = clone();
    ir.resources[0]!.roles[0]!.name = "has space";
    expect(codes(ir)).toContain("BAD_NAME");
  });

  test("rule 3: relation namespace collision (role vs can_<perm>)", () => {
    const ir = clone();
    // permission "read" -> relation can_read; add a role literally named "can_read".
    ir.resources[0]!.roles.push({ name: "can_read", assignableBy: [{ kind: "user" }] });
    expect(codes(ir)).toContain("DUP_RELATION");
  });

  test("rule 5: unknown role in grantedByRoles", () => {
    const ir = clone();
    ir.resources[0]!.permissions[0]!.grantedByRoles = ["ghost"];
    expect(codes(ir)).toContain("UNKNOWN_ROLE");
  });

  test("rule 5: empty grant", () => {
    const ir = clone();
    ir.resources[0]!.permissions[0]!.grantedByRoles = [];
    expect(codes(ir)).toContain("EMPTY_GRANT");
  });

  test("rule 6: unknown group", () => {
    const ir = clone();
    ir.resources[0]!.roles[0]!.assignableBy = [
      { kind: "group", group: "nope", relation: "member" },
    ];
    expect(codes(ir)).toContain("UNKNOWN_GROUP");
  });

  test("rule 4: unknown parent type", () => {
    const ir = clone();
    ir.resources[1]!.parents[0]!.parentTypes = ["ghost"];
    expect(codes(ir)).toContain("UNKNOWN_PARENT");
  });

  test("rule 4: inherit from unknown parent relation", () => {
    const ir = clone();
    ir.resources[1]!.permissions[0]!.inheritFromParents = ["ghostrel"];
    expect(codes(ir)).toContain("UNKNOWN_PARENT");
  });

  test("rule 7: parent missing the inherited permission", () => {
    const ir = clone();
    // remove folder's "read" permission while document still inherits read from parent(folder)
    ir.resources[0]!.permissions = [];
    expect(codes(ir)).toContain("PARENT_MISSING_PERMISSION");
  });

  test("rule 8: duplicate parent relation", () => {
    const ir = clone();
    ir.resources[1]!.parents.push({ relationName: "parent", parentTypes: ["folder"] });
    expect(codes(ir)).toContain("DUP_PARENT_RELATION");
  });

  test("rule 8: empty parentTypes", () => {
    const ir = clone();
    ir.resources[1]!.parents[0]!.parentTypes = [];
    expect(codes(ir)).toContain("UNKNOWN_PARENT");
  });

  test("empty assignableBy on a role → EMPTY_SUBJECTS", () => {
    const ir = clone();
    ir.resources[0]!.roles[0]!.assignableBy = [];
    expect(codes(ir)).toContain("EMPTY_SUBJECTS");
  });

  test("empty memberTypes on a group → EMPTY_SUBJECTS", () => {
    const ir = clone();
    ir.groups[0]!.memberTypes = [];
    expect(codes(ir)).toContain("EMPTY_SUBJECTS");
  });

  test("lazyfga-14: SubjectRef.condition referencing undefined condition → CONDITION_UNKNOWN", () => {
    const ir = clone();
    ir.resources[0]!.roles[0]!.assignableBy[0]!.condition = "ghost";
    expect(codes(ir)).toContain("CONDITION_UNKNOWN");
  });

  test("lazyfga-14: duplicate condition name → DUP_CONDITION", () => {
    const ir = clone();
    const cond: ConditionDef = {
      name: "non_expired",
      params: [{ name: "t", type: "timestamp" }],
      tree: { kind: "time", param: "t", op: "lt", rhs: { kind: "literal", rfc3339: "2026-01-01T00:00:00Z" } },
    };
    ir.conditions = [cond, { ...cond }];
    expect(codes(ir)).toContain("DUP_CONDITION");
  });

  test("lazyfga-14: valid condition attached to a role assignment passes", () => {
    const ir = clone();
    ir.conditions = [
      {
        name: "non_expired",
        params: [
          { name: "current_time", type: "timestamp" },
          { name: "expiry", type: "timestamp" },
        ],
        tree: { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
      },
    ];
    ir.resources[0]!.roles[0]!.assignableBy[0]!.condition = "non_expired";
    expect(validateModelIR(ir)).toEqual([]);
  });

  test("duplicate parent relation does not also raise DUP_RELATION", () => {
    const ir = clone();
    ir.resources[1]!.parents.push({ relationName: "parent", parentTypes: ["folder"] });
    const cs = codes(ir);
    expect(cs).toContain("DUP_PARENT_RELATION");
    expect(cs).not.toContain("DUP_RELATION");
  });
});
