import { describe, expect, it, vi } from "vitest";

import { PublicApiError, type ArticleDocument } from "../../api/public";
import {
  ArticleDraftApi,
  articleDraftErrorMessage,
  isDraftLimitError,
  isDraftMissingError,
  isStaleDraftError,
  parseArticleDraft,
  staleDraftUpdatedAt,
} from "./articleDrafts";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function success(data: unknown, status = 200): Response {
  return json({ data, meta: { request_id: "req" } }, status);
}

function failure(
  code: string,
  status: number,
  details: unknown = {},
): Response {
  return json(
    { error: { code, message: "no", details, request_id: "req" } },
    status,
  );
}

const body: ArticleDocument = {
  schema_version: 1,
  type: "doc",
  content: [{ type: "paragraph", content: [{ type: "text", text: "上游" }] }],
};

/** mirrors postdto.ArticleDraft (internal/transport/postdto/dto.go) */
const wire = {
  id: "01K2ABCDEF0123456789ABCDE0",
  title: "一条河流的十年",
  body,
  body_schema_version: 1,
  cover_media_id: "01K2ABCDEF0123456789ABCDE7",
  published_entry_id: null,
  created_at: "2026-09-25T01:00:00Z",
  updated_at: "2026-09-25T02:00:00.123Z",
};

describe("reading a draft off the wire", () => {
  it("refuses a row with a field the server does not send", () => {
    expect(() => parseArticleDraft({ ...wire, extra: 1 })).toThrow(
      PublicApiError,
    );
  });

  it("refuses a row that is missing one", () => {
    const rest: Record<string, unknown> = { ...wire };
    delete rest.published_entry_id;
    expect(() => parseArticleDraft(rest)).toThrow(PublicApiError);
  });

  it("carries the body through the same grammar the reader uses", () => {
    expect(() =>
      parseArticleDraft({
        ...wire,
        body: { ...body, content: [{ type: "unknownNode" }] },
      }),
    ).toThrow(PublicApiError);
  });

  it("keeps an untitled draft, which is what a new one is", () => {
    expect(parseArticleDraft({ ...wire, title: "" }).title).toBe("");
  });
});

describe("the draft endpoints", () => {
  it("reads the list as the bare array the server answers with", async () => {
    const fetcher = vi.fn().mockResolvedValue(success([wire]));
    const drafts = await new ArticleDraftApi(fetcher).list();
    expect(fetcher).toHaveBeenCalledWith(
      "/api/v1/articles/drafts",
      expect.objectContaining({ method: "GET" }),
    );
    expect(drafts).toHaveLength(1);
    expect(drafts[0]).not.toHaveProperty("body");
    expect(drafts[0]).not.toHaveProperty("coverMediaId");
  });

  it("creates without a revision, because there is nothing to be stale against", async () => {
    const fetcher = vi.fn().mockResolvedValue(success(wire, 201));
    await new ArticleDraftApi(fetcher).create("csrf", {
      title: "标题",
      body: { schema_version: 1, type: "doc", content: [] },
      coverMediaId: null,
    });
    const [, init] = fetcher.mock.calls[0];
    expect(init.method).toBe("POST");
    expect(new Headers(init.headers).get("X-CSRF-Token")).toBe("csrf");
    expect(JSON.parse(init.body)).toEqual({
      title: "标题",
      body: { schema_version: 1, type: "doc", content: [] },
      cover_media_id: null,
    });
  });

  it("echoes the revision it read back, exactly as the server spelled it", async () => {
    const fetcher = vi.fn().mockResolvedValue(success(wire));
    await new ArticleDraftApi(fetcher).replace(
      "csrf",
      wire.id,
      { title: "标题", body, coverMediaId: null },
      "2026-09-25T02:00:00.123Z",
    );
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe(`/api/v1/articles/drafts/${wire.id}`);
    expect(init.method).toBe("PUT");
    expect(JSON.parse(init.body).expected_updated_at).toBe(
      "2026-09-25T02:00:00.123Z",
    );
  });

  it("sends the idempotency key on a publish", async () => {
    const post = {
      id: "01K2ABCDEF0123456789ABCDE9",
      kind: "article",
      content_md: null,
      article: {
        title: "一条河流的十年",
        excerpt: "从上游走到入海口。",
        cover: null,
        body,
        body_schema_version: 1,
      },
      content_html: "<p>上游</p>",
      created_at: "2026-09-25T02:00:00Z",
      updated_at: "2026-09-25T02:00:00Z",
      published_at: "2026-09-25T02:00:00Z",
      reposted_at: null,
      visibility: "everyone",
      timeline_item_kind: "original",
      quoted_post: null,
      comment_count: 0,
      media: [],
      link_cards: [],
    };
    const fetcher = vi.fn().mockResolvedValue(success(post, 201));
    const published = await new ArticleDraftApi(fetcher).publish(
      "csrf",
      wire.id,
      "web-article-1",
      "only_me",
    );
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe(`/api/v1/articles/drafts/${wire.id}/publish`);
    expect(new Headers(init.headers).get("Idempotency-Key")).toBe(
      "web-article-1",
    );
    expect(JSON.parse(init.body)).toEqual({ visibility: "only_me" });
    expect(published.id).toBe(post.id);
  });

  it("refuses to call a deletion done when the server did not confirm it", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(success({ id: "somebody else", discarded: true }));
    await expect(
      new ArticleDraftApi(fetcher).discard("csrf", wire.id),
    ).rejects.toThrow(PublicApiError);
  });
});

describe("what a refusal is read as", () => {
  it("names a stale draft and the revision the server now holds", async () => {
    const fetcher = vi.fn().mockResolvedValue(
      failure("ARTICLE_DRAFT_STALE", 409, {
        id: wire.id,
        updated_at: "2026-09-25T03:00:00Z",
      }),
    );
    const failed = await new ArticleDraftApi(fetcher)
      .replace(
        "csrf",
        wire.id,
        { title: "", body, coverMediaId: null },
        "2026-09-25T02:00:00.123Z",
      )
      .catch((error: unknown) => error);
    expect(isStaleDraftError(failed)).toBe(true);
    expect(staleDraftUpdatedAt(failed)).toBe("2026-09-25T03:00:00Z");
    expect(articleDraftErrorMessage(failed)).toBe("这份草稿在别处改过了。");
  });

  it("says how many drafts there may be when the server says there are too many", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(failure("ARTICLE_DRAFT_LIMIT", 422, { limit: 50 }));
    const failed = await new ArticleDraftApi(fetcher)
      .create("csrf", {
        title: "",
        body: { schema_version: 1, type: "doc", content: [] },
        coverMediaId: null,
      })
      .catch((error: unknown) => error);
    expect(isDraftLimitError(failed)).toBe(true);
    expect(articleDraftErrorMessage(failed)).toBe(
      "草稿已经有 50 份，删掉一些再新建。",
    );
  });

  it("recognises a draft that is no longer there", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValue(failure("ARTICLE_DRAFT_NOT_FOUND", 404));
    const failed = await new ArticleDraftApi(fetcher)
      .read(wire.id)
      .catch((error: unknown) => error);
    expect(isDraftMissingError(failed)).toBe(true);
  });

  it("says something an owner can act on rather than repeating the server", () => {
    expect(
      articleDraftErrorMessage(
        new PublicApiError("post storage operation failed", {
          code: "INTERNAL",
          status: 500,
        }),
      ),
    ).toBe("保存没有成功。");
    expect(articleDraftErrorMessage(new TypeError("offline"))).toBe(
      "没能连上服务器。",
    );
  });
});
