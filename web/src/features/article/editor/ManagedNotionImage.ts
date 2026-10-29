import { createElement } from "react";
import { ReactNodeViewRenderer, type NodeViewProps } from "@tiptap/react";
import { ManagedImage } from "../../../components/content/ManagedImage";
import { ImageNodeView } from "@notion/components/nodes/image-node/image-node-view";
import { mediaPreviewUrl } from "../../../api/mediaUrls";

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
    return ReactNodeViewRenderer(ManagedImageView);
  },
});
