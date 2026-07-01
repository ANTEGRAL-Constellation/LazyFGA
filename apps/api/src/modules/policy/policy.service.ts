import type { Policy } from "@lazyfga/shared";
import { getCurrentVersion } from "../model/model.repo";
import { findByActionResource, findById, insertPolicy, updatePolicy } from "./policy.repo";

export class PolicyError extends Error {
  constructor(
    public readonly status: 409 | 422,
    public readonly detail: string,
  ) {
    super(detail);
    this.name = "PolicyError";
  }
}

const SLUG_RE = /^[a-z0-9-]+$/;

/**
 * 현재 발행 모델에 resourceType 타입과 can_<permission> relation(= IR permission)이
 * 실제로 존재하는지 검증. 없으면 PolicyError(422).
 */
async function assertModelHasTarget(permission: string, resourceType: string): Promise<void> {
  const current = await getCurrentVersion();
  if (!current) {
    throw new PolicyError(422, "no model published yet; publish a model first");
  }
  const resource = current.irJson.resources.find((r) => r.name === resourceType);
  if (!resource) {
    throw new PolicyError(422, `current model has no resource type "${resourceType}"`);
  }
  if (!resource.permissions.some((p) => p.name === permission)) {
    throw new PolicyError(
      422,
      `"${resourceType}" has no permission "can_${permission}" in the current model`,
    );
  }
}

export async function createPolicy(input: {
  id: string;
  permission: string;
  resourceType: string;
  description?: string;
}): Promise<Policy> {
  if (!SLUG_RE.test(input.id)) {
    throw new PolicyError(422, `id must be a slug matching ${SLUG_RE.source}`);
  }
  if (await findById(input.id))
    throw new PolicyError(409, `policy id "${input.id}" already exists`);
  if (await findByActionResource(input.permission, input.resourceType)) {
    throw new PolicyError(
      409,
      `a policy for (${input.permission}, ${input.resourceType}) already exists`,
    );
  }
  await assertModelHasTarget(input.permission, input.resourceType);
  try {
    return await insertPolicy({
      id: input.id,
      permission: input.permission,
      resourceType: input.resourceType,
      description: input.description,
    });
  } catch (e) {
    // pre-check ↔ insert 사이 경합: DB UNIQUE(id PK 또는 perm+resource)가 최종 진실 → 409.
    if ((e as { code?: string })?.code === "23505") {
      throw new PolicyError(409, "policy already exists (id or (permission, resourceType))");
    }
    throw e;
  }
}

export async function editPolicy(
  id: string,
  patch: { permission?: string; resourceType?: string; description?: string },
): Promise<Policy> {
  const existing = await findById(id);
  if (!existing) throw new PolicyError(422, `policy "${id}" not found`); // 라우트가 404로 매핑
  const permission = patch.permission ?? existing.permission;
  const resourceType = patch.resourceType ?? existing.resourceType;

  // (permission, resourceType) 유일성: 다른 정책과 충돌 금지.
  const clash = await findByActionResource(permission, resourceType);
  if (clash && clash.id !== id) {
    throw new PolicyError(409, `a policy for (${permission}, ${resourceType}) already exists`);
  }
  await assertModelHasTarget(permission, resourceType);
  try {
    const updated = await updatePolicy(id, {
      permission,
      resourceType,
      description: patch.description ?? existing.description,
    });
    if (!updated) throw new PolicyError(422, `policy "${id}" not found`);
    return updated;
  } catch (e) {
    if ((e as { code?: string })?.code === "23505") {
      throw new PolicyError(409, `a policy for (${permission}, ${resourceType}) already exists`);
    }
    throw e;
  }
}
