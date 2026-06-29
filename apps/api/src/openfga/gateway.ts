import { OpenFgaClient } from "@openfga/sdk";
import type {
  TupleKey,
  TupleKeyWithoutCondition,
  WriteAuthorizationModelRequest,
} from "@openfga/sdk";

export interface CheckInput {
  user: string;
  relation: string;
  object: string;
  context?: Record<string, unknown>;
}

export interface ReadInput {
  user?: string;
  relation?: string;
  object?: string;
}

export interface ReadTuple {
  user: string;
  relation: string;
  object: string;
  /** tuple에 부착된 조건(있을 때만, lazyfga-14/20). */
  condition?: { name: string; context?: Record<string, unknown> };
}

export interface WriteInput {
  writes?: TupleKey[];
  deletes?: TupleKeyWithoutCondition[];
}

/** 부트스트랩 시 store id 해석에 쓰이는 콜백(openfga 모듈을 DB-비의존으로 유지). */
export interface BootstrapOptions {
  /** env LAZYFGA_STORE_ID(선택). */
  envStoreId?: string;
  /** lazyFGA DB(instance_config)에 저장된 store id 로드. */
  loadStoredStoreId(): Promise<string | null>;
  /** 확정된 store id 영속. */
  persistStoreId(storeId: string): Promise<void>;
}

/**
 * 단일 store에 바인딩된 OpenFGA 클라이언트 진입점.
 * 부팅 시 store를 보장(없으면 생성)하고, 이후 모든 호출은 이 store를 사용한다.
 */
export interface OpenFgaGateway {
  bootstrap(opts: BootstrapOptions): Promise<{ storeId: string }>;
  getStoreId(): string;
  /** OpenFGA 연결 헬스. */
  ping(): Promise<boolean>;
  check(input: CheckInput, opts?: { authorizationModelId?: string }): Promise<{ allowed: boolean }>;
  read(input: ReadInput): Promise<{ tuples: ReadTuple[] }>;
  write(input: WriteInput, opts?: { authorizationModelId?: string }): Promise<void>;
  writeAuthorizationModel(
    model: WriteAuthorizationModelRequest,
  ): Promise<{ authorizationModelId: string }>;
}

class OpenFgaGatewayImpl implements OpenFgaGateway {
  private readonly apiUrl: string;
  /** store 관리(create/get/list)용. store에 바인딩되지 않는다. */
  private readonly mgmt: OpenFgaClient;
  private storeId: string | null = null;
  private client: OpenFgaClient | null = null;

  constructor(apiUrl: string) {
    this.apiUrl = apiUrl;
    this.mgmt = new OpenFgaClient({ apiUrl });
  }

  async bootstrap(opts: BootstrapOptions): Promise<{ storeId: string }> {
    const candidate = opts.envStoreId ?? (await opts.loadStoredStoreId());
    let storeId: string | null = null;

    if (candidate && (await this.storeExists(candidate))) {
      storeId = candidate;
    } else if (candidate && opts.envStoreId) {
      // env로 명시한 store가 OpenFGA에 없음 → 조용히 새로 만들면 데이터 skew 위험.
      console.warn(
        `[openfga] LAZYFGA_STORE_ID=${candidate} not found in OpenFGA; creating a new store. ` +
          `If OpenFGA was reset, lazyFGA Postgres data may now reference a fresh empty store.`,
      );
    }
    if (!storeId) {
      const created = await this.mgmt.createStore({ name: "lazyfga" });
      storeId = created.id;
    }

    await opts.persistStoreId(storeId);
    this.storeId = storeId;
    this.client = new OpenFgaClient({ apiUrl: this.apiUrl, storeId });
    return { storeId };
  }

  getStoreId(): string {
    if (!this.storeId) throw new Error("OpenFGA gateway not bootstrapped");
    return this.storeId;
  }

  async ping(): Promise<boolean> {
    try {
      await this.mgmt.listStores();
      return true;
    } catch {
      return false;
    }
  }

  async check(
    input: CheckInput,
    opts?: { authorizationModelId?: string },
  ): Promise<{ allowed: boolean }> {
    const res = await this.requireClient().check(
      {
        user: input.user,
        relation: input.relation,
        object: input.object,
        context: input.context,
      },
      opts?.authorizationModelId ? { authorizationModelId: opts.authorizationModelId } : undefined,
    );
    return { allowed: res.allowed ?? false };
  }

  async read(input: ReadInput): Promise<{ tuples: ReadTuple[] }> {
    // OpenFGA Read는 페이지네이션된다(기본 페이지 ~50). continuation_token이 빌 때까지 전 페이지를
    // 모아야 한다 — 안 그러면 권한 목록이 첫 페이지로 조용히 잘려 감사/회수가 불완전해진다(LFGA-20 review).
    const client = this.requireClient();
    const tuples: ReadTuple[] = [];
    let continuationToken: string | undefined;
    do {
      const res = await client.read(
        { user: input.user, relation: input.relation, object: input.object },
        continuationToken ? { continuationToken } : undefined,
      );
      for (const t of res.tuples ?? []) {
        if (!t.key) continue;
        const tuple: ReadTuple = { user: t.key.user, relation: t.key.relation, object: t.key.object };
        if (t.key.condition) {
          tuple.condition = {
            name: t.key.condition.name,
            context: t.key.condition.context as Record<string, unknown> | undefined,
          };
        }
        tuples.push(tuple);
      }
      continuationToken = res.continuation_token || undefined;
    } while (continuationToken);
    return { tuples };
  }

  async write(input: WriteInput, opts?: { authorizationModelId?: string }): Promise<void> {
    // 기본 transaction 모드(단일 tuple = 1회 transactional Write). duplicate write / missing delete는
    // OpenFGA가 400 invalid-input으로 던지며, 호출부가 classifyWriteError로 멱등 흡수한다(lazyfga-20).
    // authorizationModelId를 넘기면 OpenFGA가 발행본 모델 기준으로 tuple을 검증한다.
    await this.requireClient().write(
      { writes: input.writes, deletes: input.deletes },
      opts?.authorizationModelId ? { authorizationModelId: opts.authorizationModelId } : undefined,
    );
  }

  async writeAuthorizationModel(
    model: WriteAuthorizationModelRequest,
  ): Promise<{ authorizationModelId: string }> {
    const res = await this.requireClient().writeAuthorizationModel(model);
    return { authorizationModelId: res.authorization_model_id };
  }

  private requireClient(): OpenFgaClient {
    if (!this.client) throw new Error("OpenFGA gateway not bootstrapped");
    return this.client;
  }

  private async storeExists(id: string): Promise<boolean> {
    try {
      await this.mgmt.getStore({ storeId: id });
      return true;
    } catch {
      return false;
    }
  }
}

export function createOpenFgaGateway(config: { apiUrl: string }): OpenFgaGateway {
  return new OpenFgaGatewayImpl(config.apiUrl);
}
