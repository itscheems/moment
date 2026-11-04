import "@testing-library/jest-dom/vitest";
import { Editor } from "@tiptap/core";
import { closeHistory } from "@tiptap/pm/history";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { articleEditorExtensions } from "./schema";
import {
  toArticleDocument,
  toEditorContent,
  hasArticleSubstance,
} from "./document";
import { MathFormulaDialog } from "./MathFormulaDialog";
import { parseArticleDocument } from "../../../api/public";
import { contentExtensions } from "../../../components/content/schema";
import { MathContent } from "../../../components/content/MathContent";

let editor: Editor;
afterEach(() => {
  cleanup();
  editor?.destroy();
});

it("retains both formula kinds across saving, rich clipboard HTML and reading", () => {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: {
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [{ type: "inlineMath", attrs: { latex: "a_1^2" } }],
        },
        {
          type: "blockMath",
          attrs: { latex: String.raw`\begin{pmatrix}1&2\\3&4\end{pmatrix}` },
        },
      ],
    },
  });
  const saved = toArticleDocument(editor.getJSON());
  expect(hasArticleSubstance(saved)).toBe(true);
  expect(saved.schema_version).toBe(4);
  const html = editor.getHTML();
  editor.commands.setContent(html);
  expect(toArticleDocument(editor.getJSON())).toEqual(saved);
  const reader = new Editor({
    extensions: contentExtensions.article,
    content: toEditorContent(saved),
    editable: false,
  });
  expect(reader.view.dom.querySelectorAll(".katex")).toHaveLength(2);
  expect(
    reader.view.dom.querySelector('[data-type="block-math"] .katex-display'),
  ).not.toBeNull();
  reader.destroy();
  render(<MathContent node={editor.state.doc.child(1)} />);
  expect(document.querySelector(".katex-display")).not.toBeNull();
  expect(() => parseArticleDocument({ ...saved, schema_version: 3 })).toThrow();
});

it("uses native update and delete commands with undo while preserving surrounding text", () => {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: "<p>before after</p>",
  });
  editor.commands.insertInlineMath({ latex: "x", pos: 7 });
  const inserted = toArticleDocument(editor.getJSON());
  editor.view.dispatch(closeHistory(editor.state.tr));
  editor.commands.updateInlineMath({ latex: String.raw`\frac{1}{2}`, pos: 7 });
  expect(editor.state.doc.nodeAt(7)?.attrs.latex).toBe(String.raw`\frac{1}{2}`);
  expect(editor.commands.undo()).toBe(true);
  expect(toArticleDocument(editor.getJSON())).toEqual(inserted);
  editor.commands.deleteInlineMath({ pos: 7 });
  expect(editor.getText()).toBe("before after");
});

it("keeps invalid source recoverable and refuses trusted HTML commands", () => {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: {
      type: "doc",
      content: [
        { type: "blockMath", attrs: { latex: String.raw`\badCommand{` } },
        {
          type: "paragraph",
          content: [
            {
              type: "inlineMath",
              attrs: { latex: String.raw`\href{javascript:alert(1)}{x}` },
            },
          ],
        },
      ],
    },
  });
  expect(toArticleDocument(editor.getJSON()).content[0].attrs?.latex).toBe(
    String.raw`\badCommand{`,
  );
  expect(editor.view.dom.querySelector('a[href^="javascript:"]')).toBeNull();
});

it("previews input, rejects malformed formulas and leaves cancellation unchanged", async () => {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: "<p>unchanged</p>",
  });
  editor.commands.setContent(editor.getJSON());
  const onClose = vi.fn();
  render(
    <MathFormulaDialog
      editor={editor}
      target={{ type: "blockMath", latex: "" }}
      onClose={onClose}
    />,
  );
  const input = await screen.findByRole("textbox", { name: "LaTeX 源码" });
  const before = editor.getJSON();
  fireEvent.change(input, { target: { value: String.raw`\frac{` } });
  expect(screen.getByRole("button", { name: /^插入$/ })).toBeDisabled();
  expect(screen.getByRole("alert")).toBeVisible();
  fireEvent.change(input, { target: { value: String.raw`\frac{1}{2}` } });
  expect(
    screen.getByLabelText("公式预览").querySelector(".katex"),
  ).not.toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(onClose).toHaveBeenCalled();
  expect(editor.getJSON()).toEqual(before);
});
