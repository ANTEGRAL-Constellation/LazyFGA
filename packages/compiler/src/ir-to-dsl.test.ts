import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { describe, expect, test } from "bun:test";
import { compileIrToDsl } from "./ir-to-dsl";

const GOLDEN_DSL = `model
  schema 1.1
type user
type team
  relations
    define member: [user, team#member]
type folder
  relations
    define owner: [user, team#member]
    define editor: [user, team#member]
    define viewer: [user, team#member]
    define can_read: viewer or editor or owner
type document
  relations
    define parent: [folder]
    define owner: [user, team#member]
    define editor: [user, team#member]
    define viewer: [user, team#member]
    define can_read: viewer or editor or owner or can_read from parent`;

describe("compileIrToDsl", () => {
  test("golden DSL for doc-folder-team fixture", () => {
    const { dsl } = compileIrToDsl(docFolderTeamIR);
    expect(dsl).toBe(GOLDEN_DSL);
  });

  test("deterministic (repeat → identical bytes)", () => {
    expect(compileIrToDsl(docFolderTeamIR).dsl).toBe(compileIrToDsl(docFolderTeamIR).dsl);
  });

  test("model JSON: schema + type order preserved", () => {
    const { model } = compileIrToDsl(docFolderTeamIR);
    expect(model.schema_version).toBe("1.1");
    expect((model.type_definitions ?? []).map((t) => t.type)).toEqual([
      "user",
      "team",
      "folder",
      "document",
    ]);
  });

  test("throws CompileError on invalid IR (empty grant)", () => {
    const bad = structuredClone(docFolderTeamIR);
    bad.resources[0]!.permissions[0]!.grantedByRoles = [];
    expect(() => compileIrToDsl(bad)).toThrow();
  });
});
