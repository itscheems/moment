import { describe, expect, it, vi } from "vitest";

import { PublicApi, PublicApiError } from "./public";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const post = {
  id: "01K2ABCDEF0123456789ABCDE0",
  content_md: "今天下了一场雨。",
  content_html: "<p>今天下了一场雨。</p>",
  created_at: "2026-08-10T01:02:03.000Z",
  updated_at: "2026-08-10T01:02:03.000Z",
  published_at: "2026-08-10T01:02:03.000Z",
};

describe("public API client", () => {
  it("maps site identity and preserves opaque timeline cursors", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/site") {
        return json({
          data: {
            title: "乔乔的 Moment",
            bio: "一些正在发生的事",
            timezone: "Asia/Shanghai",
            public_base_url: null,
            avatar_media_id: null,
            comments_enabled: true,
          },
          meta: { request_id: "req_site" },
        });
      }
      return json({
        data: { items: [post] },
        meta: { next_cursor: "opaque+/=cursor", request_id: "req_posts" },
      });
    });
    const api = new PublicApi(fetcher as typeof fetch);

    await expect(api.site()).resolves.toMatchObject({
      title: "乔乔的 Moment",
      timezone: "Asia/Shanghai",
    });
    await expect(
      api.timeline({ cursor: "opaque+/=cursor", from: "2026-08-01" }),
    ).resolves.toEqual({
      items: [
        {
          id: post.id,
          contentMd: post.content_md,
          contentHtml: post.content_html,
          createdAt: post.created_at,
          updatedAt: post.updated_at,
          publishedAt: post.published_at,
        },
      ],
      nextCursor: "opaque+/=cursor",
    });
    expect(fetcher).toHaveBeenLastCalledWith(
      expect.stringContaining("cursor=opaque%2B%2F%3Dcursor"),
      expect.objectContaining({ headers: { Accept: "application/json" } }),
    );
  });

  it("rejects malformed success envelopes and duplicate items", async () => {
    const malformed = new PublicApi(
      vi.fn(async () =>
        json({ data: { items: [post] }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(malformed.timeline()).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const duplicate = new PublicApi(
      vi.fn(async () =>
        json({
          data: { items: [post, post] },
          meta: { next_cursor: null },
        }),
      ) as typeof fetch,
    );
    await expect(duplicate.timeline()).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("surfaces stable public errors without parsing message text", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json(
          {
            error: {
              code: "POST_NOT_FOUND",
              message: "may change",
              details: {},
              request_id: "req_missing",
            },
          },
          404,
        ),
      ) as typeof fetch,
    );
    const error = await api.post(post.id).catch((value: unknown) => value);
    expect(error).toBeInstanceOf(PublicApiError);
    expect(error).toMatchObject({
      code: "POST_NOT_FOUND",
      status: 404,
      requestId: "req_missing",
    });
  });
});
