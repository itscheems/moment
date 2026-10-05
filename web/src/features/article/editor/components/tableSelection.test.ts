import { Editor } from "@tiptap/core";
import { CellSelection, TableMap } from "@tiptap/pm/tables";
import { afterEach, expect, it } from "vitest";
import { articleEditorExtensions } from "../schema";
import { toArticleDocument } from "../document";
import { extendTable, selectTableAxis, tableCellAt } from "./tableSelection";

let editor: Editor;
afterEach(() => editor?.destroy());
const cell = (text: string, colspan = 1) => ({
  type: "tableCell",
  attrs: { colspan },
  content: [{ type: "paragraph", content: [{ type: "text", text }] }],
});
function setup() {
  editor = new Editor({
    extensions: articleEditorExtensions(),
    content: {
      type: "doc",
      content: [
        {
          type: "table",
          content: [
            { type: "tableRow", content: [cell("merged", 2), cell("right")] },
            { type: "tableRow", content: [cell("a"), cell("b"), cell("c")] },
          ],
        },
      ],
    },
  });
  const table = editor.state.doc.firstChild!;
  return { table, map: TableMap.get(table) };
}

it("resolves positions within a cell and selects an entire logical column through a merged cell", () => {
  const { map } = setup();
  const lowerMiddle = 1 + map.map[4];
  expect(tableCellAt(editor, lowerMiddle + 2)?.cell).toBe(lowerMiddle);
  expect(tableCellAt(editor, 0)).toBeNull();
  selectTableAxis(editor, lowerMiddle, "column");
  const selection = editor.state.selection as CellSelection;
  expect(selection).toBeInstanceOf(CellSelection);
  expect(selection.isColSelection()).toBe(true);
  const selected: string[] = [];
  selection.forEachCell((node) => selected.push(node.textContent));
  expect(selected).toContain("merged");
  expect(selected).toContain("b");
});

it("row deletion preserves the other row and undo restores the exact stored document", () => {
  const { map } = setup();
  const original = toArticleDocument(editor.getJSON());
  selectTableAxis(editor, 1 + map.map[3], "row");
  expect((editor.state.selection as CellSelection).isRowSelection()).toBe(true);
  editor.commands.deleteRow();
  expect(editor.state.doc.firstChild?.childCount).toBe(1);
  expect(editor.state.doc.textContent).toBe("mergedright");
  editor.commands.undo();
  expect(toArticleDocument(editor.getJSON())).toEqual(original);
});

it.each(["row", "column"] as const)(
  "the %s extend button appends at the table edge even from the first cell",
  (axis) => {
    const { map } = setup();
    const original = toArticleDocument(editor.getJSON());
    extendTable(editor, 1 + map.map[0], axis);
    const next = TableMap.get(editor.state.doc.firstChild!);
    expect([next.height, next.width]).toEqual(axis === "row" ? [3, 3] : [2, 4]);
    expect(editor.state.doc.textContent).toBe("mergedrightabc");
    expect(() => toArticleDocument(editor.getJSON())).not.toThrow();
    editor.commands.undo();
    expect(toArticleDocument(editor.getJSON())).toEqual(original);
  },
);
