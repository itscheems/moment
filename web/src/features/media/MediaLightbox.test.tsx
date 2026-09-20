import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import mediaCss from "./Media.module.css?inline";

const items: MediaLightboxItem[] = [
  {
    key: "a",
    previewSrc: "/preview/a.png",
    originalSrc: "/media/a",
    alt: "第一张",
    description: "第一张的描述",
  },
  {
    key: "b",
    previewSrc: "/preview/b.png",
    alt: "第二张",
    description: "第二张的描述",
  },
];

function Harness() {
  const [selected, setSelected] = useState<number | null>(0);
  return (
    <MediaLightbox
      items={items}
      onSelectedChange={setSelected}
      selected={selected}
    />
  );
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("media lightbox", () => {
  // The popup fills the screen, so the backdrop under it can never be
  // pressed: measured on the running page, a point far from the picture hit
  // `.lightboxStage`, inside the dialog. There was no "outside" to click and
  // the only way out was the ✕ or Escape.
  it("closes on a press in the dark surround and stays open on the picture", () => {
    render(<Harness />);

    fireEvent.click(screen.getByAltText("第一张"));
    expect(screen.getByAltText("第一张")).toBeInTheDocument();

    const surround = document.querySelector('[class*="lightboxStage"]');
    expect(surround).not.toBeNull();
    fireEvent.click(surround!);
    expect(screen.queryByAltText("第一张")).toBeNull();

    // The cursor is the only thing that can say the surround dismisses
    // before the press lands; the picture keeps the ordinary arrow because
    // pressing it does nothing.
    expect(mediaCss).toMatch(
      /\._lightboxPopup_[\w-]+\s*\{[^}]*cursor: zoom-out;/s,
    );
    expect(mediaCss).toMatch(
      /\._lightboxStage_[\w-]+ img,[\s\S]*?video\s*\{[^}]*cursor: default;/s,
    );
  });

  it("keeps the controls working rather than treating them as the surround", () => {
    render(<Harness />);

    fireEvent.click(screen.getByRole("button", { name: "下一张图片" }));
    expect(screen.getByAltText("第二张")).toBeInTheDocument();
  });

  // The failure panel used to double as the "nothing is selected" branch, so
  // it played over the whole 180ms close: the picture vanished and a light
  // grey "图片暂时无法显示。" flashed on the black backdrop every time.
  it("shows the failure panel only for media that actually failed", () => {
    const { rerender } = render(
      <MediaLightbox
        items={items}
        onSelectedChange={vi.fn()}
        selected={null}
      />,
    );
    expect(screen.queryByText("图片暂时无法显示。")).toBeNull();

    rerender(
      <MediaLightbox items={items} onSelectedChange={vi.fn()} selected={0} />,
    );
    expect(screen.queryByText("图片暂时无法显示。")).toBeNull();

    fireEvent.error(screen.getByAltText("第一张"));
    expect(screen.getByText("图片暂时无法显示。")).toBeInTheDocument();
  });

  // Two layers each at 94% composite to 99.6% -- a blackout that reads as the
  // page being replaced rather than the picture being lifted off it.
  it("lays one scrim over the page instead of two stacked blacks", () => {
    expect(mediaCss).not.toMatch(
      /\._lightboxViewport_[\w-]+\s*\{[^}]*background:/s,
    );
    const backdrop = mediaCss.match(
      /\._lightboxBackdrop_[\w-]+\s*\{([^}]*)\}/s,
    )?.[1];
    expect(backdrop).toBeDefined();
    const alpha = Number(
      backdrop!.match(/background: rgb\(0 0 0 \/ (\d+)%\)/)?.[1],
    );
    expect(alpha).toBeGreaterThan(0);
    expect(alpha).toBeLessThan(90);
    expect(backdrop).toMatch(/backdrop-filter: blur\(/);
  });
});
