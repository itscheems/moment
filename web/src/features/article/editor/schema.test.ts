import { getSchema } from "@tiptap/core";
import { describe, expect, it } from "vitest";

import { parseArticleDocument } from "../../../api/public";
import {
  ARTICLE_MARK_ATTRS,
  ARTICLE_NODE_ATTRS,
  ARTICLE_SCHEMA_VERSION,
  articleEditorExtensions,
} from "./schema";

const schema = getSchema(articleEditorExtensions());

/** A canonical ULID, which is the only thing a picture may name. */
const MEDIA_ID = "01K2ABCDEF0123456789ABCDE7";

/** One plausible value per attribute the allowlist names. */
const SAMPLE_ATTRS: Readonly<Record<string, unknown>> = {
  level: 2,
  start: 3,
  language: "go",
  media_id: MEDIA_ID,
  alt: "入海口",
  colspan: 2,
  rowspan: 2,
  href: "https://example.com/x",
};

/**
 * Attributes Tiptap has shipped at one time or another for these very nodes,
 * and which the server has never accepted. They are the drift this file exists
 * to catch: `colwidth` and `align` ride along with a table cell, `type` with an
 * ordered list, and `target`/`rel`/`class`/`title` with a link.
 */
const FOREIGN_ATTRS: Readonly<Record<string, unknown>> = {
  colwidth: [120],
  align: "center",
  type: "a",
  target: "_blank",
  rel: "noopener",
  class: "highlight",
  title: "somebody's tooltip",
};

function sampleAttrs(names: readonly string[]): Record<string, unknown> {
  return Object.fromEntries(names.map((name) => [name, SAMPLE_ATTRS[name]]));
}

function documentWith(node: unknown): unknown {
  return {
    schema_version: ARTICLE_SCHEMA_VERSION,
    type: "doc",
    content: [node],
  };
}

function nodeWith(
  type: string,
  attrs: Record<string, unknown>,
): Record<string, unknown> {
  const node: Record<string, unknown> = { type };
  if (Object.keys(attrs).length > 0) node.attrs = attrs;
  if (type === "text") node.text = "文字";
  return node;
}

describe("the editor's grammar against the server's allowlist", () => {
  it("registers every node the allowlist names, and no other", () => {
    const registered = Object.keys(schema.nodes)
      // `doc` is the document itself rather than something written inside it.
      .filter((name) => name !== "doc")
      .sort();
    expect(registered).toEqual(Object.keys(ARTICLE_NODE_ATTRS).sort());
  });

  it("registers every mark the allowlist names, and no other", () => {
    expect(Object.keys(schema.marks).sort()).toEqual(
      Object.keys(ARTICLE_MARK_ATTRS).sort(),
    );
  });

  it.each(Object.entries(ARTICLE_NODE_ATTRS))(
    "gives the %s node exactly the attributes the allowlist gives it",
    (name, allowed) => {
      const declared = Object.keys(schema.nodes[name].spec.attrs ?? {}).sort();
      expect(declared).toEqual([...allowed].sort());
    },
  );

  it.each(Object.entries(ARTICLE_MARK_ATTRS))(
    "gives the %s mark exactly the attributes the allowlist gives it",
    (name, allowed) => {
      const declared = Object.keys(schema.marks[name].spec.attrs ?? {}).sort();
      expect(declared).toEqual([...allowed].sort());
    },
  );

  it.each(Object.entries(ARTICLE_NODE_ATTRS))(
    "writes a %s the reading grammar accepts",
    (name, allowed) => {
      const node = nodeWith(name, sampleAttrs(allowed));
      expect(() => parseArticleDocument(documentWith(node))).not.toThrow();
    },
  );

  it.each(Object.entries(ARTICLE_NODE_ATTRS))(
    "would be refused if a %s carried an attribute from outside the allowlist",
    (name, allowed) => {
      for (const [foreign, value] of Object.entries(FOREIGN_ATTRS)) {
        if (allowed.includes(foreign)) continue;
        const node = nodeWith(name, {
          ...sampleAttrs(allowed),
          [foreign]: value,
        });
        expect(() => parseArticleDocument(documentWith(node))).toThrow();
      }
    },
  );

  it("writes every mark in a shape the reading grammar accepts", () => {
    for (const [name, allowed] of Object.entries(ARTICLE_MARK_ATTRS)) {
      const mark: Record<string, unknown> = { type: name };
      if (allowed.length > 0) mark.attrs = sampleAttrs(allowed);
      expect(() =>
        parseArticleDocument(
          documentWith({ type: "text", text: "文字", marks: [mark] }),
        ),
      ).not.toThrow();
    }
  });

  it("would be refused if a link carried anything but its href", () => {
    expect(() =>
      parseArticleDocument(
        documentWith({
          type: "text",
          text: "文字",
          marks: [
            {
              type: "link",
              attrs: { href: "https://example.com", target: "_blank" },
            },
          ],
        }),
      ),
    ).toThrow();
  });
});

describe("where the editor lets a node sit", () => {
  const nests = (parent: string, child: string): boolean =>
    schema.nodes[parent].contentMatch.matchType(schema.nodes[child]) !== null;

  // Mirrors articleContentNestedBlocks / articleContentCellBlocks
  // (internal/domain/article_doc.go). Tiptap's own defaults are `block+` for
  // all three, which would let a picture into a list the server refuses.
  it.each(["blockquote", "listItem"])(
    "keeps a picture, a rule and a table out of a %s",
    (parent) => {
      expect(nests(parent, "image")).toBe(false);
      expect(nests(parent, "horizontalRule")).toBe(false);
      expect(nests(parent, "table")).toBe(false);
      expect(nests(parent, "paragraph")).toBe(true);
    },
  );

  it.each(["tableCell", "tableHeader"])(
    "keeps a heading, a quote and a picture out of a %s",
    (parent) => {
      expect(nests(parent, "heading")).toBe(false);
      expect(nests(parent, "blockquote")).toBe(false);
      expect(nests(parent, "image")).toBe(false);
      expect(nests(parent, "paragraph")).toBe(true);
      expect(nests(parent, "bulletList")).toBe(true);
    },
  );

  it("lets a picture and a table stand on their own at the top", () => {
    expect(nests("doc", "image")).toBe(true);
    expect(nests("doc", "table")).toBe(true);
  });
});
