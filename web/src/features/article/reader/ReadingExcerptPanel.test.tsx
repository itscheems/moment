import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Editor } from "@tiptap/core";
import { ArticleSheet } from "../../../components/content/ArticleSheet";
import { HeaderActionsContext } from "../../../components/headerActions";
import { NavigationProvider } from "../../../app/navigation";
import { MentionProvider } from "../../../app/mentions";
import * as clipboard from "@content-notion/lib/clipboard";
import * as shareCard from "../../share/shareCard";
import type { ArticleDocument } from "../../../api/public";
import { ArticleReadingContent } from "./ArticleReadingContent";
import {
  applyReadingColor,
  clearReadingAnnotations,
  readingExcerptRanges,
} from "./readingAnnotations";

const body: ArticleDocument = {
  schema_version: 2,
  type: "doc",
  content: [
    {
      type: "paragraph",
      attrs: { id: "panel-first" },
      content: [
        {
          type: "text",
          text: "abcdefghij",
          marks: [
            { type: "textStyle", attrs: { color: "red" } },
            { type: "highlight", attrs: { color: "yellow" } },
          ],
        },
      ],
    },
    {
      type: "paragraph",
      attrs: { id: "panel-second" },
      content: [{ type: "text", text: "klmnop😀" }],
    },
  ],
};
let slot: HTMLElement;
beforeEach(() => {
  slot = document.createElement("div");
  document.body.append(slot);
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
});
afterEach(() => {
  cleanup();
  slot.remove();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
function show(enabled = true) {
  const ui = (document: ArticleDocument) => (
    <HeaderActionsContext.Provider value={slot}>
      <NavigationProvider>
        <MentionProvider known={[]}>
          <ArticleSheet title="文章" byline="作者">
            <ArticleReadingContent
              document={document}
              enableAnnotations
              enableExcerptPanel={enabled}
              excerptSource={{
                title: "文章",
                displayName: "作者",
                username: "author",
                avatarUrl: null,
                publishedLabel: "草稿摘录",
              }}
            />
          </ArticleSheet>
        </MentionProvider>
      </NavigationProvider>
    </HeaderActionsContext.Provider>
  );
  const mounted = render(ui(body));
  const element = document.querySelector(
    "[data-content-renderer]",
  ) as HTMLElement & { editor: Editor };
  return { editor: element.editor, element, mounted, ui };
}
function mark(
  editor: Editor,
  from: number,
  to: number,
  type: "text" | "highlight" = "highlight",
  color = "blue",
) {
  act(() => applyReadingColor(editor, { from, to }, type, color));
}
async function openPanel() {
  fireEvent.click(await screen.findByRole("button", { name: "本次摘录" }));
  return screen.findByRole("dialog", { name: "本次摘录面板" });
}

it("derives sorted union ranges, keeping unmarked gaps and splitting partial clears", () => {
  const { editor } = show();
  const original = editor.getJSON();
  mark(editor, 14, 17);
  mark(editor, 1, 5);
  mark(editor, 3, 7, "text", "green");
  mark(editor, 7, 9);
  expect(readingExcerptRanges(editor)).toEqual([
    { from: 1, to: 9 },
    { from: 14, to: 17 },
  ]);
  act(() => clearReadingAnnotations(editor, { from: 4, to: 6 }));
  expect(readingExcerptRanges(editor)).toEqual([
    { from: 1, to: 4 },
    { from: 6, to: 9 },
    { from: 14, to: 17 },
  ]);
  expect(editor.getJSON()).toEqual(original);
});

it("has no entrance for author styles alone or when the surface is disabled", () => {
  const { editor } = show(false);
  expect(screen.queryByRole("button", { name: "本次摘录" })).toBeNull();
  mark(editor, 1, 8);
  expect(screen.queryByRole("button", { name: "本次摘录" })).toBeNull();
});

it("copies exact cross-paragraph text and removes only that excerpt, keeping author marks and next focus", async () => {
  const { editor, element } = show();
  const original = editor.getJSON();
  const update = vi.fn();
  editor.on("update", update);
  const copy = vi.spyOn(clipboard, "writeToClipboard").mockResolvedValue();
  const storage = vi.spyOn(Storage.prototype, "setItem");
  expect(screen.queryByRole("button", { name: "本次摘录" })).toBeNull();
  mark(editor, 7, 16);
  mark(editor, 18, 21, "text", "green");
  await openPanel();
  expect(
    screen.getByText("本次摘录", { selector: "strong" }),
  ).toHaveTextContent("2");
  fireEvent.click(screen.getByRole("button", { name: "复制摘录 1" }));
  await waitFor(() => expect(copy).toHaveBeenCalledWith("ghij\nklm"));
  expect(
    screen.getByRole("dialog", { name: "本次摘录面板" }),
  ).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "清除摘录 1" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "跳转到摘录 1" })).toHaveFocus(),
  );
  expect(readingExcerptRanges(editor)).toEqual([{ from: 18, to: 21 }]);
  fireEvent.click(screen.getByRole("button", { name: "清除摘录 1" }));
  // jsdom cannot run keyframes: deliver the native completion lifecycle.
  const closing = document.querySelector(
    '[aria-label="本次摘录面板"][data-state="closed"]',
  );
  if (closing) {
    const end = new Event("animationend", { bubbles: true });
    Object.defineProperty(end, "animationName", { value: "fadeOut" });
    fireEvent(closing, end);
  }
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "本次摘录" })).toBeNull(),
  );
  await waitFor(() => expect(element).toHaveFocus());
  expect(editor.getJSON()).toEqual(original);
  expect(element.querySelector("mark")).not.toBeNull();
  expect(update).not.toHaveBeenCalled();
  expect(storage).not.toHaveBeenCalled();
});

it("shares the entry without selecting the document and restores panel focus and scroll", async () => {
  vi.stubGlobal(
    "URL",
    class extends URL {
      static createObjectURL = () => "blob:excerpt";
      static revokeObjectURL = vi.fn();
    },
  );
  const draw = vi
    .spyOn(shareCard, "renderShareCard")
    .mockResolvedValue(new Blob(["png"], { type: "image/png" }));
  const { editor } = show();
  mark(editor, 2, 6);
  mark(editor, 3, 5, "text", "green");
  const selection = editor.state.selection;
  await openPanel();
  const viewport = document.querySelector(
    ".tiptap-scroll-fade[data-scroll-top]",
  ) as HTMLElement;
  viewport.scrollTop = 24;
  fireEvent.scroll(viewport);
  fireEvent.click(screen.getByRole("button", { name: "分享摘录 1" }));
  await screen.findByRole("img", { name: "分享图片预览" });
  expect(screen.queryByRole("dialog", { name: "本次摘录面板" })).toBeNull();
  expect(draw.mock.calls[0][0].blocks).toEqual([
    {
      kind: "text",
      text: "bcde",
      runs: [
        {
          text: "b",
          colour: expect.any(String),
          background: expect.any(String),
        },
        {
          text: "cd",
          colour: expect.any(String),
          background: expect.any(String),
        },
        {
          text: "e",
          colour: expect.any(String),
          background: expect.any(String),
        },
      ],
    },
  ]);
  fireEvent.click(screen.getByRole("button", { name: "关闭" }));
  await screen.findByRole("dialog", { name: "本次摘录面板" });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "分享摘录 1" })).toHaveFocus(),
  );
  expect(
    document.querySelector(".tiptap-scroll-fade[data-scroll-top]")?.scrollTop,
  ).toBe(24);
  expect(editor.state.selection.eq(selection)).toBe(true);
  expect(draw).toHaveBeenCalledOnce();
});

it("does not report clipboard failure as success or close the panel", async () => {
  vi.spyOn(clipboard, "writeToClipboard").mockRejectedValue(
    new Error("denied"),
  );
  const { editor } = show();
  mark(editor, 2, 6);
  await openPanel();
  await act(async () =>
    fireEvent.click(screen.getByRole("button", { name: "复制摘录 1" })),
  );
  expect(screen.queryByRole("button", { name: "已复制" })).toBeNull();
  expect(
    screen.getByRole("dialog", { name: "本次摘录面板" }),
  ).toBeInTheDocument();
});

it("keeps the panel across equal-body rerenders and search but clears it on body replacement", async () => {
  const { editor, mounted, ui } = show();
  mark(editor, 2, 6);
  await openPanel();
  act(() => {
    editor.commands.setSearchTerm("abc");
    editor.commands.updateDecorations();
  });
  mounted.rerender(ui(structuredClone(body)));
  expect(
    screen.getByRole("dialog", { name: "本次摘录面板" }),
  ).toBeInTheDocument();
  expect(readingExcerptRanges(editor)).toEqual([{ from: 2, to: 6 }]);
  mounted.rerender(
    ui({
      ...body,
      content: [
        { type: "paragraph", content: [{ type: "text", text: "new" }] },
      ],
    }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "本次摘录" })).toBeNull(),
  );
});

it("removes the entrance when the existing clear-all action clears decorations", async () => {
  const { editor } = show();
  mark(editor, 2, 6);
  fireEvent.click(screen.getByRole("button", { name: "Reading tools" }));
  fireEvent.click(
    await screen.findByRole("button", { name: "Clear all marks" }),
  );
  expect(readingExcerptRanges(editor)).toEqual([]);
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "本次摘录" })).toBeNull(),
  );
});

it("closes on mobile jumps, preserves selection and uses reduced-motion navigation", async () => {
  vi.spyOn(window, "matchMedia").mockImplementation((media) => ({
    matches: media.includes("719px") || media.includes("reduced-motion"),
    media,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent() {
      return false;
    },
  }));
  const { editor, element, mounted, ui } = show();
  mark(editor, 2, 6);
  const selected = editor.state.selection;
  await openPanel();
  fireEvent.click(screen.getByRole("button", { name: "跳转到摘录 1" }));
  expect(screen.getByRole("button", { name: "本次摘录" })).toHaveAttribute(
    "aria-expanded",
    "false",
  );
  expect(element.querySelector("p")).toHaveClass("ProseMirror-selectednode");
  expect(window.scrollTo).toHaveBeenCalledWith(
    expect.objectContaining({ behavior: "auto" }),
  );
  expect(editor.state.selection.eq(selected)).toBe(true);
  const highlighted = element.querySelector("p")!;
  mounted.rerender(
    ui({
      ...body,
      content: [
        { type: "paragraph", content: [{ type: "text", text: "changed" }] },
      ],
    }),
  );
  await waitFor(() =>
    expect(highlighted).not.toHaveClass("ProseMirror-selectednode"),
  );
});

it("keeps annotations and restores the panel when an excerpt exceeds the share limit", async () => {
  vi.spyOn(shareCard, "renderShareCard").mockRejectedValue(
    new shareCard.ShareCardTooLongError(),
  );
  const { editor } = show();
  mark(editor, 2, 6);
  await openPanel();
  fireEvent.click(screen.getByRole("button", { name: "分享摘录 1" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("60 行上限");
  fireEvent.click(screen.getByRole("button", { name: "关闭" }));
  await screen.findByRole("dialog", { name: "本次摘录面板" });
  expect(readingExcerptRanges(editor)).toEqual([{ from: 2, to: 6 }]);
});
