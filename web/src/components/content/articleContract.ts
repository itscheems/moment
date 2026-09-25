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
  paragraph: [],
  heading: ["level"],
  blockquote: [],
  bulletList: [],
  orderedList: ["start"],
  listItem: [],
  codeBlock: ["language"],
  horizontalRule: [],
  table: [],
  tableRow: [],
  tableHeader: ["colspan", "rowspan"],
  tableCell: ["colspan", "rowspan"],
  image: ["media_id", "alt"],
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
};

/** The nodes a picture cannot be nested inside, mirroring articleNodeSpecs. */
export const BLOCKS_IN_QUOTE =
  "paragraph|heading|blockquote|bulletList|orderedList|codeBlock";
/** A table cell holds less again: no heading, no quote, no rule, no picture. */
export const BLOCKS_IN_CELL = "paragraph|bulletList|orderedList|codeBlock";

/** Mirrors ArticleDocSchemaVersion (internal/domain/article_doc.go). */
export const ARTICLE_SCHEMA_VERSION = 1;

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
