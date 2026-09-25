import { describe, expect, it, vi } from "vitest";

import { PublicApi, PublicApiError } from "../../api/public";
import {
  articleCoverUrl,
  articleHref,
  listArticles,
  readArticle,
} from "./articleApi";

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const moment = {
  id: "01K2ABCDEF0123456789ABCDE1",
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

const body = {
  schema_version: 1,
  type: "doc",
  content: [{ type: "paragraph", content: [{ type: "text", text: "上游" }] }],
};

/** A list row: the card, and neither the body nor the HTML rendered from it. */
const articleCard = {
  ...moment,
  id: "01K2ABCDEF0123456789ABCDE0",
  kind: "article",
  content_md: null,
  article: {
    title: "一条河流的十年",
    excerpt: "从上游走到入海口。",
    cover: null,
    body: null,
    body_schema_version: null,
  },
  content_html: "",
};

/** A detail row: both arrive. */
const articleDetail = {
  ...articleCard,
  article: { ...articleCard.article, body, body_schema_version: 1 },
  content_html: "<p>上游</p>",
};

function timelineApi(items: unknown[]) {
  const fetcher = vi.fn<(input: RequestInfo | URL) => Promise<Response>>(
    async () =>
      json({
        data: { items },
        meta: {
          next_cursor: null,
          prev_cursor: null,
          request_id: "req_posts",
        },
      }),
  );
  return { fetcher, api: new PublicApi(fetcher as typeof fetch) };
}

describe("reading articles", () => {
  it("asks the timeline for articles and nothing else", async () => {
    const { fetcher, api } = timelineApi([articleCard]);

    const page = await listArticles(api, { limit: 5 });

    expect(page.items).toHaveLength(1);
    expect(page.items[0].article.title).toBe("一条河流的十年");
    const url = String(fetcher.mock.calls[0][0]);
    expect(url).toContain("kind=article");
    expect(url).toContain("limit=5");
  });

  it("refuses a page of articles that answered with a moment", async () => {
    const { api } = timelineApi([articleCard, moment]);

    await expect(listArticles(api)).rejects.toBeInstanceOf(PublicApiError);
  });

  it("carries the cursors the timeline answered with", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: { items: [] },
        meta: {
          next_cursor: "older",
          prev_cursor: "newer",
          request_id: "req_posts",
        },
      }),
    );

    await expect(
      listArticles(new PublicApi(fetcher as typeof fetch)),
    ).resolves.toEqual({ items: [], nextCursor: "older", prevCursor: "newer" });
  });

  it("reads one article in full", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({ data: articleDetail, meta: {} }),
      ) as typeof fetch,
    );

    const post = await readArticle(api, articleDetail.id);

    expect(post.article.body.schema_version).toBe(1);
    expect(post.article.bodySchemaVersion).toBe(1);
    expect(post.contentHtml).toBe("<p>上游</p>");
  });

  it("refuses to read a moment, and an article with no body behind it", async () => {
    const momentApi = new PublicApi(
      vi.fn(async () => json({ data: moment, meta: {} })) as typeof fetch,
    );
    await expect(readArticle(momentApi, moment.id)).rejects.toBeInstanceOf(
      PublicApiError,
    );

    const cardApi = new PublicApi(
      vi.fn(async () => json({ data: articleCard, meta: {} })) as typeof fetch,
    );
    await expect(readArticle(cardApi, articleCard.id)).rejects.toBeInstanceOf(
      PublicApiError,
    );
  });

  it("sends an article to the post detail address it shares", () => {
    expect(articleHref("01K2ABCDEF0123456789ABCDE0")).toBe(
      "/m/01K2ABCDEF0123456789ABCDE0",
    );
    expect(articleHref("a/b?c")).toBe("/m/a%2Fb%3Fc");
  });

  it("loads a cover through the managed preview every picture uses", () => {
    expect(
      articleCoverUrl({
        mediaId: "01K2ABCDEF0123456789ABCDE7",
        mimeType: "image/jpeg",
        width: 1600,
        height: 900,
      }),
    ).toContain("/api/v1/media/01K2ABCDEF0123456789ABCDE7/preview");
  });
});
