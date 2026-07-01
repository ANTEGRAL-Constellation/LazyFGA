import type { ParentRef, Permission, Role, SubjectRef, ValidationError } from "@lazyfga/shared";
import { useModelStore } from "../../store/modelStore";

/** 선택된 ResourceType의 행렬 편집 액션(lazyfga-6). useModelStore의 IR을 변형. */
export function useMatrix(typeName: string): {
  roles: Role[];
  permissions: Permission[];
  parents: ParentRef[];
  groups: string[];
  toggleCell(permission: string, role: string): void;
  addRole(name: string): void;
  removeRole(name: string): void;
  renameRole(from: string, to: string): void;
  setRoleAssignableBy(role: string, refs: SubjectRef[]): void;
  addPermission(name: string): void;
  removePermission(name: string): void;
  renamePermission(from: string, to: string): void;
  toggleInherit(permission: string, parentRelation: string): void;
  errors: ValidationError[];
  readOnly: boolean;
} {
  const ir = useModelStore((s) => s.ir);
  const allErrors = useModelStore((s) => s.errors);
  const readOnly = useModelStore((s) => s.readOnly);

  const idx = ir.resources.findIndex((r) => r.name === typeName);
  const resource = idx >= 0 ? ir.resources[idx] : undefined;
  const errors = allErrors.filter((e) => e.path.startsWith(`resources[${idx}]`));

  return {
    roles: resource?.roles ?? [],
    permissions: resource?.permissions ?? [],
    parents: resource?.parents ?? [],
    groups: ir.groups.map((g) => g.name),
    toggleCell: (permission, role) =>
      useModelStore.getState().toggleCell(typeName, permission, role),
    addRole: (name) => useModelStore.getState().addRole(typeName, name),
    removeRole: (name) => useModelStore.getState().removeRole(typeName, name),
    renameRole: (from, to) => useModelStore.getState().renameRole(typeName, from, to),
    setRoleAssignableBy: (role, refs) =>
      useModelStore.getState().setRoleAssignableBy(typeName, role, refs),
    addPermission: (name) => useModelStore.getState().addPermission(typeName, name),
    removePermission: (name) => useModelStore.getState().removePermission(typeName, name),
    renamePermission: (from, to) => useModelStore.getState().renamePermission(typeName, from, to),
    toggleInherit: (permission, parentRelation) =>
      useModelStore.getState().toggleInherit(typeName, permission, parentRelation),
    errors,
    readOnly,
  };
}
