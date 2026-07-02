# lazyFGA web 컨테이너: pnpm 워크스페이스로 Vite SPA 빌드 → nginx 정적 서빙.
# 빌드 컨텍스트는 저장소 루트(web은 packages/{shared,compiler} workspace:* 에 의존).
# 런타임은 nginx가 정적 파일 + /api 프록시(→ lazyfga-api:8787)를 담당한다(apps/web/nginx.conf).
FROM node:22-alpine AS build
RUN corepack enable
WORKDIR /repo

# 1) 매니페스트/락파일 먼저 복사해 install 레이어 캐시(소스 변경과 분리).
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY apps/web/package.json apps/web/package.json
COPY packages/shared/package.json packages/shared/package.json
COPY packages/compiler/package.json packages/compiler/package.json
RUN pnpm install --frozen-lockfile

# 2) 소스 복사 후 web만 빌드(packages는 web 번들에 소스로 포함됨).
COPY tsconfig.base.json ./
COPY packages/shared packages/shared
COPY packages/compiler packages/compiler
COPY apps/web apps/web
RUN pnpm --filter @lazyfga/web build

FROM nginx:1.27-alpine
COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /repo/apps/web/dist /usr/share/nginx/html
EXPOSE 80
