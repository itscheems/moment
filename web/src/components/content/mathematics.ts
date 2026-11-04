import { InlineMath, BlockMath } from "@tiptap/extension-mathematics";
import type { Node } from "@tiptap/pm/model";
import type { KatexOptions } from "katex";
import "katex/dist/katex.min.css";

export const mathRenderOptions: KatexOptions = {
  throwOnError: false,
  trust: false,
  maxExpand: 1000,
  maxSize: 20,
};

export type MathClickHandler = (node: Node, pos: number) => void;

/** Native node views, commands and input rules; only host configuration lives here. */
export function articleMathExtensions(onClick?: MathClickHandler) {
  return [
    InlineMath.extend({
      marks: "",
      renderText: ({ node }) => node.attrs.latex,
    }).configure({
      onClick,
      katexOptions: { ...mathRenderOptions, displayMode: false },
    }),
    BlockMath.extend({ renderText: ({ node }) => node.attrs.latex }).configure({
      onClick,
      katexOptions: { ...mathRenderOptions, displayMode: true },
    }),
  ];
}
