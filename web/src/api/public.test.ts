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
  media: [],
};

const rootComment = {
  id: "01K2ABCDEF0123456789ABCDE1",
  post_id: post.id,
  parent_id: null,
  depth: 0,
  author_kind: "guest",
  author_name: "访客",
  content_md: "第一条 **回应**",
  content_html: "<p>第一条 <strong>回应</strong></p>\n",
  created_at: "2026-08-10T01:03:00Z",
};

const replyComment = {
  ...rootComment,
  id: "01K2ABCDEF0123456789ABCDE2",
  parent_id: rootComment.id,
  depth: 1,
  author_kind: "owner",
  author_name: "Owner",
  content_md: "一级回复",
  content_html: "<p>一级回复</p>\n",
  created_at: "2026-08-10T01:04:00Z",
};

describe("public API client", () => {
  it("preserves canonical ordered media and all nullable alt states", async () => {
    const usages = [
      {
        media_id: "01K2ABCDEF0123456789ABCDE1",
        position: 0,
        alt_text: null,
      },
      {
        media_id: "01K2ABCDEF0123456789ABCDE1",
        position: 1,
        alt_text: "",
      },
      {
        media_id: "01K2ABCDEF0123456789ABCDE2",
        position: 2,
        alt_text: "  雨后的街道  ",
      },
    ];
    const api = new PublicApi(
      vi.fn(async () =>
        json({ data: { ...post, media: usages }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(api.post(post.id)).resolves.toMatchObject({
      media: [
        { mediaId: usages[0].media_id, position: 0, altText: null },
        { mediaId: usages[1].media_id, position: 1, altText: "" },
        { mediaId: usages[2].media_id, position: 2, altText: "  雨后的街道  " },
      ],
    });
  });

  it.each([
    {
      name: "missing media array",
      value: (() => {
        const value = { ...post } as Record<string, unknown>;
        delete value.media;
        return value;
      })(),
    },
    {
      name: "position gap",
      value: {
        ...post,
        media: [
          {
            media_id: "01K2ABCDEF0123456789ABCDE1",
            position: 1,
            alt_text: null,
          },
        ],
      },
    },
    {
      name: "missing alt field",
      value: {
        ...post,
        media: [{ media_id: "01K2ABCDEF0123456789ABCDE1", position: 0 }],
      },
    },
    {
      name: "blank provided alt",
      value: {
        ...post,
        media: [
          {
            media_id: "01K2ABCDEF0123456789ABCDE1",
            position: 0,
            alt_text: "   ",
          },
        ],
      },
    },
    {
      name: "overlong code-point alt",
      value: {
        ...post,
        media: [
          {
            media_id: "01K2ABCDEF0123456789ABCDE1",
            position: 0,
            alt_text: "🙂".repeat(1_001),
          },
        ],
      },
    },
  ])("rejects non-canonical post media: $name", async ({ value }) => {
    const api = new PublicApi(
      vi.fn(async () => json({ data: value, meta: {} })) as typeof fetch,
    );
    await expect(api.post(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

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
          media: [],
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

  it("validates the canonical flat thread and sends the real guest form contract", async () => {
    const fetcher = vi.fn(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "POST") {
          return json(
            {
              data: {
                ...replyComment,
                author_kind: "guest",
                author_name: "访客",
              },
              meta: { replayed: false, request_id: "req_create" },
            },
            201,
          );
        }
        return json({
          data: { items: [rootComment, replyComment] },
          meta: { request_id: "req_comments" },
        });
      },
    );
    const api = new PublicApi(fetcher as typeof fetch);

    await expect(api.comments(post.id)).resolves.toMatchObject({
      items: [
        { id: rootComment.id, depth: 0, parentId: null },
        { id: replyComment.id, depth: 1, parentId: rootComment.id },
      ],
    });
    await expect(
      api.createGuestComment(
        post.id,
        {
          authorName: "访客",
          contentMd: "一级回复",
          parentId: rootComment.id,
        },
        "web-comment-retry",
      ),
    ).resolves.toMatchObject({
      comment: { id: replyComment.id },
      replayed: false,
    });

    const mutation = fetcher.mock.calls[1][1] as RequestInit;
    expect(JSON.parse(String(mutation.body))).toEqual({
      author_name: "访客",
      content_md: "一级回复",
      parent_id: rootComment.id,
      website: "",
    });
    expect(new Headers(mutation.headers).get("Idempotency-Key")).toBe(
      "web-comment-retry",
    );
  });

  it.each([
    {
      name: "reply without adjacent root",
      items: [replyComment],
    },
    {
      name: "depth mismatch",
      items: [{ ...rootComment, depth: 1 }],
    },
    {
      name: "unsafe HTML",
      items: [
        {
          ...rootComment,
          content_html: "<p>safe</p><img src=x onerror=alert(1)>",
        },
      ],
    },
    {
      name: "non-UTC time",
      items: [{ ...rootComment, created_at: "2026-08-10T09:03:00+08:00" }],
    },
    {
      name: "wrong post",
      items: [{ ...rootComment, post_id: "01K2ABCDEF0123456789ABCDE9" }],
    },
  ])("rejects malformed comment success data: $name", async ({ items }) => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({ data: { items }, meta: { request_id: "req_bad" } }),
      ) as typeof fetch,
    );
    await expect(api.comments(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("requires matching Retry-After header and error details", async () => {
    const limited = (header: string, detail: number) =>
      new Response(
        JSON.stringify({
          error: {
            code: "RATE_LIMITED",
            message: "slow down",
            details: { retry_after_seconds: detail },
            request_id: "req_rate",
          },
        }),
        { status: 429, headers: { "Retry-After": header } },
      );
    const valid = new PublicApi(
      vi.fn(async () => limited("7", 7)) as typeof fetch,
    );
    await expect(
      valid.createGuestComment(
        post.id,
        { authorName: "访客", contentMd: "回应", parentId: null },
        "web-comment-rate",
      ),
    ).rejects.toMatchObject({
      code: "RATE_LIMITED",
      retryAfterSeconds: 7,
    });

    const malformed = new PublicApi(
      vi.fn(async () => limited("7", 8)) as typeof fetch,
    );
    await expect(
      malformed.createGuestComment(
        post.id,
        { authorName: "访客", contentMd: "回应", parentId: null },
        "web-comment-rate",
      ),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it("requires create status and replay metadata to agree", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json(
          {
            data: rootComment,
            meta: { replayed: true, request_id: "req_create" },
          },
          201,
        ),
      ) as typeof fetch,
    );
    await expect(
      api.createGuestComment(
        post.id,
        { authorName: "访客", contentMd: "回应", parentId: null },
        "web-comment-replay",
      ),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it.each([
    {
      name: "extra success envelope field",
      response: json({
        data: { items: [rootComment] },
        meta: { request_id: "req_comments" },
        unexpected: true,
      }),
    },
    {
      name: "extra comment metadata",
      response: json({
        data: { items: [rootComment] },
        meta: { request_id: "req_comments", next_cursor: null },
      }),
    },
    {
      name: "non-JSON media type",
      response: new Response(
        JSON.stringify({
          data: { items: [rootComment] },
          meta: { request_id: "req_comments" },
        }),
        { status: 200, headers: { "Content-Type": "text/plain" } },
      ),
    },
    {
      name: "wrong list success status",
      response: json(
        {
          data: { items: [rootComment] },
          meta: { request_id: "req_comments" },
        },
        201,
      ),
    },
  ])(
    "rejects malformed comment success transport: $name",
    async ({ response }) => {
      const api = new PublicApi(
        vi.fn(async () => response.clone()) as typeof fetch,
      );
      await expect(api.comments(post.id)).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    },
  );

  it.each([
    { status: 200, replayed: false },
    { status: 201, replayed: true },
    { status: 202, replayed: false },
  ])(
    "rejects comment create HTTP $status with replayed=$replayed",
    async ({ status, replayed }) => {
      const api = new PublicApi(
        vi.fn(async () =>
          json(
            {
              data: rootComment,
              meta: { replayed, request_id: "req_create" },
            },
            status,
          ),
        ) as typeof fetch,
      );
      await expect(
        api.createGuestComment(
          post.id,
          { authorName: "访客", contentMd: "回应", parentId: null },
          "web-comment-status",
        ),
      ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
    },
  );

  it.each([
    {
      name: "forged owner identity",
      comment: { ...rootComment, author_kind: "owner", author_name: "Owner" },
    },
    {
      name: "different parent",
      comment: { ...rootComment, parent_id: replyComment.id, depth: 1 },
    },
    {
      name: "different normalized guest name",
      comment: { ...rootComment, author_name: "另一个人" },
    },
  ])("rejects a guest create response with $name", async ({ comment }) => {
    const api = new PublicApi(
      vi.fn(async () =>
        json(
          {
            data: comment,
            meta: { replayed: false, request_id: "req_create" },
          },
          201,
        ),
      ) as typeof fetch,
    );
    await expect(
      api.createGuestComment(
        post.id,
        { authorName: "访客", contentMd: "回应", parentId: null },
        "web-comment-identity",
      ),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });
});
