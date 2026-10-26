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
      /\._lightboxStage_[\w-]+\s*\{[^}]*cursor: zoom-out;/s,
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
  // "尚未填写描述" and "纯装饰" are facts about the metadata, not about the
  // picture; printing them under it told the reader nothing they wanted.
  it("captions the picture only when the author wrote one", () => {
    function Bare() {
      return (
        <MediaLightbox
          items={[
            {
              key: "a",
              previewSrc: "/preview/a.png",
              alt: "无描述",
              description: "",
            },
          ]}
          onSelectedChange={vi.fn()}
          selected={0}
        />
      );
    }
    render(<Bare />);

    expect(screen.getByAltText("无描述")).toBeInTheDocument();
    expect(document.querySelector('[class*="lightboxDescription"]')).toBeNull();
    cleanup();

    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "查看图片说明" }));
    expect(
      document.querySelector('[class*="lightboxDescription"]'),
    ).toHaveTextContent("第一张的描述");
  });

  it("uses the measured surround without blurring the underlying page", () => {
    const backdrop = mediaCss.match(
      /\._lightboxBackdrop_[\w-]+\s*\{([^}]*)\}/s,
    )?.[1];
    expect(backdrop).toMatch(/background: rgb\(0 0 0 \/ 40%\)/);
    expect(backdrop).not.toMatch(/backdrop-filter/);
    expect(mediaCss).toMatch(
      /\._lightboxMedia_[\w-]+\s*\{[^}]*background: rgb\(0 0 0 \/ 90%\)/s,
    );
  });

  it("stops at both boundaries and hides unavailable directions", () => {
    render(<Harness />);
    const dialog = screen.getByRole("dialog");
    expect(screen.queryByRole("button", { name: "上一张图片" })).toBeNull();
    fireEvent.keyDown(dialog, { key: "ArrowLeft" });
    expect(screen.getByAltText("第一张")).toBeInTheDocument();
    fireEvent.keyDown(dialog, { key: "ArrowRight" });
    expect(screen.getByAltText("第二张")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "下一张图片" })).toBeNull();
    fireEvent.keyDown(dialog, { key: "ArrowRight" });
    expect(screen.getByAltText("第二张")).toBeInTheDocument();
  });

  it("protects discussion clicks, text selection keys and nested dialog keys", () => {
    render(
      <MediaLightbox
        items={items}
        selected={0}
        onSelectedChange={vi.fn()}
        panelShown
        discussion={
          <div>
            <p>正文可选中</p>
            <textarea aria-label="回复内容" />
            <div role="dialog" aria-label="子窗口">
              <button>子操作</button>
            </div>
          </div>
        }
      />,
    );
    fireEvent.click(screen.getByText("正文可选中"));
    fireEvent.click(screen.getByRole("textbox", { name: "回复内容" }));
    fireEvent.keyDown(screen.getByRole("textbox", { name: "回复内容" }), {
      key: "ArrowRight",
    });
    fireEvent.keyDown(screen.getByRole("button", { name: "子操作" }), {
      key: "ArrowRight",
    });
    expect(screen.getByAltText("第一张")).toBeInTheDocument();
    const change = vi.fn();
    cleanup();
    render(
      <MediaLightbox
        items={items}
        selected={0}
        onSelectedChange={change}
        panelShown
        discussion={<textarea aria-label="回复内容" />}
      />,
    );
    fireEvent.keyDown(screen.getByRole("textbox", { name: "回复内容" }), {
      key: "ArrowLeft",
    });
    fireEvent.click(screen.getByRole("textbox", { name: "回复内容" }));
    expect(change).not.toHaveBeenCalled();
  });
});
