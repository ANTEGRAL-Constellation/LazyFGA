import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// host:true 로 원격 서버 IP(예: 100.75.251.90)에서 접근 가능하게 노출한다.
export default defineConfig({
  plugins: [react()],
  server: {
    host: true,
    port: 5173,
  },
});
