import { Extension, type Editor } from "@tiptap/core";
import {
  TextSelection,
  type EditorState,
  type Transaction,
} from "@tiptap/pm/state";

/** Move an entire top-level block, preserving its nested content and marks. */
export function moveArticleBlock(
  state: EditorState,
  from: number,
  target: number,
): Transaction | null {
  const block = state.doc.nodeAt(from);
  if (
    !block ||
    state.doc.resolve(from).depth !== 0 ||
    state.doc.resolve(target).depth !== 0 ||
    target === from ||
    target === from + block.nodeSize
  )
    return null;
  const tr = state.tr.delete(from, from + block.nodeSize);
  const at = tr.mapping.map(target);
  tr.insert(at, block);
  tr.setSelection(TextSelection.near(tr.doc.resolve(at)));
  return tr.scrollIntoView();
}

export function selectedBlockPosition(editor: Editor): number {
  const { $from } = editor.state.selection;
  return $from.depth ? $from.before(1) : $from.pos;
}

export function moveSelectedBlock(
  editor: Editor,
  direction: -1 | 1,
  pos = selectedBlockPosition(editor),
): boolean {
  const block = editor.state.doc.nodeAt(pos);
  if (!block) return false;
  const target =
    direction === -1
      ? pos - (editor.state.doc.resolve(pos).nodeBefore?.nodeSize ?? 0)
      : pos +
        block.nodeSize +
        (editor.state.doc.nodeAt(pos + block.nodeSize)?.nodeSize ?? 0);
  const tr = moveArticleBlock(editor.state, pos, target);
  if (!tr) return false;
  editor.view.dispatch(tr);
  editor.view.focus();
  return true;
}

/** Keyboard fallback; pointer targeting and dragging belong to DragHandle. */
export const BlockHandles = Extension.create({
  name: "articleBlockKeyboard",
  addKeyboardShortcuts() {
    return {
      "Alt-ArrowUp": () => moveSelectedBlock(this.editor, -1),
      "Alt-ArrowDown": () => moveSelectedBlock(this.editor, 1),
    };
  },
});
