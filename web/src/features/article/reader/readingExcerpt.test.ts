import { Editor } from "@tiptap/core";
import Emoji from "@tiptap/extension-emoji";
import { afterEach, describe, expect, it, vi } from "vitest";
import { articleContentExtensions } from "../../../components/content/schema";
import {
  ReadingAnnotations,
  applyReadingColor,
  clearReadingAnnotations,
  readingSelectionText,
} from "./readingAnnotations";
import { readingExcerptBlocks, readingExcerptCard } from "./readingExcerpt";

let editor: Editor;
const create = (content: string | object) => {
  editor = new Editor({
    editable: false,
    content,
    extensions: [...articleContentExtensions(), Emoji, ReadingAnnotations],
  });
  document.body.append(editor.view.dom);
  return editor;
};
afterEach(() => {
  editor?.view.dom.remove();
  editor?.destroy();
  vi.restoreAllMocks();
});

describe("article excerpt snapshots", () => {
  it("includes authored text colours and highlights at partial selection endpoints", () => {
    create(
      '<p><span style="color: blue">ab<mark data-color="pink" style="background-color: pink">cdef</mark>gh</span><span style="color: red">ij</span>kl<mark>mn</mark></p>',
    );
    expect(
      readingExcerptBlocks(editor, { from: 2, to: 14 }, (colour) => colour),
    ).toEqual([
      {
        kind: "text",
        text: "bcdefghijklm",
        runs: [
          { text: "b", colour: "blue" },
          { text: "cdef", colour: "blue", background: "pink" },
          { text: "gh", colour: "blue" },
          { text: "ij", colour: "red" },
          { text: "kl" },
          { text: "m", background: "yellow" },
        ],
      },
    ]);
  });

  it("overlays temporary styles independently and restores authored styles when cleared", () => {
    create(
      '<p><span style="color: blue"><mark data-color="pink" style="background-color: pink">abcdefghij</mark></span></p>',
    );
    const before = editor.getJSON();
    const updates = vi.fn();
    editor.on("update", updates);
    const storage = vi.spyOn(Storage.prototype, "setItem");
    const range = { from: 2, to: 10 };
    const snapshot = () =>
      readingExcerptBlocks(editor, range, (colour) => colour);
    const original = snapshot();
    applyReadingColor(editor, { from: 3, to: 7 }, "highlight", "yellow");
    applyReadingColor(editor, { from: 5, to: 9 }, "text", "red");
    expect(snapshot()).toEqual([
      {
        kind: "text",
        text: "bcdefghi",
        runs: [
          { text: "b", colour: "blue", background: "pink" },
          { text: "cd", colour: "blue", background: "yellow" },
          { text: "ef", colour: "red", background: "yellow" },
          { text: "gh", colour: "red", background: "pink" },
          { text: "i", colour: "blue", background: "pink" },
        ],
      },
    ]);
    clearReadingAnnotations(editor, { from: 4, to: 8 });
    expect(snapshot()).toEqual([
      {
        kind: "text",
        text: "bcdefghi",
        runs: [
          { text: "b", colour: "blue", background: "pink" },
          { text: "c", colour: "blue", background: "yellow" },
          { text: "defg", colour: "blue", background: "pink" },
          { text: "h", colour: "red", background: "pink" },
          { text: "i", colour: "blue", background: "pink" },
        ],
      },
    ]);
    clearReadingAnnotations(editor);
    expect(snapshot()).toEqual(original);
    expect(editor.getJSON()).toEqual(before);
    expect(updates).not.toHaveBeenCalled();
    expect(storage).not.toHaveBeenCalled();
  });

  it("inherits the nearest authored block background below inline and temporary highlights", () => {
    create(
      '<blockquote style="background-color: pink"><p>abc</p><p style="background-color: cyan">de<mark data-color="green" style="background-color: green">fg</mark>hi</p></blockquote>',
    );
    applyReadingColor(editor, { from: 10, to: 12 }, "highlight", "yellow");
    expect(
      readingExcerptBlocks(
        editor,
        { from: 2, to: editor.state.doc.content.size - 2 },
        (colour) => colour,
      ),
    ).toEqual([
      {
        kind: "quote",
        text: "abc",
        runs: [{ text: "abc", background: "pink" }],
      },
      {
        kind: "quote",
        text: "defghi",
        runs: [
          { text: "de", background: "cyan" },
          { text: "f", background: "green" },
          { text: "gh", background: "yellow" },
          { text: "i", background: "cyan" },
        ],
      },
    ]);
  });

  it("clips overlapping temporary colours to the exact selected characters", () => {
    create("<p>abcdefghij</p>");
    const before = editor.getJSON();
    const updates = vi.fn();
    editor.on("update", updates);
    const storage = vi.spyOn(Storage.prototype, "setItem");
    applyReadingColor(editor, { from: 1, to: 7 }, "highlight", "yellow");
    applyReadingColor(editor, { from: 5, to: 10 }, "text", "red");
    expect(
      readingExcerptBlocks(editor, { from: 3, to: 9 }, (colour) => colour),
    ).toEqual([
      {
        kind: "text",
        text: "cdefgh",
        runs: [
          { text: "cd", background: "yellow" },
          { text: "ef", colour: "red", background: "yellow" },
          { text: "gh", colour: "red" },
        ],
      },
    ]);
    expect(editor.getJSON()).toEqual(before);
    expect(updates).not.toHaveBeenCalled();
    expect(storage).not.toHaveBeenCalled();
  });

  it("preserves hard breaks, spaces, paragraph boundaries and partial endpoints", () => {
    create("<p>prefix abc<br>def suffix</p><p>next text</p>");
    const range = { from: 8, to: 25 };
    const blocks = readingExcerptBlocks(editor, range, (colour) => colour);
    expect(
      blocks
        .map((block) => (block.kind === "code" ? "" : block.text))
        .join("\n"),
    ).toBe(readingSelectionText(editor, range));
    expect(blocks).toEqual([
      {
        kind: "text",
        text: "abc\ndef suffix",
        runs: [{ text: "abc\ndef suffix" }],
      },
      { kind: "text", text: "n", runs: [{ text: "n" }] },
    ]);
  });

  it("keeps heading, quote and the source list ordinal without including neighbouring text", () => {
    create(
      '<h2>标题</h2><blockquote><p>引文</p></blockquote><ol start="7"><li><p>甲</p></li><li><p>乙</p></li></ol>',
    );
    const blocks = readingExcerptBlocks(
      editor,
      { from: 1, to: editor.state.doc.content.size - 1 },
      (colour) => colour,
    );
    expect(
      blocks.map((block) =>
        block.kind === "code"
          ? block.kind
          : {
              kind: block.kind,
              text: block.text,
              ...(block.kind === "text" ? { heading: block.heading } : {}),
              ...(block.kind === "item" ? { marker: block.marker } : {}),
            },
      ),
    ).toEqual([
      { kind: "text", text: "标题", heading: 2 },
      { kind: "quote", text: "引文" },
      { kind: "item", text: "甲", marker: "7." },
      { kind: "item", text: "乙", marker: "8." },
    ]);
  });

  it("serializes an emoji atom rather than treating its document position as a text offset", () => {
    create({
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            { type: "text", text: "中文 " },
            { type: "emoji", attrs: { name: "sparkles" } },
            { type: "text", text: " English" },
          ],
        },
      ],
    });
    editor.commands.setTextSelection({ from: 4, to: 5 });
    expect(
      readingExcerptBlocks(editor, { from: 4, to: 5 }, (colour) => colour),
    ).toEqual([{ kind: "text", text: "✨", runs: [{ text: "✨" }] }]);
  });

  it("freezes metadata and decoration runs without saving or changing the author document", () => {
    create("<p><strong>作者格式</strong> abc</p>");
    const before = editor.getJSON();
    const storage = vi.spyOn(Storage.prototype, "setItem");
    const updates = vi.fn();
    editor.on("update", updates);
    applyReadingColor(editor, { from: 1, to: 3 }, "highlight", "yellow");
    const source = {
      title: "文章标题",
      displayName: "作者",
      username: "author",
      avatarUrl: null,
      publishedLabel: "草稿摘录",
    };
    const input = readingExcerptCard(editor, { from: 1, to: 3 }, source);
    const frozen = structuredClone(input);
    source.title = "之后修改";
    applyReadingColor(editor, { from: 1, to: 3 }, "highlight", "green");
    expect(input).toEqual(frozen);
    expect(input.articleTitle).toBe("文章标题");
    expect(input).not.toHaveProperty("palette");
    expect(input.media).toEqual([]);
    expect(input.link).toBeNull();
    expect(editor.getJSON()).toEqual(before);
    expect(storage).not.toHaveBeenCalled();
    expect(updates).not.toHaveBeenCalled();
    source.title = "  ";
    expect(
      readingExcerptCard(editor, { from: 1, to: 3 }, source).articleTitle,
    ).toBe("无标题");
  });
});
