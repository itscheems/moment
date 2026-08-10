import { afterEach, describe, expect, it, vi } from "vitest";

import { OwnerApi } from "./owner";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const post = {
  id: "01K2ABCDEF0123456789ABCDE0",
  content_md: "现在写下",
  content_html: "<p>现在写下</p>",
  created_at: "2026-08-10T01:02:03.000Z",
  updated_at: "2026-08-10T01:02:03.000Z",
  published_at: "2026-08-10T01:02:03.000Z",
  media: [],
};

const comment = {
  id: "01K2ABCDEF0123456789ABCDE1",
  post_id: post.id,
  parent_id: null,
  depth: 0,
  author_kind: "owner",
  author_name: "Owner",
  content_md: "所有者回应",
  content_html: "<p>所有者回应</p>\n",
  created_at: "2026-08-10T01:03:00Z",
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
  preview_url: "/api/v1/media/01K2ABCDEF0123456789ABCDE1/preview",
};

afterEach(() => {
  FakeUploadRequest.last = null;
  vi.unstubAllGlobals();
});

describe("owner API client", () => {
  it("patches only changed site fields and parses auth rotation metadata", async () => {
    const requests: Array<{ url: string; init?: RequestInit }> = [];
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        requests.push({ url, init });
        if (url.endsWith("/site")) {
          return json({
            data: {
              title: "雨天记录",
              bio: "沿着日常留下坐标",
              timezone: "Asia/Shanghai",
              public_base_url: "https://moment.example.com",
              avatar_media_id: null,
              comments_enabled: false,
            },
            meta: { request_id: "req_site" },
          });
        }
        if (url.endsWith("/status")) {
          return json({
            data: {
              initialized: true,
              session_epoch: 4,
              initialized_at: "2026-08-01T00:00:00Z",
              rotated_at: "2026-08-10T00:00:00Z",
            },
            meta: { request_id: "req_status" },
          });
        }
        return json({
          data: {
            token: "new-owner-token",
            initialized: true,
            session_epoch: 5,
            initialized_at: "2026-08-01T00:00:00Z",
            rotated_at: "2026-08-11T00:00:00Z",
          },
          meta: { request_id: "req_rotate" },
        });
      },
    );
    const api = new OwnerApi(fetcher as typeof fetch);

    await expect(
      api.updateSite("csrf-settings", {
        title: "雨天记录",
        publicBaseUrl: "https://moment.example.com",
        commentsEnabled: false,
      }),
    ).resolves.toMatchObject({
      title: "雨天记录",
      publicBaseUrl: "https://moment.example.com",
      commentsEnabled: false,
      instanceId: null,
    });
    expect(JSON.parse(String(requests[0].init?.body))).toEqual({
      title: "雨天记录",
      public_base_url: "https://moment.example.com",
      comments_enabled: false,
    });
    expect(new Headers(requests[0].init?.headers).get("X-CSRF-Token")).toBe(
      "csrf-settings",
    );

    await expect(api.status()).resolves.toMatchObject({
      sessionEpoch: 4,
      rotatedAt: "2026-08-10T00:00:00Z",
    });
    await expect(api.rotateToken("csrf-settings")).resolves.toMatchObject({
      token: "new-owner-token",
      sessionEpoch: 5,
    });
    expect(requests[2].init?.body).toBeUndefined();
    expect(new Headers(requests[2].init?.headers).get("X-CSRF-Token")).toBe(
      "csrf-settings",
    );
  });

  it("fails closed on non-canonical settings, status, and rotation successes", async () => {
    const siteData = {
      title: "雨天记录",
      bio: "沿着日常留下坐标",
      timezone: "Asia/Shanghai",
      public_base_url: "https://moment.example.com",
      avatar_media_id: null,
      comments_enabled: false,
    };
    const statusData = {
      initialized: true,
      session_epoch: 4,
      initialized_at: "2026-08-01T00:00:00Z",
      rotated_at: "2026-08-10T00:00:00Z",
    };
    const rotationData = {
      token: "new-owner-token",
      initialized: true,
      session_epoch: 5,
      initialized_at: "2026-08-01T00:00:00Z",
      rotated_at: "2026-08-11T00:00:00Z",
    };
    const scenarios: Array<{
      name: string;
      response: () => Response;
      run: (api: OwnerApi) => Promise<unknown>;
    }> = [
      {
        name: "site requires HTTP 200",
        response: () =>
          json({ data: siteData, meta: { request_id: "req_site" } }, 201),
        run: (api) => api.updateSite("csrf", { title: "雨天记录" }),
      },
      {
        name: "site rejects extra data",
        response: () =>
          json({
            data: { ...siteData, server_path: "/private/avatar" },
            meta: { request_id: "req_site" },
          }),
        run: (api) => api.updateSite("csrf", { title: "雨天记录" }),
      },
      {
        name: "status requires application/json",
        response: () =>
          new Response(
            JSON.stringify({
              data: statusData,
              meta: { request_id: "req_status" },
            }),
            { status: 200, headers: { "Content-Type": "text/plain" } },
          ),
        run: (api) => api.status(),
      },
      {
        name: "status rejects extra envelope fields",
        response: () =>
          json({
            data: statusData,
            meta: { request_id: "req_status" },
            debug: true,
          }),
        run: (api) => api.status(),
      },
      {
        name: "status rejects extra metadata",
        response: () =>
          json({
            data: statusData,
            meta: { request_id: "req_status", session: "private" },
          }),
        run: (api) => api.status(),
      },
      {
        name: "status requires a positive epoch",
        response: () =>
          json({
            data: { ...statusData, session_epoch: 0 },
            meta: { request_id: "req_status" },
          }),
        run: (api) => api.status(),
      },
      {
        name: "status requires RFC3339 UTC instants",
        response: () =>
          json({
            data: {
              ...statusData,
              initialized_at: "2026-08-01T08:00:00+08:00",
            },
            meta: { request_id: "req_status" },
          }),
        run: (api) => api.status(),
      },
      {
        name: "rotation rejects false initialized state",
        response: () =>
          json({
            data: { ...rotationData, initialized: false },
            meta: { request_id: "req_rotate" },
          }),
        run: (api) => api.rotateToken("csrf"),
      },
      {
        name: "rotation rejects extra credential fields",
        response: () =>
          json({
            data: { ...rotationData, token_hint: "secret" },
            meta: { request_id: "req_rotate" },
          }),
        run: (api) => api.rotateToken("csrf"),
      },
      {
        name: "rotation rejects impossible UTC dates",
        response: () =>
          json({
            data: { ...rotationData, rotated_at: "2026-02-30T00:00:00Z" },
            meta: { request_id: "req_rotate" },
          }),
        run: (api) => api.rotateToken("csrf"),
      },
    ];

    for (const scenario of scenarios) {
      const api = new OwnerApi(
        vi.fn(async () => scenario.response()) as typeof fetch,
      );
      await expect(scenario.run(api), scenario.name).rejects.toMatchObject({
        code: "API_RESPONSE_INVALID",
      });
    }

    const unrotated = new OwnerApi(
      vi.fn(async () =>
        json({
          data: {
            initialized: true,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00.123456789Z",
          },
          meta: { request_id: "req_status" },
        }),
      ) as typeof fetch,
    );
    await expect(unrotated.status()).resolves.toMatchObject({
      sessionEpoch: 1,
      rotatedAt: null,
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
              })),
            },
            meta: { replayed: false },
          },
          201,
        );
      },
    );
    const api = new OwnerApi(fetcher as typeof fetch);
    await api.create("csrf", post.content_md, "web-media-order", media);

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
    const api = new OwnerApi(vi.fn() as typeof fetch);
    const progress = vi.fn();
    const promise = api.uploadMedia(
      "csrf-upload",
      new File(["image"], "rain.png", { type: "image/png" }),
      progress,
    );
    const request = FakeUploadRequest.last;
    expect(request?.method).toBe("POST");
    expect(request?.url).toBe("/api/v1/owner/media");
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
          preview_url: "/api/v1/media/81K2ABCDEF0123456789ABCDE1/preview",
        },
        meta: { request_id: "req_upload" },
      },
      contentType: "application/json",
    },
  ])(
    "rejects $name for upload success",
    async ({ status, body, contentType }) => {
      vi.stubGlobal("XMLHttpRequest", FakeUploadRequest);
      const api = new OwnerApi(vi.fn() as typeof fetch);
      const promise = api.uploadMedia(
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
    const api = new OwnerApi(fetcher as typeof fetch);
    await api.exchange("bearer-secret");
    await api.create("csrf-only", post.content_md, "web-retry-1");

    const exchange = fetcher.mock.calls[0];
    expect(exchange[0]).toBe("/api/v1/owner/session");
    expect(exchange[1]?.body).toBe(JSON.stringify({ token: "bearer-secret" }));
    const mutation = fetcher.mock.calls[1][1] as RequestInit;
    const headers = new Headers(mutation.headers);
    expect(headers.get("Authorization")).toBeNull();
    expect(headers.get("X-CSRF-Token")).toBe("csrf-only");
    expect(headers.get("Idempotency-Key")).toBe("web-retry-1");
    expect(mutation.credentials).toBe("same-origin");
  });

  it("rejects malformed session and mutation responses", async () => {
    const invalidSession = new OwnerApi(
      vi.fn(async () =>
        json({ data: { expires_at: "later" }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(invalidSession.exchange("token")).rejects.toMatchObject({
      code: "API_RESPONSE_INVALID",
    });

    const invalidPost = new OwnerApi(
      vi.fn(async () =>
        json({ data: { id: post.id }, meta: {} }),
      ) as typeof fetch,
    );
    await expect(
      invalidPost.update("csrf", post.id, "changed"),
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
    const api = new OwnerApi(fetcher as typeof fetch);

    await expect(
      api.createComment(
        "csrf",
        post.id,
        "所有者回应",
        null,
        "web-comment-owner",
      ),
    ).resolves.toMatchObject({ comment: { authorKind: "owner" } });
    const createInit = fetcher.mock.calls[0][1] as RequestInit;
    expect(JSON.parse(String(createInit.body))).toEqual({
      content_md: "所有者回应",
      parent_id: null,
    });
    expect(String(createInit.body)).not.toContain("author_name");
    expect(new Headers(createInit.headers).get("X-CSRF-Token")).toBe("csrf");

    await expect(
      api.deleteComment("csrf", post.id, comment.id),
    ).resolves.toMatchObject({ repliesAffected: 2 });
  });

  it("fails closed on contradictory comment delete metadata", async () => {
    const api = new OwnerApi(
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
    );
    await expect(
      api.deleteComment("csrf", post.id, comment.id),
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
    const api = new OwnerApi(
      vi.fn(async () => response.clone()) as typeof fetch,
    );
    await expect(
      api.deleteComment("csrf", post.id, comment.id),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });

  it("rejects a forged guest identity in an owner create response", async () => {
    const api = new OwnerApi(
      vi.fn(async () =>
        json(
          {
            data: { ...comment, author_kind: "guest", author_name: "访客" },
            meta: { replayed: false, request_id: "req_create" },
          },
          201,
        ),
      ) as typeof fetch,
    );
    await expect(
      api.createComment("csrf", post.id, "回应", null, "web-owner-identity"),
    ).rejects.toMatchObject({ code: "API_RESPONSE_INVALID" });
  });
});
