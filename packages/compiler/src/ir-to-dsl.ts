import { validateModelIR, type ModelIR, type SubjectRef, type ValidationError } from "@lazyfga/shared";
import { transformer } from "@openfga/syntax-transformer";

/** OpenFGA AuthorizationModel JSON(공식 변환기 출력, id 제외). */
export type AuthModelJSON = ReturnType<typeof transformer.transformDSLToJSONObject>;

export class CompileError extends Error {
  constructor(
    public readonly reason: "IR_INVALID" | "JSON_TRANSFORM_FAILED",
    public readonly detail: unknown,
  ) {
    super(`compileIrToDsl failed: ${reason}`);
    this.name = "CompileError";
  }
}

/** SubjectRef → DSL 토큰. user | <group>#member */
function serializeSubject(ref: SubjectRef): string {
  return ref.kind === "user" ? "user" : `${ref.group}#${ref.relation}`;
}

/** type restriction 직렬화: [a, b, ...] (IR 배열 순서 유지). */
function serializeSubjects(refs: SubjectRef[]): string {
  return `[${refs.map(serializeSubject).join(", ")}]`;
}

/**
 * ModelIR → OpenFGA DSL 문자열(결정적: 동일 IR → 바이트 단위 동일 DSL).
 * emit 순서: header → groups(입력순) → resources(입력순; relation은 parents→roles→permissions).
 */
function emitDsl(ir: ModelIR): string {
  const lines: string[] = ["model", "  schema 1.1", "type user"];

  for (const g of ir.groups) {
    lines.push(`type ${g.name}`, "  relations", `    define member: ${serializeSubjects(g.memberTypes)}`);
  }

  for (const r of ir.resources) {
    lines.push(`type ${r.name}`);
    const relLines: string[] = [];

    for (const p of r.parents) {
      relLines.push(`    define ${p.relationName}: [${p.parentTypes.join(", ")}]`);
    }
    for (const role of r.roles) {
      relLines.push(`    define ${role.name}: ${serializeSubjects(role.assignableBy)}`);
    }
    for (const perm of r.permissions) {
      const union = [
        ...perm.grantedByRoles,
        ...perm.inheritFromParents.map((rel) => `can_${perm.name} from ${rel}`),
      ].join(" or ");
      relLines.push(`    define can_${perm.name}: ${union}`);
    }

    if (relLines.length > 0) {
      lines.push("  relations", ...relLines);
    }
  }

  return lines.join("\n");
}

/**
 * ModelIR을 OpenFGA DSL 문자열과 AuthorizationModel JSON으로 컴파일한다.
 * 결정적: 동일 IR → 바이트 단위 동일 DSL. 호출 전 validateModelIR 통과 전제이며,
 * 방어적으로 재검증한다.
 * @throws CompileError IR 검증 위반 또는 JSON 변환 실패 시
 */
export function compileIrToDsl(ir: ModelIR): { dsl: string; model: AuthModelJSON } {
  const errors: ValidationError[] = validateModelIR(ir);
  if (errors.length > 0) {
    throw new CompileError("IR_INVALID", errors);
  }

  const dsl = emitDsl(ir);

  let model: AuthModelJSON;
  try {
    model = transformer.transformDSLToJSONObject(dsl);
  } catch (cause) {
    throw new CompileError("JSON_TRANSFORM_FAILED", cause);
  }

  return { dsl, model };
}
