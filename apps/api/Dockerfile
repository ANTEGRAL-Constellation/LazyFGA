# lazyFGA api 컨테이너: pnpm으로 워크스페이스 의존성 설치 후 Bun으로 실행.
# (워크스페이스는 pnpm-workspace.yaml 기준, 런타임은 Bun on Hono)
FROM node:24-slim AS deps
RUN corepack enable
WORKDIR /app
COPY pnpm-workspace.yaml pnpm-lock.yaml .npmrc package.json ./
COPY packages/shared/package.json ./packages/shared/
COPY packages/compiler/package.json ./packages/compiler/
COPY apps/api/package.json ./apps/api/
COPY apps/web/package.json ./apps/web/
RUN pnpm install --frozen-lockfile --filter "@lazyfga/api..."

FROM oven/bun:1.3-slim AS runtime
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY --from=deps /app/packages/shared/node_modules ./packages/shared/node_modules
COPY --from=deps /app/packages/compiler/node_modules ./packages/compiler/node_modules
COPY --from=deps /app/apps/api/node_modules ./apps/api/node_modules
COPY package.json pnpm-workspace.yaml ./
COPY packages ./packages
COPY apps/api ./apps/api
ENV NODE_ENV=production
EXPOSE 8787
CMD ["bun", "run", "apps/api/src/index.ts"]
