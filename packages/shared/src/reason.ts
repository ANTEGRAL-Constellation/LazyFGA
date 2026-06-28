// explainability 공유 타입(lazyfga-11). web·api 공유.
// 허용/거부 비대칭: 허용=성립한 witnessing path(존재 증명), 거부=best-effort "빠진 연결고리".

export interface ReasonResult {
  decision: boolean;
  /** 허용일 때 하나의 witnessing path(없으면 폴백 text). */
  path?: ReasonStep[];
  /** 거부일 때 통과에 필요한 요구 조건. */
  missingLinks?: MissingLink[];
  /** 사람이 읽는 한 줄. */
  text: string;
  /** 깊이/페이지 한도로 폴백되었는지(정직한 표기). */
  truncated?: boolean;
}

// group/parent는 캔버스 노드 강조용 "타입"이고, groupObject/parentObject는 인스턴스
// 수준 설명을 위한 구체 객체 id(예: team:eng, folder:1).
export type ReasonStep =
  | { via: "role"; role: string; on: string; direct: boolean; group?: string; groupObject?: string }
  | { via: "parent"; relation: string; parent: string; parentObject?: string };

export type MissingLink =
  | { kind: "role"; anyOf: string[]; on: string }
  | { kind: "parent"; relation: string; needs: string };
