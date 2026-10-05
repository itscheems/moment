import { Editor, type JSONContent } from "@tiptap/core";
import { expect, it } from "vitest";
import { articleEditorExtensions } from "./schema";
import { toArticleDocument } from "./document";
import { articleWithOutline } from "../../../components/content/outlineDocument";

const document: JSONContent = {
  type: "doc",
  content: [
    {
      type: "heading",
      attrs: { level: 1 },
      content: [{ type: "text", text: "同名章节" }],
    },
    { type: "paragraph", content: [{ type: "text", text: "正文" }] },
    {
      type: "heading",
      attrs: { level: 1 },
      content: [{ type: "text", text: "同名章节" }],
    },
    {
      type: "heading",
      attrs: { level: 3 },
      content: [{ type: "text", text: "细节" }],
    },
  ],
};

it("gives duplicate headings distinct stable reading anchors without mutating the input", () => {
  const original = structuredClone(document);
  const one = articleWithOutline(document);
  const two = articleWithOutline(document);
  const ids: string[] = [];
  one.descendants((node) => {
    if (node.type.name === "heading") ids.push(node.attrs.id);
  });
  expect(new Set(ids).size).toBe(3);
  expect(ids.every(Boolean)).toBe(true);
  expect(one.eq(two)).toBe(true);
  expect(document).toEqual(original);
});

it("persists native heading anchors across document saves", () => {
  const editor = new Editor({
    extensions: articleEditorExtensions(),
    content: document,
  });
  const original = toArticleDocument(document);
  const originalText = editor.getText();
  editor.commands.insertContentAt(editor.state.doc.content.size, {
    type: "heading",
    attrs: { level: 2 },
    content: [{ type: "text", text: "新增" }],
  });
  const headings = editor.storage.tableOfContents.content;
  expect(headings.map((item) => item.textContent)).toEqual([
    "同名章节",
    "同名章节",
    "细节",
    "新增",
  ]);
  expect(new Set(headings.map((item) => item.id)).size).toBe(4);
  const saved = toArticleDocument(editor.getJSON());
  expect(
    saved.content
      .filter((item) => item.type === "heading")
      .map((item) => item.attrs?.["data-toc-id"]),
  ).toEqual(headings.map((item) => item.id));
  editor.commands.undo();
  expect(
    toArticleDocument(editor.getJSON()).content.map((item) => item.type),
  ).toEqual(original.content.map((item) => item.type));
  expect(editor.getText()).toBe(originalText);
  editor.destroy();
});
