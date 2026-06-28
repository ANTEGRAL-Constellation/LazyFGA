import type { ModelIR } from "@lazyfga/shared";
import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { describe, expect, test } from "bun:test";
import { diffModels } from "./diff";

const base = docFolderTeamIR;
const clone = (): ModelIR => structuredClone(base);

describe("diffModels", () => {
  test("identical → empty", () => {
    expect(diffModels(base, base)).toEqual([]);
  });

  test("role added + grant changed", () => {
    const to = clone();
    to.resources[1]!.roles.push({ name: "auditor", assignableBy: [{ kind: "user" }] });
    to.resources[1]!.permissions[0]!.grantedByRoles.push("auditor");
    const changes = diffModels(base, to);
    expect(changes).toContainEqual({ kind: "ROLE_ADDED", type: "document", role: "auditor" });
    expect(
      changes.some(
        (c) => c.kind === "GRANT_CHANGED" && c.type === "document" && c.added.includes("auditor"),
      ),
    ).toBe(true);
  });

  test("type removed", () => {
    const to = clone();
    to.resources = to.resources.filter((r) => r.name !== "folder");
    expect(diffModels(base, to)).toContainEqual({ kind: "TYPE_REMOVED", type: "folder" });
  });

  test("parent added", () => {
    const to = clone();
    to.resources[0]!.parents.push({ relationName: "parent", parentTypes: ["folder"] });
    expect(diffModels(base, to)).toContainEqual({
      kind: "PARENT_ADDED",
      type: "folder",
      relationName: "parent",
      parentType: "folder",
    });
  });

  test("role assignableBy changed", () => {
    const to = clone();
    to.resources[1]!.roles[1]!.assignableBy = [{ kind: "user" }]; // editor: drop team#member
    expect(diffModels(base, to)).toContainEqual({
      kind: "ROLE_ASSIGNABLE_CHANGED",
      type: "document",
      role: "editor",
      added: [],
      removed: ["team#member"],
    });
  });

  test("permission inheritFromParents changed", () => {
    const to = clone();
    to.resources[1]!.permissions[0]!.inheritFromParents = []; // drop "parent"
    expect(diffModels(base, to)).toContainEqual({
      kind: "PERMISSION_INHERIT_CHANGED",
      type: "document",
      permission: "read",
      added: [],
      removed: ["parent"],
    });
  });

  test("deterministic ordering", () => {
    const to = clone();
    to.resources[1]!.roles.push({ name: "auditor", assignableBy: [{ kind: "user" }] });
    expect(JSON.stringify(diffModels(base, to))).toBe(JSON.stringify(diffModels(base, to)));
  });
});
