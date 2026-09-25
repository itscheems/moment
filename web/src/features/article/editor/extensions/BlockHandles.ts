import { Extension } from "@tiptap/core";
import {
  Plugin,
  PluginKey,
  TextSelection,
  type EditorState,
  type Transaction,
} from "@tiptap/pm/state";
import { Decoration, DecorationSet, type EditorView } from "@tiptap/pm/view";
import styles from "../Editor.module.css";

/** Move an entire top-level block, preserving its nested content and marks. */
export function moveArticleBlock(
  state: EditorState,
  from: number,
  target: number,
): Transaction | null {
  const block = state.doc.nodeAt(from);
  if (
    !block ||
    state.doc.resolve(from).depth !== 0 ||
    state.doc.resolve(target).depth !== 0 ||
    target === from ||
    target === from + block.nodeSize
  )
    return null;
  const tr = state.tr.delete(from, from + block.nodeSize);
  const at = tr.mapping.map(target);
  tr.insert(at, block);
  tr.setSelection(TextSelection.near(tr.doc.resolve(at)));
  return tr.scrollIntoView();
}

const key = new PluginKey<{ source: number | null; target: number | null }>(
  "articleBlockHandles",
);
export const BlockHandles = Extension.create({
  name: "articleBlockHandles",
  addProseMirrorPlugins() {
    const editor = this.editor;
    let cancelDrag: (() => void) | null = null;
    const reset = (view: EditorView) =>
      view.dispatch(view.state.tr.setMeta(key, { source: null, target: null }));
    const boundaryAt = (
      view: EditorView,
      event: { clientX: number; clientY: number },
    ) => {
      const found = view.posAtCoords({
        left: event.clientX,
        top: event.clientY,
      });
      if (!found) return view.state.doc.content.size;
      const pos = view.state.doc.resolve(found.pos);
      if (!pos.depth) return pos.pos;
      const start = pos.before(1);
      const dom = view.nodeDOM(start);
      const rect =
        dom instanceof HTMLElement ? dom.getBoundingClientRect() : null;
      return rect && event.clientY > (rect.top + rect.bottom) / 2
        ? pos.after(1)
        : start;
    };
    return [
      new Plugin<{ source: number | null; target: number | null }>({
        key,
        state: {
          init: () => ({ source: null, target: null }),
          apply(tr, previous) {
            const next = tr.getMeta(key) as
              { source: number | null; target: number | null } | undefined;
            if (next) return next;
            return {
              source:
                previous.source === null
                  ? null
                  : tr.mapping.map(previous.source),
              target:
                previous.target === null
                  ? null
                  : tr.mapping.map(previous.target),
            };
          },
        },
        view: () => ({ destroy: () => cancelDrag?.() }),
        props: {
          decorations(state) {
            if (!editor.isEditable) return DecorationSet.empty;
            const decorations: Decoration[] = [];
            state.doc.forEach((block, pos, index) => {
              decorations.push(
                Decoration.widget(
                  pos,
                  (view) => {
                    const wrap = document.createElement("span");
                    wrap.className = styles.blockHandle;
                    wrap.dataset.contentDecoration = "true";
                    wrap.contentEditable = "false";
                    const handle = document.createElement("button");
                    handle.type = "button";
                    handle.textContent = "⠿";
                    handle.draggable = false;
                    handle.setAttribute("aria-label", `移动第 ${index + 1} 块`);
                    handle.title = "拖动排序，点击打开块菜单；Alt + ↑ / ↓ 移动";
                    handle.setAttribute("aria-expanded", "false");
                    const controls = document.createElement("span");
                    controls.className = styles.blockMoveActions;
                    controls.hidden = true;
                    const move = (direction: -1 | 1) => {
                      const current = view.state.doc.resolve(pos);
                      const target =
                        direction === -1
                          ? pos - (current.nodeBefore?.nodeSize ?? 0)
                          : pos +
                            block.nodeSize +
                            (view.state.doc.nodeAt(pos + block.nodeSize)
                              ?.nodeSize ?? 0);
                      const tr = moveArticleBlock(view.state, pos, target);
                      if (tr) {
                        view.dispatch(tr);
                        view.focus();
                      }
                    };
                    for (const [direction, label] of [
                      [-1, "上移"],
                      [1, "下移"],
                    ] as const) {
                      const button = document.createElement("button");
                      button.type = "button";
                      button.textContent = label;
                      button.disabled =
                        direction === -1
                          ? index === 0
                          : index === state.doc.childCount - 1;
                      button.onclick = () => move(direction);
                      controls.append(button);
                    }
                    const actOnBlock = (label: string, action: () => void) => {
                      const button = document.createElement("button");
                      button.type = "button";
                      button.textContent = label;
                      button.onclick = () => {
                        action();
                        controls.hidden = true;
                        handle.setAttribute("aria-expanded", "false");
                      };
                      controls.append(button);
                    };
                    actOnBlock("在下方插入", () => {
                      const at = pos + block.nodeSize;
                      const paragraph =
                        view.state.schema.nodes.paragraph.create(
                          null,
                          view.state.schema.text("/"),
                        );
                      const tr = view.state.tr.insert(at, paragraph);
                      tr.setSelection(TextSelection.create(tr.doc, at + 2));
                      view.dispatch(tr.scrollIntoView());
                      view.focus();
                    });
                    actOnBlock("复制块", () => {
                      const tr = view.state.tr.insert(
                        pos + block.nodeSize,
                        view.state.doc.nodeAt(pos)!,
                      );
                      view.dispatch(tr.scrollIntoView());
                      view.focus();
                    });
                    actOnBlock("删除块", () => {
                      view.dispatch(
                        view.state.tr
                          .delete(pos, pos + block.nodeSize)
                          .scrollIntoView(),
                      );
                      view.focus();
                    });
                    wrap.onkeydown = (event) => {
                      if (event.key === "Escape") {
                        event.preventDefault();
                        controls.hidden = true;
                        handle.setAttribute("aria-expanded", "false");
                        handle.focus();
                      }
                    };
                    wrap.addEventListener("focusout", (event) => {
                      if (!wrap.contains(event.relatedTarget as Node | null)) {
                        controls.hidden = true;
                        handle.setAttribute("aria-expanded", "false");
                      }
                    });
                    let suppressClick = false;
                    handle.onclick = () => {
                      if (suppressClick) {
                        suppressClick = false;
                        return;
                      }
                      controls.hidden = !controls.hidden;
                      handle.setAttribute(
                        "aria-expanded",
                        String(!controls.hidden),
                      );
                    };
                    handle.onkeydown = (event) => {
                      if (
                        event.altKey &&
                        (event.key === "ArrowUp" || event.key === "ArrowDown")
                      ) {
                        event.preventDefault();
                        move(event.key === "ArrowUp" ? -1 : 1);
                      }
                      if (event.key === "Escape") {
                        controls.hidden = true;
                        handle.setAttribute("aria-expanded", "false");
                        view.focus();
                      }
                    };
                    handle.onpointerdown = (event) => {
                      if (event.button !== 0) return;
                      cancelDrag?.();
                      const start = { x: event.clientX, y: event.clientY };
                      let moved = false;
                      const update = (next: PointerEvent) => {
                        if (next.pointerId !== event.pointerId) return;
                        if (
                          !moved &&
                          Math.hypot(
                            next.clientX - start.x,
                            next.clientY - start.y,
                          ) < 5
                        )
                          return;
                        moved = true;
                        next.preventDefault();
                        const dragging = key.getState(view.state);
                        view.dispatch(
                          view.state.tr.setMeta(key, {
                            source: dragging?.source ?? pos,
                            target: boundaryAt(view, next),
                          }),
                        );
                      };
                      const clean = () => {
                        document.removeEventListener("pointermove", update);
                        document.removeEventListener("pointerup", finish);
                        document.removeEventListener("pointercancel", cancel);
                        cancelDrag = null;
                      };
                      const cancel = () => {
                        clean();
                        if (!editor.isDestroyed) reset(view);
                      };
                      const finish = (next: PointerEvent) => {
                        if (next.pointerId !== event.pointerId) return;
                        clean();
                        if (!moved || editor.isDestroyed) return;
                        suppressClick = true;
                        const dragging = key.getState(view.state);
                        const bounds = view.dom.getBoundingClientRect();
                        if (
                          dragging?.source == null ||
                          next.clientX < bounds.left - 32 ||
                          next.clientX > bounds.right ||
                          next.clientY < bounds.top ||
                          next.clientY > bounds.bottom + 32
                        ) {
                          reset(view);
                          return;
                        }
                        const tr = moveArticleBlock(
                          view.state,
                          dragging.source,
                          boundaryAt(view, next),
                        );
                        if (tr)
                          view.dispatch(
                            tr.setMeta(key, { source: null, target: null }),
                          );
                        else reset(view);
                        view.focus();
                      };
                      cancelDrag = clean;
                      document.addEventListener("pointermove", update, {
                        passive: false,
                      });
                      document.addEventListener("pointerup", finish);
                      document.addEventListener("pointercancel", cancel);
                    };
                    wrap.append(handle, controls);
                    return wrap;
                  },
                  {
                    side: -1,
                    key: `block-${pos}-${block.nodeSize}-${index}-${state.doc.childCount}`,
                    stopEvent: () => true,
                  },
                ),
              );
            });
            const target = key.getState(state)?.target;
            if (target !== null && target !== undefined)
              decorations.push(
                Decoration.widget(
                  target,
                  () => {
                    const line = document.createElement("span");
                    line.className = styles.blockDropLine;
                    line.setAttribute("aria-hidden", "true");
                    return line;
                  },
                  { side: -2, key: "drop-line" },
                ),
              );
            return DecorationSet.create(state.doc, decorations);
          },
        },
      }),
    ];
  },
});
