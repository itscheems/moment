import { describe, expect, it, vi } from "vitest";
import type { PublicPost, SiteIdentity } from "../../api/public";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import { loadPostShareCard } from "./shareCardInput";

const site: SiteIdentity = {
  profileUserId: "0".repeat(32),
  username: "writer",
  displayName: "Writer",
  title: "Notes",
  bio: "",
  timezone: "UTC",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

const original: PublicPost = {
  id: "01K2ABCDEF0123456789ABCDE0",
  kind: "moment",
  article: null,
  contentMd: "A linked note",
  contentHtml: "<p>A linked note</p>",
  createdAt: "2026-08-10T01:02:03.000Z",
  updatedAt: "2026-08-10T01:02:03.000Z",
  publishedAt: "2026-08-10T01:02:03.000Z",
  repostedAt: null,
  timelineItemKind: "original",
  visibility: "everyone",
  commentCount: 0,
  media: [],
  linkCards: [
    {
      position: 0,
      canonicalUrl: "https://example.test/notes",
      cardKind: "link",
      title: "Linked notes",
      description: "",
      siteName: "Example",
      authorName: "",
      durationSeconds: null,
      viewCount: null,
      commentCount: null,
      likeCount: null,
      faviconMediaId: null,
      thumbnailMediaId: "01K2ABCDEF0123456789ABCDE2",
      thumbnailMimeType: "image/png",
      thumbnailWidth: 640,
      thumbnailHeight: 360,
      embedUrl: null,
    },
  ],
};

const quoting: PublicPost = {
  ...original,
  id: "01K2ABCDEF0123456789ABCDE1",
  contentMd: "A response",
  contentHtml: "<p>A response</p>",
  linkCards: [],
  quotedPost: { ...original, unavailable: false, location: null },
};

describe("loading a post share image", () => {
  it("loads the quoted target's preview and keeps the wrapper's body", async () => {
    const api = { post: vi.fn().mockResolvedValue(original) };
    const signal = new AbortController().signal;
    const card = await loadPostShareCard(api, quoting, site, signal);
    expect(api.post).toHaveBeenCalledExactlyOnceWith(original.id, signal);
    expect(card.blocks).toEqual([{ kind: "text", text: "A response" }]);
    expect(card.link).toBeNull();
    expect(card.quote?.link).toEqual({
      title: "Linked notes",
      siteName: "Example",
      thumbnailUrl: mediaPreviewUrl(original.linkCards[0].thumbnailMediaId!),
      video: false,
    });
    expect(card.quote?.blocks).toEqual([
      { kind: "text", text: "A linked note" },
    ]);
  });

  it("does not read a post endpoint for unavailable or comment quotes", async () => {
    const api = { post: vi.fn() };
    const missing = await loadPostShareCard(
      api,
      { ...quoting, quotedPost: { unavailable: true } },
      site,
    );
    expect(missing.quoteUnavailable).toBe(true);
    await loadPostShareCard(
      api,
      {
        ...quoting,
        quotedPost: {
          ...original,
          unavailable: false,
          location: null,
          commentPostId: original.id,
        },
      },
      site,
    );
    await loadPostShareCard(api, original, site);
    expect(api.post).not.toHaveBeenCalled();
  });

  it("reports failed quote reads instead of exporting an incomplete preview", async () => {
    const failure = new Error("read failed");
    await expect(
      loadPostShareCard(
        { post: vi.fn().mockRejectedValue(failure) },
        quoting,
        site,
      ),
    ).rejects.toBe(failure);
  });
});
