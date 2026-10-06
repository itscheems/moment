import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import { MentionProvider } from "../../app/mentions";
import type { ArticleDocument } from "../../api/public";
import { ArticleSheet } from "./ArticleSheet";
import { RichContent } from "./RichContent";

const document: ArticleDocument = {
  schema_version: 2,
  type: "doc",
  content: [
    { type: "tocNode" },
    {
      type: "heading",
      attrs: { level: 1, id: "first" },
      content: [{ type: "text", text: "第一章" }],
    },
    {
      type: "heading",
      attrs: { level: 2, id: "second" },
      content: [{ type: "text", text: "第二章" }],
    },
  ],
};

beforeEach(() => {
  vi.useFakeTimers();
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  window.history.replaceState({ index: 7 }, "", "/");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

function showArticle() {
  return render(
    <NavigationProvider>
      <ArticleSheet title="测试文章" byline="作者">
        <RichContent document={document} />
      </ArticleSheet>
    </NavigationProvider>,
  );
}

it("briefly emphasizes an inline directory destination without changing document or selection", () => {
  const original = JSON.stringify(document);
  showArticle();
  const heading = screen.getByRole("heading", { name: "第二章" });
  const selection = window.getSelection()?.toString();
  fireEvent.click(screen.getAllByRole("link", { name: "第二章" })[0]);
  expect(heading).toHaveClass("ProseMirror-selectednode");
  expect(window.location.hash).toBe(`#${heading.id}`);
  expect(window.history.state).toEqual({ index: 7 });
  expect(window.getSelection()?.toString()).toBe(selection);
  expect(JSON.stringify(document)).toBe(original);
  act(() => vi.advanceTimersByTime(1_999));
  expect(heading).toHaveClass("ProseMirror-selectednode");
  act(() => vi.advanceTimersByTime(1));
  expect(heading).not.toHaveClass("ProseMirror-selectednode");
});

it("moves sidebar feedback to the latest destination and restarts its duration on repeated clicks", () => {
  showArticle();
  const first = screen.getByRole("heading", { name: "第一章" });
  const second = screen.getByRole("heading", { name: "第二章" });
  fireEvent.click(screen.getAllByRole("link", { name: "第一章" })[1]);
  act(() => vi.advanceTimersByTime(1_000));
  fireEvent.click(screen.getAllByRole("link", { name: "第二章" })[1]);
  expect(first).not.toHaveClass("ProseMirror-selectednode");
  expect(second).toHaveClass("ProseMirror-selectednode");
  act(() => vi.advanceTimersByTime(1_000));
  expect(second).toHaveClass("ProseMirror-selectednode");
  fireEvent.click(screen.getAllByRole("link", { name: "第二章" })[1]);
  act(() => vi.advanceTimersByTime(1_999));
  expect(second).toHaveClass("ProseMirror-selectednode");
  act(() => vi.advanceTimersByTime(1));
  expect(second).not.toHaveClass("ProseMirror-selectednode");
});

it("clears destination feedback when leaving the reader", () => {
  const view = showArticle();
  const heading = screen.getByRole("heading", { name: "第一章" });
  fireEvent.click(screen.getAllByRole("link", { name: "第一章" })[0]);
  view.unmount();
  expect(heading).not.toHaveClass("ProseMirror-selectednode");
  expect(vi.getTimerCount()).toBe(0);
});

it("keeps the destination mounted and emphasized while a mention finishes loading", async () => {
  let complete!: (response: Response) => void;
  const fetcher = vi.fn(
    () => new Promise<Response>((resolve) => (complete = resolve)),
  );
  vi.stubGlobal("fetch", fetcher);
  const withMention: ArticleDocument = {
    ...document,
    content: [
      ...document.content!,
      {
        type: "paragraph",
        content: [{ type: "text", text: "你好 @linlin" }],
      },
    ],
  };
  render(
    <NavigationProvider>
      <MentionProvider>
        <ArticleSheet title="测试文章" byline="作者">
          <RichContent document={withMention} />
        </ArticleSheet>
      </MentionProvider>
    </NavigationProvider>,
  );
  const heading = screen.getByRole("heading", { name: "第二章" });
  fireEvent.click(screen.getAllByRole("link", { name: "第二章" })[0]);
  expect(heading).toHaveClass("ProseMirror-selectednode");
  await act(async () => {
    complete(
      new Response(
        JSON.stringify({
          data: {
            user_id: "a".repeat(32),
            username: "linlin",
            display_name: "林林",
            bio: "沿河散步",
            avatar_media_id: null,
            cover_media_id: null,
            role: "member",
            profile_url: "/linlin",
          },
          meta: {},
        }),
        { headers: { "Content-Type": "application/json" } },
      ),
    );
  });
  expect(screen.getByRole("link", { name: "@linlin" })).toBeInTheDocument();
  expect(screen.getByRole("heading", { name: "第二章" })).toBe(heading);
  expect(heading).toHaveClass("ProseMirror-selectednode");
  act(() => vi.advanceTimersByTime(1_999));
  expect(heading).toHaveClass("ProseMirror-selectednode");
  act(() => vi.advanceTimersByTime(1));
  expect(heading).not.toHaveClass("ProseMirror-selectednode");
});
