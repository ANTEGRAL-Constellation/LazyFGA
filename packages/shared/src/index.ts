// @lazyfga/shared — end-to-end 타입·계약의 단일 진입점.
// web·api·compiler가 공유하는 contract. 런타임 의존성은 zod(검증)만 허용한다.
export * from "./model"; // lazyfga-2: 5-primitive ModelIR + validateModelIR
export * from "./edit"; // 순수 IR 편집 연산(canvas/matrix 공용)
export * from "./authzen"; // lazyfga-9: OpenID AuthZEN 1.0 요청/응답 타입
export * from "./policy"; // lazyfga-8: named policy 계약
export * from "./reason"; // lazyfga-11: ReasonResult / ReasonStep / MissingLink
export * from "./condition"; // lazyfga-13/14: 조건(ABAC/CEL) 계약
