import { describe, expect, it } from "vitest";

import type { ArticleDocument } from "../../../api/public";
import {
  emptyArticleDocument,
  hasArticleSubstance,
  sameArticleDocument,
  toArticleDocument,
  toEditorContent,
} from "./document";

const MEDIA_ID = "01K2ABCDEF0123456789ABCDE7";

describe("serialising the editor's document", () => {
  it("drops the null attributes ProseMirror writes for every default", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        { type: "image", attrs: { media_id: MEDIA_ID, alt: null } },
        { type: "codeBlock", attrs: { language: null }, content: [] },
        {
          type: "orderedList",
          attrs: { start: 1, type: null },
          content: [
            {
              type: "listItem",
              content: [
                { type: "paragraph", content: [{ type: "text", text: "一" }] },
              ],
            },
          ],
        },
      ],
    });
    expect(wire.content[0]).toEqual({
      type: "image",
      attrs: { media_id: MEDIA_ID },
    });
    expect(wire.content[1]).toEqual({ type: "codeBlock" });
    expect(wire.content[2].attrs).toBeUndefined();
  });

  it("keeps an attribute once it says something the default does not", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        {
          type: "orderedList",
          attrs: { start: 4 },
          content: [
            {
              type: "listItem",
              content: [
                { type: "paragraph", content: [{ type: "text", text: "四" }] },
              ],
            },
          ],
        },
        {
          type: "codeBlock",
          attrs: { language: "go" },
          content: [{ type: "text", text: "package main" }],
        },
        { type: "image", attrs: { media_id: MEDIA_ID, alt: "入海口" } },
      ],
    });
    expect(wire.content[0].attrs).toEqual({ start: 4 });
    expect(wire.content[1].attrs).toEqual({ language: "go" });
    expect(wire.content[2].attrs).toEqual({
      media_id: MEDIA_ID,
      alt: "入海口",
    });
  });

  it("writes a cell's span only when it spans more than one", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        {
          type: "table",
          content: [
            {
              type: "tableRow",
              content: [
                {
                  type: "tableHeader",
                  attrs: { colspan: 1, rowspan: 1, colwidth: [120] },
                  content: [
                    {
                      type: "paragraph",
                      content: [{ type: "text", text: "年份" }],
                    },
                  ],
                },
                {
                  type: "tableCell",
                  attrs: { colspan: 2, rowspan: 1 },
                  content: [
                    {
                      type: "paragraph",
                      content: [{ type: "text", text: "水色" }],
                    },
                  ],
                },
              ],
            },
          ],
        },
      ],
    });
    const row = wire.content[0].content?.[0];
    expect(row?.content?.[0].attrs).toEqual({ colwidth: [120] });
    expect(row?.content?.[1].attrs).toEqual({ colspan: 2 });
  });

  it("never emits a run of text with no text", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            { type: "text", text: "" },
            { type: "text", text: "在" },
          ],
        },
      ],
    });
    expect(wire.content[0].content).toEqual([{ type: "text", text: "在" }]);
  });

  it("drops a node that must hold something and holds nothing", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        { type: "heading", attrs: { level: 2 } },
        { type: "paragraph" },
        { type: "paragraph", content: [{ type: "text", text: "在写" }] },
      ],
    });
    // The empty heading goes; the empty paragraph stays, because the server
    // allows one and the owner is probably standing in it.
    expect(wire.content.map((node) => node.type)).toEqual([
      "paragraph",
      "paragraph",
    ]);
  });

  it("keeps the words when a link points somewhere it may not", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            {
              type: "text",
              text: "看这里",
              marks: [{ type: "link", attrs: { href: "javascript:alert(1)" } }],
            },
          ],
        },
      ],
    });
    expect(wire.content[0].content).toEqual([{ type: "text", text: "看这里" }]);
  });

  it("strips everything but the href from a link", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            {
              type: "text",
              text: "看这里",
              marks: [
                {
                  type: "link",
                  attrs: {
                    href: "https://example.com/x",
                    target: "_blank",
                    rel: "noopener",
                    class: null,
                  },
                },
              ],
            },
          ],
        },
      ],
    });
    expect(wire.content[0].content?.[0].marks).toEqual([
      { type: "link", attrs: { href: "https://example.com/x" } },
    ]);
  });

  it("drops a node the allowlist does not name rather than failing the save", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [
        { type: "taskList", content: [{ type: "taskItem" }] },
        { type: "paragraph", content: [{ type: "text", text: "留下" }] },
      ],
    });
    expect(wire.content).toEqual([
      { type: "paragraph", content: [{ type: "text", text: "留下" }] },
    ]);
  });

  it("drops a picture that names no media", () => {
    const wire = toArticleDocument({
      type: "doc",
      content: [{ type: "image", attrs: { media_id: null, alt: "x" } }],
    });
    expect(wire.content).toEqual([]);
  });

  it("always answers with the one schema version the server takes", () => {
    expect(toArticleDocument({ type: "doc", content: [] }).schema_version).toBe(
      2,
    );
  });
});

describe("a stored body round-tripping through the editor", () => {
  const stored: ArticleDocument = {
    schema_version: 1,
    type: "doc",
    content: [
      {
        type: "heading",
        attrs: { level: 2 },
        content: [{ type: "text", text: "上游" }],
      },
      {
        type: "paragraph",
        content: [
          { type: "text", text: "河面" },
          { type: "text", text: "很窄", marks: [{ type: "bold" }] },
          { type: "hardBreak" },
          {
            type: "text",
            text: "看这里",
            marks: [{ type: "link", attrs: { href: "https://example.com/x" } }],
          },
        ],
      },
      { type: "image", attrs: { media_id: MEDIA_ID, alt: "入海口" } },
      { type: "horizontalRule" },
    ],
  };

  it("comes back byte for byte, so a draft that was only read is not dirty", () => {
    const again = toArticleDocument(toEditorContent(stored));
    expect(again).toEqual({ ...stored, schema_version: 2 });
    expect(sameArticleDocument(again, { ...stored, schema_version: 2 })).toBe(
      true,
    );
  });

  it("hands the editor a document without the schema version on it", () => {
    expect(toEditorContent(stored)).toMatchObject({ type: "doc" });
    expect(Object.keys(toEditorContent(stored))).toEqual(["type", "content"]);
  });
});

describe("whether a body may be published", () => {
  it("counts text, a picture and a table as substance", () => {
    expect(
      hasArticleSubstance({
        schema_version: 1,
        type: "doc",
        content: [
          { type: "paragraph", content: [{ type: "text", text: "在" }] },
        ],
      }),
    ).toBe(true);
    expect(
      hasArticleSubstance({
        schema_version: 1,
        type: "doc",
        content: [{ type: "image", attrs: { media_id: MEDIA_ID } }],
      }),
    ).toBe(true);
  });

  it("counts an empty document and one of blank lines as nothing", () => {
    expect(hasArticleSubstance(emptyArticleDocument())).toBe(false);
    expect(
      hasArticleSubstance({
        schema_version: 1,
        type: "doc",
        content: [
          { type: "paragraph" },
          { type: "paragraph", content: [{ type: "text", text: "   " }] },
        ],
      }),
    ).toBe(false);
  });
});
