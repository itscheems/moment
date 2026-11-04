import katex from "katex";
import type { Node } from "@tiptap/pm/model";
import { mathRenderOptions } from "./mathematics";

/** The static reading surface uses the same renderer and options as native node views. */
export function MathContent({ node }: { node: Node }) {
  const block = node.type.name === "blockMath";
  const Tag = block ? "div" : "span";
  return (
    <Tag
      className="tiptap-mathematics-render"
      data-type={block ? "block-math" : "inline-math"}
      data-latex={node.attrs.latex}
      dangerouslySetInnerHTML={{
        __html: katex.renderToString(node.attrs.latex, {
          ...mathRenderOptions,
          displayMode: block,
        }),
      }}
    />
  );
}
