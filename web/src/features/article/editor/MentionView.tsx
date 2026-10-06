import { NodeViewWrapper, type NodeViewProps } from "@tiptap/react";
import { MentionLink } from "../../../components/content/MentionLink";

/** Native mention atoms resolve through the same account cache as readers. */
export function MentionView({ node }: NodeViewProps) {
  const handle = String(node.attrs.id ?? node.attrs.label ?? "");
  return (
    <NodeViewWrapper as="span" data-type="mention" contentEditable={false}>
      <MentionLink handle={handle}>@{node.attrs.label ?? handle}</MentionLink>
    </NodeViewWrapper>
  );
}
