import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  build: {
    emptyOutDir: true,
    outDir: "../internal/webassets/dist",
  },
  test: {
    environment: "jsdom",
  },
});
