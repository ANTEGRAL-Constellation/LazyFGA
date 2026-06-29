import { FgaApiError } from "@openfga/sdk";
import { describe, expect, test } from "bun:test";
import { classifyWriteError } from "./idp.routes";

// FgaApiError without an HTTP response (network단절: ECONNRESET/소켓 끊김 등). SDK가 .code를
// 보존하지 않으므로 statusCode 부재로만 식별된다.
const fgaNetworkError = (message: string): unknown =>
  Object.assign(Object.create(FgaApiError.prototype) as object, { message, statusCode: undefined });

describe("classifyWriteError (lazyfga-15 hardening)", () => {
  test("network FgaApiError (no statusCode) → transient (FINDING 1: no silent lost write)", () => {
    expect(classifyWriteError(fgaNetworkError("read ECONNRESET"), "write")).toEqual({
      idempotent: false,
      transient: true,
    });
    expect(classifyWriteError(fgaNetworkError("socket hang up"), "delete")).toEqual({
      idempotent: false,
      transient: true,
    });
  });

  test("raw socket error by code → transient", () => {
    expect(classifyWriteError({ code: "ECONNRESET", message: "read ECONNRESET" }, "write").transient).toBe(true);
  });

  test("5xx / 429 → transient", () => {
    expect(classifyWriteError({ statusCode: 503 }, "write").transient).toBe(true);
    expect(classifyWriteError({ statusCode: 429 }, "write").transient).toBe(true);
  });

  test("4xx with event-controlled 'timeout' in message → NOT transient (FINDING 2: no infinite retry)", () => {
    const r = classifyWriteError(
      { statusCode: 400, message: "relation not found for object doc:timeout-1" },
      "write",
    );
    expect(r.transient).toBe(false);
    expect(r.idempotent).toBe(false); // deterministic → counted as failed, 200
  });

  test("duplicate write (invalid-input + already exists) → idempotent", () => {
    expect(
      classifyWriteError(
        {
          statusCode: 400,
          responseData: { code: "write_failed_due_to_invalid_input" },
          message: "cannot write a tuple which already exists",
        },
        "write",
      ),
    ).toEqual({ idempotent: true, transient: false });
  });

  test("delete of missing tuple → idempotent", () => {
    expect(
      classifyWriteError(
        {
          statusCode: 400,
          responseData: { code: "write_failed_due_to_invalid_input" },
          message: "cannot delete a tuple which does not exist",
        },
        "delete",
      ),
    ).toEqual({ idempotent: true, transient: false });
  });

  test("write op with 'not found' (wrong op pattern / not invalid-input) → deterministic fail, not idempotent", () => {
    expect(
      classifyWriteError({ statusCode: 400, responseData: { code: "validation_error" }, message: "relation not found" }, "write"),
    ).toEqual({ idempotent: false, transient: false });
  });
});
