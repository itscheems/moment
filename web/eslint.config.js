import js from "@eslint/js";
import tseslint from "typescript-eslint";
import path from "node:path";

const editorDirectory = path.join(
  import.meta.dirname,
  "src/features/article/editor",
);
const lazyBoundary = {
  meta: {
    type: "problem",
    schema: [],
    messages: {
      eager:
        "Keep Tiptap and article editor internals behind the lazy editor entry.",
    },
  },
  create(context) {
    const check = (node) => {
      const source = node.source?.value;
      if (
        typeof source !== "string" ||
        node.importKind === "type" ||
        node.exportKind === "type"
      )
        return;
      const resolved = path.resolve(path.dirname(context.filename), source);
      if (
        source.startsWith("@tiptap/") ||
        source.startsWith("prosemirror-") ||
        (resolved.startsWith(`${editorDirectory}/`) &&
          !/^index(?:\.tsx?)?$/.test(path.basename(resolved)))
      ) {
        context.report({ node, messageId: "eager" });
      }
    };
    return {
      ImportDeclaration: check,
      ExportNamedDeclaration: check,
      ExportAllDeclaration: check,
    };
  },
};

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
    plugins: { moment: { rules: { "lazy-editor": lazyBoundary } } },
    rules: {
      "moment/lazy-editor": "error",
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
