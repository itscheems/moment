import { Extension } from "@tiptap/core";
import type { Node as DocumentNode } from "@tiptap/pm/model";
import { Plugin, PluginKey } from "@tiptap/pm/state";
import { Decoration, DecorationSet } from "@tiptap/pm/view";
import { highlightSource } from "../../../../components/codeHighlight";

function highlight(doc: DocumentNode): DecorationSet {
  const decorations: Decoration[] = [];
  doc.descendants((node, pos) => {
    if (node.type.name !== "codeBlock") return;
    const html = highlightSource(node.textContent, node.attrs.language ?? "");
    if (!html) return false;
    const parsed = new DOMParser().parseFromString(html, "text/html").body;
    let offset = pos + 1;
    const visit = (part: Node) => {
      if (part.nodeType === Node.TEXT_NODE) {
        offset += part.textContent?.length ?? 0;
        return;
      }
      const start = offset;
      part.childNodes.forEach(visit);
      const className = (part as Element).getAttribute("class");
      if (className && offset > start)
        decorations.push(
          Decoration.inline(start, offset, { class: className }),
        );
    };
    parsed.childNodes.forEach(visit);
    return false;
  });
  return DecorationSet.create(doc, decorations);
}

/** Syntax colour is a decoration, never a mark in the saved document. */
export const CodeHighlight = Extension.create({
  name: "articleCodeHighlight",
  addProseMirrorPlugins() {
    const key = new PluginKey<DecorationSet>("articleCodeHighlight");
    return [
      new Plugin<DecorationSet>({
        key,
        state: {
          init: (_, state) => highlight(state.doc),
          apply: (tr, previous) =>
            tr.docChanged ? highlight(tr.doc) : previous,
        },
        props: { decorations: (state) => key.getState(state) },
      }),
    ];
  },
});
