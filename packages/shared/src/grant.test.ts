import { describe, expect, test } from "bun:test";
import { docFolderTeamIR } from "./fixtures";
import {
  grantTupleKey,
  revokeTupleKey,
  subjectToUser,
  validateGrant,
  validateRevoke,
  type GrantRequest,
} from "./grant";
import type { ModelIR } from "./model";

const M = docFolderTeamIR;

const grant = (over: Partial<GrantRequest>): GrantRequest => ({
  subject: { type: "user", id: "alice" },
  relation: "editor",
  resource: { type: "document", id: "readme" },
  ...over,
});

describe("validateGrant — assignability", () => {
  test("user → role on resource: ok", () => {
    expect(validateGrant(M, grant({}))).toEqual({ ok: true });
  });

  test("userset (team#member) → role: ok", () => {
    expect(
      validateGrant(M, grant({ subject: { type: "team", id: "eng", relation: "member" } })),
    ).toEqual({ ok: true });
  });

  test("user → group member: ok (populating a group)", () => {
    expect(
      validateGrant(M, grant({ relation: "member", resource: { type: "team", id: "eng" } })),
    ).toEqual({ ok: true });
  });

  test("permission relation (can_read / read) → relation_not_assignable", () => {
    const r = validateGrant(M, grant({ relation: "read" }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("relation_not_assignable");
  });

  test("parent relation → relation_not_assignable (subjects are object refs, out of scope)", () => {
    const r = validateGrant(
      M,
      grant({ relation: "parent", subject: { type: "folder", id: "f1" } }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("relation_not_assignable");
  });

  test("unknown resource type → relation_not_assignable", () => {
    const r = validateGrant(M, grant({ resource: { type: "widget", id: "x" } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("relation_not_assignable");
  });

  test("bare non-user subject → subject_type_not_allowed", () => {
    // team without #member can't be a direct subject of a role.
    const r = validateGrant(M, grant({ subject: { type: "team", id: "eng" } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("subject_type_not_allowed");
  });

  test("userset with non-member relation → subject_type_not_allowed", () => {
    const r = validateGrant(M, grant({ subject: { type: "team", id: "eng", relation: "owner" } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("subject_type_not_allowed");
  });
});

describe("validateGrant — malformed", () => {
  test("empty subject id", () => {
    const r = validateGrant(M, grant({ subject: { type: "user", id: "" } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("malformed_request");
  });
  test("subject id with forbidden char (#)", () => {
    const r = validateGrant(M, grant({ subject: { type: "user", id: "a#b" } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("malformed_request");
  });
  test("resource id with whitespace", () => {
    const r = validateGrant(M, grant({ resource: { type: "document", id: "a b" } }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("malformed_request");
  });
  test("non-ident relation", () => {
    const r = validateGrant(M, grant({ relation: "can-read" }));
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("malformed_request");
  });
});

// 조건 부착 모델(인라인): editor는 user(조건부 expiry만) | team#member(무조건).
const conditioned: ModelIR = {
  schemaVersion: "1.1",
  groups: [{ name: "team", memberTypes: [{ kind: "user" }] }],
  resources: [
    {
      name: "document",
      parents: [],
      roles: [
        {
          name: "editor",
          assignableBy: [
            { kind: "user", condition: "expiry" },
            { kind: "group", group: "team", relation: "member" },
          ],
        },
        {
          name: "viewer",
          // user: 무조건 + 조건부 둘 다 허용.
          assignableBy: [{ kind: "user" }, { kind: "user", condition: "expiry" }],
        },
      ],
      permissions: [{ name: "read", grantedByRoles: ["editor", "viewer"], inheritFromParents: [] }],
    },
  ],
  conditions: [
    {
      name: "expiry",
      params: [{ name: "now", type: "timestamp" }],
      tree: {
        kind: "time",
        param: "now",
        op: "lt",
        rhs: { kind: "literal", rfc3339: "2030-01-01T00:00:00Z" },
      },
    },
  ],
};

describe("validateGrant — conditions (lazyfga-14)", () => {
  test("conditionless grant on condition-only subject type → condition_required", () => {
    const r = validateGrant(
      conditioned,
      grant({ subject: { type: "user", id: "a" }, relation: "editor" }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("condition_required");
  });

  test("conditionless grant on userset (unconditioned ref) → ok", () => {
    const r = validateGrant(
      conditioned,
      grant({ subject: { type: "team", id: "eng", relation: "member" }, relation: "editor" }),
    );
    expect(r).toEqual({ ok: true });
  });

  test("grant with permitted condition → ok", () => {
    const r = validateGrant(
      conditioned,
      grant({
        subject: { type: "user", id: "a" },
        relation: "editor",
        condition: { name: "expiry" },
      }),
    );
    expect(r).toEqual({ ok: true });
  });

  test("grant with unknown condition → unknown_condition", () => {
    const r = validateGrant(
      conditioned,
      grant({
        subject: { type: "user", id: "a" },
        relation: "editor",
        condition: { name: "nope" },
      }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("unknown_condition");
  });

  test("grant with known-but-not-attached condition → condition_not_permitted", () => {
    // viewer's user refs don't include nothing-but the expiry one for editor — use editor + a known cond not on team.
    const r = validateGrant(
      conditioned,
      grant({
        subject: { type: "team", id: "eng", relation: "member" },
        relation: "editor",
        condition: { name: "expiry" },
      }),
    );
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("condition_not_permitted");
  });

  test("viewer allows both conditionless and conditioned user grants", () => {
    expect(
      validateGrant(conditioned, grant({ subject: { type: "user", id: "a" }, relation: "viewer" })),
    ).toEqual({ ok: true });
    expect(
      validateGrant(
        conditioned,
        grant({
          subject: { type: "user", id: "a" },
          relation: "viewer",
          condition: { name: "expiry" },
        }),
      ),
    ).toEqual({ ok: true });
  });
});

describe("validateRevoke — structural only (ignores conditions)", () => {
  test("revoke on condition-only subject type → ok (no condition_required)", () => {
    expect(
      validateRevoke(conditioned, {
        subject: { type: "user", id: "a" },
        relation: "editor",
        resource: { type: "document", id: "r" },
      }),
    ).toEqual({ ok: true });
  });
  test("revoke of non-assignable relation → relation_not_assignable", () => {
    const r = validateRevoke(M, {
      subject: { type: "user", id: "a" },
      relation: "read",
      resource: { type: "document", id: "r" },
    });
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.code).toBe("relation_not_assignable");
  });
});

describe("tuple-key builders", () => {
  test("subjectToUser: user vs userset", () => {
    expect(subjectToUser({ type: "user", id: "alice" })).toBe("user:alice");
    expect(subjectToUser({ type: "team", id: "eng", relation: "member" })).toBe("team:eng#member");
  });
  test("grantTupleKey without condition", () => {
    expect(grantTupleKey(grant({}))).toEqual({
      user: "user:alice",
      relation: "editor",
      object: "document:readme",
    });
  });
  test("grantTupleKey with condition + context", () => {
    expect(
      grantTupleKey(
        grant({ condition: { name: "expiry", context: { now: "2025-01-01T00:00:00Z" } } }),
      ),
    ).toEqual({
      user: "user:alice",
      relation: "editor",
      object: "document:readme",
      condition: { name: "expiry", context: { now: "2025-01-01T00:00:00Z" } },
    });
  });
  test("revokeTupleKey omits condition", () => {
    expect(
      revokeTupleKey({
        subject: { type: "team", id: "eng", relation: "member" },
        relation: "viewer",
        resource: { type: "folder", id: "f" },
      }),
    ).toEqual({ user: "team:eng#member", relation: "viewer", object: "folder:f" });
  });
});
