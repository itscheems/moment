import type { Editor } from "@tiptap/core";
import { NodeSelection } from "@tiptap/pm/state";
export const HIDE_FLOATING_META = "hideFloatingToolbar";
export const selectNodeAndHideFloating = (editor: Editor, pos: number) => {
  if (!editor) return;
  const { state, view } = editor;
  view.dispatch(
    state.tr
      .setSelection(NodeSelection.create(state.doc, pos))
      .setMeta(HIDE_FLOATING_META, true),
  );
};
