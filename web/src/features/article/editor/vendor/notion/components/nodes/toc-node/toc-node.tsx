import { NodeViewWrapper, type NodeViewProps } from "@tiptap/react";
import { TocContent } from "@content-notion/components/nodes/toc-node/toc-content";
export function TocNodeComponent({
  node,
  extension,
  HTMLAttributes,
}: NodeViewProps) {
  return (
    <TocContent
      wrapper={NodeViewWrapper}
      attrs={node.attrs}
      options={extension.options}
      HTMLAttributes={HTMLAttributes}
    />
  );
}
export default TocNodeComponent;
