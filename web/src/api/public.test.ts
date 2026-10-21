import { describe, expect, it, vi } from "vitest";

import { PublicApi, PublicApiError, parsePost } from "./public";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const post = {
  id: "01K2ABCDEF0123456789ABCDE0",
  kind: "moment",
  content_md: "今天下了一场雨。",
  article: null,
  content_html: "<p>今天下了一场雨。</p>",
  created_at: "2026-08-10T01:02:03.000Z",
  updated_at: "2026-08-10T01:02:03.000Z",
  published_at: "2026-08-10T01:02:03.000Z",
  reposted_at: null,
  visibility: "everyone",
  timeline_item_kind: "original",
  quoted_post: null,
  comment_count: 0,
  media: [],
  link_cards: [],
};

// A legacy guest row: a name and nothing else behind it.
const guestAuthor = {
  user_id: null,
  username: null,
  display_name: "访客",
  avatar_media_id: null,
  profile_url: null,
};

const ownerAuthor = {
  user_id: "0123456789abcdef0123456789abcdef",
  username: "owner",
  display_name: "Moment",
  avatar_media_id: null,
  profile_url: "/owner",
};

const rootComment = {
  id: "01K2ABCDEF0123456789ABCDE1",
  post_id: post.id,
  parent_id: null,
  depth: 0,

  author_name: "访客",
  author: guestAuthor,
  reply_to: null,
  content_md: "第一条 **回应**",
  content_html: "<p>第一条 <strong>回应</strong></p>\n",
  created_at: "2026-08-10T01:03:00Z",
  media: [],
  reply_count: 1,
};

const replyComment = {
  ...rootComment,
  id: "01K2ABCDEF0123456789ABCDE2",
  parent_id: rootComment.id,
  depth: 1,

  author_name: "Owner",
  author: ownerAuthor,
  content_md: "一级回复",
  content_html: "<p>一级回复</p>\n",
  created_at: "2026-08-10T01:04:00Z",
  reply_count: 0,
};

describe("public API client", () => {
  it("maps the non-recursive quoted Moment projection", async () => {
    const quoted = {
      ...post,
      content_md: "写一点新的想法",
      content_html: "<p>写一点新的想法</p>",
      quoted_post: {
        unavailable: false,
        article: null,
        id: "01K2ABCDEF0123456789ABCDE9",
        kind: "moment",
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
        article: null,
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
        kind: null,
        article: null,
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

  it("requires a known post visibility and carries it through", async () => {
    const hidden = new PublicApi(
      vi.fn(async () =>
        json({ data: { ...post, visibility: "only_me" }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(hidden.post(post.id)).resolves.toMatchObject({
      visibility: "only_me",
    });

    const missing = { ...post } as Record<string, unknown>;
    delete missing.visibility;
    for (const data of [
      missing,
      { ...post, visibility: "friends" },
      { ...post, visibility: null },
    ]) {
      const api = new PublicApi(
        vi.fn(async () => json({ data, meta: {} })) as typeof fetch,
      );
      await expect(api.post(post.id)).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    }
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
            profile_user_id: "00000000000000000000000000000000",
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
          kind: "moment",
          contentMd: post.content_md,
          article: null,
          contentHtml: post.content_html,
          createdAt: post.created_at,
          updatedAt: post.updated_at,
          editedAt: null,
          publishedAt: post.published_at,
          repostedAt: post.reposted_at,
          visibility: "everyone",
          timelineItemKind: "original",
          commentCount: post.comment_count,
          media: [],
          linkCards: [],
          location: null,
          quotedPost: null,
        },
      ],
      nextCursor: "opaque+/=cursor",
      prevCursor: null,
      refreshCursor: null,
    });
    expect(fetcher).toHaveBeenLastCalledWith(
      expect.stringContaining("cursor=opaque%2B%2F%3Dcursor"),
      expect.objectContaining({ headers: { Accept: "application/json" } }),
    );
  });

  it("preserves refresh watermarks and rejects malformed metadata", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: { items: [] },
        meta: {
          next_cursor: null,
          prev_cursor: null,
          refresh_cursor: "opaque-refresh+/=",
        },
      }),
    );
    const api = new PublicApi(fetcher as typeof fetch);
    await expect(api.timeline()).resolves.toMatchObject({
      refreshCursor: "opaque-refresh+/=",
    });
    fetcher.mockResolvedValueOnce(
      json({
        data: { items: [] },
        meta: { next_cursor: null, refresh_cursor: 12 },
      }),
    );
    await expect(api.timeline()).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("reads complete author counts without deriving them from a timeline page", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: { items: [], author_post_count: 137 },
        meta: { next_cursor: null },
      }),
    );
    const api = new PublicApi(fetcher as typeof fetch);
    await expect(api.timeline()).resolves.toMatchObject({
      items: [],
      authorPostCount: 137,
    });
    for (const invalid of [-1, 1.5, "137", null]) {
      fetcher.mockResolvedValueOnce(
        json({
          data: { items: [], author_post_count: invalid },
          meta: { next_cursor: null },
        }),
      );
      await expect(api.timeline()).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    }
  });

  it("maps search hits, complete year counts, and encoded cursor filters", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: {
          items: [
            {
              ...post,
              author: ownerAuthor,
              edited_at: null,
              map_card: null,
              location: null,
              snippet: "旧城雨落在街口",
            },
          ],
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

  it("rejects an unknown field in a search hit through the shared post parser", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [{ ...post, snippet: "雨", unrecognized: true }],
            year_counts: [{ year: 2026, count: 1 }],
          },
          meta: { next_cursor: null, request_id: "req_search" },
        }),
      ) as typeof fetch,
    );
    await expect(api.search({ query: "雨" })).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
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
            profile_user_id: "00000000000000000000000000000000",
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
            profile_user_id: "00000000000000000000000000000000",
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
                visibility: "everyone",
                timeline_item_kind: "repost",
              },
              {
                ...post,
                updated_at: repostedAt,
                reposted_at: repostedAt,
                visibility: "everyone",
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

  it("validates the canonical flat thread and still reads legacy guest rows", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [rootComment, replyComment],
            pinned_comment_id: rootComment.id,
            pinned: [rootComment, replyComment],
          },
          meta: { next_cursor: null, request_id: "req_comments" },
        }),
      ) as typeof fetch,
    );

    await expect(api.comments(post.id)).resolves.toMatchObject({
      pinnedCommentId: rootComment.id,
      items: [
        {
          id: rootComment.id,
          depth: 0,
          parentId: null,

          authorName: "访客",
        },
        {
          id: replyComment.id,
          depth: 1,
          parentId: rootComment.id,
        },
      ],
    });
  });

  it("rejects a pinned reply instead of a top-level comment", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [rootComment, replyComment],
            pinned_comment_id: replyComment.id,
          },
          meta: { request_id: "req_bad_pin" },
        }),
      ) as typeof fetch,
    );
    await expect(api.comments(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
  });

  it("accepts a location-only owner comment with the post location projection", async () => {
    const located = {
      ...rootComment,

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
        json({
          data: { items: [located], pinned_comment_id: null, pinned: [] },
          meta: { next_cursor: null, request_id: "req" },
        }),
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

  // author_name is the writer's display name, which the server bounds at 80
  // code points. The browser bounded it at 40 -- and parseComment throws from
  // inside the map over the thread's items, so one comment by an account with a
  // longer name turned the whole conversation into an error for every reader,
  // not just that row.
  it("renders a thread whose author holds a display name at the server's ceiling", async () => {
    const longName = "长".repeat(80);
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [
              {
                ...rootComment,

                author_name: longName,
                author: { ...ownerAuthor, display_name: longName },
              },
            ],
            pinned_comment_id: null,
            pinned: [],
          },
          meta: { next_cursor: null, request_id: "req" },
        }),
      ) as typeof fetch,
    );

    await expect(api.comments(post.id)).resolves.toMatchObject({
      items: [{ authorName: longName }],
    });
  });

  it("still refuses an author name past the server's ceiling", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [
              {
                ...rootComment,

                author_name: "长".repeat(81),
              },
            ],
            pinned_comment_id: null,
            pinned: [],
          },
          meta: { next_cursor: null, request_id: "req" },
        }),
      ) as typeof fetch,
    );

    await expect(api.comments(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
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
    await expect(valid.comments(post.id)).rejects.toMatchObject({
      code: "RATE_LIMITED",
      retryAfterSeconds: 7,
    });

    const malformed = new PublicApi(
      vi.fn(async () => limited("7", 8)) as typeof fetch,
    );
    await expect(malformed.comments(post.id)).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });
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
});

it("reads authoritative edit time and accepts legacy posts without it", () => {
  expect(parsePost(post).editedAt).toBeNull();
  const edited = "2026-08-10T01:02:04.000Z";
  expect(
    parsePost({ ...post, updated_at: edited, edited_at: edited }).editedAt,
  ).toBe(edited);
  expect(() => parsePost({ ...post, edited_at: "nonsense" })).toThrow();
});

function textNode(text: string): Record<string, unknown> {
  return { type: "text", text };
}

describe("post kind and the body it is allowed to carry", () => {
  const body = {
    schema_version: 1,
    type: "doc",
    content: [
      { type: "heading", attrs: { level: 2 }, content: [textNode("一篇文章")] },
      { type: "paragraph", content: [textNode("正文。")] },
    ],
  };

  const article = {
    ...post,
    kind: "article",
    content_md: null,
    article: {
      title: "一篇文章",
      excerpt: "正文。",
      cover: null,
      body,
      body_schema_version: 1,
    },
    content_html: "<h1>一篇文章</h1>\n<p>正文。</p>",
  };

  // What the timeline receives: the card fields, and neither the body nor the
  // rendering of it.
  const articleCard = {
    ...article,
    article: { ...article.article, body: null, body_schema_version: null },
    content_html: "",
  };

  it("parses an article, which renders from HTML and carries no Markdown", () => {
    expect(parsePost(article)).toMatchObject({
      kind: "article",
      contentMd: null,
      contentHtml: "<h1>一篇文章</h1>\n<p>正文。</p>",
      article: {
        title: "一篇文章",
        excerpt: "正文。",
        cover: null,
        bodySchemaVersion: 1,
      },
    });
  });

  it("reads validated inline geometry independently of the attachment grid", () => {
    const geometry = {
      media_id: "01K2ABCDEF0123456789ABCDE7",
      mime_type: "image/jpeg",
      width: 1200,
      height: 800,
    };
    expect(
      parsePost({
        ...article,
        article: { ...article.article, body_media: [geometry] },
      }).article?.bodyMedia,
    ).toEqual([
      {
        mediaId: geometry.media_id,
        mimeType: "image/jpeg",
        width: 1200,
        height: 800,
      },
    ]);
    for (const body_media of [
      null,
      [null],
      [{ ...geometry, width: 0 }],
      [geometry, geometry],
    ]) {
      expect(() =>
        parsePost({ ...article, article: { ...article.article, body_media } }),
      ).toThrow(PublicApiError);
    }
    expect(() =>
      parsePost({
        ...articleCard,
        article: { ...articleCard.article, body_media: [geometry] },
      }),
    ).toThrow(PublicApiError);
  });

  it("parses a moment, which carries its Markdown source", () => {
    expect(parsePost(post)).toMatchObject({
      kind: "moment",
      contentMd: "今天下了一场雨。",
    });
  });

  it("rejects a moment whose content_md is null", () => {
    expect(() => parsePost({ ...post, content_md: null })).toThrow(
      PublicApiError,
    );
  });

  it("rejects an article whose content_md is a string", () => {
    expect(() =>
      parsePost({ ...article, content_md: "文章的正文不是 Markdown" }),
    ).toThrow(PublicApiError);
  });

  it("rejects a post that carries no kind", () => {
    const withoutKind: Record<string, unknown> = { ...post };
    delete withoutKind.kind;
    expect(() => parsePost(withoutKind)).toThrow(PublicApiError);
  });

  it("rejects a kind this client does not know", () => {
    expect(() => parsePost({ ...post, kind: "newsletter" })).toThrow(
      PublicApiError,
    );
    expect(() => parsePost({ ...post, kind: null })).toThrow(PublicApiError);
  });

  it("carries the kind through a search hit", async () => {
    const api = new PublicApi(
      vi.fn(async () =>
        json({
          data: {
            items: [{ ...articleCard, snippet: "文章里的一段" }],
            year_counts: [{ year: 2026, count: 1 }],
          },
          meta: { next_cursor: null, request_id: "req_search" },
        }),
      ) as typeof fetch,
    );

    await expect(api.search({ query: "文章" })).resolves.toMatchObject({
      items: [{ post: { kind: "article", contentMd: null } }],
    });
  });

  it("parses a quoted article, which is available while carrying no Markdown", () => {
    const quoting = {
      ...post,
      quoted_post: {
        unavailable: false,
        id: "01K2ABCDEF0123456789ABCDE9",
        kind: "article",
        article: {
          title: "被引用的文章",
          excerpt: "被引用的摘要",
          cover: null,
          body: null,
          body_schema_version: null,
        },
        content_md: null,
        content_html: "",
        published_at: "2026-08-09T01:02:03.000Z",
        media: [],
        location: null,
      },
    };

    expect(parsePost(quoting).quotedPost).toEqual({
      unavailable: false,
      id: "01K2ABCDEF0123456789ABCDE9",
      kind: "article",
      article: {
        title: "被引用的文章",
        excerpt: "被引用的摘要",
        cover: null,
        body: null,
        bodySchemaVersion: null,
      },
      contentMd: null,
      contentHtml: "",
      publishedAt: "2026-08-09T01:02:03.000Z",
      media: [],
      location: null,
    });
  });

  // A quote shows a card. Embedding a whole long post inside a short one is
  // not a quote, and the server does not send one.
  it("refuses a quoted article that carries its body", () => {
    const withBody = {
      ...post,
      quoted_post: {
        unavailable: false,
        id: "01K2ABCDEF0123456789ABCDE9",
        kind: "article",
        article: {
          title: "被引用的文章",
          excerpt: "摘要",
          cover: null,
          body: { schema_version: 1, type: "doc", content: [] },
          body_schema_version: 1,
        },
        content_md: null,
        content_html: "<h1>被引用的文章</h1>",
        published_at: "2026-08-09T01:02:03.000Z",
        media: [],
        location: null,
      },
    };
    expect(() => parsePost(withBody)).toThrow(PublicApiError);
  });

  it("holds a quoted post to the same pairing as the post quoting it", () => {
    const quoting = (overrides: Record<string, unknown>) => ({
      ...post,
      quoted_post: {
        unavailable: false,
        article: null,
        id: "01K2ABCDEF0123456789ABCDE9",
        kind: "moment",
        content_md: "被引用的正文",
        content_html: "<p>被引用的正文</p>",
        published_at: "2026-08-09T01:02:03.000Z",
        media: [],
        location: null,
        ...overrides,
      },
    });

    expect(() => parsePost(quoting({ content_md: null }))).toThrow(
      PublicApiError,
    );
    expect(() => parsePost(quoting({ kind: "article" }))).toThrow(
      PublicApiError,
    );
    expect(() => parsePost(quoting({ kind: "newsletter" }))).toThrow(
      PublicApiError,
    );
  });

  it("tells a tombstone from an article by unavailable rather than by content_md", () => {
    const tombstone = {
      unavailable: true,
      id: null,
      kind: null,
      article: null,
      content_md: null,
      content_html: null,
      published_at: null,
      media: null,
      location: null,
    };

    expect(parsePost({ ...post, quoted_post: tombstone }).quotedPost).toEqual({
      unavailable: true,
    });
    // A null content_md is what an article looks like now, so it can no longer
    // stand for a deleted target: the tombstone still has to be empty of
    // everything, kind included.
    expect(() =>
      parsePost({ ...post, quoted_post: { ...tombstone, kind: "article" } }),
    ).toThrow(PublicApiError);
  });

  it("reads a list article, whose body and rendering both stay behind", () => {
    expect(parsePost(articleCard)).toMatchObject({
      kind: "article",
      contentHtml: "",
      article: {
        title: "一篇文章",
        excerpt: "正文。",
        body: null,
        bodySchemaVersion: null,
      },
    });
  });

  it("refuses a moment that carries an article, and one that carries none", () => {
    expect(() =>
      parsePost({ ...post, article: { title: "", excerpt: "" } }),
    ).toThrow(PublicApiError);
    const without: Record<string, unknown> = { ...post };
    delete without.article;
    expect(() => parsePost(without)).toThrow(PublicApiError);
  });

  it("refuses an article whose article object is missing or reshaped", () => {
    expect(() => parsePost({ ...article, article: null })).toThrow(
      PublicApiError,
    );
    expect(() =>
      parsePost({
        ...article,
        article: { ...article.article, subtitle: "多出来的字段" },
      }),
    ).toThrow(PublicApiError);
    expect(() =>
      parsePost({ ...article, article: { ...article.article, title: "  " } }),
    ).toThrow(PublicApiError);
  });

  it("holds the body, its schema version and the rendered HTML to one another", () => {
    // A list row that still carries HTML, and a detail row that lost it.
    expect(() =>
      parsePost({ ...articleCard, content_html: "<p>正文。</p>" }),
    ).toThrow(PublicApiError);
    expect(() => parsePost({ ...article, content_html: "" })).toThrow(
      PublicApiError,
    );
    expect(() =>
      parsePost({
        ...article,
        article: { ...article.article, body_schema_version: null },
      }),
    ).toThrow(PublicApiError);
    expect(() =>
      parsePost({
        ...article,
        article: { ...article.article, body_schema_version: 2 },
      }),
    ).toThrow(PublicApiError);
  });

  it("parses every node and mark the body grammar allows", () => {
    const rich = {
      schema_version: 1,
      type: "doc",
      content: [
        {
          type: "paragraph",
          content: [
            {
              ...textNode("粗链接"),
              marks: [
                { type: "bold" },
                { type: "link", attrs: { href: "https://example.com/x" } },
              ],
            },
            { type: "hardBreak" },
          ],
        },
        { type: "heading", attrs: { level: 3 }, content: [textNode("小标题")] },
        {
          type: "blockquote",
          content: [{ type: "paragraph", content: [textNode("引文")] }],
        },
        {
          type: "bulletList",
          content: [
            {
              type: "listItem",
              content: [{ type: "paragraph", content: [textNode("一")] }],
            },
          ],
        },
        {
          type: "orderedList",
          attrs: { start: 7 },
          content: [
            {
              type: "listItem",
              content: [{ type: "paragraph", content: [textNode("七")] }],
            },
          ],
        },
        {
          type: "codeBlock",
          attrs: { language: "go" },
          content: [textNode("package main")],
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
                  content: [{ type: "paragraph", content: [textNode("头")] }],
                },
              ],
            },
            {
              type: "tableRow",
              content: [
                {
                  type: "tableCell",
                  attrs: { rowspan: 2 },
                  content: [{ type: "paragraph", content: [textNode("格")] }],
                },
              ],
            },
          ],
        },
        {
          type: "image",
          attrs: { media_id: "01K2ABCDEF0123456789ABCDE7", alt: "一张图" },
        },
      ],
    };

    const parsed = parsePost({
      ...article,
      article: { ...article.article, body: rich },
    });
    expect(parsed.article?.body).toEqual(rich);
  });

  it("refuses a body node, mark or attribute the grammar does not name", () => {
    const withBody = (content: unknown) =>
      parsePost({
        ...article,
        article: {
          ...article.article,
          body: { schema_version: 1, type: "doc", content },
        },
      });

    expect(() => withBody([{ type: "callout", content: [] }])).toThrow(
      PublicApiError,
    );
    expect(() =>
      withBody([
        { type: "paragraph", attrs: { align: "center" }, content: [] },
      ]),
    ).toThrow(PublicApiError);
    expect(() =>
      withBody([
        {
          type: "paragraph",
          content: [{ ...textNode("x"), marks: [{ type: "highlight" }] }],
        },
      ]),
    ).toThrow(PublicApiError);
    expect(() =>
      withBody([
        {
          type: "paragraph",
          content: [
            {
              ...textNode("x"),
              marks: [{ type: "link", attrs: { href: "javascript:alert(1)" } }],
            },
          ],
        },
      ]),
    ).toThrow(PublicApiError);
    expect(() =>
      withBody([
        { type: "heading", attrs: { level: 4 }, content: [textNode("x")] },
      ]),
    ).toThrow(PublicApiError);
    expect(() =>
      withBody([{ type: "paragraph", content: [textNode("")] }]),
    ).toThrow(PublicApiError);
    expect(() =>
      withBody([{ type: "paragraph", text: "x", content: [] }]),
    ).toThrow(PublicApiError);
    // A heading with no level and a picture naming no media are both nodes
    // the renderer could not have produced.
    expect(() =>
      withBody([{ type: "heading", content: [textNode("x")] }]),
    ).toThrow(PublicApiError);
    expect(() => withBody([{ type: "image", attrs: { alt: "x" } }])).toThrow(
      PublicApiError,
    );
    expect(() => withBody([{ type: "horizontalRule", content: [] }])).toThrow(
      PublicApiError,
    );
  });

  it("reads a cover with the geometry a card reserves space from", () => {
    const cover = {
      media_id: "01K2ABCDEF0123456789ABCDE7",
      mime_type: "image/jpeg",
      width: 1600,
      height: 900,
    };
    expect(
      parsePost({ ...articleCard, article: { ...articleCard.article, cover } })
        .article?.cover,
    ).toEqual({
      mediaId: "01K2ABCDEF0123456789ABCDE7",
      mimeType: "image/jpeg",
      width: 1600,
      height: 900,
    });
    for (const broken of [
      { ...cover, width: 0 },
      { ...cover, mime_type: "video/mp4" },
      { ...cover, media_id: "not-a-ulid" },
      { ...cover, caption: "多出来的字段" },
    ]) {
      expect(() =>
        parsePost({
          ...articleCard,
          article: { ...articleCard.article, cover: broken },
        }),
      ).toThrow(PublicApiError);
    }
  });

  it("lets an article place more pictures than a moment may attach", () => {
    const picture = (index: number) => ({
      media_id: `01K2ABCDEF0123456789ABCDE${"0123456789ABCDEFGHJK"[index]}`,
      position: index,
      alt_text: null,
      mime_type: "image/jpeg",
      width: 1600,
      height: 900,
    });
    const pictures = Array.from({ length: 12 }, (_, index) => picture(index));

    // An article's body places these; they reach `media` as a projection, and
    // the grid's nine is not their ceiling.
    expect(parsePost({ ...article, media: pictures }).media).toHaveLength(12);
    // A moment attaches them, and nine is exactly the ceiling.
    expect(() => parsePost({ ...post, media: pictures })).toThrow(
      PublicApiError,
    );
  });

  it("asks the timeline for one kind only when one was named", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: { items: [] },
        meta: { next_cursor: null, prev_cursor: null, request_id: "req_posts" },
      }),
    );
    const api = new PublicApi(fetcher as typeof fetch);

    await api.timeline({ kind: "article" });
    expect(fetcher).toHaveBeenLastCalledWith(
      expect.stringContaining("kind=article"),
      expect.anything(),
    );

    await api.timeline();
    expect(fetcher).toHaveBeenLastCalledWith(
      expect.not.stringContaining("kind="),
      expect.anything(),
    );
  });
});
