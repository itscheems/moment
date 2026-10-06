import {
  getTextBetween,
  getTextSerializersFromSchema,
  type Editor,
} from "@tiptap/core";
import type {
  ShareCardBlock,
  ShareCardInput,
  ShareCardTextRun,
} from "../../share/shareCard";
import type { ReadingRange } from "./readingAnnotations";

/** Supplied by the page that already owns the article's identity. */
export interface ReadingExcerptSource {
  title: string;
  displayName: string;
  username: string;
  avatarUrl: string | null;
  publishedLabel: string;
}

/** Slice source positions before serializing: atoms and newlines aren't offsets in text. */
export function readingExcerptBlocks(
  editor: Editor,
  selection: ReadingRange,
  resolveColour: (value: string) => string,
): ShareCardBlock[] {
  const { doc } = editor.state;
  const serializers = getTextSerializersFromSchema(editor.schema);
  const annotations = editor.storage.readingAnnotations.ranges;
  const blocks: ShareCardBlock[] = [];
  doc.nodesBetween(selection.from, selection.to, (node, pos) => {
    if (!node.isTextblock) return;
    const position = doc.resolve(pos + 1);
    let nodeBackground: string | undefined;
    for (let depth = position.depth; depth > 0; depth--) {
      const background = position.node(depth).attrs.backgroundColor;
      if (background) {
        nodeBackground = background;
        break;
      }
    }
    const from = Math.max(selection.from, pos + 1);
    const to = Math.min(selection.to, pos + node.nodeSize - 1);
    const runs: ShareCardTextRun[] = [];
    if (from < to)
      node.nodesBetween(from - pos - 1, to - pos - 1, (child, offset) => {
        if (!child.isInline) return;
        const authorColour = child.marks.find(
          (mark) => mark.type.name === "textStyle",
        )?.attrs.color;
        const authorHighlight = child.marks.find(
          (mark) => mark.type.name === "highlight",
        );
        // A colourless official Highlight mark renders <mark>'s default yellow.
        const authorBackground = authorHighlight
          ? authorHighlight.attrs.color || "yellow"
          : nodeBackground;
        const start = Math.max(from, pos + 1 + offset);
        const end = Math.min(to, pos + 1 + offset + child.nodeSize);
        const boundaries = [
          ...new Set([
            start,
            end,
            ...annotations.flatMap((range) =>
              range.from < end && range.to > start
                ? [Math.max(start, range.from), Math.min(end, range.to)]
                : [],
            ),
          ]),
        ].sort((a, b) => a - b);
        for (let index = 0; index < boundaries.length - 1; index++) {
          const a = boundaries[index],
            b = boundaries[index + 1];
          const text = getTextBetween(
            doc,
            { from: a, to: b },
            {
              blockSeparator: "\n",
              textSerializers: serializers,
            },
          );
          if (!text) continue;
          const styles = annotations.filter(
            (range) => range.from <= a && range.to >= b,
          );
          // Reader decorations override only their own style and selected range.
          const colour =
            styles.find((range) => range.type === "text")?.color ||
            authorColour;
          const background =
            styles.find((range) => range.type === "highlight")?.color ||
            authorBackground;
          const run: ShareCardTextRun = {
            text,
            ...(colour ? { colour: resolveColour(colour) } : {}),
            ...(background ? { background: resolveColour(background) } : {}),
          };
          const previous = runs.at(-1);
          if (
            previous &&
            previous.colour === run.colour &&
            previous.background === run.background
          )
            previous.text += text;
          else runs.push(run);
        }
        return false;
      });
    // No neighbouring block's text or marks are pulled into this selection.
    const text = runs.map((run) => run.text).join("");
    let marker: string | undefined;
    let quote = false;
    for (let depth = position.depth; depth > 0; depth--) {
      const ancestor = position.node(depth);
      if (ancestor.type.name === "blockquote") quote = true;
      if (
        ["listItem", "taskItem"].includes(ancestor.type.name) &&
        marker === undefined &&
        position.index(depth) === 0
      ) {
        const list = position.node(depth - 1);
        marker =
          list.type.name === "orderedList"
            ? `${Number(list.attrs.start ?? 1) + position.index(depth - 1)}.`
            : "•";
      }
    }
    if (marker !== undefined) blocks.push({ kind: "item", text, marker, runs });
    else if (quote) blocks.push({ kind: "quote", text, runs });
    else
      blocks.push({
        kind: "text",
        text,
        runs,
        ...(node.type.name === "heading"
          ? { heading: Math.min(3, node.attrs.level) as 1 | 2 | 3 }
          : {}),
      });
    return false;
  });
  return blocks;
}

/** Freeze the existing light colour tokens for the white share card. */
export function readingExcerptCard(
  editor: Editor,
  selection: ReadingRange,
  source: ReadingExcerptSource,
): ShareCardInput {
  const probe = document.createElement("span");
  probe.hidden = true;
  // A sibling avoids changing ProseMirror's managed content or document.
  editor.view.dom.parentElement!.append(probe);
  // Reuse the loaded template's light defaults without changing the page theme.
  for (const stylesheet of document.styleSheets) {
    if (stylesheet.href && new URL(stylesheet.href).origin !== location.origin)
      continue;
    for (const rule of stylesheet.cssRules) {
      if (!(rule instanceof CSSStyleRule) || rule.selectorText !== ":root")
        continue;
      for (const property of Array.from(rule.style))
        if (property.startsWith("--tt-color-"))
          probe.style.setProperty(
            property,
            rule.style.getPropertyValue(property),
          );
    }
  }
  const colours = new Map<string, string>();
  const resolveColour = (value: string) => {
    const cached = colours.get(value);
    if (cached) return cached;
    probe.style.color = value;
    const colour = getComputedStyle(probe).color;
    colours.set(value, colour);
    return colour;
  };
  try {
    return {
      displayName: source.displayName,
      username: source.username,
      avatarUrl: source.avatarUrl,
      publishedLabel: source.publishedLabel,
      articleTitle: source.title.trim() || "无标题",
      blocks: readingExcerptBlocks(editor, selection, resolveColour),
      media: [],
      location: null,
      link: null,
    };
  } finally {
    probe.remove();
  }
}
