import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [
    react(),
    {
      name: "article-editor-lazy-boundary",
      generateBundle(_options, bundle) {
        const seen = new Set<string>();
        const visit = (name: string) => {
          if (seen.has(name)) return;
          seen.add(name);
          const chunk = bundle[name];
          if (!chunk || chunk.type !== "chunk") return;
          for (const id of Object.keys(chunk.modules)) {
            if (
              /node_modules\/(?:@tiptap\/|prosemirror-)/.test(id) ||
              (/\/features\/article\/editor\//.test(id) &&
                !id.endsWith("/editor/index.ts"))
            ) {
              this.error(`Article editor leaked into the reader entry: ${id}`);
            }
          }
          chunk.imports.forEach(visit);
        };
        for (const chunk of Object.values(bundle)) {
          if (chunk.type === "chunk" && chunk.isEntry) visit(chunk.fileName);
        }
      },
    },
  ],
  build: {
    emptyOutDir: true,
    outDir: "../internal/webassets/dist",
  },
  test: {
    css: true,
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
  },
});
