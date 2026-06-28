import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { describe, expect, test } from "bun:test";
import { parseDslToIr } from "./dsl-to-ir";
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

describe("parseDslToIr — supported subset", () => {
  test("golden DSL is fully representable", () => {
    const { coverage } = parseDslToIr(GOLDEN_DSL);
    expect(coverage.fullyRepresentable).toBe(true);
    expect(coverage.advanced).toEqual([]);
  });

  test("golden DSL parses back to the reference IR", () => {
    const { ir } = parseDslToIr(GOLDEN_DSL);
    expect(ir).toEqual(docFolderTeamIR);
  });

  test("round-trip: compile(parse(dsl)).dsl === dsl (byte-exact, subset)", () => {
    const { ir } = parseDslToIr(GOLDEN_DSL);
    expect(ir).not.toBeNull();
    expect(compileIrToDsl(ir!).dsl).toBe(GOLDEN_DSL);
  });
});

describe("parseDslToIr — advanced (read-only) detection", () => {
  const wrap = (relations: string) =>
    `model\n  schema 1.1\ntype user\ntype document\n  relations\n${relations}`;

  test("EXCLUSION (but not) → advanced", () => {
    const dsl = wrap(
      "    define banned: [user]\n    define editor: [user] but not banned",
    );
    const { coverage } = parseDslToIr(dsl);
    expect(coverage.fullyRepresentable).toBe(false);
    expect(coverage.advanced).toContainEqual({
      type: "document",
      relation: "editor",
      reason: "EXCLUSION",
    });
  });

  test("INTERSECTION (and) → advanced", () => {
    const dsl = wrap(
      "    define a: [user]\n    define b: [user]\n    define c: a and b",
    );
    const { coverage } = parseDslToIr(dsl);
    expect(coverage.advanced).toContainEqual({ type: "document", relation: "c", reason: "INTERSECTION" });
  });

  test("role-implication (computed, not can_) → NON_ROLE_UNION", () => {
    const dsl = wrap("    define viewer: [user]\n    define editor: viewer");
    const { coverage } = parseDslToIr(dsl);
    expect(coverage.advanced).toContainEqual({
      type: "document",
      relation: "editor",
      reason: "NON_ROLE_UNION",
    });
  });

  test("partial: representable relations still mapped alongside advanced ones", () => {
    const dsl = wrap(
      "    define banned: [user]\n    define viewer: [user]\n    define editor: [user] but not banned",
    );
    const { ir, coverage } = parseDslToIr(dsl);
    expect(coverage.fullyRepresentable).toBe(false);
    // viewer/banned are direct roles → mapped; editor is advanced → omitted.
    const doc = ir!.resources.find((r) => r.name === "document")!;
    expect(doc.roles.map((r) => r.name).sort()).toEqual(["banned", "viewer"]);
  });

  test("syntax error → ir null + parseError", () => {
    const { ir, coverage } = parseDslToIr("this is not a valid model {{{");
    expect(ir).toBeNull();
    expect(coverage.fullyRepresentable).toBe(false);
    expect(coverage.parseError).toBeDefined();
  });
});

describe("parseDslToIr — validate backstop & model-level", () => {
  test("group with extra relation used via #member → not representable (validate backstop)", () => {
    const dsl = `model
  schema 1.1
type user
type team
  relations
    define member: [user]
    define admin: [user]
type document
  relations
    define owner: [team#member]`;
    const { coverage } = parseDslToIr(dsl);
    // team has 2 relations → treated as resource → team#member is a dangling group ref.
    // classification finds no advanced, but validateModelIR catches it.
    expect(coverage.fullyRepresentable).toBe(false);
    expect((coverage.validationErrors ?? []).length).toBeGreaterThan(0);
  });

  test("condition block → not representable", () => {
    const dsl = `model
  schema 1.1
type user
type document
  relations
    define viewer: [user with non_expired]

condition non_expired(current_time: timestamp, expiry: timestamp) {
  current_time < expiry
}`;
    const { coverage } = parseDslToIr(dsl);
    expect(coverage.fullyRepresentable).toBe(false);
    expect(coverage.advanced).toContainEqual({
      type: "document",
      relation: "viewer",
      reason: "CONDITION",
    });
  });
});
