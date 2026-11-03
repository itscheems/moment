import { afterEach, describe, expect, it, vi } from "vitest";
import { linksApi } from "../../api/links";

import { fingerprintComposeDraft } from "./draft";
import {
  clearKeptDrafts,
  dropKeptDraft,
  dropKeptDraftByContent,
  keepDraft,
  keepDraftWithPreview,
  keptDraftsKey,
  listKeptDrafts,
  linkDraftResourceKey,
} from "./drafts";

const instance = "a".repeat(32) + "." + "b".repeat(32);

it("isolates identical draft resources across browsers and account scopes", () => {
  const fingerprint = fingerprintComposeDraft("https://example.com/draft");
  const first = linkDraftResourceKey(instance, fingerprint);
  expect(linkDraftResourceKey(instance, fingerprint)).toBe(first);
  expect(
    linkDraftResourceKey("c".repeat(32) + "." + "d".repeat(32), fingerprint),
  ).not.toBe(first);
  window.localStorage.clear();
  expect(linkDraftResourceKey(instance, fingerprint)).not.toBe(first);
});

afterEach(() => {
  vi.restoreAllMocks();
  window.localStorage.clear();
});

it("preserves an existing draft and its resource references when local saving fails", async () => {
  const content = "https://example.com/draft";
  const snapshot = {
    card: {
      source_url: content,
      canonical_url: content,
      provider: "generic",
      card_kind: "link",
      title: "Saved preview",
      description: "",
      site_name: "Example",
      author_name: "",
      duration_seconds: null,
      view_count: null,
      comment_count: null,
      like_count: null,
      thumbnail_media_id: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
      favicon_media_id: null,
      thumbnail_mime_type: "image/png",
      thumbnail_width: 100,
      thumbnail_height: 100,
      embed_url: null,
      captured_at_ms: 1800000000000,
    },
    dismissed: [],
  };
  keepDraft(instance, content, undefined, undefined, undefined, snapshot);
  linkDraftResourceKey(instance, fingerprintComposeDraft(content));
  const retain = vi.spyOn(linksApi, "retainDraft").mockResolvedValue();
  vi.spyOn(window.localStorage, "setItem").mockImplementationOnce(() => {
    throw new Error("quota exceeded");
  });
  expect(
    await keepDraftWithPreview(
      instance,
      "csrf",
      content,
      undefined,
      undefined,
      { card: null, dismissed: [content] },
    ),
  ).toBeNull();
  expect(listKeptDrafts(instance)[0]?.linkPreview).toEqual(snapshot);
  expect(retain.mock.calls.map((call) => call[2])).toEqual([
    [],
    [snapshot.card.thumbnail_media_id],
  ]);
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

it("keeps pure comment quotes distinct and removes only the matching target", () => {
  const a = {
      id: "01K2ABCDEF0123456789ABCDE1",
      commentPostId: "01K2ABCDEF0123456789ABCDE0",
    },
    b = { ...a, id: "01K2ABCDEF0123456789ABCDE2" };
  expect(keepDraft(instance, "", new Date(), undefined, a)).not.toBeNull();
  expect(keepDraft(instance, "", new Date(), undefined, b)).not.toBeNull();
  expect(listKeptDrafts(instance)).toHaveLength(2);
  dropKeptDraftByContent(instance, "", undefined, undefined, a);
  expect(listKeptDrafts(instance).map((d) => d.quote)).toEqual([b]);
});
