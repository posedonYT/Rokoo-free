import path from "path";
import { defineConfig } from "vitest/config";

export default defineConfig({
  esbuild: { jsx: "automatic" },
  test: {
    environment: "jsdom",
    setupFiles: ["./vitest.setup.ts"],
    exclude: ["**/node_modules/**", "tests/**"],
  },
  resolve: {
    alias: { "@": path.resolve(__dirname, "src") },
  },
});
