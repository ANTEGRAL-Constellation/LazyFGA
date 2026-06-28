import { create } from "zustand";

export interface Highlight {
  nodes: string[];
  edges: string[];
}

interface ExplainStore {
  highlight: Highlight;
  setHighlight(h: Highlight): void;
}

/** explain 결과의 캔버스 강조 대상(model 스토어와 분리해 결합도 최소화). */
export const useExplainStore = create<ExplainStore>((set) => ({
  highlight: { nodes: [], edges: [] },
  setHighlight: (highlight) => set({ highlight }),
}));
