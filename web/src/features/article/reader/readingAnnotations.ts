import {
  Decoration,
  Extension,
  getTextBetween,
  getTextSerializersFromSchema,
  type Editor,
} from "@tiptap/core";
import { isTextSelection } from "@tiptap/react";
import type { ColorActionType } from "@notion/hooks/use-color-action-adapter";

export interface ReadingRange {
  from: number;
  to: number;
}
export interface ReadingColorRange extends ReadingRange {
  type: ColorActionType;
  color: string;
}
interface AnnotationStorage {
  ranges: ReadingColorRange[];
}
declare module "@tiptap/core" {
  interface Storage {
    readingAnnotations: AnnotationStorage;
  }
}

/** This storage belongs to one read-only view, never to its document. */
export const ReadingAnnotations = Extension.create<object, AnnotationStorage>({
  name: "readingAnnotations",
  addStorage: () => ({ ranges: [] }),
  addDecorations() {
    return {
      update: "manual",
      create: () =>
        this.storage.ranges.map(({ from, to, type, color }) =>
          Decoration.Inline(from, to, {
            "data-reading-annotation": type,
            style: `${type === "text" ? "color" : "background-color"}: ${color}`,
          }),
        ),
    };
  },
});

export function readingSelection(editor: Editor): ReadingRange | null {
  const { selection, doc } = editor.state;
  if (
    !isTextSelection(selection) ||
    selection.empty ||
    !doc.textBetween(selection.from, selection.to).trim()
  )
    return null;
  let excluded = false;
  doc.nodesBetween(selection.from, selection.to, (node) => {
    if (node.type.spec.code || node.type.name === "image") excluded = true;
  });
  return excluded ? null : { from: selection.from, to: selection.to };
}

export function readingSelectionText(editor: Editor, range: ReadingRange) {
  return getTextBetween(editor.state.doc, range, {
    blockSeparator: "\n",
    textSerializers: getTextSerializersFromSchema(editor.schema),
  });
}

function subtract(
  ranges: ReadingColorRange[],
  selection: ReadingRange,
  type?: ColorActionType,
) {
  return ranges.flatMap((range) => {
    if (
      (type && range.type !== type) ||
      range.to <= selection.from ||
      range.from >= selection.to
    )
      return [range];
    return [
      ...(range.from < selection.from
        ? [{ ...range, to: selection.from }]
        : []),
      ...(range.to > selection.to ? [{ ...range, from: selection.to }] : []),
    ];
  });
}

export function activeReadingColor(
  editor: Editor,
  selection: ReadingRange | null,
  type: ColorActionType,
) {
  if (!selection) return undefined;
  const ranges = editor.storage.readingAnnotations.ranges
    .filter(
      (range) =>
        range.type === type &&
        range.to > selection.from &&
        range.from < selection.to,
    )
    .sort((a, b) => a.from - b.from);
  let covered = selection.from;
  const color = ranges[0]?.color;
  for (const range of ranges) {
    if (range.from > covered || range.color !== color) return undefined;
    covered = Math.max(covered, range.to);
  }
  return covered >= selection.to ? color : undefined;
}

export function clearReadingAnnotations(
  editor: Editor,
  selection?: ReadingRange,
  type?: ColorActionType,
) {
  editor.storage.readingAnnotations.ranges = selection
    ? subtract(editor.storage.readingAnnotations.ranges, selection, type)
    : [];
  editor.commands.updateDecorations("readingAnnotations");
}

export function applyReadingColor(
  editor: Editor,
  selection: ReadingRange,
  type: ColorActionType,
  color: string,
) {
  const defaultColor =
    type === "text" ? "var(--tt-color-text)" : "var(--tt-bg-color)";
  const remove =
    color === defaultColor ||
    activeReadingColor(editor, selection, type) === color;
  const remaining = subtract(
    editor.storage.readingAnnotations.ranges,
    selection,
    type,
  );
  editor.storage.readingAnnotations.ranges = remove
    ? remaining
    : [...remaining, { ...selection, type, color }];
  editor.commands.updateDecorations("readingAnnotations");
}
