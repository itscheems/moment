import { afterEach, describe, expect, it } from "vitest";

import { fingerprintComposeDraft } from "./draft";
import {
  clearKeptDrafts,
  dropKeptDraft,
  dropKeptDraftByContent,
  keepDraft,
  keptDraftsKey,
  listKeptDrafts,
} from "./drafts";

const instance = "a".repeat(32);

afterEach(() => {
  window.localStorage.clear();
});

describe("kept compose drafts", () => {
  it("keeps drafts newest first and lists them back", () => {
    keepDraft(instance, "第一段", new Date("2026-09-01T00:00:00.000Z"));
    keepDraft(instance, "第二段", new Date("2026-09-02T00:00:00.000Z"));

    expect(
      listKeptDrafts(instance, Date.parse("2026-09-03T00:00:00.000Z")).map(
        (draft) => draft.contentMd,
      ),
    ).toEqual(["第二段", "第一段"]);
  });

  it("refiles the same text in place instead of listing it twice", () => {
    const first = keepDraft(
      instance,
      "同一段",
      new Date("2026-09-01T00:00:00.000Z"),
    );
    const again = keepDraft(
      instance,
      "同一段",
      new Date("2026-09-02T00:00:00.000Z"),
    );

    expect(again?.id).toBe(first?.id);
    const listed = listKeptDrafts(
      instance,
      Date.parse("2026-09-03T00:00:00.000Z"),
    );
    expect(listed).toHaveLength(1);
    expect(listed[0].savedAt).toBe("2026-09-02T00:00:00.000Z");
  });

  it("refuses blank text and a foreign instance", () => {
    expect(keepDraft(instance, "   ")).toBeNull();
    expect(keepDraft("not-an-instance", "有内容")).toBeNull();
    expect(listKeptDrafts("not-an-instance")).toEqual([]);
  });

  it("drops one draft by id and one by its exact text", () => {
    const one = keepDraft(instance, "留着", new Date("2026-09-01T00:00:00Z"));
    keepDraft(instance, "删掉", new Date("2026-09-02T00:00:00.000Z"));
    const now = Date.parse("2026-09-03T00:00:00.000Z");

    dropKeptDraftByContent(instance, "删掉", now);
    expect(listKeptDrafts(instance, now).map((draft) => draft.id)).toEqual([
      one?.id,
    ]);

    dropKeptDraft(instance, one?.id ?? "", now);
    expect(listKeptDrafts(instance, now)).toEqual([]);
    expect(window.localStorage.getItem(keptDraftsKey(instance))).toBeNull();
  });

  it("carries a content fingerprint so the recovery slot can be matched", () => {
    keepDraft(instance, "对照一下", new Date("2026-09-01T00:00:00.000Z"));
    expect(
      listKeptDrafts(instance, Date.parse("2026-09-02T00:00:00.000Z"))[0]
        .fingerprint,
    ).toBe(fingerprintComposeDraft("对照一下"));
  });

  it("ignores stored data that is not exactly this schema", () => {
    const now = Date.parse("2026-09-03T00:00:00.000Z");
    for (const stored of [
      "{",
      JSON.stringify({ schema: "other", drafts: [] }),
      JSON.stringify({ drafts: [] }),
      JSON.stringify({
        schema: "moment.compose-kept-drafts.v1",
        drafts: [{ id: "short", content_md: "x", saved_at: "2026-09-01" }],
      }),
    ]) {
      window.localStorage.setItem(keptDraftsKey(instance), stored);
      expect(listKeptDrafts(instance, now)).toEqual([]);
    }
  });

  it("forgets a draft older than thirty days and one dated ahead", () => {
    keepDraft(instance, "很久以前", new Date("2026-07-01T00:00:00.000Z"));
    expect(
      listKeptDrafts(instance, Date.parse("2026-09-03T00:00:00.000Z")),
    ).toEqual([]);

    keepDraft(instance, "来自未来", new Date("2026-09-10T00:00:00.000Z"));
    expect(
      listKeptDrafts(instance, Date.parse("2026-09-03T00:00:00.000Z")),
    ).toEqual([]);
  });

  it("clears everything this instance kept when the owner signs out", () => {
    keepDraft(instance, "签退前写的", new Date("2026-09-01T00:00:00.000Z"));
    clearKeptDrafts(instance);
    expect(
      listKeptDrafts(instance, Date.parse("2026-09-02T00:00:00.000Z")),
    ).toEqual([]);
  });
});
