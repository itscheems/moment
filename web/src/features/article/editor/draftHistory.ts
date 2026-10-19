import { Extension, type Editor, type JSONContent } from "@tiptap/core";
import { EditorState } from "@tiptap/pm/state";

/** Draft fields share the editor's upstream history, but stay out of body JSON. */
export const DraftHistory = Extension.create({
  name: "draftHistory",
  addGlobalAttributes() {
    return [
      {
        types: ["doc"],
        attributes: {
          draftTitle: { default: "", rendered: false },
          draftCover: { default: null, rendered: false },
        },
      },
    ];
  },
});

export function updateDraftFields(
  editor: Editor,
  fields: { title?: string; coverMediaId?: string | null },
) {
  editor.commands.command(({ tr }) => {
    if (fields.title !== undefined)
      tr.setDocAttribute("draftTitle", fields.title);
    if (fields.coverMediaId !== undefined)
      tr.setDocAttribute("draftCover", fields.coverMediaId);
    return true;
  });
}

export function loadDraftContent(
  editor: Editor,
  body: JSONContent,
  title = "",
  coverMediaId: string | null = null,
) {
  editor.commands.setContent(
    { ...body, attrs: { draftTitle: title, draftCover: coverMediaId } },
    { emitUpdate: false },
  );
  // A different draft starts a fresh native history rather than undoing a load.
  const doc = editor.state.doc;
  editor.view.updateState(
    EditorState.create({
      schema: editor.schema,
      doc: doc.type.create(
        { ...doc.attrs, draftTitle: title, draftCover: coverMediaId },
        doc.content,
        doc.marks,
      ),
      plugins: editor.state.plugins,
    }),
  );
  editor.view.dispatch(
    editor.state.tr
      .setMeta("addToHistory", false)
      .setMeta("preventUpdate", true),
  );
}
