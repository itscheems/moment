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
  reposted_at: null,
  timeline_item_kind: "original",
  quoted_post: null,
  comment_count: 0,
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
  media: [],
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
  it("maps the non-recursive quoted Moment projection", async () => {
    const quoted = {
      ...post,
      content_md: "写一点新的想法",
      content_html: "<p>写一点新的想法</p>",
      quoted_post: {
        unavailable: false,
        id: "01K2ABCDEF0123456789ABCDE9",
        content_md: "被引用的正文",
        content_html: "<p>被引用的正文</p>",
        published_at: "2026-08-09T01:02:03.000Z",
        media: [],
        location: {
          name: "陆悦天地",
          display_name: "上海市浦东新区 · 陆悦天地",
        },
      },
    };
    const api = new PublicApi(
      vi.fn(async () => json({ data: quoted, meta: {} })) as typeof fetch,
    );

    await expect(api.post(quoted.id)).resolves.toMatchObject({
      contentMd: "写一点新的想法",
      quotedPost: {
        unavailable: false,
        id: "01K2ABCDEF0123456789ABCDE9",
        contentMd: "被引用的正文",
        location: { name: "陆悦天地" },
      },
    });
  });

  it("maps an unavailable quote only when every deleted target field is null", async () => {
    const tombstone = {
      ...post,
      content_md: "引用外层仍然存在",
      content_html: "<p>引用外层仍然存在</p>",
      quoted_post: {
        unavailable: true,
        id: null,
        content_md: null,
        content_html: null,
        published_at: null,
        media: null,
        location: null,
      },
    };
    const api = new PublicApi(
      vi.fn(async () => json({ data: tombstone, meta: {} })) as typeof fetch,
    );
    await expect(api.post(tombstone.id)).resolves.toMatchObject({
      contentMd: "引用外层仍然存在",
      quotedPost: { unavailable: true },
    });

    const invalidApi = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            ...tombstone,
            quoted_post: { ...tombstone.quoted_post, id: post.id },
          },
          meta: {},
        }),
      ) as typeof fetch,
    );
    await expect(invalidApi.post(tombstone.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("requires a non-negative canonical comment count", async () => {
    const missing = { ...post } as Record<string, unknown>;
    delete missing.comment_count;
    const missingApi = new PublicApi(
      vi.fn(async () => json({ data: missing, meta: {} })) as typeof fetch,
    );
    await expect(missingApi.post(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const negativeApi = new PublicApi(
      vi.fn(async () =>
        json({ data: { ...post, comment_count: -1 }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(negativeApi.post(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("preserves canonical ordered media and all nullable alt states", async () => {
    const usages = [
      {
        media_id: "01K2ABCDEF0123456789ABCDE1",
        position: 0,
        alt_text: null,
        mime_type: "image/jpeg",
        width: 1_200,
        height: 800,
      },
      {
        media_id: "01K2ABCDEF0123456789ABCDE1",
        position: 1,
        alt_text: "",
        mime_type: "image/png",
        width: 800,
        height: 1_200,
      },
      {
        media_id: "01K2ABCDEF0123456789ABCDE2",
        position: 2,
        alt_text: "  雨后的街道  ",
        mime_type: "image/webp",
        width: 1_600,
        height: 900,
      },
    ];
    const api = new PublicApi(
      vi.fn(async () =>
        json({ data: { ...post, media: usages }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(api.post(post.id)).resolves.toMatchObject({
      media: [
        {
          mediaId: usages[0].media_id,
          position: 0,
          altText: null,
          width: 1_200,
          height: 800,
        },
        {
          mediaId: usages[1].media_id,
          position: 1,
          altText: "",
          width: 800,
          height: 1_200,
        },
        {
          mediaId: usages[2].media_id,
          position: 2,
          altText: "  雨后的街道  ",
          width: 1_600,
          height: 900,
        },
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
            width: 800,
            height: 600,
          },
        ],
      },
    },
    {
      name: "missing alt field",
      value: {
        ...post,
        media: [
          {
            media_id: "01K2ABCDEF0123456789ABCDE1",
            position: 0,
            width: 800,
            height: 600,
          },
        ],
      },
    },
    {
      name: "missing width",
      value: {
        ...post,
        media: [
          {
            media_id: "01K2ABCDEF0123456789ABCDE1",
            position: 0,
            alt_text: null,
            height: 600,
          },
        ],
      },
    },
    {
      name: "zero height",
      value: {
        ...post,
        media: [
          {
            media_id: "01K2ABCDEF0123456789ABCDE1",
            position: 0,
            alt_text: null,
            width: 800,
            height: 0,
          },
        ],
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
            width: 800,
            height: 600,
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
            width: 800,
            height: 600,
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
            owner_id: "00000000000000000000000000000000",
            username: "owner",
            display_name: "乔乔",
            title: "乔乔的 Moment",
            bio: "一些正在发生的事",
            timezone: "Asia/Shanghai",
            public_base_url: null,
            avatar_media_id: null,
            cover_media_id: null,
            pinned_post_id: null,
            comments_enabled: true,
          },
          meta: {
            instance_id: "0123456789abcdef0123456789abcdef",
            instance_initialized_at: "2026-08-01T09:08:00Z",
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
      instanceInitializedAt: "2026-08-01T09:08:00Z",
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
          repostedAt: post.reposted_at,
          timelineItemKind: "original",
          commentCount: post.comment_count,
          media: [],
          location: null,
          quotedPost: null,
        },
      ],
      nextCursor: "opaque+/=cursor",
    });
    expect(fetcher).toHaveBeenLastCalledWith(
      expect.stringContaining("cursor=opaque%2B%2F%3Dcursor"),
      expect.objectContaining({ headers: { Accept: "application/json" } }),
    );
  });

  it("maps search hits, complete year counts, and encoded cursor filters", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: {
          items: [{ ...post, snippet: "旧城雨落在街口" }],
          year_counts: [
            { year: 2026, count: 3 },
            { year: 2024, count: 1 },
          ],
        },
        meta: { next_cursor: "opaque+/=cursor", request_id: "req_search" },
      }),
    );
    const api = new PublicApi(fetcher as typeof fetch);

    await expect(
      api.search({
        query: "旧城 雨",
        from: "2024-01-01",
        to: "2027-01-01",
        cursor: "opaque+/=before",
      }),
    ).resolves.toEqual({
      items: [
        {
          post: expect.objectContaining({ id: post.id }),
          snippet: "旧城雨落在街口",
        },
      ],
      yearCounts: [
        { year: 2026, count: 3 },
        { year: 2024, count: 1 },
      ],
      nextCursor: "opaque+/=cursor",
    });
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringMatching(
        /\/api\/v1\/search\?q=%E6%97%A7%E5%9F%8E\+%E9%9B%A8&limit=20&cursor=opaque%2B%2F%3Dbefore&from=2024-01-01&to=2027-01-01/,
      ),
      expect.objectContaining({ signal: undefined }),
    );
  });

  it("rejects malformed search and archive aggregate ordering", async () => {
    const badSearch = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [{ ...post, snippet: "match" }],
            year_counts: [
              { year: 2025, count: 1 },
              { year: 2026, count: 1 },
            ],
          },
          meta: { next_cursor: null, request_id: "req_search" },
        }),
      ) as typeof fetch,
    );
    await expect(badSearch.search({ query: "match" })).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const badArchive = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [
              { year: 2026, month: 7, count: 1 },
              { year: 2026, month: 8, count: 1 },
            ],
          },
          meta: { request_id: "req_archive" },
        }),
      ) as typeof fetch,
    );
    await expect(badArchive.archiveMonths()).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("maps archive month counts and validates the requested calendar", async () => {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url.startsWith("/api/v1/archive/months")) {
        return json({
          data: {
            items: [
              { year: 2026, month: 8, count: 4 },
              { year: 2025, month: 12, count: 2 },
            ],
          },
          meta: { request_id: "req_archive" },
        });
      }
      return json({
        data: {
          month: "2026-08",
          days: [
            { day: 2, count: 1 },
            { day: 31, count: 3 },
          ],
        },
        meta: { request_id: "req_archive" },
      });
    });
    const api = new PublicApi(fetcher as typeof fetch);
    await expect(api.archiveMonths()).resolves.toEqual({
      items: [
        { year: 2026, month: 8, count: 4 },
        { year: 2025, month: 12, count: 2 },
      ],
    });
    await expect(api.archiveMonth("2026-08")).resolves.toEqual({
      month: "2026-08",
      days: [
        { day: 2, count: 1 },
        { day: 31, count: 3 },
      ],
    });
  });

  it("requires canonical HTTP, JSON, envelope, and metadata for discovery reads", async () => {
    const endpoints: Array<{
      call: (api: PublicApi) => Promise<unknown>;
      data: unknown;
      meta: Record<string, unknown>;
    }> = [
      {
        call: (api) => api.search({ query: "雨" }),
        data: { items: [], year_counts: [] },
        meta: { request_id: "req_search", next_cursor: null },
      },
      {
        call: (api) => api.archiveMonths(),
        data: { items: [] },
        meta: { request_id: "req_archive" },
      },
      {
        call: (api) => api.archiveMonth("2026-08"),
        data: { month: "2026-08", days: [] },
        meta: { request_id: "req_archive" },
      },
    ];

    for (const endpoint of endpoints) {
      const envelope = { data: endpoint.data, meta: endpoint.meta };
      const missingRequestId = Object.fromEntries(
        Object.entries(endpoint.meta).filter(([key]) => key !== "request_id"),
      );
      const responses = [
        json(envelope, 201),
        new Response(JSON.stringify(envelope), {
          status: 200,
          headers: { "Content-Type": "text/plain" },
        }),
        json({ ...envelope, extra: true }),
        json({
          ...envelope,
          meta: { ...endpoint.meta, request_id: "" },
        }),
        json({ ...envelope, meta: missingRequestId }),
        json({ ...envelope, meta: { ...endpoint.meta, extra: true } }),
      ];
      for (const response of responses) {
        const api = new PublicApi(vi.fn(async () => response) as typeof fetch);
        await expect(endpoint.call(api)).rejects.toMatchObject({
          code: "API_RESPONSE_INVALID",
        });
      }
    }
  });

  it("preserves a canonical INVALID_ARGUMENT discovery error", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json(
          {
            error: {
              code: "INVALID_ARGUMENT",
              message: "cursor is stale",
              details: { field: "cursor" },
              request_id: "req_stale",
            },
          },
          400,
        ),
      ) as typeof fetch,
    );
    await expect(
      api.search({ query: "雨", cursor: "stale" }),
    ).rejects.toMatchObject({
      code: "INVALID_ARGUMENT",
      status: 400,
      requestId: "req_stale",
    });
  });

  it("gracefully disables instance-scoped features when site meta omits the runtime ID", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            owner_id: "00000000000000000000000000000000",
            username: "owner",
            display_name: "Moment",
            title: "Moment",
            bio: "",
            timezone: "UTC",
            public_base_url: null,
            avatar_media_id: null,
            cover_media_id: null,
            pinned_post_id: null,
            comments_enabled: true,
          },
          meta: { request_id: "req_site" },
        }),
      ) as typeof fetch,
    );
    await expect(api.site()).resolves.toMatchObject({
      instanceId: null,
      instanceInitializedAt: null,
    });
  });

  it("rejects a forged or malformed runtime instance ID", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            owner_id: "00000000000000000000000000000000",
            username: "owner",
            display_name: "Moment",
            title: "Moment",
            bio: "",
            timezone: "UTC",
            public_base_url: null,
            avatar_media_id: null,
            cover_media_id: null,
            pinned_post_id: null,
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

  it.each([
    {
      instance_id: "0123456789abcdef0123456789abcdef",
      instance_initialized_at: "2026-08-01T09:08:00+00:00",
    },
    { instance_initialized_at: "2026-08-01T09:08:00Z" },
  ])("rejects invalid instance initialization metadata", async (meta) => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            title: "Moment",
            bio: "",
            timezone: "UTC",
            public_base_url: null,
            avatar_media_id: null,
            cover_media_id: null,
            pinned_post_id: null,
            comments_enabled: true,
          },
          meta,
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

  it("accepts linked original and repost projections of the same Moment", async () => {
    const repostedAt = "2026-08-10T02:00:00.000Z";
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [
              {
                ...post,
                updated_at: repostedAt,
                reposted_at: repostedAt,
                timeline_item_kind: "repost",
              },
              {
                ...post,
                updated_at: repostedAt,
                reposted_at: repostedAt,
                timeline_item_kind: "original",
              },
            ],
          },
          meta: { next_cursor: null },
        }),
      ) as typeof fetch,
    );

    await expect(api.timeline()).resolves.toMatchObject({
      items: [
        { id: post.id, timelineItemKind: "repost", repostedAt },
        { id: post.id, timelineItemKind: "original", repostedAt },
      ],
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
      media: [],
    });
    expect(new Headers(mutation.headers).get("Idempotency-Key")).toBe(
      "web-comment-retry",
    );
  });

  it("accepts a location-only owner comment with the post location projection", async () => {
    const located = {
      ...rootComment,
      author_kind: "owner",
      author_name: "Owner",
      content_md: "",
      content_html: "",
      location: {
        name: "BANANA+香蕉攀岩",
        display_name: "天目西路218号嘉里合集B1B103B104",
      },
    };
    const api = new PublicApi(
      vi.fn(async () =>
        json({ data: { items: [located] }, meta: { request_id: "req" } }),
      ) as typeof fetch,
    );

    await expect(api.comments(post.id)).resolves.toMatchObject({
      items: [
        {
          contentMd: "",
          contentHtml: "",
          location: {
            name: "BANANA+香蕉攀岩",
            displayName: "天目西路218号嘉里合集B1B103B104",
          },
        },
      ],
    });
  });

  it("rejects whitespace-only comment content even when a location exists", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [
              {
                ...rootComment,
                author_kind: "owner",
                author_name: "Owner",
                content_md: " ",
                content_html: "",
                location: {
                  name: "静安寺",
                  display_name: "上海市静安区南京西路1686号",
                },
              },
            ],
          },
          meta: { request_id: "req" },
        }),
      ) as typeof fetch,
    );

    await expect(api.comments(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
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

  it("rejects more than four media usages in a comment response", async () => {
    const media = Array.from({ length: 5 }, (_, position) => ({
      media_id: "01K2ABCDEF0123456789ABCDE4",
      position,
      alt_text: null,
      width: 800,
      height: 600,
    }));
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: { items: [{ ...rootComment, media }] },
          meta: { request_id: "req_bad_media" },
        }),
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
