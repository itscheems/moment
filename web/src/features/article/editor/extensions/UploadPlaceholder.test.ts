import { Editor } from "@tiptap/core";
import { afterEach, expect, it } from "vitest";
import { articleEditorExtensions } from "../schema";
import { toArticleDocument } from "../document";
import {
  addUploadPlaceholder,
  clearUploadPlaceholders,
  finishImageUpload,
} from "./UploadPlaceholder";

let editor: Editor;
afterEach(() => editor?.destroy());
function setup() {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: "<p>first</p><p>last</p>",
  });
  editor.commands.setTextSelection(2);
  addUploadPlaceholder(editor, "upload", "river.png", "blob:test");
}

it("inserts at the mapped anchor even after typing and moving the caret", () => {
  setup();
  editor.commands.insertContentAt(1, "new ");
  editor.commands.setTextSelection(editor.state.doc.content.size - 1);
  expect(
    finishImageUpload(editor, "upload", "01K2ABCDEF0123456789ABCDE7"),
  ).toBe(true);
  const body = toArticleDocument(editor.getJSON());
  expect(body.content.map((node) => node.type)).toEqual([
    "paragraph",
    "image",
    "paragraph",
  ]);
  expect(editor.state.doc.child(0).textContent).toBe("new first");
  expect(editor.state.doc.child(2).textContent).toBe("last");
});

it("never saves a temporary node or lets a late response enter another draft", () => {
  setup();
  expect(
    toArticleDocument(editor.getJSON()).content.map((node) => node.type),
  ).toEqual(["paragraph", "paragraph"]);
  clearUploadPlaceholders(editor);
  editor.commands.setContent("<p>another draft</p>");
  expect(
    finishImageUpload(editor, "upload", "01K2ABCDEF0123456789ABCDE7"),
  ).toBe(false);
  expect(editor.state.doc.childCount).toBe(1);
});
