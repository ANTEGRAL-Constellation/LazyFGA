// cross-language parity 코퍼스의 TS 측 검증(LFGA-24). 코퍼스에 저장된 기대값을 현재 TS
// 레퍼런스 구현이 그대로 재생산하는지 확인한다(= 레퍼런스 가드). Go 측(parity_test.go)은
// 동일 코퍼스로 포트를 검증하며, 전이적으로 TS == Go 를 보증한다.
// (compiler 산출물(DSL/CEL) 케이스는 의존 방향상 packages/compiler/src/parity.test.ts 가 검증한다.)
import { describe, expect, test } from "bun:test";
import { modelIrSchema, validateModelIR, type ModelIR } from "./model";
import { validateConditionDef, type ConditionDef } from "./condition";
import {
  grantTupleKey,
  isAssignableRelation,
  parseGrantSubject,
  parseResourceRef,
  revokeTupleKey,
  subjectToUser,
  validateGrant,
  validateRevoke,
  type GrantRequest,
  type GrantSubject,
  type RevokeRequest,
} from "./grant";
import modelCases from "./__fixtures__/parity/model-cases.json";
import conditionCases from "./__fixtures__/parity/condition-cases.json";
import grantCases from "./__fixtures__/parity/grant-cases.json";

describe("parity: model-cases", () => {
  for (const c of modelCases.validate) {
    test(`validate: ${c.name}`, () => {
      expect(validateModelIR(c.ir as unknown as ModelIR)).toEqual(c.errors as never);
    });
  }
  for (const c of modelCases.shape) {
    test(`shape: ${c.name}`, () => {
      expect(modelIrSchema.safeParse(c.json).success).toBe(c.valid);
    });
  }
});

describe("parity: condition-cases (validate)", () => {
  for (const c of conditionCases.validate) {
    test(`validate: ${c.name}`, () => {
      expect(validateConditionDef(c.def as unknown as ConditionDef)).toEqual(c.errors as never);
    });
  }
});

describe("parity: marshal round-trip (canonical bytes)", () => {
  // 각 canonical `json`이 TS JSON.stringify와 바이트 동일한지(속성 순서/포맷) 확인한다.
  // Go 측은 동일 문자열을 unmarshal→marshal 로 재현한다.
  const all = [
    ...(modelCases as { marshal: { name: string; type: string; json: string }[] }).marshal,
    ...(conditionCases as { marshal: { name: string; type: string; json: string }[] }).marshal,
    ...(grantCases as { marshal: { name: string; type: string; json: string }[] }).marshal,
  ];
  for (const c of all) {
    test(`${c.type}: ${c.name}`, () => {
      expect(JSON.stringify(JSON.parse(c.json))).toBe(c.json);
    });
  }
});

describe("parity: grant-cases", () => {
  const models = grantCases.models as unknown as Record<string, ModelIR>;

  for (const c of grantCases.validateGrant) {
    test(`validateGrant: ${c.name}`, () => {
      const r = validateGrant(models[c.model]!, c.req as unknown as GrantRequest);
      expect(r.ok).toBe(c.ok);
      if (!r.ok) {
        expect(r.code as string).toBe(c.code);
        expect(r.message).toBe(c.message);
      }
    });
  }
  for (const c of grantCases.validateRevoke) {
    test(`validateRevoke: ${c.name}`, () => {
      const r = validateRevoke(models[c.model]!, c.req as unknown as RevokeRequest);
      expect(r.ok).toBe(c.ok);
      if (!r.ok) {
        expect(r.code as string).toBe(c.code);
        expect(r.message).toBe(c.message);
      }
    });
  }
  for (const c of grantCases.isAssignable) {
    test(`isAssignable: ${c.name}`, () => {
      expect(isAssignableRelation(models[c.model]!, c.type, c.relation)).toBe(c.expect);
    });
  }
  for (const c of grantCases.subjectToUser) {
    test(`subjectToUser: ${c.name}`, () => {
      expect(subjectToUser(c.subject as unknown as GrantSubject)).toBe(c.expect);
    });
  }
  for (const c of grantCases.grantTupleKey) {
    test(`grantTupleKey: ${c.name}`, () => {
      expect(grantTupleKey(c.req as unknown as GrantRequest)).toEqual(c.expect as never);
    });
  }
  for (const c of grantCases.revokeTupleKey) {
    test(`revokeTupleKey: ${c.name}`, () => {
      expect(revokeTupleKey(c.req as unknown as RevokeRequest)).toEqual(c.expect as never);
    });
  }
  for (const c of grantCases.parseResourceRef) {
    test(`parseResourceRef: ${c.name}`, () => {
      expect(parseResourceRef(c.input)).toEqual(c.expect as never);
    });
  }
  for (const c of grantCases.parseGrantSubject) {
    test(`parseGrantSubject: ${c.name}`, () => {
      expect(parseGrantSubject(c.input)).toEqual(c.expect as never);
    });
  }
});
