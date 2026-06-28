import {
  addGroup as opAddGroup,
  addPermission as opAddPermission,
  addResource as opAddResource,
  addRole as opAddRole,
  connectParent as opConnectParent,
  disconnectParent as opDisconnectParent,
  removePermission as opRemovePermission,
  removeRole as opRemoveRole,
  removeType as opRemoveType,
  renamePermission as opRenamePermission,
  renameRole as opRenameRole,
  setRoleAssignableBy as opSetRoleAssignableBy,
  toggleCell as opToggleCell,
  toggleInherit as opToggleInherit,
  validateModelIR,
  type ModelIR,
  type SubjectRef,
  type ValidationError,
} from "@lazyfga/shared";
import { docFolderTeamIR } from "@lazyfga/shared/fixtures";
import { compileIrToDsl, parseDslToIr, type Coverage } from "@lazyfga/compiler";
import { create } from "zustand";

export interface XY {
  x: number;
  y: number;
}

interface DerivedState {
  ir: ModelIR;
  dsl: string;
  errors: ValidationError[];
  coverage: Coverage | null;
  readOnly: boolean;
  /** validateModelIR는 통과했으나 DSL→JSON 변환이 실패한 경우의 메시지. */
  compileError: string | null;
  /** DSL import 실패(구문 오류) 메시지. 모델 상태와 분리(모델 오염 방지). */
  importError: string | null;
}

export interface ModelStore extends DerivedState {
  positions: Record<string, XY>;
  selectedType: string | null;

  setSelected(name: string | null): void;
  setPosition(id: string, pos: XY): void;

  addResource(name: string): void;
  addGroup(name: string): void;
  removeType(name: string): void;
  connectParent(child: string, parent: string, relationName?: string): void;
  disconnectParent(child: string, relationName: string, parentType?: string): void;

  toggleCell(type: string, permission: string, role: string): void;
  addRole(type: string, name: string): void;
  removeRole(type: string, name: string): void;
  renameRole(type: string, from: string, to: string): void;
  setRoleAssignableBy(type: string, role: string, refs: SubjectRef[]): void;
  addPermission(type: string, name: string): void;
  removePermission(type: string, name: string): void;
  renamePermission(type: string, from: string, to: string): void;
  toggleInherit(type: string, permission: string, parentRelation: string): void;

  loadFromDsl(dsl: string): void;
  resetTo(ir: ModelIR): void;
}

const layout = (i: number): XY => ({ x: 60 + (i % 3) * 260, y: 80 + Math.floor(i / 3) * 190 });

function ensurePositions(ir: ModelIR, prev: Record<string, XY>): Record<string, XY> {
  const names = [...ir.groups.map((g) => g.name), ...ir.resources.map((r) => r.name)];
  const next: Record<string, XY> = {};
  let idx = 0;
  for (const n of names) {
    next[n] = prev[n] ?? layout(idx);
    idx++;
  }
  return next;
}

/** IR로부터 DSL/errors를 파생. compile 실패 시 마지막 정상 DSL을 유지하고 사유를 노출. */
function recompute(
  ir: ModelIR,
  prevDsl: string,
): { dsl: string; errors: ValidationError[]; compileError: string | null } {
  const errors = validateModelIR(ir);
  let dsl = prevDsl;
  let compileError: string | null = null;
  try {
    dsl = compileIrToDsl(ir).dsl;
  } catch (e) {
    // validateModelIR를 통과했는데도 변환이 실패한 경우만 사용자에게 표면화(드묾).
    compileError = errors.length === 0 ? String(e) : null;
  }
  return { dsl, errors, compileError };
}

const seedIr: ModelIR = docFolderTeamIR;
const seedDerived = recompute(seedIr, "");

export const useModelStore = create<ModelStore>((set, get) => {
  /** readOnly가 아니면 mutator를 적용하고 파생 상태를 갱신. */
  const apply = (mutate: (ir: ModelIR) => ModelIR): void => {
    const s = get();
    if (s.readOnly) return;
    const ir = mutate(s.ir);
    if (ir === s.ir) return;
    const { dsl, errors, compileError } = recompute(ir, s.dsl);
    set({ ir, dsl, errors, compileError, positions: ensurePositions(ir, s.positions) });
  };

  return {
    ir: seedIr,
    dsl: seedDerived.dsl,
    errors: seedDerived.errors,
    compileError: seedDerived.compileError,
    importError: null,
    coverage: null,
    readOnly: false,
    positions: ensurePositions(seedIr, {}),
    selectedType: null,

    setSelected: (name) => set({ selectedType: name }),
    setPosition: (id, pos) => set((s) => ({ positions: { ...s.positions, [id]: pos } })),

    addResource: (name) => apply((ir) => opAddResource(ir, name)),
    addGroup: (name) => apply((ir) => opAddGroup(ir, name)),
    removeType: (name) => {
      apply((ir) => opRemoveType(ir, name));
      if (get().selectedType === name) set({ selectedType: null });
    },
    connectParent: (child, parent, relationName) =>
      apply((ir) => opConnectParent(ir, child, parent, relationName)),
    disconnectParent: (child, relationName, parentType) =>
      apply((ir) => opDisconnectParent(ir, child, relationName, parentType)),

    toggleCell: (type, permission, role) => apply((ir) => opToggleCell(ir, type, permission, role)),
    addRole: (type, name) => apply((ir) => opAddRole(ir, type, name)),
    removeRole: (type, name) => apply((ir) => opRemoveRole(ir, type, name)),
    renameRole: (type, from, to) => apply((ir) => opRenameRole(ir, type, from, to)),
    setRoleAssignableBy: (type, role, refs) =>
      apply((ir) => opSetRoleAssignableBy(ir, type, role, refs)),
    addPermission: (type, name) => apply((ir) => opAddPermission(ir, type, name)),
    removePermission: (type, name) => apply((ir) => opRemovePermission(ir, type, name)),
    renamePermission: (type, from, to) => apply((ir) => opRenamePermission(ir, type, from, to)),
    toggleInherit: (type, permission, parentRelation) =>
      apply((ir) => opToggleInherit(ir, type, permission, parentRelation)),

    resetTo: (ir) => {
      const { dsl, errors, compileError } = recompute(ir, "");
      set({
        ir,
        dsl,
        errors,
        compileError,
        importError: null,
        coverage: null,
        readOnly: false,
        positions: ensurePositions(ir, {}),
        selectedType: null,
      });
    },

    loadFromDsl: (dslText) => {
      const { ir, coverage } = parseDslToIr(dslText);
      if (!ir) {
        // 구문 오류: 모델 상태를 건드리지 않고 import 에러만 표시(기존 모델 오염 금지).
        set({ importError: coverage.parseError ?? "failed to parse DSL" });
        return;
      }
      const readOnly = !coverage.fullyRepresentable;
      const errors = validateModelIR(ir);
      // 완전 표현 가능하면 정규화된 DSL을, 아니면 원본 텍스트를 보여준다.
      const dsl = readOnly ? dslText : recompute(ir, "").dsl;
      set({
        ir,
        coverage,
        readOnly,
        errors,
        dsl,
        compileError: null,
        importError: null,
        positions: ensurePositions(ir, {}),
        selectedType: null,
      });
    },
  };
});
