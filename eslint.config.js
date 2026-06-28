// @ts-check
import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  {
    ignores: [
      "**/dist/**",
      "**/node_modules/**",
      "**/.turbo/**",
      "**/*.config.{js,ts,mjs,cjs}",
      "**/vite.config.ts",
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    rules: {
      "@typescript-eslint/no-unused-vars": [
        "warn",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },

  // 의존성 방향 강제(lazyfga-0 §4.3): 위반 시 lint 에러로 빌드 실패.
  // 그래프는 무순환 유지: web/api → compiler → shared, web/api → shared, shared=leaf.
  {
    // packages/shared 는 어떤 워크스페이스도 import 하지 않는다(최하위 계약).
    files: ["packages/shared/**/*.ts"],
    rules: {
      "@typescript-eslint/no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@lazyfga/*", "**/apps/**", "**/packages/compiler/**"],
              message: "shared is the leaf contract: it must not import any workspace package.",
            },
          ],
        },
      ],
    },
  },
  {
    // packages/compiler 는 apps/* 를 import 할 수 없다(역의존 금지). shared(타입)는 허용.
    files: ["packages/compiler/**/*.ts"],
    rules: {
      "@typescript-eslint/no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@lazyfga/api", "@lazyfga/web", "**/apps/**"],
              message: "compiler must stay isomorphic and may not import apps/*.",
            },
          ],
        },
      ],
    },
  },
  {
    // apps 끼리는 직접 import 금지(계약은 shared 경유).
    files: ["apps/web/**/*.{ts,tsx}"],
    rules: {
      "@typescript-eslint/no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@lazyfga/api", "**/apps/api/**"],
              message: "apps must not import each other; share contracts via @lazyfga/shared.",
            },
          ],
        },
      ],
    },
  },
  {
    files: ["apps/api/**/*.ts"],
    rules: {
      "@typescript-eslint/no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: ["@lazyfga/web", "**/apps/web/**"],
              message: "apps must not import each other; share contracts via @lazyfga/shared.",
            },
          ],
        },
      ],
    },
  },
);
