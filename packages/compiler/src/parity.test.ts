// cross-language parity 코퍼스의 compiler 측 검증(LFGA-24). 코퍼스의 DSL/CEL 기대값을
// 현재 TS 컴파일러가 그대로 재생산하는지 확인한다. Go 측(internal/compiler/parity_test.go)이
// 동일 코퍼스를 검증하므로 전이적으로 TS == Go. 코퍼스 데이터는 shared 픽스처가 단일 원본이다.
import { describe, expect, test } from "bun:test";
import type { ConditionDef, ModelIR } from "@lazyfga/shared";
import { compileIrToDsl } from "./ir-to-dsl";
import { conditionToCel } from "./condition-to-cel";
import conditionCases from "../../shared/src/__fixtures__/parity/condition-cases.json";
import dslCases from "../../shared/src/__fixtures__/parity/dsl-cases.json";

describe("parity: dsl-cases", () => {
  for (const c of dslCases.cases) {
    test(`dsl: ${c.name}`, () => {
      expect(compileIrToDsl(c.ir as unknown as ModelIR).dsl).toBe(c.dsl);
    });
  }
});

describe("parity: condition-cases (cel)", () => {
  for (const c of conditionCases.cel) {
    test(`cel: ${c.name}`, () => {
      const { decl, cel } = conditionToCel(c.def as unknown as ConditionDef);
      expect(decl).toBe(c.decl);
      expect(cel).toBe(c.cel);
    });
  }
});
