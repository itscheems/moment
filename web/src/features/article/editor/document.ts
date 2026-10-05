import type { JSONContent } from "@tiptap/core";
import { gitHubEmojis } from "@tiptap/extension-emoji";

import {
  parseArticleDocument,
  type ArticleDocument,
  type ArticleMarkType,
  type ArticleNode,
  type ArticleNodeType,
} from "../../../api/public";
import { mediaPreviewUrl } from "../../../api/mediaUrls";
import { isAllowedArticleHref } from "./links";
import {
  ARTICLE_MARK_ATTRS,
  ARTICLE_NODE_ATTRS,
  ARTICLE_SCHEMA_VERSION,
} from "./schema";

/**
 * The two directions an article body travels between the editor and the wire.
 *
 * They are not the identity function in either direction, and the differences
 * are the whole reason this file exists:
 *
 *  - ProseMirror writes every declared attribute, default value and all, so an
 *    untitled picture arrives as `{"media_id":"…","alt":null}` and the server
 *    refuses it: `alt` is allowed to be absent, not to be null.
 *  - The server writes an attribute only when it carries information -- a list
 *    starting at one and a cell spanning one column are both left out -- so
 *    echoing back what it sent has to produce the same bytes, or every reopened
 *    draft would look changed the moment it loaded.
 *  - A node that must hold something can be empty in an editor and cannot be
 *    empty on the wire. A heading with nothing under it is a line the owner is
 *    still typing, not an article that fails to save.
 *
 * Whatever comes out is put through `parseArticleDocument`, the same grammar
 * the reading side applies to a body arriving from the server. A document that
 * would be refused is refused here, where the owner can be told, rather than
 * at the far end of a save they thought had worked.
 */

/** Mirrors articleNodeSpecs' `needsChild` (internal/domain/article_doc.go). */
const NEEDS_CHILD: ReadonlySet<string> = new Set<ArticleNodeType>([
  "heading",
  "blockquote",
  "bulletList",
  "orderedList",
  "listItem",
  "taskList",
  "taskItem",
  "table",
  "tableRow",
  "tableHeader",
  "tableCell",
]);

const NODE_TYPES: readonly string[] = Object.keys(ARTICLE_NODE_ATTRS);
const MARK_TYPES: readonly string[] = Object.keys(ARTICLE_MARK_ATTRS);
const emojiText = new Map(
  gitHubEmojis.map((emoji) => [
    emoji.name,
    emoji.emoji ?? `:${emoji.shortcodes[0]}:`,
  ]),
);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isNodeType(value: unknown): value is ArticleNodeType {
  return typeof value === "string" && NODE_TYPES.includes(value);
}

function isMarkType(value: unknown): value is ArticleMarkType {
  return typeof value === "string" && MARK_TYPES.includes(value);
}

/** A count the server will accept: a whole number of at least one. */
function counted(value: unknown): number | null {
  return typeof value === "number" && Number.isSafeInteger(value) && value >= 1
    ? value
    : null;
}

function nonEmptyString(value: unknown): string | null {
  return typeof value === "string" && value !== "" ? value : null;
}

/**
 * One node's attributes in the wire's own shape: only the names the allowlist
 * gives this node, and only where the value says something the default does
 * not.
 *
 * Null means the node itself cannot be written -- which today is only a
 * picture naming no media. A node that simply arrived without an `attrs`
 * object is an ordinary node with no attributes, not a broken one.
 */
function wireAttrs(
  type: ArticleNodeType,
  raw: unknown,
): Record<string, unknown> | null {
  const value: Record<string, unknown> = isRecord(raw) ? raw : {};
  const attrs: Record<string, unknown> = {};
  if (type === "heading") {
    const level = counted(value.level) ?? 1;
    attrs.level = Math.min(level, 3);
  }
  if (type === "orderedList") {
    const start = counted(value.start);
    if (start !== null && start > 1) attrs.start = start;
  }
  if (type === "codeBlock") {
    const language = nonEmptyString(value.language);
    if (language !== null) attrs.language = language;
  }
  if (type === "image") {
    const mediaId =
      nonEmptyString(value.media_id) ??
      (typeof value.src === "string"
        ? (value.src.match(
            /^\/api\/v1\/media\/([0-9A-HJKMNP-TV-Z]{26})(?:\/preview)?(?:\?|$)/,
          )?.[1] ?? null)
        : null);
    // A picture with no media behind it is not a picture. Returning null drops
    // the node rather than writing one the server has to refuse.
    if (mediaId === null) return null;
    attrs.media_id = mediaId;
    const alt = nonEmptyString(value.alt);
    if (alt !== null) attrs.alt = alt;
  }
  if (type === "tableHeader" || type === "tableCell") {
    const colspan = counted(value.colspan);
    const rowspan = counted(value.rowspan);
    if (colspan !== null && colspan > 1) attrs.colspan = colspan;
    if (rowspan !== null && rowspan > 1) attrs.rowspan = rowspan;
  }
  for (const key of ARTICLE_NODE_ATTRS[type]) {
    if (
      key in attrs ||
      [
        "level",
        "start",
        "language",
        "media_id",
        "alt",
        "colspan",
        "rowspan",
      ].includes(key)
    )
      continue;
    const item = value[key];
    if (item === null || item === undefined || item === "") continue;
    if (
      (key === "showCaption" && item === false) ||
      (key === "showTitle" && item === true)
    )
      continue;
    attrs[key] = item;
  }
  return attrs;
}

function wireMarks(value: unknown): Record<string, unknown>[] {
  if (!Array.isArray(value)) return [];
  const marks: Record<string, unknown>[] = [];
  const seen = new Set<ArticleMarkType>();
  for (const candidate of value) {
    if (!isRecord(candidate)) continue;
    const type = candidate.type;
    // One run of text carries a mark once. ProseMirror never repeats one, but
    // a pasted document can, and the server counts a repeat as a broken body.
    if (!isMarkType(type) || seen.has(type)) continue;
    if (type === "textStyle" || type === "highlight") {
      const color = isRecord(candidate.attrs)
        ? nonEmptyString(candidate.attrs.color)
        : null;
      if (color) {
        seen.add(type);
        marks.push({ type, attrs: { color } });
      }
      continue;
    }
    if (type !== "link") {
      seen.add(type);
      marks.push({ type });
      continue;
    }
    const attrs = candidate.attrs;
    const href = isRecord(attrs) ? nonEmptyString(attrs.href) : null;
    // A link with nowhere to go loses the mark, not the words under it.
    if (href === null || !isAllowedArticleHref(href)) continue;
    seen.add(type);
    marks.push({ type, attrs: { href } });
  }
  return marks;
}

function wireNodes(value: unknown): Record<string, unknown>[] {
  if (!Array.isArray(value)) return [];
  const nodes: Record<string, unknown>[] = [];
  for (const candidate of value) {
    const node = wireNode(candidate);
    if (node !== null) nodes.push(node);
  }
  return nodes;
}

function wireNode(value: unknown): Record<string, unknown> | null {
  if (!isRecord(value)) return null;
  const type = value.type;
  // Suggestions are native editor atoms; the shared content contract stores
  // their visible Unicode/handle text, which also works in Markdown posts.
  if (type === "emoji" || type === "mention") {
    const attrs = isRecord(value.attrs) ? value.attrs : {};
    const text =
      type === "emoji"
        ? emojiText.get(String(attrs.name))
        : typeof attrs.id === "string"
          ? `@${attrs.id}`
          : undefined;
    return text ? wireNode({ type: "text", text, marks: value.marks }) : null;
  }
  // Transient upload placeholders carry no persisted content until upload
  // finishes. Any other unsupported node is outside the stored grammar.
  if (!isNodeType(type)) return null;

  if (type === "text") {
    const text = nonEmptyString(value.text);
    // An empty run of text is refused by the server, and it is never anything
    // a reader would have seen.
    if (text === null) return null;
    const marks = wireMarks(value.marks);
    return marks.length > 0 ? { type, text, marks } : { type, text };
  }

  const node: Record<string, unknown> = { type };
  const attrs = wireAttrs(type, value.attrs);
  if (attrs === null) return null;
  if (Object.keys(attrs).length > 0) node.attrs = attrs;

  if (type === "horizontalRule" || type === "hardBreak" || type === "tocNode") {
    return node;
  }

  const content = wireNodes(value.content);
  if (content.length === 0) {
    // A heading or a cell with nothing in it is a place the owner is still
    // typing. Saving it would fail the whole document; dropping it saves the
    // rest, and the empty line comes back the moment they type into it.
    return NEEDS_CHILD.has(type) ? null : node;
  }
  node.content = content;
  return node;
}

/**
 * The editor's document as the server stores it.
 *
 * Throws a PublicApiError when what came out still breaks the grammar, which
 * is a bug in this file rather than something the owner did -- but it is
 * thrown where a save can report it instead of being discovered as a 400.
 */
export function toArticleDocument(value: unknown): ArticleDocument {
  const content = isRecord(value) ? wireNodes(value.content) : [];
  return parseArticleDocument({
    schema_version: ARTICLE_SCHEMA_VERSION,
    type: "doc",
    content,
  });
}

function editorNode(node: ArticleNode): JSONContent {
  const result: JSONContent = { type: node.type };
  if (node.attrs) result.attrs = { ...node.attrs };
  if (node.type === "image" && node.attrs?.media_id)
    result.attrs = {
      ...result.attrs,
      src: mediaPreviewUrl(node.attrs.media_id),
    };
  if (node.text !== undefined) result.text = node.text;
  if (node.marks) {
    result.marks = node.marks.map((mark) =>
      mark.attrs
        ? { type: mark.type, attrs: { ...mark.attrs } }
        : { type: mark.type },
    );
  }
  if (node.content) result.content = node.content.map(editorNode);
  return result;
}

/**
 * A stored body as the editor loads it. The attribute names already match, so
 * this only sheds the schema version, which belongs to the document and not to
 * any node inside it.
 */
export function toEditorContent(document: ArticleDocument): JSONContent {
  return { type: "doc", content: document.content.map(editorNode) };
}

export function emptyArticleDocument(): ArticleDocument {
  return {
    schema_version: ARTICLE_SCHEMA_VERSION,
    type: "doc",
    content: [],
  };
}

/**
 * Whether a body carries anything at all, mirroring articleHasSubstance: a run
 * of visible text, a picture or a table. The server enforces it at publish and
 * not at save, so a draft may be empty and an article may not.
 */
export function hasArticleSubstance(document: ArticleDocument): boolean {
  const walk = (nodes: readonly ArticleNode[]): boolean =>
    nodes.some((node) => {
      if (node.type === "image" || node.type === "table") return true;
      if (node.type === "text") return (node.text ?? "").trim() !== "";
      return node.content ? walk(node.content) : false;
    });
  return walk(document.content);
}

/** Two bodies are the same body when they serialise the same way. */
export function sameArticleDocument(
  left: ArticleDocument,
  right: ArticleDocument,
): boolean {
  return JSON.stringify(left) === JSON.stringify(right);
}
