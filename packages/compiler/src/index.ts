// @lazyfga/compiler — 비주얼 IR ↔ OpenFGA DSL 변환의 단일 진입점.
// 제품의 심장. isomorphic-only(브라우저·Bun 양쪽 동작): Node/플랫폼 전용 의존 금지,
// apps/* import 금지. @lazyfga/shared(타입 계약)는 import 가능.
export * from "./ir-to-dsl"; // lazyfga-3: IR → .fga DSL + AuthModel JSON
export * from "./dsl-to-ir"; // lazyfga-4: .fga → IR (subset 안에서만)
export * from "./coverage"; // lazyfga-4: subset 경계 판정
