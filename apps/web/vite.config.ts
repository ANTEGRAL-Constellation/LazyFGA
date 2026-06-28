import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// host:true 로 원격 서버 IP(예: 100.75.251.90)에서 접근 가능하게 노출한다.
// /api 는 lazyFGA api로 프록시(같은 출처 호출 → CORS 회피). 대상은 VITE_API_TARGET로 재정의.
const apiTarget = process.env.VITE_API_TARGET ?? "http://localhost:8787";

export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        rewrite: (p) => p.replace(/^\/api/, ""),
      },
    },
  },
});
