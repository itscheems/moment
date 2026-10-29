import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Editor } from "@tiptap/core";
import { EditorContent } from "@tiptap/react";
import { FindAndReplace } from "@tiptap/extension-find-and-replace";
import { articleContentExtensions } from "../../../components/content/schema";
import { ArticleSheet } from "../../../components/content/ArticleSheet";
import { NavigationProvider } from "../../../app/navigation";
import { MentionProvider } from "../../../app/mentions";
import { ArticleSearch } from "../ArticleSearch";
import { ArticleReadingContent } from "./ArticleReadingContent";
import type { ArticleDocument } from "../../../api/public";

let editor: Editor | undefined;
beforeEach(() => {
  vi.stubGlobal(
    "ResizeObserver",
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(),
  });
});
afterEach(() => {
  cleanup();
  editor?.destroy();
  editor = undefined;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function showSearch(editable = false) {
  editor = new Editor({
    editable,
    extensions: [
      ...articleContentExtensions(),
      FindAndReplace.configure({ injectCSS: false }),
    ],
    content:
      '<p data-id="one">River <strong>river</strong> rivers</p><p data-id="two">river</p>',
  });
  return render(
    <>
      <input aria-label="其他输入" />
      <ArticleSearch editor={editor} />
      <EditorContent editor={editor} />
    </>,
  );
}

describe("native article find", () => {
  it.each(["metaKey", "ctrlKey"])(
    "captures %s + F, refocuses search, and restores the origin on Escape",
    (modifier) => {
      showSearch();
      const origin = screen.getByRole("textbox", { name: "其他输入" });
      origin.focus();
      expect(
        fireEvent.keyDown(origin, { key: "f", code: "KeyF", [modifier]: true }),
      ).toBe(false);
      const input = screen.getByRole("textbox", { name: "查找" });
      expect(input).toHaveFocus();
      fireEvent.change(input, { target: { value: "river" } });
      expect(
        fireEvent.keyDown(input, { key: "f", code: "KeyF", [modifier]: true }),
      ).toBe(false);
      expect(input).toHaveFocus();
      expect((input as HTMLInputElement).selectionEnd).toBe(5);
      fireEvent.keyDown(input, { key: "Escape", code: "Escape" });
      expect(
        screen.queryByRole("dialog", { name: "查找" }),
      ).not.toBeInTheDocument();
      expect(origin).toHaveFocus();
    },
  );

  it.each([false, true])(
    "keeps navigation, options and close/reopen without replacement or document changes (editable=%s)",
    async (editable) => {
      showSearch(editable);
      const before = editor!.getJSON();
      editor!.commands.setTextSelection(3);
      const selection = editor!.state.selection.toJSON();
      fireEvent.click(screen.getByRole("button", { name: "查找" }));
      const input = screen.getByRole("textbox", { name: "查找" });
      fireEvent.change(input, { target: { value: "river" } });
      await waitFor(() =>
        expect(screen.getByText("1 / 4")).toBeInTheDocument(),
      );
      expect(
        document.querySelectorAll(".find-and-replace-result"),
      ).toHaveLength(4);
      fireEvent.click(screen.getByRole("button", { name: "下一个结果" }));
      expect(screen.getByText("2 / 4")).toBeInTheDocument();
      expect(editor!.state.selection.toJSON()).toEqual(selection);
      fireEvent.click(screen.getByText("区分大小写"));
      expect(editor!.storage.findAndReplace.results).toHaveLength(3);
      fireEvent.click(screen.getByText("全字匹配"));
      expect(editor!.storage.findAndReplace.results).toHaveLength(2);
      fireEvent.click(screen.getByRole("switch"));
      fireEvent.change(input, { target: { value: "[" } });
      await waitFor(() =>
        expect(screen.getByText("0 / 0")).toBeInTheDocument(),
      );
      expect(screen.getByRole("button", { name: "下一个结果" })).toBeDisabled();
      expect(document.querySelector('[data-field="replace-query"]')).toBeNull();
      expect(
        document.querySelector('[data-search-replace-action="replace"]'),
      ).toBeNull();
      expect(
        screen.queryByText(/Try search and replace|Replace all/),
      ).not.toBeInTheDocument();
      fireEvent.change(input, { target: { value: "river" } });
      fireEvent.click(screen.getByRole("button", { name: "关闭" }));
      await act(async () => {
        await new Promise((resolve) => setTimeout(resolve, 300));
      });
      expect(editor!.storage.findAndReplace.results).toHaveLength(0);
      fireEvent.click(screen.getByRole("button", { name: "查找" }));
      expect(screen.getByRole("textbox", { name: "查找" })).toHaveValue(
        "river",
      );
      await waitFor(() =>
        expect(editor!.storage.findAndReplace.results.length).toBeGreaterThan(
          0,
        ),
      );
      expect(editor!.getJSON()).toEqual(before);
    },
  );

  it("searches read-only captions, code and mentions while retaining their controls and directory", async () => {
    const body: ArticleDocument = {
      schema_version: 2,
      type: "doc",
      content: [
        { type: "tocNode" },
        {
          type: "heading",
          attrs: { level: 1, id: "chapter", "data-toc-id": "chapter" },
          content: [{ type: "text", text: "章节" }],
        },
        {
          type: "paragraph",
          content: [{ type: "text", text: "你好 @xiaoman" }],
        },
        {
          type: "codeBlock",
          attrs: { language: "javascript" },
          content: [{ type: "text", text: 'const name = "xiaoman";' }],
        },
        {
          type: "image",
          attrs: {
            media_id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
            alt: "带图注的图片",
            width: 200,
            showCaption: true,
          },
          content: [{ type: "text", text: "xiaoman 的图注" }],
        },
      ],
    };
    const before = JSON.stringify(body);
    render(
      <NavigationProvider>
        <MentionProvider known={["xiaoman"]}>
          <ArticleSheet title="文章" byline="作者">
            <ArticleReadingContent document={body} />
          </ArticleSheet>
        </MentionProvider>
      </NavigationProvider>,
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "复制代码" }),
      ).toBeInTheDocument(),
    );
    expect(document.querySelector("[data-content-renderer]")).toHaveAttribute(
      "contenteditable",
      "false",
    );
    expect(screen.getByRole("link", { name: "@xiaoman" })).toHaveAttribute(
      "data-moment-mention",
      "xiaoman",
    );
    expect(
      document.querySelector(
        "[data-content-renderer] .tiptap-table-of-contents-item",
      ),
    ).toHaveAttribute("href", "#chapter");
    expect(screen.getByText("xiaoman 的图注")).toBeVisible();
    const nativeEditor = (
      document.querySelector("[data-content-renderer]") as HTMLElement & {
        editor: Editor;
      }
    ).editor;
    const nativeDocument = nativeEditor.getJSON();
    const selection = nativeEditor.state.selection.toJSON();
    const picture = screen.getByRole("img", { name: "带图注的图片" });
    fireEvent.mouseEnter(picture.closest(".tiptap-image")!);
    expect(document.querySelector(".tiptap-image-handle")).toBeNull();
    fireEvent.click(picture);
    expect(nativeEditor.state.selection.toJSON()).toEqual(selection);
    expect(nativeEditor.getJSON()).toEqual(nativeDocument);
    expect(
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      }),
    ).toBe(false);
    fireEvent.change(screen.getByRole("textbox", { name: "查找" }), {
      target: { value: "xiaoman" },
    });
    await waitFor(() => expect(screen.getByText("1 / 3")).toBeInTheDocument());
    expect(
      document.querySelector("pre code .find-and-replace-result"),
    ).not.toBeNull();
    expect(
      document.querySelector(".tiptap-image-caption .find-and-replace-result"),
    ).not.toBeNull();
    expect(JSON.stringify(body)).toBe(before);
  });
});
