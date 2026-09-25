import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
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
import { OwnerProvider } from "../owner/OwnerProvider";
import { DetailPage } from "../timeline/DetailPage";
import { ArticlePage } from "./ArticlePage";
import articleCss from "./Article.module.css?inline";

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
  it("leads with the title, the mark and the identity that wrote it", () => {
    renderArticle();

    expect(
      screen.getByRole("heading", { level: 1, name: "一条河流的十年" }),
    ).toBeInTheDocument();
    expect(screen.getByText("文章")).toBeInTheDocument();
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
    expect(articleCss).toMatch(
      /\._hero_[\w-]+\s*\{[^}]*margin:[^;]*calc\(0px - var\(--article-bleed\)\)/s,
    );
  });

  it("renders the HTML the server produced, tables and pictures included", () => {
    const { container } = renderArticle();

    expect(
      screen.getByRole("heading", { level: 2, name: "上游" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("table")).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: "年份" })).toHaveAttribute(
      "colspan",
      "2",
    );
    expect(screen.getByRole("img", { name: "入海口" })).toHaveAttribute(
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
        articleCss,
        `the article body has no rule for <${selector}>`,
      ).toMatch(new RegExp(`\\._body_[\\w-]+ ${selector}[\\s,{]`));
    }
    expect(articleCss).toMatch(
      /\._body_[\w-]+ table\s*\{[^}]*overflow-x: auto/s,
    );
  });

  it("holds the reading column to a measure a reader can follow", () => {
    // 47.5rem is 760px, the top of the range this page was specified at; the
    // column it sits in may still be narrower, which `min` handles.
    expect(articleCss).toMatch(/--article-measure:\s*47\.5rem/);
    expect(articleCss).toMatch(
      /\._column_[\w-]+\s*\{[^}]*inline-size: min\(100%, var\(--article-measure\)\)/s,
    );
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
        <OwnerProvider>
          <NavigationProvider>
            <DetailPage
              api={new PublicApi(fetcher as typeof fetch)}
              id="01K2ABCDEF0123456789ABCDE0"
              site={site}
            />
          </NavigationProvider>
        </OwnerProvider>
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
    expect(screen.getByText("文章")).toBeInTheDocument();
  });

  it("leaves a moment on the post page", async () => {
    renderDetail(wirePost);

    await waitFor(() =>
      expect(screen.getByText("今天下了一场雨。")).toBeInTheDocument(),
    );
    expect(screen.queryByText("文章")).toBeNull();
  });
});
