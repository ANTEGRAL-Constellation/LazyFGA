import { describe, expect, test } from "bun:test";
import type { ConditionDef } from "./condition";
import {
  addCondition,
  removeCondition,
  renameCondition,
  setAssignmentCondition,
  updateCondition,
} from "./edit";
import { validateModelIR, type ModelIR } from "./model";
import { policyContextParams } from "./policy";

const biz: ConditionDef = {
  name: "biz",
  params: [
    { name: "current_time", type: "timestamp" },
    { name: "expiry", type: "timestamp" },
  ],
  tree: { kind: "time", param: "current_time", op: "lt", rhs: { kind: "param", param: "expiry" } },
};

const base: ModelIR = {
  schemaVersion: "1.1",
  groups: [],
  resources: [
    {
      name: "folder",
      parents: [],
      roles: [{ name: "viewer", assignableBy: [{ kind: "user", condition: "biz" }] }],
      permissions: [{ name: "read", grantedByRoles: ["viewer"], inheritFromParents: [] }],
    },
    {
      name: "document",
      parents: [{ relationName: "parent", parentTypes: ["folder"] }],
      roles: [{ name: "editor", assignableBy: [{ kind: "user" }] }],
      permissions: [{ name: "read", grantedByRoles: ["editor"], inheritFromParents: ["parent"] }],
    },
  ],
  conditions: [biz],
};

const clone = (): ModelIR => structuredClone(base);
const folderViewerCond = (ir: ModelIR): string | undefined =>
  ir.resources.find((r) => r.name === "folder")!.roles[0]!.assignableBy[0]!.condition;

describe("base fixture", () => {
  test("is valid", () => expect(validateModelIR(base)).toEqual([]));
});

describe("condition edit ops (pure)", () => {
  test("addCondition adds; duplicate name is a no-op", () => {
    const ir = clone();
    const next = addCondition(ir, { ...biz, name: "biz2" });
    expect(next.conditions?.map((c) => c.name)).toEqual(["biz", "biz2"]);
    expect(addCondition(ir, biz)).toBe(ir); // duplicate → same ref
  });

  test("updateCondition replaces by name", () => {
    const ir = clone();
    const next = updateCondition(ir, "biz", {
      ...biz,
      tree: {
        kind: "time",
        param: "current_time",
        op: "lte",
        rhs: { kind: "param", param: "expiry" },
      },
    });
    expect((next.conditions![0]!.tree as { op?: string; param?: string }).param).toBe(
      "current_time",
    );
  });

  test("renameCondition updates referencing SubjectRef.condition", () => {
    const next = renameCondition(clone(), "biz", "biz_renamed");
    expect(next.conditions![0]!.name).toBe("biz_renamed");
    expect(folderViewerCond(next)).toBe("biz_renamed");
    expect(validateModelIR(next)).toEqual([]);
  });

  test("removeCondition removes def and clears references", () => {
    const next = removeCondition(clone(), "biz");
    expect(next.conditions).toBeUndefined();
    expect(folderViewerCond(next)).toBeUndefined();
    expect(validateModelIR(next)).toEqual([]);
  });

  test("setAssignmentCondition attaches; out-of-range is a no-op", () => {
    const ir = clone();
    const attached = setAssignmentCondition(ir, "document", "editor", 0, "biz");
    expect(attached.resources[1]!.roles[0]!.assignableBy[0]!.condition).toBe("biz");
    const detached = setAssignmentCondition(attached, "document", "editor", 0, null);
    expect(detached.resources[1]!.roles[0]!.assignableBy[0]!.condition).toBeUndefined();
    expect(setAssignmentCondition(ir, "document", "editor", 9, "biz")).toBe(ir);
  });

  test("setAssignmentCondition rejects non-integer index (no prototype pollution)", () => {
    const ir = clone();
    // number 타입을 우회한 악성 인덱스가 assignableBy[idx]로 프로토타입에 닿지 못하게 한다.
    for (const bad of ["__proto__", "constructor", "prototype", 1.5, NaN]) {
      expect(
        setAssignmentCondition(ir, "document", "editor", bad as unknown as number, "biz"),
      ).toBe(ir);
    }
    expect("condition" in ({} as Record<string, unknown>)).toBe(false);
    expect("condition" in ([] as unknown[])).toBe(false);
  });
});

describe("policyContextParams", () => {
  test("direct conditioned role → its params", () => {
    expect(policyContextParams(base, { permission: "read", resourceType: "folder" })).toEqual(
      biz.params,
    );
  });

  test("follows inherited parent to find conditioned grant", () => {
    expect(policyContextParams(base, { permission: "read", resourceType: "document" })).toEqual(
      biz.params,
    );
  });

  test("no conditions on path → empty", () => {
    const ir = removeCondition(clone(), "biz");
    expect(policyContextParams(ir, { permission: "read", resourceType: "document" })).toEqual([]);
  });
});
