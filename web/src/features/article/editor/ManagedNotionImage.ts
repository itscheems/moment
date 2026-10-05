import { ReactNodeViewRenderer } from "@tiptap/react";
import { ManagedImage } from "../../../components/content/ManagedImage";
import { ImageNodeView } from "@notion/components/nodes/image-node/image-node-view";

export const ManagedNotionImage = ManagedImage.extend({
  addNodeView() {
    return ReactNodeViewRenderer(ImageNodeView);
  },
});
