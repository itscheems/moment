import { type Extensions } from "@tiptap/core";
import Blockquote from "@tiptap/extension-blockquote";
import Link from "@tiptap/extension-link";
import {
  BulletList,
  ListItem,
  ListKeymap,
  OrderedList,
} from "@tiptap/extension-list";
import Placeholder from "@tiptap/extension-placeholder";
import {
  Table,
  TableCell,
  TableHeader,
  TableRow,
} from "@tiptap/extension-table";
import Underline from "@tiptap/extension-underline";
import StarterKit from "@tiptap/starter-kit";

import type { ArticleMarkType, ArticleNodeType } from "../../../api/public";
import { isAllowedArticleHref } from "./links";
import { ManagedImage } from "./extensions/ManagedImage";
import { BlockHandles } from "./extensions/BlockHandles";
import { UploadPlaceholder } from "./extensions/UploadPlaceholder";

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
const BLOCKS_IN_QUOTE =
  "paragraph|heading|blockquote|bulletList|orderedList|codeBlock";
/** A table cell holds less again: no heading, no quote, no rule, no picture. */
const BLOCKS_IN_CELL = "paragraph|bulletList|orderedList|codeBlock";

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

/**
 * Every extension the article editor runs, and no other.
 *
 * Three kinds of correction are applied to what Tiptap ships:
 *
 *  - Nodes outside the allowlist are turned off. StarterKit's trailing node is
 *    among them: it appends an empty paragraph that would be stored as part of
 *    the article rather than as a place to click.
 *  - Attributes outside the allowlist are stripped by replacing the whole
 *    `addAttributes` rather than extending it. `resizable: false` does *not*
 *    remove `colwidth` from a table cell -- it only hides the drag handles --
 *    and a stored `colwidth` is a body the server refuses.
 *  - Content models are narrowed. Tiptap lets a blockquote, a list item and a
 *    table cell hold any block at all; the server lets them hold far less, so
 *    a picture dragged into a list would save as a rejection.
 */
export function articleEditorExtensions(
  options: { placeholder?: string } = {},
): Extensions {
  return [
    StarterKit.configure({
      heading: { levels: [1, 2, 3] },
      codeBlock: { languageClassPrefix: "language-" },
      // Registered below, each with the allowlist's attributes and nothing
      // else. StarterKit offers no way to take an attribute back.
      blockquote: false,
      bulletList: false,
      listItem: false,
      listKeymap: false,
      orderedList: false,
      link: false,
      underline: false,
      // Would write an empty paragraph into every article.
      trailingNode: false,
    }),
    Blockquote.extend({ content: `(${BLOCKS_IN_QUOTE})+` }),
    BulletList,
    OrderedList.extend({
      // Tiptap 3 also carries `type`, for `<ol type="a">`. The server has no
      // such attribute, so the node is rebuilt with `start` alone.
      addAttributes() {
        return {
          start: {
            default: 1,
            parseHTML: (element) => {
              const start = element.getAttribute("start");
              return start ? Number.parseInt(start, 10) : 1;
            },
          },
        };
      },
    }),
    ListItem.extend({ content: `paragraph (${BLOCKS_IN_QUOTE})*` }),
    ListKeymap,
    Underline,
    Link.configure({
      openOnClick: false,
      autolink: true,
      linkOnPaste: true,
      defaultProtocol: "https",
      protocols: ["http", "https", "mailto"],
      // Rendered, not stored: the document carries only `href`.
      HTMLAttributes: { rel: "nofollow noopener noreferrer", target: "_blank" },
      isAllowedUri: (url) => isAllowedArticleHref(url),
    }).extend({
      addAttributes() {
        return {
          href: { default: null },
        };
      },
    }),
    Table.configure({ resizable: false }),
    TableRow.extend({ content: "(tableCell | tableHeader)+" }),
    TableHeader.extend({
      content: `(${BLOCKS_IN_CELL})+`,
      addAttributes() {
        return {
          colspan: { default: 1 },
          rowspan: { default: 1 },
        };
      },
    }),
    TableCell.extend({
      content: `(${BLOCKS_IN_CELL})+`,
      addAttributes() {
        return {
          colspan: { default: 1 },
          rowspan: { default: 1 },
        };
      },
    }),
    ManagedImage,
    UploadPlaceholder,
    BlockHandles,
    Placeholder.configure({
      placeholder: options.placeholder ?? "写点什么…",
      showOnlyWhenEditable: true,
    }),
  ];
}
