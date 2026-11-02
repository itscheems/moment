import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, renderHook } from "@testing-library/react";
import { LinksApi } from "../../api/links";
import type { PublicLinkCard } from "../../api/public";

import { detectLinkUrl, useLinkPreview } from "./useLinkPreview";

describe("detectLinkUrl", () => {
  it("picks the last URL, which is the one the renderer may hide", () => {
    expect(
      detectLinkUrl("先看 https://example.com/a 再看 https://example.com/b"),
    ).toBe("https://example.com/b");
  });

  it("strips trailing sentence punctuation, including full-width marks", () => {
    // The server's linkifier stops at the first non-ASCII character, so a
    // full-width period must not end up inside the URL the composer resolves.
    expect(detectLinkUrl("推荐 https://example.com/a。")).toBe(
      "https://example.com/a",
    );
    expect(detectLinkUrl("推荐 https://example.com/a).")).toBe(
      "https://example.com/a",
    );
    expect(detectLinkUrl("看 https://example.com/a，然后")).toBe(
      "https://example.com/a",
    );
  });

  it("refuses anything that is not http(s)", () => {
    expect(detectLinkUrl("file:///etc/passwd")).toBeNull();
    expect(detectLinkUrl("看看 www.example.com")).toBeNull();
    expect(detectLinkUrl("没有链接的一段话")).toBeNull();
  });
});

const stored: PublicLinkCard = {
  position: 0,
  canonicalUrl: "https://example.com/saved",
  cardKind: "link",
  title: "Stored title",
  description: "",
  siteName: "Example",
  authorName: "",
  durationSeconds: null,
  viewCount: null,
  commentCount: null,
  likeCount: null,
  faviconMediaId: null,
  thumbnailMediaId: null,
  thumbnailMimeType: null,
  thumbnailWidth: null,
  thumbnailHeight: null,
  embedUrl: null,
};
afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

it("dismisses and restores a detached saved card without treating it as a new snapshot", () => {
  const fetcher = vi.fn();
  const api = new LinksApi(fetcher);
  const { result } = renderHook(() =>
    useLinkPreview(api, "Caption", true, { card: stored, content: "Caption" }),
  );
  expect(result.current.storedCard).toEqual(stored);
  expect(result.current.changed).toBe(false);
  act(() => result.current.dismiss());
  expect(result.current.storedCard).toBeNull();
  expect(result.current.changed).toBe(true);
  act(() => result.current.restoreDismissed(new Set()));
  expect(result.current.storedCard).toEqual(stored);
  expect(result.current.changed).toBe(false);
  expect(fetcher).not.toHaveBeenCalled();
});

it("removes a body-bound card when its URL is removed and restores the saved snapshot on undo", () => {
  const api = new LinksApi(vi.fn());
  const content = "Caption https://example.com/saved";
  const { result, rerender } = renderHook(
    ({ text }) => useLinkPreview(api, text, true, { card: stored, content }),
    { initialProps: { text: content } },
  );
  rerender({ text: "Caption" });
  expect(result.current.storedCard).toBeNull();
  expect(result.current.changed).toBe(true);
  rerender({ text: content });
  expect(result.current.storedCard).toEqual(stored);
  expect(result.current.changed).toBe(false);
});

it("replaces a saved card only after a new URL resolves and rejects an obsolete response", async () => {
  vi.useFakeTimers();
  const pending = new Map<string, (response: Response) => void>();
  const fetcher = vi.fn(
    (input: RequestInfo | URL) =>
      new Promise<Response>((resolve) => pending.set(String(input), resolve)),
  );
  const api = new LinksApi(fetcher);
  const { result, rerender } = renderHook(
    ({ text }) =>
      useLinkPreview(api, text, true, { card: stored, content: "Caption" }),
    { initialProps: { text: "https://example.com/first" } },
  );
  await act(async () => {
    await vi.advanceTimersByTimeAsync(400);
  });
  rerender({ text: "https://example.com/second" });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(400);
  });
  const raw = (url: string) => ({
    source_url: url,
    canonical_url: url,
    provider: "generic",
    card_kind: "link",
    title: url,
    description: "",
    site_name: "Example",
    author_name: "",
    duration_seconds: null,
    view_count: null,
    comment_count: null,
    like_count: null,
    favicon_media_id: null,
    thumbnail_media_id: null,
    thumbnail_mime_type: null,
    thumbnail_width: null,
    thumbnail_height: null,
    embed_url: null,
    captured_at_ms: null,
  });
  const finish = (url: string) =>
    pending.get(`/api/v1/links/unfurl?url=${encodeURIComponent(url)}`)!(
      new Response(JSON.stringify({ data: raw(url), meta: {} }), {
        status: 200,
      }),
    );
  await act(async () => {
    finish("https://example.com/first");
  });
  expect(result.current.card).toBeNull();
  expect(result.current.storedCard).toEqual(stored);
  await act(async () => {
    finish("https://example.com/second");
  });
  expect(result.current.card?.canonical_url).toBe("https://example.com/second");
  expect(result.current.changed).toBe(true);
  rerender({ text: "Caption" });
  expect(result.current.storedCard).toEqual(stored);
  expect(result.current.changed).toBe(false);
});

it("does not revive a dismissed saved card when a replacement URL is removed", () => {
  const api = new LinksApi(vi.fn());
  const { result, rerender } = renderHook(
    ({ text }) =>
      useLinkPreview(api, text, true, { card: stored, content: "Caption" }),
    { initialProps: { text: "https://example.com/new" } },
  );
  expect(result.current.storedCard).toEqual(stored);
  act(() => result.current.dismiss());
  rerender({ text: "Caption" });
  expect(result.current.storedCard).toBeNull();
  expect(result.current.changed).toBe(true);
  rerender({ text: "https://example.com/another" });
  expect(result.current.storedCard).toBeNull();
});
