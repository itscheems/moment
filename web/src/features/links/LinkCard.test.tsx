import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { PublicLinkCard } from "../../api/public";
import { LinkCard } from "./LinkCard";

/**
 * Find an SVG by its viewBox.
 *
 * A `[viewBox=...]` selector cannot be used: querySelector case-folds
 * attribute names in an HTML document, so it would match nothing and any
 * `not.toBeNull()` assertion on the result would pass vacuously.
 */
function svgWithViewBox(viewBox: string): SVGElement | null {
  return (
    [...document.querySelectorAll("svg")].find(
      (node) => node.getAttribute("viewBox") === viewBox,
    ) ?? null
  );
}

afterEach(cleanup);

function card(overrides: Partial<PublicLinkCard> = {}): PublicLinkCard {
  return {
    position: 0,
    canonicalUrl: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    cardKind: "video",
    title: "Never Gonna Give You Up",
    description: "The official video",
    siteName: "YouTube",
    authorName: "Rick Astley",
    durationSeconds: 212,
    viewCount: null,
    commentCount: null,
    likeCount: null,
    faviconMediaId: null,
    thumbnailMediaId: "01ARZ3NDEKTSV4RRFFQ69G5FAV",
    thumbnailMimeType: "image/jpeg",
    thumbnailWidth: 1280,
    thumbnailHeight: 720,
    embedUrl: "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
    ...overrides,
  };
}

describe("LinkCard", () => {
  it("renders the captured snapshot without contacting the origin", () => {
    render(<LinkCard card={card()} />);
    expect(screen.getByText("Never Gonna Give You Up")).toBeInTheDocument();
    expect(screen.getByText("YouTube")).toBeInTheDocument();
    // The thumbnail is managed media served from this instance, and it carries
    // the stored geometry so the row reserves space before the image loads.
    const image = document.querySelector("img");
    expect(image?.getAttribute("src")).toContain("/api/v1/media/");
    expect(image?.getAttribute("width")).toBe("1280");
    expect(image?.getAttribute("height")).toBe("720");
    // Nothing is framed until the reader asks for it.
    expect(document.querySelector("iframe")).toBeNull();
  });

  it("loads the player only after an explicit click", () => {
    render(<LinkCard card={card()} />);
    expect(document.querySelector("iframe")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: /播放/ }));
    const frame = document.querySelector("iframe");
    expect(frame?.getAttribute("src")).toBe(
      "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ?autoplay=1",
    );
    // A route back to the source must survive; the player's own controls are
    // the platform's implementation detail, not a contract.
    expect(
      screen.getByRole("link", { name: /在 YouTube 打开/ }),
    ).toHaveAttribute("href", "https://www.youtube.com/watch?v=dQw4w9WgXcQ");
  });

  it("offers no player when the server withheld an embed URL", () => {
    // This is how a withdrawn embed origin degrades: a static card, never a
    // frame the Content Security Policy would refuse.
    render(<LinkCard card={card({ embedUrl: null, cardKind: "link" })} />);
    expect(screen.queryByRole("button", { name: /播放/ })).toBeNull();
    expect(screen.getByRole("link")).toHaveAttribute(
      "href",
      "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
    );
  });

  it("degrades to a text card when the thumbnail is missing", () => {
    render(
      <LinkCard
        card={card({
          thumbnailMediaId: null,
          thumbnailMimeType: null,
          thumbnailWidth: null,
          thumbnailHeight: null,
        })}
      />,
    );
    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText("Never Gonna Give You Up")).toBeInTheDocument();
  });

  it("falls back to the canonical host when the site name is empty", () => {
    render(<LinkCard card={card({ siteName: "", title: "" })} />);
    expect(screen.getByText("youtube.com")).toBeInTheDocument();
  });

  it("formats a duration for scanning", () => {
    render(<LinkCard card={card({ durationSeconds: 3725 })} />);
    expect(screen.getByText("1:02:05")).toBeInTheDocument();
  });

  it("keeps external links from leaking the referrer or opener", () => {
    render(<LinkCard card={card()} />);
    const link = screen.getAllByRole("link")[0];
    expect(link).toHaveAttribute("rel", "nofollow noopener noreferrer");
    expect(link).toHaveAttribute("target", "_blank");
  });
});

describe("LinkCard playback URL", () => {
  it("sets autoplay instead of appending it to a stored embed URL", () => {
    // Cards stored before playback flags moved out of the snapshot already
    // carry an autoplay value; the key must not appear twice.
    render(
      <LinkCard
        card={card({
          embedUrl:
            "https://player.bilibili.com/player.html?bvid=BV1fx3y6pEPD&autoplay=0&danmaku=0",
        })}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /播放/ }));
    const src = document.querySelector("iframe")?.getAttribute("src") ?? "";
    expect(src.match(/autoplay=/g)).toHaveLength(1);
    expect(src).toContain("autoplay=1");
    expect(src).toContain("danmaku=0");
  });
});

describe("LinkCard statistics", () => {
  it("renders captured engagement figures with glyphs and accessible names", () => {
    render(
      <LinkCard
        card={card({
          viewCount: 480312,
          commentCount: 86,
          likeCount: 10879,
          description: "",
        })}
      />,
    );
    // Platform convention: above ten thousand the exact figure stops being the
    // useful part, so it collapses to 万 the way the source site shows it.
    expect(screen.getByText("48万")).toBeInTheDocument();
    expect(screen.getByText("86")).toBeInTheDocument();
    expect(screen.getByText("1.1万")).toBeInTheDocument();
    // A four-digit figure must not be the only value in the row wearing a
    // thousands separator.
    expect(screen.queryByText("1,171")).toBeNull();
    // The exact numbers stay available to assistive technology.
    expect(screen.getByText("播放 480,312")).toBeInTheDocument();
    expect(screen.getByText("弹幕 86")).toBeInTheDocument();
    expect(screen.getByText("点赞 10,879")).toBeInTheDocument();
    expect(
      document.querySelector('svg[data-icon="play-count"]'),
    ).not.toBeNull();
    expect(
      document.querySelector('svg[data-icon="danmaku-count"]'),
    ).not.toBeNull();
  });

  it("keeps the description when a platform published no figures", () => {
    render(<LinkCard card={card({ description: "一段真正的简介" })} />);
    expect(screen.getByText("一段真正的简介")).toBeInTheDocument();
    expect(document.querySelector('svg[data-icon="play-count"]')).toBeNull();
  });

  it("shows a partial set when only some figures were published", () => {
    render(<LinkCard card={card({ viewCount: 42, description: "" })} />);
    expect(screen.getByText("42")).toBeInTheDocument();
    expect(screen.queryByText(/弹幕/)).toBeNull();
  });

  it("writes four-digit figures plainly and keeps the grouped number for readers", () => {
    render(
      <LinkCard
        card={card({ likeCount: 1171, viewCount: 370, description: "" })}
      />,
    );
    // Compact display: no separator, so it matches every other figure beside it.
    expect(screen.getByText("1171")).toBeInTheDocument();
    expect(screen.getByText("370")).toBeInTheDocument();
    // Accessible label: the precise, grouped figure.
    expect(screen.getByText("点赞 1,171")).toBeInTheDocument();
  });
});

describe("LinkCard site mark", () => {
  it("renders a brand mark from the canonical host, not from the recognizer", () => {
    // Keying on the stored URL rather than a provider label is what keeps a
    // retired recognizer from changing how an existing card looks.
    render(<LinkCard card={card()} />);
    const mark = svgWithViewBox("0 0 93 20");
    expect(mark).not.toBeNull();
    expect(mark?.querySelector("path[fill='#FF0033']")).not.toBeNull();
  });

  it("renders the Bilibili mark in its own colour", () => {
    render(
      <LinkCard
        card={card({
          canonicalUrl: "https://www.bilibili.com/video/BV1fx3y6pEPD",
          embedUrl: null,
          cardKind: "link",
        })}
      />,
    );
    const mark = svgWithViewBox("0 0 2240 1024");
    expect(mark).not.toBeNull();
    expect(mark?.querySelector("path[fill='#00AEEC']")).not.toBeNull();
  });

  it("falls back to the captured favicon for an unknown site", () => {
    render(
      <LinkCard
        card={card({
          canonicalUrl: "https://example.com/a",
          faviconMediaId: "01ARZ3NDEKTSV4RRFFQ69G5FAW",
          embedUrl: null,
          cardKind: "link",
        })}
      />,
    );
    const favicon = [...document.querySelectorAll("img")].find((image) =>
      image.getAttribute("src")?.includes("01ARZ3NDEKTSV4RRFFQ69G5FAW"),
    );
    // Served from managed media, never hotlinked from the origin.
    expect(favicon?.getAttribute("src")).toContain("/api/v1/media/");
  });

  it("shows no mark when the site published none in a supported format", () => {
    render(
      <LinkCard
        card={card({
          canonicalUrl: "https://example.com/a",
          faviconMediaId: null,
          thumbnailMediaId: null,
          embedUrl: null,
          cardKind: "link",
        })}
      />,
    );
    expect(document.querySelector("img")).toBeNull();
    expect(svgWithViewBox("0 0 93 20")).toBeNull();
  });

  it("puts the uploader first, before the figures", () => {
    render(
      <LinkCard
        card={card({
          authorName: "崽崽阿阿_",
          viewCount: 480312,
          commentCount: 86,
          likeCount: 10879,
          description: "",
        })}
      />,
    );
    const order = [...document.querySelectorAll("li")].map((node) =>
      node.querySelector("svg")?.getAttribute("data-icon"),
    );
    expect(order).toEqual([
      "uploader",
      "play-count",
      "danmaku-count",
      "like-count",
    ]);
    expect(screen.getByText("崽崽阿阿_")).toBeInTheDocument();
  });
});
