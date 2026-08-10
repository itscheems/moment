import { describe, expect, it, vi } from "vitest";

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

describe("owner API client", () => {
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
