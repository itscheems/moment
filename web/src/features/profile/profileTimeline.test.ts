import { afterEach, describe, expect, it, vi } from "vitest";
import {
  PublicApi,
  type PublicPost,
  type TimelinePage,
  type TimelineRequest,
} from "../../api/public";
import { ProfileTimeline } from "./profileTimeline";

function post(id: string, kind: "moment" | "article" = "moment"): PublicPost {
  return {
    id,
    kind,
    contentMd: kind === "moment" ? id : null,
    article: null,
    contentHtml: "",
    createdAt: "2026-10-06T10:00:00Z",
    updatedAt: "2026-10-06T10:00:00Z",
    editedAt: null,
    publishedAt: "2026-10-06T10:00:00Z",
    repostedAt: null,
    timelineItemKind: "original",
    visibility: "everyone",
    commentCount: 0,
    media: [],
    linkCards: [],
    location: null,
    quotedPost: null,
  };
}
function page(
  items: PublicPost[] = [],
  refreshCursor = "watermark",
  prevCursor: string | null = null,
  nextCursor: string | null = null,
): TimelinePage {
  return { items, refreshCursor, prevCursor, nextCursor };
}
function setup(handler: (request: TimelineRequest) => Promise<TimelinePage>) {
  const timeline = vi.fn(handler);
  const api = { timeline } as unknown as PublicApi;
  return { timeline, store: new ProfileTimeline(api, "") };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}
afterEach(() => vi.restoreAllMocks());

describe("profile incremental feeds", () => {
  it("checks an empty filter with its durable cursor, preserving older pagination", async () => {
    const { store, timeline } = setup(async (request) =>
      request.cursor
        ? page([post("new", "article")], "new-watermark")
        : page([], "empty-watermark"),
    );
    await store.activate("article");
    await store.activate("article");
    expect(timeline.mock.calls[1][0]).toMatchObject({
      kind: "article",
      cursor: "empty-watermark",
    });
    expect(store.snapshot().article.items.map((row) => row.id)).toEqual([
      "new",
    ]);
    expect(store.snapshot().article.nextCursor).toBeNull();
  });
  it("shares article cards without advancing All's coverage or losing new posts", async () => {
    const { store, timeline } = setup(async (request) => {
      if (!request.cursor)
        return page(
          [
            post(
              request.kind === "article" ? "old-article" : "old",
              request.kind ?? "moment",
            ),
          ],
          request.kind ? "article-head" : "all-head",
        );
      return request.kind === "article"
        ? page([post("new-article", "article")], "new-article-head")
        : page(
            [post("new-article", "article"), post("new-post")],
            "new-all-head",
          );
    });
    await store.activate("all");
    await store.activate("article");
    await store.activate("article");
    expect(
      store.snapshot().all.items.some((row) => row.id === "new-article"),
    ).toBe(true);
    expect(store.snapshot().all.refreshCursor).toBe("all-head");
    await store.activate("all");
    expect(timeline.mock.calls.at(-1)?.[0].cursor).toBe("all-head");
    expect(
      store.snapshot().all.items.filter((row) => row.id === "new-article"),
    ).toHaveLength(1);
    expect(
      store.snapshot().all.items.some((row) => row.id === "new-post"),
    ).toBe(true);
  });
  it("merges same-millisecond overlap and a local publication idempotently", async () => {
    const existing = post("b"),
      created = post("a");
    const { store } = setup(async (request) =>
      request.cursor ? page([existing, created]) : page([existing]),
    );
    await store.activate("all");
    store.apply({ kind: "created", post: created });
    await store.refresh("all");
    expect(store.snapshot().all.items.map((row) => row.id)).toEqual(["b", "a"]);
  });
  it("keeps older paging usable when refresh has already loaded rows in its millisecond", async () => {
    const { store } = setup(async (request) => {
      if (!request.cursor) return page([post("c")], "head", null, "older");
      if (request.cursor === "head")
        return page([post("c"), post("b")], "refreshed");
      return page([post("b"), post("a")], "ignored-older-watermark");
    });
    await store.activate("all");
    await store.refresh("all");
    await store.more("all");
    expect(store.snapshot().all.items.map((row) => row.id)).toEqual([
      "c",
      "b",
      "a",
    ]);
    expect(store.snapshot().all.moreError).toBeNull();
    expect(store.snapshot().all.nextCursor).toBeNull();
    expect(store.snapshot().all.refreshCursor).toBe("refreshed");
  });
  it("deduplicates in-flight checks and rechecks a publication received during the request", async () => {
    const pending = deferred<TimelinePage>();
    let checks = 0;
    const { store, timeline } = setup(async (request) =>
      !request.cursor
        ? page()
        : ++checks === 1
          ? pending.promise
          : page([post("new")]),
    );
    await store.activate("all");
    const check = store.refresh("all");
    void store.refresh("all");
    store.invalidate();
    store.invalidate();
    expect(timeline).toHaveBeenCalledTimes(2);
    pending.resolve(page());
    await check;
    await vi.waitFor(() => expect(timeline).toHaveBeenCalledTimes(3));
    expect(store.snapshot().all.items[0].id).toBe("new");
  });
  it("retains continuation progress on failure and retries without losing cached content", async () => {
    let fail = true;
    const { store, timeline } = setup(async (request) => {
      if (!request.cursor) return page([post("a")], "head", null, "older");
      if (request.cursor === "head")
        return page([post("b")], "partial", "next-update");
      if (fail) {
        fail = false;
        throw new TypeError("offline");
      }
      return page([post("c")], "complete");
    });
    await store.activate("all");
    await store.refresh("all");
    expect(store.snapshot().all.continuation).toBe("next-update");
    expect(store.snapshot().all.refreshError).toBeInstanceOf(TypeError);
    await store.refresh("all");
    expect(timeline.mock.calls.at(-1)?.[0].cursor).toBe("next-update");
    expect(store.snapshot().all.items).toHaveLength(3);
    expect(store.snapshot().all.nextCursor).toBe("older");
    expect(store.snapshot().all.refreshError).toBeNull();
  });
  it("continues more than three newer pages without rereading the head", async () => {
    const { store, timeline } = setup(async (request) => {
      if (!request.cursor) return page();
      const n = request.cursor === "watermark" ? 0 : Number(request.cursor);
      return page(
        [post(String(n))],
        `watermark-${n}`,
        n < 4 ? String(n + 1) : null,
      );
    });
    await store.activate("all");
    await store.refresh("all");
    expect(timeline).toHaveBeenCalledTimes(6);
    expect(store.snapshot().all.items).toHaveLength(5);
  });
  it("defers hidden-tab invalidations and aborts disposed page requests", async () => {
    const pending = deferred<TimelinePage>();
    const { store, timeline } = setup(async (request) =>
      request.cursor ? pending.promise : page(),
    );
    await store.activate("all");
    const visible = vi
      .spyOn(document, "visibilityState", "get")
      .mockReturnValue("hidden");
    store.invalidate();
    expect(timeline).toHaveBeenCalledTimes(1);
    visible.mockReturnValue("visible");
    const check = store.activate("all");
    const signal = timeline.mock.calls.at(-1)?.[0].signal;
    store.dispose();
    expect(signal?.aborted).toBe(true);
    pending.resolve(page([post("late")]));
    await check;
    expect(store.snapshot().all.items).toEqual([]);
  });
});
