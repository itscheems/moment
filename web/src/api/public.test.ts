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
          meta: {
            instance_id: "0123456789abcdef0123456789abcdef",
            request_id: "req_site",
          },
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
      instanceId: "0123456789abcdef0123456789abcdef",
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

  it("gracefully disables instance-scoped features when site meta omits the runtime ID", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            title: "Moment",
            bio: "",
            timezone: "UTC",
            public_base_url: null,
            avatar_media_id: null,
            comments_enabled: true,
          },
          meta: { request_id: "req_site" },
        }),
      ) as typeof fetch,
    );
    await expect(api.site()).resolves.toMatchObject({ instanceId: null });
  });

  it("rejects a forged or malformed runtime instance ID", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            title: "Moment",
            bio: "",
            timezone: "UTC",
            public_base_url: null,
            avatar_media_id: null,
            comments_enabled: true,
          },
          meta: { instance_id: "from-host-header" },
        }),
      ) as typeof fetch,
    );
    await expect(api.site()).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
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

  it.each([
    "2026-08-10Z",
    "2026-02-30T01:02:03Z",
    "2026-13-01T01:02:03Z",
    "2026-08-10T24:02:03Z",
    "2026-08-10T01:60:03Z",
    "2026-08-10T01:02:60Z",
    "2026-08-10T01:02:03+00:00",
  ])("rejects a non-RFC3339 UTC instant: %s", async (publishedAt) => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: { items: [{ ...post, published_at: publishedAt }] },
          meta: { next_cursor: null },
        }),
      ) as typeof fetch,
    );

    await expect(api.timeline()).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("accepts valid UTC instants, including leap day and nanoseconds", async () => {
    const publishedAt = "2024-02-29T23:59:59.123456789Z";
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: { items: [{ ...post, published_at: publishedAt }] },
          meta: { next_cursor: null },
        }),
      ) as typeof fetch,
    );

    await expect(api.timeline()).resolves.toMatchObject({
      items: [{ publishedAt }],
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
