import type { WebhookSignatureSpec } from "./signature";
import type { IdpEvent } from "./types";

// lazyfga-21: 설정형 이벤트 추출 엔진. dotted-path로 raw payload를 정규 IdpEvent로 정규화한다.
// provider별 코드 없음 — preset이 event-type별 추출 규칙을 들고 있다.

/** preset이 든 event-type별 추출 규칙. 한 connection이 여러 이벤트 패밀리를 다룰 수 있게 한다. */
export interface EventExtractionRule {
  /** 이 규칙이 적용되는 이벤트 타입들. */
  match: string[];
  /** 주체 타입(예: "user"). */
  subjectType: string;
  /** 매칭된 이벤트 내 주체 id 경로. */
  subjectIdPath: string;
  /** 정규 attribute 이름 → payload 경로. 값은 스칼라(string으로 강제) 또는 배열(fan-out 소스). */
  attributePaths: Record<string, string>;
}

/** provider preset: 서명 spec + 이벤트 타입 경로 + event-type별 추출 규칙. */
export interface ProviderPreset {
  signature: WebhookSignatureSpec;
  /** 이벤트 타입을 읽는 경로. 예: "event_type". */
  typePath: string;
  extraction: EventExtractionRule[];
}

const DANGEROUS_KEYS = new Set(["__proto__", "constructor", "prototype"]);

/**
 * dotted-path 값 조회(예: "event_payload.userId"). 중간이 객체가 아니면 undefined.
 * prototype-pollution 방지: 위험 세그먼트(__proto__/constructor/prototype) 거부 + own-property만 읽는다
 * (상속 프로퍼티/생성자 노출 차단). 읽기 전용이라 write 벡터는 없지만 방어적으로 가드한다.
 */
function getPath(obj: unknown, path: string): unknown {
  let cur: unknown = obj;
  for (const key of path.split(".")) {
    if (cur === null || typeof cur !== "object") return undefined;
    if (DANGEROUS_KEYS.has(key)) return undefined;
    if (!Object.prototype.hasOwnProperty.call(cur, key)) return undefined;
    cur = (cur as Record<string, unknown>)[key];
  }
  return cur;
}

/** 이벤트 타입만 읽는다(추출 실패 시 audit 관측용). */
export function readEventType(preset: ProviderPreset, parsedBody: unknown): string | undefined {
  if (parsedBody === null || typeof parsedBody !== "object") return undefined;
  const type = getPath(parsedBody, preset.typePath);
  return typeof type === "string" && type !== "" ? type : undefined;
}

/** 주어진 이벤트 타입에 매칭되는 추출 규칙들이 생성하는 정규 attribute 이름 집합(fanOut 검증용). */
export function attributeNamesForEvent(preset: ProviderPreset, eventType: string): Set<string> {
  const names = new Set<string>();
  for (const rule of preset.extraction) {
    if (rule.match.includes(eventType)) {
      for (const k of Object.keys(rule.attributePaths)) names.add(k);
    }
  }
  return names;
}

/** 스칼라(string/number/bool) → string. 그 외(array/object/null/undefined) → undefined. */
function coerceScalar(v: unknown): string | undefined {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return undefined;
}

/**
 * raw payload → 정규 IdpEvent | null.
 * - 이벤트 타입을 preset.typePath로 읽고, match에 그 타입을 포함하는 **첫** 규칙을 고른다(없으면 null).
 * - subjectIdPath 값은 비어있지 않은 **string**이어야 한다(강제 변환 안 함 — lazyfga-16 하드닝).
 * - attributePaths: 스칼라(→string 강제) 또는 스칼라 배열(→string[], fan-out 소스). 그 외는 생략.
 */
export function extractEvent(preset: ProviderPreset, parsedBody: unknown): IdpEvent | null {
  if (parsedBody === null || typeof parsedBody !== "object") return null;
  const type = getPath(parsedBody, preset.typePath);
  if (typeof type !== "string" || type === "") return null;

  const rule = preset.extraction.find((r) => r.match.includes(type));
  if (!rule) return null; // 매핑 대상 아닌 이벤트 → 무시.

  // 주체 id: 비어있지 않은 string만(숫자/객체 강제 변환 금지 — 잘못된 subject로의 tuple write 방지).
  const subjectIdRaw = getPath(parsedBody, rule.subjectIdPath);
  if (typeof subjectIdRaw !== "string" || subjectIdRaw === "") return null;

  const attributes: Record<string, string | string[]> = {};
  for (const [name, path] of Object.entries(rule.attributePaths)) {
    const raw = getPath(parsedBody, path);
    if (Array.isArray(raw)) {
      const arr = raw.map(coerceScalar).filter((s): s is string => s !== undefined);
      attributes[name] = arr; // 빈 배열도 그대로(fan-out에서 0 tuple로 처리).
    } else {
      const scalar = coerceScalar(raw);
      if (scalar !== undefined) attributes[name] = scalar;
    }
  }
  return { type, subject: { type: rule.subjectType, id: subjectIdRaw }, attributes };
}
