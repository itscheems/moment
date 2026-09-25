import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import { EditorContent } from "@tiptap/react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { articleEditorExtensions } from "./schema";
import { toArticleDocument } from "./document";
import { CharacterCount } from "./components/CharacterCount";
import { SlashMenu } from "./components/SlashMenu";
import { moveArticleBlock } from "./extensions/BlockHandles";

let editor: Editor;
afterEach(() => {
  cleanup();
  editor?.destroy();
  vi.restoreAllMocks();
});
function setup(content: string) {
  editor = new Editor({ extensions: articleEditorExtensions(), content });
  // Geometry is covered in the browser; these tests exercise command semantics.
  vi.spyOn(editor.view, "coordsAtPos").mockReturnValue({
    left: 20,
    right: 20,
    top: 20,
    bottom: 40,
  });
  render(
    <>
      <EditorContent editor={editor} />
      <SlashMenu editor={editor} onPickImage={vi.fn()} />
      <CharacterCount editor={editor} />
    </>,
  );
}

it("filters slash commands and applies the selected command from the keyboard", async () => {
  setup("<p></p>");
  await act(async () => {
    editor.commands.insertContent("/h2");
  });
  expect(screen.getByRole("listbox", { name: "插入内容" })).toBeInTheDocument();
  expect(screen.getAllByRole("option")).toHaveLength(1);
  fireEvent.keyDown(editor.view.dom, { key: "Enter" });
  expect(editor.state.doc.child(0).type.name).toBe("heading");
  expect(editor.state.doc.child(0).attrs.level).toBe(2);
  expect(editor.state.doc.textContent).toBe("");
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
});

it("supports arrow selection and leaves slash text intact on Escape or IME Enter", async () => {
  setup("<p></p>");
  await act(async () => {
    editor.commands.insertContent("/");
  });
  fireEvent.keyDown(editor.view.dom, { key: "ArrowDown" });
  expect(screen.getByRole("option", { name: "标题 1" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  fireEvent.keyDown(editor.view.dom, { key: "Enter", isComposing: true });
  expect(editor.state.doc.textContent).toBe("/");
  fireEvent.keyDown(editor.view.dom, { key: "Escape" });
  expect(screen.queryByRole("listbox")).not.toBeInTheDocument();
  expect(editor.state.doc.textContent).toBe("/");
});

it("reopens slash commands after a dismissed query is cleared and typed again", async () => {
  setup("<p></p>");
  await act(async () => {
    editor.commands.insertContent("/");
  });
  fireEvent.keyDown(editor.view.dom, { key: "Escape" });
  await act(async () => {
    editor.commands.clearContent();
  });
  await act(async () => {
    editor.commands.insertContent("/");
  });
  expect(screen.getByRole("listbox", { name: "插入内容" })).toBeInTheDocument();
});

it("counts Unicode characters without counting whitespace or control labels", async () => {
  setup("<p>河流 🏞️ A</p>");
  expect(screen.getByText("正文 5 字")).toBeInTheDocument();
  await act(async () => {
    editor.commands.insertContentAt(1, "你好");
  });
  expect(screen.getByText("正文 7 字")).toBeInTheDocument();
});

it("moves a whole nested block, preserves marks, and can undo the move", async () => {
  setup("<blockquote><p><strong>river</strong></p></blockquote><p>last</p>");
  const original = toArticleDocument(editor.getJSON());
  const tr = moveArticleBlock(editor.state, 0, editor.state.doc.content.size);
  expect(tr).not.toBeNull();
  await act(async () => {
    editor.view.dispatch(tr!);
  });
  expect(editor.state.doc.child(0).textContent).toBe("last");
  expect(toArticleDocument(editor.getJSON()).content[1]).toEqual(
    original.content[0],
  );
  await act(async () => {
    editor.commands.undo();
  });
  expect(toArticleDocument(editor.getJSON())).toEqual(original);
});

it("offers keyboard and touch actions for moving blocks without drag and drop", async () => {
  setup("<p>first</p><p>second</p>");
  fireEvent.keyDown(screen.getByRole("button", { name: "移动第 1 块" }), {
    key: "ArrowDown",
    altKey: true,
  });
  expect(editor.state.doc.child(0).textContent).toBe("second");
  fireEvent.click(screen.getByRole("button", { name: "移动第 2 块" }));
  fireEvent.click(screen.getByRole("button", { name: "上移" }));
  expect(editor.state.doc.child(0).textContent).toBe("first");
});
