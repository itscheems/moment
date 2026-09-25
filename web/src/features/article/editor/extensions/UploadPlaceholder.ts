import { Extension, type Editor } from "@tiptap/core";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";

import styles from "../Editor.module.css";

const key = new PluginKey<DecorationSet>("articleUploads");
type Action =
  | { add: { id: string; pos: number; element: HTMLElement } }
  | { remove: string }
  | { clear: true };

/** Local decorations follow edits, but never become persisted document nodes. */
export const UploadPlaceholder = Extension.create({
  name: "articleUploads",
  addProseMirrorPlugins() {
    return [
      new Plugin<DecorationSet>({
        key,
        state: {
          init: () => DecorationSet.empty,
          apply(tr, previous) {
            let decorations = previous.map(tr.mapping, tr.doc);
            const action = tr.getMeta(key) as Action | undefined;
            if (action && "clear" in action) return DecorationSet.empty;
            if (action && "add" in action) {
              const { id, pos, element } = action.add;
              decorations = decorations.add(tr.doc, [
                Decoration.widget(pos, element, { id, side: -1, key: id }),
              ]);
            }
            if (action && "remove" in action) {
              decorations = decorations.remove(
                decorations.find(
                  undefined,
                  undefined,
                  (spec) => spec.id === action.remove,
                ),
              );
            }
            return decorations;
          },
        },
        props: { decorations: (state) => key.getState(state) },
      }),
    ];
  },
});

export function addUploadPlaceholder(
  editor: Editor,
  id: string,
  name: string,
  previewUrl: string,
): HTMLElement {
  const element = document.createElement("div");
  element.className = styles.uploadPlaceholder;
  element.contentEditable = "false";
  element.setAttribute("role", "status");
  const image = document.createElement("img");
  image.src = previewUrl;
  image.alt = "";
  const label = document.createElement("span");
  label.textContent = `正在上传 ${name}`;
  element.append(image, label);
  const { $from } = editor.state.selection;
  // Images belong at the top level, even when the caret is in a list or cell.
  const pos = $from.depth ? $from.after(1) : $from.pos;
  editor.view.dispatch(
    editor.state.tr.setMeta(key, {
      add: { id, pos, element },
    } satisfies Action),
  );
  return element;
}

export function removeUploadPlaceholder(editor: Editor, id: string) {
  if (!editor.isDestroyed)
    editor.view.dispatch(
      editor.state.tr.setMeta(key, { remove: id } satisfies Action),
    );
}

export function clearUploadPlaceholders(editor: Editor) {
  if (!editor.isDestroyed)
    editor.view.dispatch(
      editor.state.tr.setMeta(key, { clear: true } satisfies Action),
    );
}

export function finishImageUpload(
  editor: Editor,
  id: string,
  mediaId: string,
): boolean {
  if (editor.isDestroyed) return false;
  const decoration = key
    .getState(editor.state)
    ?.find(undefined, undefined, (spec) => spec.id === id)[0];
  if (!decoration) return false; // Deleted anchor or a different draft: never append elsewhere.
  const image = editor.schema.nodes.image.create({ media_id: mediaId });
  editor.view.dispatch(
    editor.state.tr
      .insert(decoration.from, image)
      .setMeta(key, { remove: id } satisfies Action),
  );
  return true;
}
