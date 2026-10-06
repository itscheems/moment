import {
  NodeViewContent,
  NodeViewWrapper,
  type NodeViewProps,
} from "@tiptap/react";
import { CodeBlockCopy } from "../../../components/content/CodeBlockCopy";
/** Native code content with the shared reader/editor copy control. */
export function CodeBlockView({ node }: NodeViewProps) {
  return (
    <NodeViewWrapper
      className="code-block"
      data-language={node.attrs.language ?? ""}
    >
      <div contentEditable={false}>
        <CodeBlockCopy source={node.textContent} />
      </div>
      <NodeViewContent<"pre"> as="pre" style={{ whiteSpace: "pre" }} />
    </NodeViewWrapper>
  );
}
