import type { ArticleMarkType, ArticleNodeType } from "../../api/public";

/**
 * The grammar an article body is allowed to be written in.
 *
 * This is the client's copy of the allowlist in
 * internal/domain/article_doc.go: every node, every mark and every attribute
 * the server will accept, and nothing else. A body that names anything outside
 * it is refused whole, so a toolbar button that produces one is a button that
 * silently loses somebody's article.
 *
 * Keyed by the reading side's own union types, so a node the server grows has
 * to be spelled in api/public.ts first and then here -- the two cannot drift
 * apart without the compiler saying so. `schema.test.ts` closes the other
 * half of the loop by comparing this table against the schema the extensions
 * below actually build.
 */
export const ARTICLE_NODE_ATTRS: Readonly<
  Record<ArticleNodeType, readonly string[]>
> = {
  mapCard: ["id", "map_card"],
  paragraph: [
    "id",
    "textAlign",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  heading: [
    "level",
    "id",
    "data-toc-id",
    "textAlign",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  blockquote: ["id", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"],
  bulletList: ["id", "backgroundColor"],
  orderedList: ["start", "type", "id", "backgroundColor"],
  listItem: [],
  taskList: ["id", "backgroundColor"],
  taskItem: ["checked"],
  codeBlock: ["language", "id"],
  horizontalRule: [],
  table: ["id"],
  tableRow: [],
  tableHeader: [
    "colspan",
    "rowspan",
    "colwidth",
    "align",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  tableCell: [
    "colspan",
    "rowspan",
    "colwidth",
    "align",
    "backgroundColor",
    "nodeTextAlign",
    "nodeVerticalAlign",
  ],
  image: [
    "media_id",
    "alt",
    "title",
    "width",
    "height",
    "data-align",
    "showCaption",
  ],
  tocNode: ["id", "backgroundColor", "topOffset", "maxShowCount", "showTitle"],
  hardBreak: [],
  text: [],
};

export const ARTICLE_MARK_ATTRS: Readonly<
  Record<ArticleMarkType, readonly string[]>
> = {
  bold: [],
  italic: [],
  strike: [],
  code: [],
  underline: [],
  link: ["href"],
  textStyle: ["color"],
  highlight: ["color"],
  superscript: [],
  subscript: [],
};

/** Mirrors ArticleDocSchemaVersion (internal/domain/article_doc.go). */
export const ARTICLE_SCHEMA_VERSION = 3;

/** Mirrors articleCodeLanguages: a fence keeps a label only from this list. */
export const ARTICLE_CODE_LANGUAGES: readonly string[] = [
  "bash",
  "c",
  "cpp",
  "csharp",
  "css",
  "diff",
  "go",
  "graphql",
  "html",
  "java",
  "javascript",
  "json",
  "kotlin",
  "lua",
  "makefile",
  "markdown",
  "nginx",
  "objectivec",
  "php",
  "plaintext",
  "python",
  "ruby",
  "rust",
  "scss",
  "shell",
  "sql",
  "swift",
  "toml",
  "typescript",
  "xml",
  "yaml",
];
