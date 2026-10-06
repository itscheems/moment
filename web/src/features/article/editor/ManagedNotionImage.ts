import { createElement } from "react";
import { ReactNodeViewRenderer, type NodeViewProps } from "@tiptap/react";
import { ManagedImage } from "../../../components/content/ManagedImage";
import { ImageNodeView } from "@notion/components/nodes/image-node/image-node-view";

function ManagedImageView(props: NodeViewProps) {
  return createElement(ImageNodeView, {
    ...props,
    // Keep Moment's delivery attributes when the static image becomes a view.
    imageAttributes: { loading: "lazy", decoding: "async" },
  });
}

export const ManagedNotionImage = ManagedImage.extend({
  addNodeView() {
    return ReactNodeViewRenderer(ManagedImageView);
  },
});
