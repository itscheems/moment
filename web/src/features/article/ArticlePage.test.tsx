import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import type { Editor } from "@tiptap/core";
import { afterEach, describe, expect, it, vi } from "vitest";

import type {
  ArticleDocument,
  PublicReadArticlePost,
  SiteIdentity,
} from "../../api/public";
import { PublicApi } from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import { HeaderActionsContext } from "../../components/headerActions";
import { SessionProvider } from "../auth/SessionProvider";
import { DetailPage } from "../timeline/DetailPage";
import { ArticlePage } from "./ArticlePage";
import sheetCss from "../../components/content/ArticleSheet.module.css?inline";
import contentCss from "../../components/content/notion.scss?inline";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "乔乔的 Moment",
  bio: "一些正在发生的事",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

const coverMediaId = "01K2ABCDEF0123456789ABCDE7";
const pictureMediaId = "01K2ABCDEF0123456789ABCDE6";

// Everything the renderer can emit: a table, a lazy picture, a fenced block
// with a language, a quote, a rule and a link.
const contentHtml = [
  "<h2>上游</h2>",
  '<p>河面<strong>很窄</strong>，<a rel="nofollow noopener noreferrer" href="https://example.com/x">看这里</a>。</p>',
  "<blockquote><p>水色是灰的。</p></blockquote>",
  '<pre><code class="language-go">package main</code></pre>',
  "<hr>",
  '<table><thead><tr><th colspan="2">年份</th></tr></thead><tbody><tr><td>2016</td><td>清</td></tr></tbody></table>',
  `<img src="/api/v1/media/${pictureMediaId}" alt="入海口" loading="lazy">`,
].join("\n");

const body: ArticleDocument = {
  schema_version: 1,
  type: "doc",
  content: [
    {
      type: "heading",
      attrs: { level: 2 },
      content: [{ type: "text", text: "上游" }],
    },
    {
      type: "paragraph",
      content: [
        { type: "text", text: "河面" },
        { type: "text", text: "很窄", marks: [{ type: "bold" }] },
        { type: "text", text: "，" },
        {
          type: "text",
          text: "看这里",
          marks: [{ type: "link", attrs: { href: "https://example.com/x" } }],
        },
        { type: "text", text: "。" },
      ],
    },
    {
      type: "blockquote",
      content: [
        {
          type: "paragraph",
          content: [{ type: "text", text: "水色是灰的。" }],
        },
      ],
    },
    {
      type: "codeBlock",
      attrs: { language: "go" },
      content: [{ type: "text", text: "package main" }],
    },
    { type: "horizontalRule" },
    {
      type: "table",
      content: [
        {
          type: "tableRow",
          content: [
            {
              type: "tableHeader",
              attrs: { colspan: 2 },
              content: [
                {
                  type: "paragraph",
                  content: [{ type: "text", text: "年份" }],
                },
              ],
            },
          ],
        },
        {
          type: "tableRow",
          content: ["2016", "清"].map((text) => ({
            type: "tableCell",
            content: [{ type: "paragraph", content: [{ type: "text", text }] }],
          })),
        },
      ],
    },
    { type: "image", attrs: { media_id: pictureMediaId, alt: "入海口" } },
  ],
};

function makeArticle(
  overrides: Partial<PublicReadArticlePost["article"]> = {},
): PublicReadArticlePost {
  return {
    id: "01K2ABCDEF0123456789ABCDE0",
    kind: "article",
    contentMd: null,
    contentHtml,
    article: {
      title: "一条河流的十年",
      excerpt: "从上游走到入海口，记下水色每一年的变化。",
      cover: {
        mediaId: coverMediaId,
        mimeType: "image/jpeg",
        width: 1600,
        height: 900,
      },
      body,
      bodySchemaVersion: 1,
      ...overrides,
    },
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
    repostedAt: null,
    visibility: "everyone",
    timelineItemKind: "original",
    commentCount: 0,
    media: [],
    linkCards: [],
  };
}

function renderArticle(post = makeArticle()) {
  return render(
    <NavigationProvider>
      <ArticlePage
        now={new Date("2026-08-10T02:00:00.000Z")}
        post={post}
        site={site}
      />
    </NavigationProvider>,
  );
}

describe("article detail page", () => {
  it("enables only reading actions and find, with isolated detail and focus instances", async () => {
    const show = (focusReading = false) => (
      <FeedbackProvider>
        <NavigationProvider>
          <HeaderActionsContext.Provider value={document.body}>
            <ArticlePage
              post={makeArticle()}
              site={site}
              focusReading={focusReading}
            />
          </HeaderActionsContext.Provider>
        </NavigationProvider>
      </FeedbackProvider>
    );
    const mounted = render(show());
    const current = () =>
      (
        document.querySelector("[data-content-renderer]") as HTMLElement & {
          editor: Editor;
        }
      ).editor;
    await waitFor(() => expect(current()).toBeDefined());
    const detail = current();
    const original = detail.getJSON();
    const updated = vi.fn();
    detail.on("update", updated);
    expect(detail.isEditable).toBe(false);
    expect(screen.queryByRole("button", { name: "Search" })).toBeNull();
    await act(async () => detail.commands.setTextSelection({ from: 5, to: 7 }));
    const toolbar = await screen.findByRole("toolbar", {
      name: "Reading annotations",
    });
    expect(toolbar.querySelectorAll("button")).toHaveLength(5);
    for (const name of [
      "Copy",
      "Share excerpt",
      "Highlight",
      "Text color",
      "Clear selection marks",
    ])
      expect(screen.getByRole("button", { name })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Highlight" }));
    expect(detail.storage.readingAnnotations.ranges).toHaveLength(1);
    expect(screen.queryByRole("button", { name: "Reading tools" })).toBeNull();
    expect(
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      }),
    ).toBe(false);
    const input = screen.getByRole("textbox", { name: "Search" });
    fireEvent.change(input, { target: { value: "河" } });
    await waitFor(() => expect(screen.getByText("1 / 1")).toBeInTheDocument());
    expect(document.querySelector('[data-field="replace-query"]')).toBeNull();
    expect(detail.storage.readingAnnotations.ranges).toHaveLength(1);
    expect(detail.getJSON()).toEqual(original);
    expect(updated).not.toHaveBeenCalled();
    fireEvent.keyDown(input, { key: "Escape" });
    mounted.rerender(show());
    expect(current()).toBe(detail);
    expect(detail.storage.readingAnnotations.ranges).toHaveLength(1);
    mounted.rerender(show(true));
    await waitFor(() => expect(current()).not.toBe(detail));
    expect(detail.isDestroyed).toBe(true);
    const focus = current();
    expect(screen.getByRole("button", { name: "Search" })).toBeInTheDocument();
    expect(focus.storage.readingAnnotations.ranges).toHaveLength(0);
    expect(focus.storage.findAndReplace.searchTerm).toBe("");
    await act(async () => focus.commands.setTextSelection({ from: 5, to: 7 }));
    fireEvent.click(await screen.findByRole("button", { name: "Highlight" }));
    expect(
      screen.getByRole("button", { name: "Reading tools" }),
    ).toBeInTheDocument();
    mounted.rerender(show());
    await waitFor(() => expect(current()).not.toBe(focus));
    expect(current().storage.readingAnnotations.ranges).toHaveLength(0);
    expect(screen.queryByRole("button", { name: "Reading tools" })).toBeNull();
    mounted.unmount();
    expect(
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      }),
    ).toBe(true);
  });

  it("shows the title and the identity on the shared article sheet", () => {
    renderArticle();

    expect(
      screen.getByRole("heading", { level: 1, name: "一条河流的十年" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("article")).toHaveAccessibleName(
      "Moment发布的文章：一条河流的十年",
    );
    expect(screen.getByText("Moment")).toBeInTheDocument();
    expect(screen.getAllByText("@owner").length).toBeGreaterThan(0);
  });

  it("runs the cover edge to edge with its box reserved", () => {
    const { container } = renderArticle();
    const cover = container.querySelector("figure img");

    expect(cover).toHaveAttribute("src", mediaPreviewUrl(coverMediaId));
    expect(cover).toHaveAttribute("width", "1600");
    expect(cover).toHaveAttribute("height", "900");
    // Above the fold: it is not deferred the way a card's cover is.
    expect(cover).not.toHaveAttribute("loading");
    expect(sheetCss).toMatch(
      /\._cover_[\w-]+\s*\{[^}]*inline-size: calc\(100% \+ 2 \* var\(--article-inset\)\)/s,
    );
  });

  it("renders the structured document with Tiptap, tables and pictures included", async () => {
    const { container } = renderArticle();

    expect(
      screen.getByRole("heading", { level: 2, name: "上游" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "年份" })).toHaveAttribute(
      "colspan",
      "2",
    );
    expect(await screen.findByRole("img", { name: "入海口" })).toHaveAttribute(
      "loading",
      "lazy",
    );
    expect(container.querySelector("pre code")).not.toBeNull();
    expect(screen.getByRole("link", { name: "看这里" })).toHaveAttribute(
      "href",
      "https://example.com/x",
    );
  });

  it("styles every block the renderer can emit", () => {
    for (const selector of [
      "table",
      "pre",
      "img",
      "blockquote",
      "hr",
      "code",
    ]) {
      expect(
        contentCss,
        `the article body has no rule for <${selector}>`,
      ).toMatch(new RegExp(`${selector}[\\s,{]`));
    }
    expect(contentCss).toContain("overflow-x: auto");
  });

  it("holds the reading column to a measure a reader can follow", () => {
    expect(sheetCss).toMatch(/inline-size: min\(100%, 44\.25rem\)/);
    const { container } = renderArticle();
    expect(
      container.querySelector("[data-article-sheet] [data-content-renderer]"),
    ).toBeInTheDocument();
  });

  it("keeps the reply and share actions a post detail page has", () => {
    renderArticle();

    expect(
      screen.getByRole("button", { name: "回复这篇文章" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "分享这篇文章" }),
    ).toBeInTheDocument();
  });

  it("ends focus reading with the article author instead of discussion actions", () => {
    const post = makeArticle();
    post.commentCount = 12;
    post.author = {
      userId: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      username: "alice",
      displayName: "文章作者",
      avatarMediaId: null,
      profileUrl: "/alice",
    };
    render(
      <NavigationProvider>
        <ArticlePage post={post} site={site} focusReading />
      </NavigationProvider>,
    );
    const footer = screen.getByLabelText("文章作者");
    expect(within(footer).getByText("文章作者")).toBeInTheDocument();
    expect(within(footer).getByText("@alice")).toBeInTheDocument();
    expect(within(footer).getByRole("link")).toHaveAttribute("href", "/alice");
    expect(screen.queryByRole("button", { name: /回复这篇文章/ })).toBeNull();
    expect(screen.queryByRole("button", { name: "分享这篇文章" })).toBeNull();
    expect(document.querySelector('a[href="#comments"]')).toBeNull();
    expect(footer.closest("[data-article-sheet]")).not.toBeNull();
  });

  it("does not add a reading footer to the workspace publication preview", () => {
    render(
      <NavigationProvider>
        <ArticlePage post={makeArticle()} site={site} showActions={false} />
      </NavigationProvider>,
    );
    expect(screen.queryByLabelText("文章作者")).toBeNull();
  });

  it("drops the cover block and the lede when the article has neither", () => {
    const { container } = renderArticle(
      makeArticle({ cover: null, excerpt: "" }),
    );

    expect(container.querySelector("figure")).toBeNull();
    expect(
      screen.getByRole("heading", { level: 1, name: "一条河流的十年" }),
    ).toBeInTheDocument();
  });

  it("marks a post only its owner can read", () => {
    renderArticle({ ...makeArticle(), visibility: "only_me" });

    expect(screen.getByLabelText("仅自己可见")).toBeInTheDocument();
  });
});

describe("the post detail route", () => {
  function detailResponse(data: unknown): Response {
    return new Response(JSON.stringify({ data, meta: { request_id: "r" } }), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  }

  const wirePost = {
    id: "01K2ABCDEF0123456789ABCDE0",
    kind: "moment",
    content_md: "今天下了一场雨。",
    article: null,
    content_html: "<p>今天下了一场雨。</p>",
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    reposted_at: null,
    visibility: "everyone",
    timeline_item_kind: "original",
    quoted_post: null,
    comment_count: 0,
    media: [],
    link_cards: [],
  };

  const wireArticle = {
    ...wirePost,
    kind: "article",
    content_md: null,
    article: {
      title: "一条河流的十年",
      excerpt: "从上游走到入海口。",
      cover: null,
      body,
      body_schema_version: 1,
    },
    content_html: "<h2>上游</h2>",
  };

  function renderDetail(data: unknown) {
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("/comments")
        ? new Response(
            JSON.stringify({
              data: { items: [] },
              meta: { next_cursor: null, request_id: "r" },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          )
        : detailResponse(data),
    );
    render(
      <FeedbackProvider>
        <SessionProvider>
          <NavigationProvider>
            <DetailPage
              api={new PublicApi(fetcher as typeof fetch)}
              id="01K2ABCDEF0123456789ABCDE0"
              site={site}
            />
          </NavigationProvider>
        </SessionProvider>
      </FeedbackProvider>,
    );
  }

  it("opens an article on the article page", async () => {
    renderDetail(wireArticle);

    await waitFor(() =>
      expect(
        screen.getByRole("heading", { level: 1, name: "一条河流的十年" }),
      ).toBeInTheDocument(),
    );
    expect(screen.getByRole("article")).toHaveAccessibleName(
      "Moment发布的文章：一条河流的十年",
    );
  });

  it("leaves a moment on the post page", async () => {
    renderDetail(wirePost);

    await waitFor(() =>
      expect(screen.getByText("今天下了一场雨。")).toBeInTheDocument(),
    );
    expect(screen.queryByText("文章")).toBeNull();
    expect(screen.queryByRole("button", { name: "Search" })).toBeNull();
    expect(
      screen.queryByRole("toolbar", { name: "Reading annotations" }),
    ).toBeNull();
    expect(
      fireEvent.keyDown(document.body, {
        key: "f",
        code: "KeyF",
        metaKey: true,
      }),
    ).toBe(true);
  });
});
