import { Extension } from "@tiptap/core";
import { closeHistory } from "@tiptap/pm/history";
import { Plugin, TextSelection } from "@tiptap/pm/state";

const media = new Set(["image", "mapCard"]);

/** Make the whitespace beside media blocks a writing target, without adding
 * empty paragraphs to the stored document until the author actually clicks. */
export const BlockGap = Extension.create({
  name: "blockGap",
  addProseMirrorPlugins() {
    return [
      new Plugin({
        props: {
          handleDOMEvents: {
            mousedown(view, event) {
              if (
                !view.editable ||
                event.button !== 0 ||
                event.shiftKey ||
                event.metaKey ||
                event.ctrlKey ||
                event.altKey
              )
                return false;
              if (
                !(event.target instanceof Element) ||
                event.target.closest(
                  "button, input, textarea, a, [role=button], p, h1, h2, h3, pre, li, .tiptap-image-content, .tiptap-image-caption",
                )
              )
                return false;
              const rect = view.dom.getBoundingClientRect();
              const padding = getComputedStyle(view.dom);
              if (
                event.clientX < rect.left + parseFloat(padding.paddingLeft) ||
                event.clientX > rect.right - parseFloat(padding.paddingRight)
              )
                return false;
              const blocks: {
                node: typeof view.state.doc;
                pos: number;
                rect: DOMRect;
              }[] = [];
              view.state.doc.forEach((node, pos) => {
                const element = view.nodeDOM(pos);
                if (element instanceof HTMLElement)
                  blocks.push({
                    node,
                    pos,
                    rect: element.getBoundingClientRect(),
                  });
              });
              for (let index = 0; index <= blocks.length; index += 1) {
                const before = blocks[index - 1];
                const after = blocks[index];
                if (
                  !media.has(before?.node.type.name) &&
                  !media.has(after?.node.type.name)
                )
                  continue;
                const top = before
                  ? before.rect.bottom - 6
                  : after.rect.top - 18;
                const bottom = after
                  ? after.rect.top + 6
                  : before.rect.bottom + 18;
                if (event.clientY < top || event.clientY > bottom) continue;
                const empty = [before, after].find(
                  (block) =>
                    block?.node.type.name === "paragraph" &&
                    block.node.content.size === 0,
                );
                const pos = empty
                  ? empty.pos
                  : after
                    ? after.pos
                    : view.state.doc.content.size;
                const tr = closeHistory(view.state.tr);
                if (!empty)
                  tr.insert(pos, view.state.schema.nodes.paragraph.create());
                tr.setSelection(TextSelection.create(tr.doc, pos + 1));
                event.preventDefault();
                view.dispatch(tr.scrollIntoView());
                view.focus();
                return true;
              }
              return false;
            },
          },
        },
      }),
    ];
  },
});
