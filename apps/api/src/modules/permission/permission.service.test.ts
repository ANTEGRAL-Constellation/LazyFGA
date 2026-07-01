import { describe, expect, test } from "bun:test";
import { GrantError, interpretWriteError } from "./permission.service";

// interpretWriteError: classifyWriteError(lazyfga-15) 결과를 grant/revoke 결과/오류로 매핑.
describe("interpretWriteError", () => {
  test("duplicate write → no-op (idempotent)", () => {
    const r = interpretWriteError(
      {
        statusCode: 400,
        responseData: { code: "write_failed_due_to_invalid_input" },
        message: "cannot write a tuple which already exists",
      },
      "write",
    );
    expect(r).toEqual({ noop: true });
  });

  test("missing delete → no-op (idempotent)", () => {
    const r = interpretWriteError(
      {
        statusCode: 400,
        responseData: { code: "write_failed_due_to_invalid_input" },
        message: "cannot delete a tuple which does not exist",
      },
      "delete",
    );
    expect(r).toEqual({ noop: true });
  });

  test("5xx → transient → 502 GrantError", () => {
    const r = interpretWriteError({ statusCode: 503 }, "write");
    expect("error" in r && r.error).toBeInstanceOf(GrantError);
    if ("error" in r) {
      expect(r.error.status).toBe(502);
      expect(r.error.code).toBe("openfga_unavailable");
    }
  });

  test("deterministic 4xx (non-idempotent) → 400 backstop GrantError", () => {
    const r = interpretWriteError(
      {
        statusCode: 400,
        responseData: { code: "validation_error" },
        message: "relation not found",
      },
      "write",
    );
    expect("error" in r && r.error).toBeInstanceOf(GrantError);
    if ("error" in r) {
      expect(r.error.status).toBe(400);
      expect(r.error.code).toBe("openfga_invalid_input");
    }
  });
});
