import { lazy } from "react";

/**
 * The only way in.
 *
 * Everything behind this file -- Tiptap, ProseMirror, the schema, the toolbar
 * -- is about 120 KB gzipped, against roughly 314 KB for the whole
 * application. A reader cannot open the editor: it is the owner's, and it is
 * reached from one address. Loading it with the timeline would make every
 * visitor pay a third again on their first page for a surface they will never
 * see.
 *
 * So the import is dynamic and nothing outside this directory may reach past
 * this file. An eslint rule says so (`no-restricted-imports` in
 * eslint.config.js) and a test proves it, by walking the static import graph
 * out of the entry point and failing if anything under `editor/` is reachable
 * (src/app/entryChunk.test.ts).
 */
export const ArticleEditorPage = lazy(() => import("./ArticleEditor"));
