import { Extension } from "@tiptap/core";
import { isNodeRangeSelection } from "@tiptap/extension-node-range";
import type { Node } from "@tiptap/pm/model";
import { NodeSelection, Plugin } from "@tiptap/pm/state";
import { dropPoint } from "@tiptap/pm/transform";
import { animate, type AnimationPlaybackControlsWithThen } from "motion";

let nextScope = 0;

/** Visual adapter for upstream dragging. Animate outside the editable DOM:
 * changing node styles during a native drag makes its DOMObserver reread the
 * browser caret, which can replace a block selection with a caption caret. */
export const BlockMotion = Extension.create({
  name: "blockMotion",
  addProseMirrorPlugins() {
    const scope = `article-block-motion-${nextScope++}`;
    return [
      new Plugin({
        props: { attributes: { "data-block-motion": scope } },
        view(view) {
          const doc = view.dom.ownerDocument;
          const reducedMotion = doc.defaultView!.matchMedia(
            "(prefers-reduced-motion: reduce)",
          );
          const stylesheet = doc.createElement("style");
          doc.head.append(stylesheet);
          let animation: AnimationPlaybackControlsWithThen | undefined;
          let preview: HTMLElement | null = null;
          let source: { from: number; to: number } | null = null;
          let sourceStyles = "";
          let target: number | null = null;
          let dropRects: Map<Node, DOMRect> | null = null;
          const gap = `--${scope}-gap`;
          const progress = `--${scope}-progress`;
          const transition = () => ({
            duration: reducedMotion.matches ? 0 : 0.2,
            ease: [0.22, 1, 0.36, 1] as const,
          });

          function blocks() {
            const result: { node: Node; pos: number; element: HTMLElement }[] =
              [];
            view.state.doc.forEach((node, pos) => {
              const element = view.nodeDOM(pos);
              if (element instanceof HTMLElement)
                result.push({ node, pos, element });
            });
            return result;
          }
          function selector(element: HTMLElement) {
            return `[data-block-motion="${scope}"] > :nth-child(${Array.from(view.dom.children).indexOf(element) + 1})`;
          }
          function restore() {
            animation?.stop();
            stylesheet.textContent = "";
            doc.body.style.removeProperty(gap);
            doc.body.style.removeProperty(progress);
          }
          function finish() {
            source = null;
            sourceStyles = "";
            target = null;
            preview?.remove();
            preview = null;
            doc.body.removeAttribute("data-article-block-dragging");
            restore();
          }
          function start(event: DragEvent) {
            if (!view.editable || !view.dragging || !event.dataTransfer) return;
            const selection =
              "node" in view.dragging &&
              view.dragging.node instanceof NodeSelection
                ? view.dragging.node
                : view.state.selection;
            if (
              !(selection instanceof NodeSelection) &&
              !isNodeRangeSelection(selection)
            )
              return;
            finish();
            source = {
              from: selection.from,
              to: selection.to,
            };
            const selected: HTMLElement[] = [];
            view.state.doc.nodesBetween(source.from, source.to, (node, pos) => {
              if (pos < source!.from) return true;
              const element = view.nodeDOM(pos);
              if (node.isBlock && element instanceof HTMLElement) {
                if (element.parentElement === view.dom)
                  sourceStyles += `${selector(element)} { opacity: 0.3; }\n`;
                selected.push(
                  node.type.name === "image"
                    ? (element.querySelector<HTMLElement>(
                        ".tiptap-image-container",
                      ) ?? element)
                    : element,
                );
              }
              return false;
            });
            if (!selected.length) {
              source = null;
              return;
            }
            const width = Math.max(
              ...selected.map(
                (element) => element.getBoundingClientRect().width,
              ),
            );
            const height = selected.reduce(
              (sum, element) => sum + element.getBoundingClientRect().height,
              0,
            );
            const scale = Math.min(
              0.5,
              240 / Math.max(width, 1),
              180 / Math.max(height, 1),
            );
            preview = doc.createElement("div");
            preview.className = "article-block-drag-preview";
            preview.setAttribute("aria-hidden", "true");
            preview.inert = true;
            preview.style.width = `${width * scale}px`;
            preview.style.height = `${height * scale}px`;
            const content = doc.createElement("div");
            content.className = "tiptap ProseMirror notion-like-editor";
            Object.assign(content.style, {
              width: `${width}px`,
              minHeight: "0",
              padding: "0",
              transform: `scale(${scale})`,
              transformOrigin: "top left",
            });
            selected.forEach((element) => {
              const clone = element.cloneNode(true);
              if (!(clone instanceof HTMLElement)) return;
              clone.removeAttribute("id");
              clone.classList.remove(
                "ProseMirror-selectednode",
                "ProseMirror-selectednoderange",
              );
              clone.style.margin = "0";
              clone
                .querySelectorAll("[id]")
                .forEach((child) => child.removeAttribute("id"));
              const canvases = clone.querySelectorAll("canvas");
              element.querySelectorAll("canvas").forEach((canvas, index) => {
                if (canvas.width && canvas.height)
                  canvases[index]?.getContext("2d")?.drawImage(canvas, 0, 0);
              });
              content.append(clone);
            });
            preview.append(content);
            doc.body.append(preview);
            event.dataTransfer.setDragImage(preview, -12, -12);
            stylesheet.textContent = sourceStyles;
            doc.body.setAttribute("data-article-block-dragging", "");
          }
          function over(event: DragEvent) {
            if (!source || !view.dragging) return;
            const hit = view.posAtCoords({
              left: event.clientX,
              top: event.clientY,
            });
            const next = hit
              ? dropPoint(view.state.doc, hit.pos, view.dragging.slice)
              : null;
            const boundary =
              next !== null && view.state.doc.resolve(next).depth === 0
                ? next
                : null;
            if (boundary === target) return;
            const wasOpen = target !== null;
            target = boundary;
            animation?.stop();
            if (
              boundary === null ||
              (boundary >= source.from && boundary <= source.to)
            ) {
              animation = animate(doc.body, { [gap]: 0 }, transition());
              return;
            }
            const duration = reducedMotion.matches ? 0 : 180;
            stylesheet.textContent =
              sourceStyles +
              blocks()
                .map(
                  ({ pos, element }) =>
                    `${selector(element)} { transform: translateY(calc(${pos < boundary ? -1 : 1} * var(${gap}, 0) * 1px)); transition: transform ${duration}ms cubic-bezier(.22,1,.36,1); }`,
                )
                .join("\n");
            if (!wasOpen) doc.body.style.setProperty(gap, "0");
            animation = animate(doc.body, { [gap]: 10 }, transition());
          }
          function leave(event: DragEvent) {
            if (!source || target === null) return;
            const rect = view.dom.getBoundingClientRect();
            if (
              event.clientX >= rect.left &&
              event.clientX <= rect.right &&
              event.clientY >= rect.top &&
              event.clientY <= rect.bottom
            )
              return;
            target = null;
            animation?.stop();
            animation = animate(doc.body, { [gap]: 0 }, transition());
          }
          function drop(event: DragEvent) {
            if (!source) return;
            restore();
            dropRects =
              event.target instanceof globalThis.Node &&
              view.dom.contains(event.target)
                ? new Map(
                    blocks().map(({ node, element }) => [
                      node,
                      element.getBoundingClientRect(),
                    ]),
                  )
                : null;
            finish();
          }
          function end() {
            if (source) finish();
            dropRects = null;
          }
          doc.addEventListener("dragstart", start);
          doc.addEventListener("dragend", end);
          doc.addEventListener("drop", drop, true);
          view.dom.addEventListener("dragover", over);
          view.dom.addEventListener("dragleave", leave);
          return {
            update(_view, previousState) {
              if (!view.editable && source) finish();
              if (previousState.doc === view.state.doc) return;
              if (!dropRects) {
                if (!source) restore();
                return;
              }
              const previous = dropRects;
              dropRects = null;
              if (reducedMotion.matches) return;
              stylesheet.textContent = blocks()
                .map(({ node, element }) => {
                  const before = previous.get(node);
                  const delta = before
                    ? before.top - element.getBoundingClientRect().top
                    : 0;
                  return Math.abs(delta) > 1
                    ? `${selector(element)} { transform: translateY(calc(${delta}px * (1 - var(${progress}, 0)))); }`
                    : "";
                })
                .join("\n");
              doc.body.style.setProperty(progress, "0");
              animation = animate(doc.body, { [progress]: 1 }, transition());
              const settling = animation;
              void settling.then(() => {
                if (animation === settling && !source) restore();
              });
            },
            destroy() {
              doc.removeEventListener("dragstart", start);
              doc.removeEventListener("dragend", end);
              doc.removeEventListener("drop", drop, true);
              view.dom.removeEventListener("dragover", over);
              view.dom.removeEventListener("dragleave", leave);
              finish();
              stylesheet.remove();
            },
          };
        },
      }),
    ];
  },
});
