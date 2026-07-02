# lazyFGA api 컨테이너: multi-stage Go 빌드 → distroless static 바이너리.
# 빌드 컨텍스트는 저장소 루트지만 apps/api만 복사한다(parity fixture·migration은 go:embed되어
# packages/ 가 이미지에 필요 없다). 헬스체크는 셸 없는 distroless를 위해 바이너리 self-GET 모드로 한다
# (compose: ["CMD","/lazyfga-api","healthcheck"]). 스왑 시 오케스트레이터가 경로를 apps/api로 바꾼다.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download
COPY apps/api/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lazyfga-api ./cmd/lazyfga-api

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/lazyfga-api /lazyfga-api
EXPOSE 8787
ENTRYPOINT ["/lazyfga-api"]
