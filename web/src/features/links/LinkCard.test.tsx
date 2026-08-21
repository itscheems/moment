import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { PublicLinkCard } from "../../api/public";
import { LinkCard } from "./LinkCard";

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
