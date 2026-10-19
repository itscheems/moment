import { Editor } from "@tiptap/core";
import { UndoRedo } from "@tiptap/extensions";
import { afterEach, expect, it } from "vitest";
import { articleContentExtensions } from "../../../components/content/schema";
import {
  emptyArticleDocument,
  toArticleDocument,
  toEditorContent,
} from "./document";
import {
  DraftHistory,
  loadDraftContent,
  updateDraftFields,
} from "./draftHistory";

let editor: Editor | null = null;
afterEach(() => {
  editor?.destroy();
});

it("keeps title, cover and body in upstream history without persisting editor metadata", () => {
  editor = new Editor({
    extensions: [...articleContentExtensions(), UndoRedo, DraftHistory],
  });
  loadDraftContent(editor, toEditorContent(emptyArticleDocument()), "原题");
  updateDraftFields(editor, { title: "新标题" });
  updateDraftFields(editor, { coverMediaId: "01ARZ3NDEKTSV4RRFFQ69G5FAV" });
  expect(editor.commands.undo()).toBe(true);
  expect(editor.state.doc.attrs.draftCover).toBeNull();
  expect(editor.state.doc.attrs.draftTitle).toBe("新标题");
  expect(editor.commands.undo()).toBe(true);
  expect(editor.state.doc.attrs.draftTitle).toBe("原题");
  expect(editor.commands.undo()).toBe(false);
  expect(editor.commands.redo()).toBe(true);
  expect(editor.state.doc.attrs.draftTitle).toBe("新标题");
  expect(toArticleDocument(editor.getJSON())).not.toHaveProperty("attrs");
  loadDraftContent(
    editor,
    toEditorContent(emptyArticleDocument()),
    "另一份草稿",
  );
  expect(editor.commands.undo()).toBe(false);
});
