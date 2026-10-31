import { describe, it, expect, vi } from "vitest";
import {
  InteractionsApi,
  parseInboxItem,
  parseNotificationState,
} from "./interactions";
const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV";
const event = {
  id,
  event_type: "comment.created",
  subject: { type: "comment", id },
  post_id: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
  post_kind: "article",
  reply_to_comment_id: null,
  reasons: ["reply", "mention"],
  occurred_at: "2026-10-31T01:00:00Z",
  actor: {
    user_id: "1".repeat(32),
    username: "cedar",
    display_name: "Cedar",
    avatar_media_id: null,
    profile_url: "/cedar",
  },
  seen: false,
  available: true,
  excerpt: "Current text",
  post: null,
  comment: null,
};
const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify({ data, meta: { request_id: "interactions" } }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
describe("interaction API contracts", () => {
  it("keeps event type, source kind and reason combinations independently readable", () => {
    const item = parseInboxItem(event);
    expect(item.eventType).toBe("comment.created");
    expect(item.subject.kind).toBe("comment");
    expect(item.postKind).toBe("article");
    expect(item.reasons).toEqual(["reply", "mention"]);
    expect(item.excerpt).toBe("Current text");
  });
  it("refuses protected projections in unavailable events and malformed progress", () => {
    expect(() => parseInboxItem({ ...event, available: false })).toThrow();
    expect(() =>
      parseInboxItem({
        ...event,
        actor: { ...event.actor, profile_url: "https://external.test" },
      }),
    ).toThrow();
    expect(() =>
      parseNotificationState({
        new_count: 1,
        mentions_new_count: 2,
        through_cursor: "cursor",
      }),
    ).toThrow();
    expect(
      parseInboxItem({ ...event, available: false, excerpt: "" }).available,
    ).toBe(false);
  });
  it("never acknowledges list or detail reads and sends the exact reviewed view boundary", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        json({
          items: [event],
          next_cursor: "",
          through_cursor: "mentions-boundary",
          new_count: 2,
          mentions_new_count: 1,
        }),
      )
      .mockResolvedValueOnce(json(event))
      .mockResolvedValueOnce(
        json({
          new_count: 1,
          mentions_new_count: 0,
          through_cursor: "all-boundary",
        }),
      );
    const api = new InteractionsApi(fetcher);
    await api.list("mentions");
    await api.get(id);
    expect(fetcher.mock.calls.map((call) => call[1].method)).toEqual([
      "GET",
      "GET",
    ]);
    await api.seen("csrf", "mentions", "mentions-boundary");
    expect(JSON.parse(fetcher.mock.calls[2][1].body)).toEqual({
      scope: "mentions",
      through_cursor: "mentions-boundary",
    });
    expect(
      new Headers(fetcher.mock.calls[2][1].headers).get("X-CSRF-Token"),
    ).toBe("csrf");
    expect(
      fetcher.mock.calls.map((call) => String(call[0])).join(),
    ).not.toContain("csrf");
  });
  it("batches only requested visible like targets and mutates without request bodies", async () => {
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce(
        json({ [id]: { like_count: 3, liked_by_me: false } }),
      )
      .mockResolvedValueOnce(json({ like_count: 4, liked_by_me: true }));
    const api = new InteractionsApi(fetcher);
    expect((await api.likes("post", [id]))[id].count).toBe(3);
    expect((await api.setLike("csrf", "post", id, true)).liked).toBe(true);
    expect(fetcher.mock.calls[1][1].method).toBe("PUT");
    expect(fetcher.mock.calls[1][1].body).toBeUndefined();
  });
});
