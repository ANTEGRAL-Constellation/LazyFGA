import { describe, expect, test } from "bun:test";
import fixtureJson from "./__fixtures__/doc-folder-team.ir.json";
import {
  addGroup,
  addPermission,
  addResource,
  addRole,
  connectParent,
  disconnectParent,
  removePermission,
  removeRole,
  removeType,
  renamePermission,
  renameRole,
  toggleCell,
  toggleInherit,
} from "./edit";
import { validateModelIR, type ModelIR } from "./model";

const base = fixtureJson as unknown as ModelIR;
const doc = (ir: ModelIR) => ir.resources.find((r) => r.name === "document")!;

describe("edit ops are pure", () => {
  test("addResource does not mutate input and adds a type", () => {
    const before = JSON.stringify(base);
    const next = addResource(base, "comment");
    expect(JSON.stringify(base)).toBe(before); // unchanged
    expect(next.resources.map((r) => r.name)).toContain("comment");
  });

  test("addResource is a no-op on duplicate name", () => {
    expect(addResource(base, "document")).toBe(base);
  });
});

describe("connect/disconnect parent", () => {
  test("connectParent merges into existing relationName", () => {
    const next = connectParent(base, "document", "document", "parent"); // self-nest
    expect(doc(next).parents[0]!.parentTypes).toEqual(["folder", "document"]);
  });

  test("connectParent creates a new ParentRef when relationName absent", () => {
    const next = connectParent(base, "folder", "folder", "parent");
    const folder = next.resources.find((r) => r.name === "folder")!;
    expect(folder.parents).toEqual([{ relationName: "parent", parentTypes: ["folder"] }]);
  });

  test("disconnectParent(parentType) removes only that type and cleans inherit when empty", () => {
    const next = disconnectParent(base, "document", "parent", "folder");
    expect(doc(next).parents).toEqual([]); // was [folder] → empty → removed
    expect(doc(next).permissions[0]!.inheritFromParents).toEqual([]); // cleaned
  });

  test("result stays valid", () => {
    const next = disconnectParent(base, "document", "parent", "folder");
    expect(validateModelIR(next)).toEqual([]);
  });
});

describe("removeType orphan cleanup", () => {
  test("removing folder cleans document's parent edge + inherit", () => {
    const next = removeType(base, "folder");
    expect(next.resources.map((r) => r.name)).toEqual(["document"]);
    expect(doc(next).parents).toEqual([]);
    expect(doc(next).permissions[0]!.inheritFromParents).toEqual([]);
    expect(validateModelIR(next)).toEqual([]);
  });

  test("removing team (group) strips group refs from role.assignableBy", () => {
    const next = removeType(base, "team");
    expect(next.groups).toEqual([]);
    for (const r of next.resources) {
      for (const role of r.roles) {
        expect(role.assignableBy.every((ref) => ref.kind === "user")).toBe(true);
      }
    }
    expect(validateModelIR(next)).toEqual([]);
  });
});

describe("matrix ops", () => {
  test("toggleCell off then on", () => {
    const off = toggleCell(base, "folder", "read", "viewer");
    expect(off.resources[0]!.permissions[0]!.grantedByRoles).not.toContain("viewer");
    const on = toggleCell(off, "folder", "read", "viewer");
    expect(on.resources[0]!.permissions[0]!.grantedByRoles).toContain("viewer");
  });

  test("removeRole strips it from grantedByRoles", () => {
    const next = removeRole(base, "folder", "viewer");
    expect(next.resources[0]!.roles.map((r) => r.name)).not.toContain("viewer");
    expect(next.resources[0]!.permissions[0]!.grantedByRoles).not.toContain("viewer");
  });

  test("renameRole updates grant references", () => {
    const next = renameRole(base, "folder", "viewer", "reader");
    expect(next.resources[0]!.permissions[0]!.grantedByRoles).toContain("reader");
    expect(next.resources[0]!.permissions[0]!.grantedByRoles).not.toContain("viewer");
  });

  test("addRole / addPermission / removePermission", () => {
    let ir = addRole(base, "document", "auditor");
    expect(doc(ir).roles.map((r) => r.name)).toContain("auditor");
    ir = addPermission(ir, "document", "write");
    expect(doc(ir).permissions.map((p) => p.name)).toContain("write");
    ir = removePermission(ir, "document", "write");
    expect(doc(ir).permissions.map((p) => p.name)).not.toContain("write");
  });

  test("toggleInherit on/off", () => {
    const off = toggleInherit(base, "document", "read", "parent");
    expect(doc(off).permissions[0]!.inheritFromParents).toEqual([]);
    const on = toggleInherit(off, "document", "read", "parent");
    expect(doc(on).permissions[0]!.inheritFromParents).toEqual(["parent"]);
  });

  test("renamePermission renames the permission field", () => {
    const next = renamePermission(base, "folder", "read", "view");
    const folder = next.resources.find((r) => r.name === "folder")!;
    expect(folder.permissions.map((p) => p.name)).toEqual(["view"]);
  });

  test("addGroup adds a group with default user member", () => {
    const next = addGroup(base, "org");
    expect(next.groups.map((g) => g.name)).toContain("org");
    expect(next.groups.find((g) => g.name === "org")!.memberTypes).toEqual([{ kind: "user" }]);
  });
});
