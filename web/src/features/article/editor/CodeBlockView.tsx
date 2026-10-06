import {
  NodeViewContent,
  NodeViewWrapper,
  type NodeViewProps,
} from "@tiptap/react";
import { CodeBlockCopy } from "../../../components/content/CodeBlockCopy";
import { ARTICLE_CODE_LANGUAGES } from "../../../components/content/articleContract";

/** The official CodeBlockLanguage example, restricted to the stored grammar. */
export function CodeBlockView({ node, updateAttributes }: NodeViewProps) {
  return (
    <NodeViewWrapper
      className="code-block"
      data-language={node.attrs.language ?? ""}
    >
      <div className="code-block-controls" contentEditable={false}>
        <select
          aria-label="代码语言"
          value={node.attrs.language ?? "null"}
          onChange={(event) =>
            updateAttributes({
              language:
                event.target.value === "null" ? null : event.target.value,
            })
          }
        >
          <option value="null">自动识别</option>
          <option disabled>—</option>
          {ARTICLE_CODE_LANGUAGES.map((language) => (
            <option key={language} value={language}>
              {language}
            </option>
          ))}
        </select>
        <CodeBlockCopy source={node.textContent} />
      </div>
      <pre>
        <NodeViewContent<"code"> as="code" />
      </pre>
    </NodeViewWrapper>
  );
}
