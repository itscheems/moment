import { PostsApi } from "./posts";
import { LocationsApi } from "./locations";

import { MediaApi } from "./media";
import { AuthApi } from "./auth";
import { CommentsApi } from "./comments";
import { afterEach, describe, expect, it, vi } from "vitest";

import { mediaPreviewUrl } from "./mediaUrls";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const post = {
  id: "01K2ABCDEF0123456789ABCDE0",
  kind: "moment",
  content_md: "现在写下",
  article: null,
  content_html: "<p>现在写下</p>",
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

const comment = {
  id: "01K2ABCDEF0123456789ABCDE1",
  post_id: post.id,
  parent_id: null,
  depth: 0,

  // The stored placeholder. What a reader sees comes from `author`, resolved
  // server side, which is why these two disagree.
  author_name: "Owner",
  author: {
    user_id: "0123456789abcdef0123456789abcdef",
    username: "owner",
    display_name: "Moment",
    avatar_media_id: null,
    profile_url: "/owner",
  },
  reply_to: null,
  content_md: "所有者回应",
  content_html: "<p>所有者回应</p>\n",
  created_at: "2026-08-10T01:03:00Z",
  media: [],
  reply_count: 0,
};

class FakeUploadRequest extends EventTarget {
  static last: FakeUploadRequest | null = null;

  readonly upload = new EventTarget();
  readonly headers = new Map<string, string>();
  method = "";
  url = "";
  withCredentials = false;
  status = 0;
  responseText = "";
  responseType: XMLHttpRequestResponseType = "";
  sent: Document | XMLHttpRequestBodyInit | null = null;
  contentType = "application/json";

  constructor() {
    super();
    FakeUploadRequest.last = this;
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader(name: string, value: string) {
    this.headers.set(name, value);
  }

  getResponseHeader(name: string) {
    return name.toLowerCase() === "content-type" ? this.contentType : null;
  }

  send(body: Document | XMLHttpRequestBodyInit | null) {
    this.sent = body;
  }

  abort() {
    this.dispatchEvent(new Event("abort"));
    this.dispatchEvent(new Event("loadend"));
  }

  respond(status: number, body: unknown, contentType = "application/json") {
    this.status = status;
    this.responseText = JSON.stringify(body);
    this.contentType = contentType;
    this.dispatchEvent(new Event("load"));
    this.dispatchEvent(new Event("loadend"));
  }
}

const uploaded = {
  media: {
    id: "01K2ABCDEF0123456789ABCDE1",
    sha256: "a".repeat(64),
    original_name: "rain.png",
    mime_type: "image/png",
    byte_size: 1024,
    width: 800,
    height: 600,
    created_at: "2026-08-10T01:02:03.000Z",
    unreferenced_since: "2026-08-10T01:02:03.000Z",
    animated_original: false,
  },
  preview: {
    recipe_version: "preview-v1",
    sha256: "b".repeat(64),
    mime_type: "image/png",
    width: 800,
    height: 600,
    animated_original: false,
  },
  original_url: "/api/v1/media/01K2ABCDEF0123456789ABCDE1",
  preview_url: mediaPreviewUrl("01K2ABCDEF0123456789ABCDE1"),
};

afterEach(() => {
  FakeUploadRequest.last = null;
  vi.unstubAllGlobals();
});

describe("owner API client", () => {
  it("creates a quoted Moment with commentary and the quoted post identifier", async () => {
    const fetcher = vi.fn<typeof fetch>(async () =>
      json(
        {
          data: {
            ...post,
            content_md: "我的补充",
            content_html: "<p>我的补充</p>",
            quoted_post: {
              unavailable: false,
              article: null,
              id: "01K2ABCDEF0123456789ABCDE9",
              kind: "moment",
              content_md: "原始内容",
              content_html: "<p>原始内容</p>",
              published_at: "2026-08-09T01:02:03.000Z",
              media: [],
              location: null,
            },
          },
          meta: { replayed: false },
        },
        201,
      ),
    );

    await expect(
      new PostsApi(fetcher as typeof fetch).create(
        "csrf",
        "我的补充",
        "web-quote",
        [],
        undefined,
        "01K2ABCDEF0123456789ABCDE9",
      ),
    ).resolves.toMatchObject({
      quotedPost: { id: "01K2ABCDEF0123456789ABCDE9" },
    });
    expect(JSON.parse(String(fetcher.mock.calls[0]?.[1]?.body))).toEqual({
      content_md: "我的补充",
      media: [],
      quoted_post_id: "01K2ABCDEF0123456789ABCDE9",
    });
  });

  it("searches, resolves, and submits canonical post locations", async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = [];
    const fullLocation = {
      name: "静安寺",
      display_name: "上海市静安区南京西路1686号",
      country_code: "CN",
      country: "中国",
      region: "上海市",
      city: "上海市",
      district: "静安区",
      category: "风景名胜",
      provider: "amap",
      provider_place_id: "B0FFF",
      latitude: 31.223186,
      longitude: 121.44526,
      coordinate_system: "gcj02",
    };
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        requests.push({ url, init });
        if (url.includes("/locations/")) {
          return json({
            data: { items: [fullLocation] },
            meta: { request_id: "req_location" },
          });
        }
        return json(
          {
            data: {
              ...post,
              location: {
                name: fullLocation.name,
                display_name: fullLocation.display_name,
              },
            },
            meta: { replayed: false },
          },
          201,
        );
      },
    );

    const locations = await new LocationsApi(
      fetcher as typeof fetch,
    ).searchLocations("静安寺", {
      latitude: 31.23,
      longitude: 121.47,
    });
    expect(locations[0]).toMatchObject({
      name: "静安寺",
      provider: "amap",
      coordinateSystem: "gcj02",
    });
    expect(requests[0].url).toContain("q=%E9%9D%99%E5%AE%89%E5%AF%BA");
    expect(requests[0].url).toContain("latitude=31.23");

    await new PostsApi(fetcher as typeof fetch).create(
      "csrf",
      "带地点",
      "web-location",
      [],
      locations[0],
    );
    expect(JSON.parse(String(requests[1].init?.body))).toEqual({
      content_md: "带地点",
      media: [],
      location: fullLocation,
    });
  });

  it("submits exact ordered usages while preserving nullable and padded alt text", async () => {
    const media = [
      {
        mediaId: "01K2ABCDEF0123456789ABCDE1",
        position: 8,
        altText: null,
      },
      {
        mediaId: "01K2ABCDEF0123456789ABCDE1",
        position: 4,
        altText: "",
      },
      {
        mediaId: "01K2ABCDEF0123456789ABCDE2",
        position: 2,
        altText: "  雨后的街道  ",
      },
    ];
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        void input;
        void init;
        return json(
          {
            data: {
              ...post,
              media: media.map((usage, position) => ({
                media_id: usage.mediaId,
                position,
                alt_text: usage.altText,
                mime_type: "image/jpeg",
                width: 1_200,
                height: 800,
              })),
            },
            meta: { replayed: false },
          },
          201,
        );
      },
    );

    await new PostsApi(fetcher as typeof fetch).create(
      "csrf",
      post.content_md,
      "web-media-order",
      media,
    );

    expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toEqual({
      content_md: post.content_md,
      media: [
        { media_id: media[0].mediaId, position: 0, alt_text: null },
        { media_id: media[1].mediaId, position: 1, alt_text: "" },
        {
          media_id: media[2].mediaId,
          position: 2,
          alt_text: "  雨后的街道  ",
        },
      ],
    });
  });

  it("uploads with progress and accepts only the canonical HTTP 201 JSON contract", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeUploadRequest);

    const progress = vi.fn();
    const promise = new MediaApi(vi.fn() as typeof fetch).uploadMedia(
      "csrf-upload",
      new File(["image"], "rain.png", { type: "image/png" }),
      progress,
    );
    const request = FakeUploadRequest.last;
    expect(request?.method).toBe("POST");
    expect(request?.url).toBe("/api/v1/media");
    expect(request?.withCredentials).toBe(true);
    expect(request?.headers.get("X-CSRF-Token")).toBe("csrf-upload");
    expect(request?.sent).toBeInstanceOf(FormData);
    request?.upload.dispatchEvent(
      new ProgressEvent("progress", {
        lengthComputable: true,
        loaded: 1,
        total: 2,
      }),
    );
    request?.respond(201, {
      data: uploaded,
      meta: { request_id: "req_upload" },
    });

    await expect(promise).resolves.toMatchObject({
      id: uploaded.media.id,
      previewUrl: uploaded.preview_url,
      originalUrl: uploaded.original_url,
    });
    expect(progress).toHaveBeenCalledWith(50);
    expect(progress).toHaveBeenLastCalledWith(100);
  });

  it("reveals an early server video poster while the primary upload keeps processing", async () => {
    vi.stubGlobal("XMLHttpRequest", FakeUploadRequest);
    const posterFetch = vi.fn(
      async () =>
        new Response(new Blob(["jpeg"]), {
          status: 200,
          headers: { "Content-Type": "image/jpeg" },
        }),
    );
    vi.stubGlobal("fetch", posterFetch);
    const objectURL = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:http://localhost/video-poster");
    const onPoster = vi.fn();

    const promise = new MediaApi(vi.fn() as typeof fetch).uploadMedia(
      "csrf-upload",
      new File(["video"], "climb.mov", { type: "video/quicktime" }),
      vi.fn(),
      undefined,
      onPoster,
    );
    const request = FakeUploadRequest.last;
    expect(request?.headers.get("X-Moment-Upload-ID")).toMatch(
      /^[\w-]{16,64}$/,
    );
    request?.upload.dispatchEvent(
      new ProgressEvent("progress", {
        lengthComputable: true,
        loaded: 2,
        total: 2,
      }),
    );
    await vi.waitFor(() =>
      expect(onPoster).toHaveBeenCalledWith(
        "blob:http://localhost/video-poster",
      ),
    );
    expect(posterFetch).toHaveBeenCalledWith(
      expect.stringMatching(/^\/api\/v1\/media\/uploads\/.+\/poster$/),
      expect.objectContaining({ credentials: "same-origin" }),
    );
    request?.respond(201, {
      data: uploaded,
      meta: { request_id: "req_upload" },
    });
    await expect(promise).resolves.toMatchObject({ id: uploaded.media.id });
    objectURL.mockRestore();
  });

  it.each([
    {
      name: "wrong success status",
      status: 200,
      body: { data: uploaded, meta: { request_id: "req_upload" } },
      contentType: "application/json",
    },
    {
      name: "non-JSON success",
      status: 201,
      body: { data: uploaded, meta: { request_id: "req_upload" } },
      contentType: "text/plain",
    },
    {
      name: "extra metadata",
      status: 201,
      body: {
        data: uploaded,
        meta: { request_id: "req_upload", replayed: false },
      },
      contentType: "application/json",
    },
    {
      name: "uncontrolled original URL",
      status: 201,
      body: {
        data: { ...uploaded, original_url: "/storage/private/rain.png" },
        meta: { request_id: "req_upload" },
      },
      contentType: "application/json",
    },
    {
      name: "media ID above the canonical ULID range",
      status: 201,
      body: {
        data: {
          ...uploaded,
          media: { ...uploaded.media, id: "81K2ABCDEF0123456789ABCDE1" },
          original_url: "/api/v1/media/81K2ABCDEF0123456789ABCDE1",
          preview_url: mediaPreviewUrl("81K2ABCDEF0123456789ABCDE1"),
        },
        meta: { request_id: "req_upload" },
      },
      contentType: "application/json",
    },
  ])(
    "rejects $name for upload success",
    async ({ status, body, contentType }) => {
      vi.stubGlobal("XMLHttpRequest", FakeUploadRequest);

      const promise = new MediaApi(vi.fn() as typeof fetch).uploadMedia(
        "csrf-upload",
        new File(["image"], "rain.png", { type: "image/png" }),
      );
      FakeUploadRequest.last?.respond(status, body, contentType);
      await expect(promise).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    },
  );

  it("exchanges the bearer once and keeps mutations on cookie plus CSRF", async () => {
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, _init?: RequestInit) => {
        void _init;
        const url = String(input);
        if (url.endsWith("/session")) {
          return json({
            data: {
              csrf_token: "csrf-only",
              expires_at: "2026-08-11T01:02:03.000Z",
            },
            meta: {},
          });
        }
        return json({ data: post, meta: { replayed: false } }, 201);
      },
    );

    await new AuthApi(fetcher as typeof fetch).exchange(
      "bearer-secret",
      "token",
    );
    await new PostsApi(fetcher as typeof fetch).create(
      "csrf-only",
      post.content_md,
      "web-retry-1",
    );

    const exchange = fetcher.mock.calls[0];
    expect(exchange[0]).toBe("/api/v1/login/session");
    expect(exchange[1]?.body).toBe(JSON.stringify({ token: "bearer-secret" }));
    const mutation = fetcher.mock.calls[1][1] as RequestInit;
    const headers = new Headers(mutation.headers);
    expect(headers.get("Authorization")).toBeNull();
    expect(headers.get("X-CSRF-Token")).toBe("csrf-only");
    expect(headers.get("Idempotency-Key")).toBe("web-retry-1");
    expect(mutation.credentials).toBe("same-origin");
  });

  it("rejects malformed session and mutation responses", async () => {
    await expect(
      new AuthApi(
        vi.fn(async () =>
          json({ data: { expires_at: "later" }, meta: {} }),
        ) as typeof fetch,
      ).exchange("token"),
    ).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    await expect(
      new PostsApi(
        vi.fn(async () =>
          json({ data: { id: post.id }, meta: {} }),
        ) as typeof fetch,
      ).update("csrf", post.id, "changed"),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it("keeps owner comment identity server-derived and validates deletion metadata", async () => {
    const fetcher = vi.fn(
      async (_input: RequestInfo | URL, init?: RequestInit) => {
        if (init?.method === "DELETE") {
          return json({
            data: comment,
            meta: {
              deleted: true,
              dry_run: false,
              replies_affected: 2,
              request_id: "req_delete",
            },
          });
        }
        return json(
          {
            data: comment,
            meta: { replayed: false, request_id: "req_create" },
          },
          201,
        );
      },
    );

    await expect(
      new CommentsApi(fetcher as typeof fetch).createComment(
        "csrf",
        post.id,
        "所有者回应",
        null,
        "web-comment-owner",
      ),
    ).resolves.toMatchObject({ comment: {} });
    const createInit = fetcher.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(createInit.body))).toEqual({
      content_md: "所有者回应",
      parent_id: null,
      reply_to_comment_id: null,
      media: [],
      location: null,
    });
    expect(String(createInit.body)).not.toContain("author_name");
    expect(new Headers(createInit.headers).get("X-CSRF-Token")).toBe("csrf");

    await expect(
      new CommentsApi(fetcher as typeof fetch).deleteComment(
        "csrf",
        post.id,
        comment.id,
      ),
    ).resolves.toMatchObject({ repliesAffected: 2 });
  });

  it("fails closed on contradictory comment delete metadata", async () => {
    await expect(
      new CommentsApi(
        vi.fn(async () =>
          json({
            data: comment,
            meta: {
              deleted: false,
              dry_run: false,
              replies_affected: 0,
              request_id: "req_delete",
            },
          }),
        ) as typeof fetch,
      ).deleteComment("csrf", post.id, comment.id),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it("pins and unpins one comment through the owner CSRF contract", async () => {
    const fetcher = vi.fn(
      async (_input: RequestInfo | URL, init?: RequestInit) =>
        json({
          data: {
            post_id: post.id,
            pinned_comment_id: init?.method === "PUT" ? comment.id : null,
          },
          meta: { request_id: "req_pin" },
        }),
    );

    await expect(
      new CommentsApi(fetcher as typeof fetch).pinComment(
        "csrf",
        post.id,
        comment.id,
      ),
    ).resolves.toEqual({
      postId: post.id,
      pinnedCommentId: comment.id,
    });
    await expect(
      new CommentsApi(fetcher as typeof fetch).unpinComment(
        "csrf",
        post.id,
        comment.id,
      ),
    ).resolves.toEqual({ postId: post.id, pinnedCommentId: null });
    expect(fetcher.mock.calls.map(([, init]) => init?.method)).toEqual([
      "PUT",
      "DELETE",
    ]);
    for (const [url, init] of fetcher.mock.calls) {
      expect(String(url)).toBe(
        `/api/v1/posts/${post.id}/comments/${comment.id}/pin`,
      );
      expect(new Headers(init?.headers).get("X-CSRF-Token")).toBe("csrf");
    }
  });

  it("fails closed on contradictory comment pin selections", async () => {
    await expect(
      new CommentsApi(
        vi.fn(async () =>
          json({
            data: { post_id: post.id, pinned_comment_id: null },
            meta: { request_id: "req_pin" },
          }),
        ) as typeof fetch,
      ).pinComment("csrf", post.id, comment.id),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it.each([
    {
      name: "wrong delete status",
      response: json(
        {
          data: comment,
          meta: {
            deleted: true,
            dry_run: false,
            replies_affected: 0,
            request_id: "req_delete",
          },
        },
        201,
      ),
    },
    {
      name: "extra delete metadata",
      response: json({
        data: comment,
        meta: {
          deleted: true,
          dry_run: false,
          replies_affected: 0,
          request_id: "req_delete",
          extra: true,
        },
      }),
    },
  ])("fails closed on $name", async ({ response }) => {
    await expect(
      new CommentsApi(
        vi.fn(async () => response.clone()) as typeof fetch,
      ).deleteComment("csrf", post.id, comment.id),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it("preserves the resolved account when the stored author name differs", async () => {
    await expect(
      new CommentsApi(
        vi.fn(async () =>
          json(
            {
              data: { ...comment, author_name: "访客" },
              meta: { replayed: false, request_id: "req_create" },
            },
            201,
          ),
        ) as typeof fetch,
      ).createComment("csrf", post.id, "回应", null, "web-account-identity"),
    ).resolves.toMatchObject({
      comment: {
        authorName: "访客",
        author: { userId: comment.author.user_id },
      },
    });
  });

  it("rejects an owner create response for a different parent", async () => {
    await expect(
      new CommentsApi(
        vi.fn(async () =>
          json(
            {
              data: comment,
              meta: { replayed: false, request_id: "req_create" },
            },
            201,
          ),
        ) as typeof fetch,
      ).createComment(
        "csrf",
        post.id,
        "回应",
        "01K2ABCDEF0123456789ABCDE2",
        "web-owner-parent",
      ),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it.each([
    { status: 200, replayed: false },
    { status: 201, replayed: true },
    { status: 202, replayed: false },
  ])(
    "rejects comment create HTTP $status with replayed=$replayed",
    async ({ status, replayed }) => {
      await expect(
        new CommentsApi(
          vi.fn(async () =>
            json(
              { data: comment, meta: { replayed, request_id: "req_create" } },
              status,
            ),
          ) as typeof fetch,
        ).createComment("csrf", post.id, "回应", null, "web-owner-status"),
      ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
    },
  );
});

it("forwards optional reply exclusions without replacing explicit body mentions", async () => {
  const fetcher = vi.fn<typeof fetch>(async () =>
    json(
      {
        data: comment,
        meta: { request_id: "reply-exclusions", replayed: false },
      },
      201,
    ),
  );
  const api = new CommentsApi(fetcher);
  await api.createComment(
    "csrf",
    post.id,
    "@cedar stays explicit",
    null,
    "reply-key",
    [],
    null,
    null,
    ["1".repeat(32)],
  );
  expect(JSON.parse(String(fetcher.mock.calls[0][1]?.body))).toMatchObject({
    content_md: "@cedar stays explicit",
    exclude_reply_user_ids: ["1".repeat(32)],
  });
});
