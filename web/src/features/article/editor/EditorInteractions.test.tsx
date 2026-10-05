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
import { toArticleDocument, toEditorContent } from "./document";
import { DuplicateButton } from "@notion/components/ui/duplicate-button";
import { TextAlignButton } from "@notion/components/ui/text-align-button";
import { TableAddRowColumnButton } from "@notion/components/nodes/table-node/ui/table-add-row-column-button";
import { CharacterCount } from "./components/CharacterCount";

let editor: Editor;
afterEach(() => {
  cleanup();
  editor?.destroy();
  vi.restoreAllMocks();
});
function setup(content: string) {
  editor = new Editor({ extensions: articleEditorExtensions(), content });
  vi.spyOn(editor.view, "coordsAtPos").mockReturnValue({
    left: 20,
    right: 20,
    top: 20,
    bottom: 40,
  });
}

it("uses the source duplicate action and undo without losing block content", () => {
  setup("<p><strong>first</strong></p><p>last</p>");
  editor.commands.setNodeSelection(0);
  render(
    <>
      <EditorContent editor={editor} />
      <DuplicateButton editor={editor} />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Duplicate node" }));
  expect(editor.state.doc.childCount).toBe(3);
  expect(editor.state.doc.child(1).textContent).toBe("first");
  act(() => {
    editor.commands.undo();
  });
  expect(editor.state.doc.childCount).toBe(2);
});

it("persists native alignment through save and reopen", () => {
  setup("<p>alignment</p>");
  editor.commands.setTextSelection(2);
  render(
    <>
      <EditorContent editor={editor} />
      <TextAlignButton editor={editor} align="center" />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Align center" }));
  const saved = toArticleDocument(editor.getJSON());
  expect(saved.content[0].attrs?.textAlign).toBe("center");
  act(() => {
    editor.commands.setContent(toEditorContent(saved));
  });
  expect(editor.state.doc.child(0).attrs.textAlign).toBe("center");
});

it("inserts a row with the source table action and preserves text on undo", () => {
  setup(
    "<table><tbody><tr><td>A</td><td>B</td></tr><tr><td>C</td><td>D</td></tr></tbody></table>",
  );
  editor.commands.setTextSelection(4);
  render(
    <>
      <EditorContent editor={editor} />
      <TableAddRowColumnButton
        editor={editor}
        orientation="row"
        index={0}
        tablePos={0}
        side="below"
      />
    </>,
  );
  fireEvent.click(screen.getByRole("button", { name: "Insert row below" }));
  expect(editor.state.doc.child(0).childCount).toBe(3);
  act(() => {
    editor.commands.undo();
  });
  expect(editor.state.doc.child(0).childCount).toBe(2);
  expect(editor.state.doc.textContent).toBe("ABCD");
});

it("uses native highlighting and character counting without saving presentation decorations", () => {
  setup(
    '<pre><code class="language-js">const value = 1;</code></pre><p>中文 👋</p>',
  );
  render(
    <>
      <EditorContent editor={editor} />
      <CharacterCount editor={editor} />
    </>,
  );
  expect(editor.view.dom.querySelector(".hljs-keyword")).toHaveTextContent(
    "const",
  );
  expect(JSON.stringify(toArticleDocument(editor.getJSON()))).not.toContain(
    "hljs",
  );
  expect(editor.storage.characterCount.characters()).toBe(
    Array.from("constvalue=1;中文👋").length,
  );
});
