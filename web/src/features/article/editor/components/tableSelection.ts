import type { Editor } from "@tiptap/core";
import { CellSelection, TableMap } from "@tiptap/pm/tables";

export type TableAxis = "row" | "column";

/** Resolve any position inside a cell through the native schema, including lists. */
export function tableCellAt(editor: Editor, position: number) {
  const $pos = editor.state.doc.resolve(position);
  for (let depth = $pos.depth; depth > 0; depth--) {
    const role = $pos.node(depth).type.spec.tableRole;
    if (role !== "cell" && role !== "header_cell") continue;
    const table = $pos.node(depth - 2);
    return { cell: $pos.before(depth), table, start: $pos.start(depth - 2) };
  }
  return null;
}

/** Native CellSelection expands through merged cells instead of counting DOM columns. */
export function selectTableAxis(editor: Editor, cell: number, axis: TableAxis) {
  const $cell = editor.state.doc.resolve(cell);
  const selection =
    axis === "row"
      ? CellSelection.rowSelection($cell)
      : CellSelection.colSelection($cell);
  editor.view.dispatch(editor.state.tr.setSelection(selection));
}

export function extendTable(editor: Editor, cell: number, axis: TableAxis) {
  const context = tableCellAt(editor, cell + 1);
  if (!context) return;
  const map = TableMap.get(context.table);
  const last = context.start + map.map[map.map.length - 1];
  selectTableAxis(editor, last, axis);
  const chain = editor.chain().focus();
  if (axis === "row") chain.addRowAfter().run();
  else chain.addColumnAfter().run();
}
