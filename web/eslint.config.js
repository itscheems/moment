import js from "@eslint/js";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["../internal/webassets/dist", "coverage"] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  {
    files: ["**/*.{ts,tsx}"],
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  // The article editor is about 120 KB gzipped of Tiptap and ProseMirror, for
  // a surface only the owner can open. It is reached through one lazily
  // imported barrel (src/features/article/editor/index.ts); a static import of
  // anything behind that barrel would pull the whole thing into the entry
  // chunk, and every reader would download an editor they cannot use.
  // src/app/entryChunk.test.ts proves the graph; this rule is what says so at
  // the moment the import is typed.
  {
    files: ["src/**/*.{ts,tsx}"],
    ignores: ["src/features/article/editor/**"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            {
              group: [
                "**/features/article/editor/*",
                "**/features/article/editor/*/**",
              ],
              message:
                "The article editor is code-split. Import the lazy component from 'features/article/editor' instead of reaching inside it.",
            },
          ],
        },
      ],
    },
  },
);
