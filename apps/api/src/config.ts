/** 환경변수 단일 소스. compose/.env로 주입된다. */
export const config = {
  port: Number(process.env.PORT ?? 8787),
  databaseUrl: process.env.DATABASE_URL ?? "postgres://lazyfga:lazyfga@localhost:5432/lazyfga",
  openfgaApiUrl: process.env.OPENFGA_API_URL ?? "http://localhost:8080",
  /** 선택: 기존 OpenFGA store에 바인딩. 미지정이면 부트스트랩이 store를 생성한다. */
  storeId: process.env.LAZYFGA_STORE_ID || undefined,
  /** control-plane admin 토큰(lazyfga-10에서 사용). */
  adminToken: process.env.ADMIN_TOKEN ?? "",
} as const;
