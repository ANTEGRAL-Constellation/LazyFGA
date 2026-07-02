#!/usr/bin/env bash
# LFGA-27 §4.3: Go 백엔드 총 statement 커버리지 게이트(하드 95%).
# -coverpkg=./... 로 cross-package 귀속(cmd/* 포함), -race + atomic 모드.
# CI는 LAZYFGA_TEST_INTEGRATION=1을 설정해 의존성 미가용 시 skip 대신 fail로 만든다.
# -count=1: 캐시된 프로파일(소스 리팩터 전 라인 경계)이 병합을 오염시키는 것을 방지한다.
set -euo pipefail

go test ./... -count=1 -race -coverpkg=./... -coverprofile=coverage.out -covermode=atomic
go tool cover -func=coverage.out # 전체 per-package 리포트(CI 로그).
total=$(go tool cover -func=coverage.out | awk '/^total:/{sub("%","",$NF); print $NF}')
awk -v t="$total" 'BEGIN{ exit !(t+0 >= 95.0) }' ||
  { echo "FAIL: total coverage ${total}% < 95%"; exit 1; }
echo "OK: total coverage ${total}%"
