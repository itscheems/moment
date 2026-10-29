import { createElement } from "react";
import { ReactNodeViewRenderer, type NodeViewProps } from "@tiptap/react";
import { ManagedImage } from "../../../components/content/ManagedImage";
import { ImageNodeView } from "@notion/components/nodes/image-node/image-node-view";
import { mediaPreviewUrl } from "../../../api/mediaUrls";
import { NodeSelection, Plugin, TextSelection } from "@tiptap/pm/state";
import { closeHistory } from "@tiptap/pm/history";

function ManagedImageView(props: NodeViewProps) {
  return createElement(ImageNodeView, {
    ...props,
    node: props.node.attrs.media_id
      ? props.node.type.create(
          {
            ...props.node.attrs,
            src: mediaPreviewUrl(props.node.attrs.media_id),
          },
          props.node.content,
          props.node.marks,
        )
      : props.node,
    // Keep Moment's delivery attributes when the static image becomes a view.
    imageAttributes: { loading: "lazy", decoding: "async" },
  });
}

export const ManagedNotionImage = ManagedImage.extend({
  addNodeView() {
    const render = ReactNodeViewRenderer(ManagedImageView, {
      attrs: { contenteditable: "false" },
    });
    return (props) => {
      const nodeView = render(props);
      // contentDOM can be created after the first React layout effect.
      nodeView.contentDOM?.setAttribute(
        "contenteditable",
        String(
          props.editor.isEditable &&
            (props.node.attrs.showCaption || props.node.content.size > 0),
        ),
      );
      return nodeView;
    };
  },

  addKeyboardShortcuts() {
    return {
      ...this.parent?.(),
      // Inline caption content does not make the media node a splittable paragraph.
      Enter: ({ editor }) => {
        const { selection, doc, schema } = editor.state;
        const { $from, $to } = selection;
        const selectedImage =
          selection instanceof NodeSelection &&
          selection.node.type === this.type;
        const caption =
          selection instanceof TextSelection &&
          $from.sameParent($to) &&
          $from.parent.type === this.type;
        if (!selectedImage && !caption) return false;
        const after = selectedImage ? selection.to : $from.after();
        const $after = doc.resolve(after);
        const paragraph = schema.nodes.paragraph;
        const tr = closeHistory(editor.state.tr);
        if ($after.nodeAfter?.type !== paragraph) {
          if (
            !$after.parent.canReplaceWith(
              $after.index(),
              $after.index(),
              paragraph,
            )
          )
            return false;
          tr.insert(after, paragraph.create());
        }
        tr.setSelection(TextSelection.create(tr.doc, after + 1));
        editor.view.dispatch(tr.scrollIntoView());
        editor.view.focus();
        return true;
      },
    };
  },

  addProseMirrorPlugins() {
    return [
      ...(this.parent?.() ?? []),
      new Plugin({
        // Pointer and arrow navigation share the same visible selection boundary.
        appendTransaction: (_transactions, _oldState, state) => {
          const { selection } = state;
          if (!(selection instanceof TextSelection) || !selection.empty)
            return null;
          const { $from } = selection;
          const image = $from.parent;
          if (
            image.type !== this.type ||
            image.attrs.showCaption ||
            image.content.size > 0
          )
            return null;
          return state.tr
            .setSelection(NodeSelection.create(state.doc, $from.before()))
            .setMeta("addToHistory", false);
        },
      }),
    ];
  },
});
