import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  COMPOSE_DRAFT_CLEARANCE_SCHEMA,
  COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
  COMPOSE_DRAFT_CLEARED_EVENT,
  clearComposeDraft,
  composeDraftClearAllKey,
  composeDraftClearanceKey,
  composeDraftKey,
  createComposeDraft,
  fingerprintComposeDraft,
  loadComposeDraft,
  parseComposeDraftClearance,
  saveComposeDraft,
  type ComposeDraft,
} from "./draft";

const instance = "0123456789abcdef0123456789abcdef";
const other = "fedcba9876543210fedcba9876543210";

function draft(contentMd: string, savedAt: string): ComposeDraft {
  return {
    contentMd,
    fingerprint: fingerprintComposeDraft(contentMd),
    savedAt,
  };
}

describe("instance-scoped compose draft", () => {
  beforeEach(() => window.localStorage.clear());
  afterEach(() => vi.restoreAllMocks());

  it("keeps the frozen v1 value exactly to Markdown and saved_at", () => {
    const value = draft("一段草稿", "2026-08-10T01:02:03.000Z");
    expect(saveComposeDraft(instance, value)).toBe(true);
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBe(
      JSON.stringify({
        content_md: "一段草稿",
        saved_at: "2026-08-10T01:02:03.000Z",
      }),
    );
    expect(loadComposeDraft(instance, Date.parse(value.savedAt))).toEqual(
      value,
    );
  });

  it("reads old exact v1 values and removes expired or malformed values", () => {
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "旧版草稿",
        saved_at: "2026-08-10T01:02:03.000Z",
      }),
    );
    expect(
      loadComposeDraft(instance, Date.parse("2026-08-10T01:02:03.000Z")),
    ).toMatchObject({ contentMd: "旧版草稿" });
    expect(window.localStorage.length).toBe(1);

    const old = draft("过期", "2026-01-01T00:00:00.000Z");
    saveComposeDraft(instance, old);
    expect(
      loadComposeDraft(
        instance,
        Date.parse(old.savedAt) + 31 * 24 * 60 * 60 * 1_000,
      ),
    ).toBeNull();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();

    window.localStorage.setItem(composeDraftKey(instance), '{"token":"no"}');
    const writes = vi
      .spyOn(window.localStorage, "setItem")
      .mockImplementation(() => {
        throw new DOMException("quota exceeded");
      });
    expect(loadComposeDraft(instance)).toBeNull();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    writes.mockRestore();
  });

  it("removes a far-future draft without poisoning the next timestamp", () => {
    const now = Date.parse("2026-08-10T01:02:03.000Z");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "来自遥远未来",
        saved_at: "+275760-09-13T00:00:00.000Z",
      }),
    );

    expect(loadComposeDraft(instance, now)).toBeNull();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();

    const current = createComposeDraft(
      "现在重新写",
      null,
      new Date("2026-08-10T01:02:04.000Z"),
    );
    expect(current.savedAt).toBe("2026-08-10T01:02:04.000Z");
    expect(saveComposeDraft(instance, current)).toBe(true);
    expect(loadComposeDraft(instance, Date.parse(current.savedAt))).toEqual(
      current,
    );
  });

  it("fails open when localStorage is denied", () => {
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new DOMException("denied");
    });
    expect(
      saveComposeDraft(instance, draft("仍可发布", "2026-08-10T01:02:03.000Z")),
    ).toBe(false);
  });

  it("physically removes published content and leaves only a bounded marker", () => {
    const published = draft("已经发布", "2026-08-10T01:02:03.000Z");
    const clearedAt = new Date("2026-08-10T01:03:00.000Z");
    saveComposeDraft(instance, published);
    saveComposeDraft(other, draft("另一个实例", "2026-08-10T01:02:03.000Z"));
    const cleared = vi.fn();
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);

    clearComposeDraft(
      instance,
      { reason: "published", draft: published },
      clearedAt,
    );

    window.removeEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    expect(loadComposeDraft(instance, clearedAt.getTime())).toBeNull();
    expect(loadComposeDraft(other, clearedAt.getTime())?.contentMd).toBe(
      "另一个实例",
    );
    const detail = {
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instance,
      draft_fingerprint: published.fingerprint,
      draft_saved_at: published.savedAt,
      reason: "published",
      cleared_at: clearedAt.toISOString(),
    };
    expect((cleared.mock.calls[0]?.[0] as CustomEvent).detail).toEqual(detail);
    const marker = window.localStorage.getItem(
      composeDraftClearanceKey(instance),
    );
    expect(marker).toBe(JSON.stringify(detail));
    expect(marker).not.toContain("已经发布");
  });

  it("removes an older projection of the same published text", () => {
    const olderProjection = draft("同一份正文", "2026-08-10T01:02:03.000Z");
    const submitted = draft("同一份正文", "2026-08-10T01:02:04.000Z");
    saveComposeDraft(instance, olderProjection);

    clearComposeDraft(
      instance,
      { reason: "published", draft: submitted },
      new Date("2026-08-10T01:03:00.000Z"),
    );

    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    expect(
      loadComposeDraft(instance, Date.parse("2026-08-10T01:03:00.000Z")),
    ).toBeNull();
  });

  it("does not let a stale discard remove a newer persisted draft", () => {
    const stale = draft("准备丢弃", "2026-08-10T01:02:03.000Z");
    const newer = draft("更新后的版本", "2026-08-10T01:02:04.000Z");
    saveComposeDraft(instance, stale);
    saveComposeDraft(instance, newer);

    clearComposeDraft(instance, { reason: "discarded", draft: stale });

    expect(window.localStorage.getItem(composeDraftKey(instance))).toBe(
      JSON.stringify({
        content_md: newer.contentMd,
        saved_at: newer.savedAt,
      }),
    );
    expect(loadComposeDraft(instance, Date.parse(newer.savedAt))).toEqual(
      newer,
    );
  });

  it("keeps one clearance key and restores the same text saved later", () => {
    const first = draft("同一句话", "2026-08-10T01:02:03.000Z");
    const second = draft("同一句话", "2026-08-10T01:04:00.000Z");
    const third = draft("第三版", "2026-08-10T01:05:00.000Z");
    saveComposeDraft(instance, first);
    clearComposeDraft(
      instance,
      { reason: "published", draft: first },
      new Date("2026-08-10T01:03:00.000Z"),
    );
    expect(loadComposeDraft(instance, Date.parse(first.savedAt))).toBeNull();

    saveComposeDraft(instance, second);
    expect(loadComposeDraft(instance, Date.parse(second.savedAt))).toEqual(
      second,
    );
    clearComposeDraft(
      instance,
      { reason: "discarded", draft: second },
      new Date("2026-08-10T01:04:01.000Z"),
    );
    expect(loadComposeDraft(instance, Date.parse(second.savedAt))).toBeNull();
    expect(
      Array.from({ length: window.localStorage.length }, (_, index) =>
        window.localStorage.key(index),
      ).filter((key) => key?.startsWith("moment.compose.clearance.v1:")),
    ).toEqual([composeDraftClearanceKey(instance)]);

    saveComposeDraft(instance, third);
    expect(loadComposeDraft(instance, Date.parse(third.savedAt))).toEqual(
      third,
    );
  });

  it("dispatches a strong clearance when marker persistence is denied", () => {
    vi.spyOn(window.localStorage, "setItem").mockImplementation(() => {
      throw new DOMException("denied");
    });
    const published = draft("无法落盘", "2026-08-10T01:02:03.000Z");
    const cleared = vi.fn();
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);

    clearComposeDraft(instance, { reason: "published", draft: published });

    window.removeEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);
    expect(cleared).toHaveBeenCalledOnce();
    expect((cleared.mock.calls[0]?.[0] as CustomEvent).detail).toMatchObject({
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instance,
      draft_fingerprint: published.fingerprint,
      draft_saved_at: published.savedAt,
      reason: "published",
    });
  });

  it("still removes matching text when the clearance marker cannot be written", () => {
    const published = draft("写标记失败也不留正文", "2026-08-10T01:02:03.000Z");
    saveComposeDraft(instance, published);
    const originalSetItem = window.localStorage.setItem.bind(
      window.localStorage,
    );
    vi.spyOn(window.localStorage, "setItem").mockImplementation(
      (key, value) => {
        if (key === composeDraftClearanceKey(instance)) {
          throw new DOMException("quota exceeded");
        }
        originalSetItem(key, value);
      },
    );

    clearComposeDraft(instance, { reason: "published", draft: published });

    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    expect(
      window.localStorage.getItem(composeDraftClearanceKey(instance)),
    ).toBeNull();
  });

  it("keeps logout as the only unconditional all-clear", () => {
    const clearedAt = new Date("2026-08-10T01:03:00.000Z");
    saveComposeDraft(
      instance,
      draft("退出时全部清理", "2026-08-10T01:02:03.000Z"),
    );
    const cleared = vi.fn();
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);

    clearComposeDraft(instance, { reason: "logout" }, clearedAt);

    window.removeEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    const detail = {
      schema: COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
      instance_id: instance,
      reason: "logout",
      cleared_at: clearedAt.toISOString(),
    };
    expect((cleared.mock.calls[0]?.[0] as CustomEvent).detail).toEqual(detail);
    expect(window.localStorage.getItem(composeDraftClearAllKey(instance))).toBe(
      JSON.stringify(detail),
    );
  });

  it("announces logout after its marker and before removing the draft", () => {
    const value = draft("退出顺序", "2026-08-10T01:02:03.000Z");
    saveComposeDraft(instance, value);
    const operations: string[] = [];
    const originalSetItem = window.localStorage.setItem.bind(
      window.localStorage,
    );
    const originalRemoveItem = window.localStorage.removeItem.bind(
      window.localStorage,
    );
    vi.spyOn(window.localStorage, "setItem").mockImplementation((key, next) => {
      if (key === composeDraftClearAllKey(instance)) operations.push("marker");
      originalSetItem(key, next);
    });
    vi.spyOn(window.localStorage, "removeItem").mockImplementation((key) => {
      if (key === composeDraftKey(instance)) operations.push("remove");
      originalRemoveItem(key);
    });
    const onClear = () => operations.push("event");
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, onClear);

    clearComposeDraft(instance, { reason: "logout" });

    window.removeEventListener(COMPOSE_DRAFT_CLEARED_EVENT, onClear);
    expect(operations).toEqual(["marker", "event", "remove"]);
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
  });

  it("strictly rejects malformed fingerprint clearances", () => {
    const published = draft("这里", "2026-08-10T01:02:03.000Z");
    expect(
      parseComposeDraftClearance({
        schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
        instance_id: instance,
        draft_fingerprint: published.fingerprint,
        draft_saved_at: published.savedAt,
        reason: "published",
      }),
    ).toBeNull();
    expect(
      parseComposeDraftClearance({
        schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
        instance_id: instance,
        draft_fingerprint: published.fingerprint,
        draft_saved_at: published.savedAt,
        reason: "published",
        cleared_at: "2026-08-10T01:03:00.000Z",
        unexpected: true,
      }),
    ).toBeNull();
  });
});
