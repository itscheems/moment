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
        "Keep content extensions in components/content and editing UI behind the lazy editor entry.",
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

const loginFormBoundary = {
  meta: {
    type: "problem",
    schema: [],
    messages: {
      standalone:
        "Render credential forms only in the authentication page or account switcher; use loginNavigation for other account-entry actions.",
    },
  },
  create(context) {
    if (
      context.filename ===
        path.join(import.meta.dirname, "src/features/auth/LoginPage.tsx") ||
      context.filename ===
        path.join(
          import.meta.dirname,
          "src/features/auth/AccountSwitcher.tsx",
        ) ||
      /\.test\.tsx?$/.test(context.filename)
    )
      return {};
    const check = (node) => {
      const source = node.source?.value;
      if (
        typeof source === "string" &&
        /(?:^|\/)CredentialLogin(?:\.[cm]?[jt]sx?)?$/.test(source)
      )
        context.report({ node, messageId: "standalone" });
    };
    return {
      ImportDeclaration: check,
      ImportExpression: check,
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
    plugins: {
      moment: {
        rules: {
          "lazy-editor": lazyBoundary,
          "login-page-only": loginFormBoundary,
        },
      },
    },
    rules: { "moment/login-page-only": "error" },
    languageOptions: {
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
  },
  {
    files: ["src/components/content/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "@tiptap/core",
              importNames: ["Editor"],
              allowTypeImports: true,
              message:
                "Reading uses the static renderer, never an Editor instance.",
            },
          ],
          patterns: [
            {
              group: [
                "@tiptap/react",
                "@tiptap/react/*",
                "**/features/article/editor/**",
              ],
              message: "Shared content must not depend on the editing UI.",
            },
          ],
        },
      ],
    },
  },
  // Reading shares content definitions with writing. Only the writing UI and
  // interaction plugins live behind the lazy editor entry.
  {
    files: ["src/**/*.{ts,tsx}"],
    ignores: [
      "src/features/article/editor/**",
      "src/features/article/reader/**",
      "src/components/content/**",
    ],
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
