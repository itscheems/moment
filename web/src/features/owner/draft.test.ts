import { beforeEach, describe, expect, it, vi } from "vitest";

import {
  COMPOSE_DRAFT_CLEARED_EVENT,
  COMPOSE_DRAFT_CLEARED_SCHEMA,
  clearComposeDraft,
  composeDraftKey,
  loadComposeDraft,
  parseComposeDraftClearedIdentity,
  saveComposeDraft,
} from "./draft";

const instance = "0123456789abcdef0123456789abcdef";

describe("instance-scoped compose draft", () => {
  beforeEach(() => window.localStorage.clear());

  it("stores only Markdown and a timestamp under the runtime instance ID", () => {
    const savedAt = new Date("2026-08-10T01:02:03.000Z");
    expect(saveComposeDraft(instance, "一段草稿", savedAt)).toBe(true);
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBe(
      JSON.stringify({
        content_md: "一段草稿",
        saved_at: savedAt.toISOString(),
      }),
    );
    expect(loadComposeDraft(instance, savedAt.getTime())).toEqual({
      contentMd: "一段草稿",
      savedAt: savedAt.toISOString(),
    });
  });

  it("does not guess an instance and removes expired or malformed drafts", () => {
    expect(saveComposeDraft(null, "不能落盘")).toBe(false);
    expect(window.localStorage.length).toBe(0);

    const old = new Date("2026-01-01T00:00:00.000Z");
    saveComposeDraft(instance, "过期", old);
    expect(
      loadComposeDraft(instance, old.getTime() + 31 * 24 * 60 * 60 * 1_000),
    ).toBeNull();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();

    window.localStorage.setItem(composeDraftKey(instance), '{"token":"no"}');
    expect(loadComposeDraft(instance)).toBeNull();
  });

  it("fails open when localStorage is denied", () => {
    const setter = vi
      .spyOn(window.localStorage, "setItem")
      .mockImplementation(() => {
        throw new DOMException("denied");
      });
    expect(saveComposeDraft(instance, "仍可发布")).toBe(false);
    setter.mockRestore();
  });

  it("clears only the selected instance draft", () => {
    const other = "fedcba9876543210fedcba9876543210";
    const savedAt = new Date("2026-08-10T01:02:03.000Z");
    saveComposeDraft(instance, "这里", savedAt);
    saveComposeDraft(other, "那里");
    const cleared = vi.fn();
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);
    clearComposeDraft(instance);
    window.removeEventListener(COMPOSE_DRAFT_CLEARED_EVENT, cleared);
    expect(loadComposeDraft(instance)).toBeNull();
    expect(loadComposeDraft(other)?.contentMd).toBe("那里");
    expect(cleared).toHaveBeenCalledOnce();
    expect((cleared.mock.calls[0]?.[0] as CustomEvent).detail).toEqual({
      schema: COMPOSE_DRAFT_CLEARED_SCHEMA,
      instance_id: instance,
      content_md: "这里",
      saved_at: savedAt.toISOString(),
    });
  });

  it("strictly rejects malformed clear identities", () => {
    expect(
      parseComposeDraftClearedIdentity({
        schema: COMPOSE_DRAFT_CLEARED_SCHEMA,
        instance_id: instance,
        content_md: "这里",
      }),
    ).toBeNull();
    expect(
      parseComposeDraftClearedIdentity({
        schema: COMPOSE_DRAFT_CLEARED_SCHEMA,
        instance_id: instance,
        content_md: "这里",
        saved_at: "2026-08-10T01:02:03.000Z",
        unexpected: true,
      }),
    ).toBeNull();
  });
});
