import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import {
  render,
  screen,
  fireEvent,
  cleanup,
  act,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { articleEditorExtensions } from "./schema";
import { LinkContent } from "@notion/components/ui/link-popover";

let editor: Editor;
afterEach(() => {
  cleanup();
  editor?.destroy();
});
it("updates an existing link URL without replacing its label and can undo it", async () => {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content:
      '<p><a href="https://example.com/before">A named reference</a></p>',
  });
  await new Promise<void>((resolve) => editor.on("create", () => resolve()));
  editor.commands.setTextSelection(4);
  const original = editor.getJSON();
  render(<LinkContent editor={editor} />);
  const input = await screen.findByPlaceholderText("粘贴链接…");
  fireEvent.change(input, { target: { value: "https://example.com/after" } });
  fireEvent.keyDown(input, { key: "Enter" });
  expect(editor.getText()).toBe("A named reference");
  expect(editor.getAttributes("link").href).toBe("https://example.com/after");
  await act(async () => {
    editor.commands.undo();
  });
  expect(editor.getJSON()).toEqual(original);
});
