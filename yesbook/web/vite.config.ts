import react from "@vitejs/plugin-react";
import { loadEnv } from "vite";
import { defineConfig } from "vitest/config";

const DEFAULT_DEV_API_KEY = "dev-key-0123456789abcdef";
const DEFAULT_API_ORIGIN = "http://127.0.0.1:8080";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiKey = env.YESBOOK_DEV_API_KEY ?? DEFAULT_DEV_API_KEY;
  const target = env.YESBOOK_API_ORIGIN ?? DEFAULT_API_ORIGIN;

  return {
    plugins: [react()],
    server: {
      proxy: {
        "/api": {
          target,
          changeOrigin: true,
          rewrite: (path) => path.replace(/^\/api/, ""),
          configure: (proxy) => {
            proxy.on("proxyReq", (proxyRequest) => {
              proxyRequest.setHeader("Authorization", `Bearer ${apiKey}`);
            });
          },
        },
      },
    },
    test: {
      environment: "jsdom",
      setupFiles: "./src/test/setup.ts",
      css: true,
      restoreMocks: true,
    },
  };
});
