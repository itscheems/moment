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
import { BlockActionsMenu } from "./components/BlockControls";
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
      <BlockActionsMenu editor={editor} />
      <SlashMenu editor={editor} onPickImage={vi.fn()} />
      <CharacterCount editor={editor} />
    </>,
  );
}

it("duplicates and deletes complete blocks with undo, and inserts from the block menu", () => {
  setup("<p><strong>first</strong></p><p>last</p>");
  fireEvent.click(screen.getByRole("button", { name: "当前块操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "复制块" }));
  expect(editor.state.doc.childCount).toBe(3);
  expect(editor.state.doc.child(1).eq(editor.state.doc.child(0))).toBe(true);
  act(() => {
    editor.commands.undo();
  });
  expect(editor.state.doc.childCount).toBe(2);
  fireEvent.click(screen.getByRole("button", { name: "当前块操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "删除块" }));
  expect(editor.state.doc.textContent).toBe("last");
  act(() => {
    editor.commands.undo();
  });
  fireEvent.click(screen.getByRole("button", { name: "当前块操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "在下方插入" }));
  expect(editor.state.doc.child(1).textContent).toBe("/");
  expect(screen.getByRole("listbox", { name: "插入内容" })).toBeInTheDocument();
});

it("colours code while writing without persisting highlighting marks", () => {
  setup('<pre><code class="language-js">const value = 1;</code></pre>');
  expect(editor.view.dom.querySelector(".hljs-keyword")).toHaveTextContent(
    "const",
  );
  expect(JSON.stringify(toArticleDocument(editor.getJSON()))).not.toContain(
    "hljs",
  );
  act(() => {
    editor.commands.setContent(
      '<pre><code class="language-python">return True</code></pre>',
    );
  });
  expect(editor.view.dom.querySelector(".hljs-keyword")).toHaveTextContent(
    "return",
  );
  expect(editor.state.doc.textContent).toBe("return True");
});

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

it("does not request a selection reveal when the slash menu is scrolled", async () => {
  setup("<p></p>");
  await act(async () => {
    editor.commands.insertContent("/");
  });
  const reveal = vi.fn();
  for (const option of screen.getAllByRole("option")) {
    option.scrollIntoView = reveal;
  }
  // This tests which input requests scrolling, not jsdom geometry.
  // Real pointer scrolling and lower-option clicks are verified in the browser.
  fireEvent.scroll(screen.getByRole("listbox", { name: "插入内容" }));
  fireEvent.scroll(window);
  expect(reveal).not.toHaveBeenCalled();
  fireEvent.keyDown(editor.view.dom, { key: "ArrowDown" });
  expect(reveal).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("option", { name: "标题 1" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
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
  fireEvent.keyDown(editor.view.dom, {
    key: "ArrowDown",
    altKey: true,
  });
  expect(editor.state.doc.child(0).textContent).toBe("second");
  fireEvent.click(screen.getByRole("button", { name: "当前块操作" }));
  fireEvent.click(screen.getByRole("menuitem", { name: "上移" }));
  expect(editor.state.doc.child(0).textContent).toBe("first");
});

it("keeps the drag handle unlocked until the click completes", () => {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: "<p>first</p>",
  });
  render(<BlockActionsMenu editor={editor} position={0} />);
  const handle = screen.getByRole("button", { name: "块操作", exact: true });
  fireEvent.mouseDown(handle, { button: 0 });
  expect(screen.queryByRole("menu")).not.toBeInTheDocument();
  fireEvent.mouseUp(handle, { button: 0 });
  fireEvent.click(handle);
  expect(screen.getByRole("menuitem", { name: "复制块" })).toBeInTheDocument();
});
