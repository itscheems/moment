import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { MediaComposer } from "./MediaComposer";
import mediaCss from "./Media.module.css?inline";
import type { ComposerMediaItem } from "./mediaState";

beforeEach(() => {
  vi.stubGlobal("scrollTo", vi.fn());
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function item(key: string): ComposerMediaItem {
  return {
    key,
    file: null,
    localPreviewUrl: null,
    mediaId: `01K2ABCDEF0123456789ABCD${key}`,
    previewUrl: `/api/v1/media/${key}/preview`,
    originalUrl: `/api/v1/media/${key}`,
    mimeType: "image/jpeg",
    previewMimeType: "image/jpeg",
    width: null,
    height: null,
    byteSize: null,
    animatedOriginal: false,
    altMode: "missing",
    altText: "",
    state: "ready",
    progress: 100,
    error: null,
  };
}

function FocusHarness({ initial }: { initial: ComposerMediaItem[] }) {
  const [items, setItems] = useState(initial);
  return (
    <MediaComposer
      disabled={false}
      items={items}
      onAdd={vi.fn()}
      onChange={(next) => setItems(next)}
      onRemove={(key) =>
        setItems((current) => current.filter((value) => value.key !== key))
      }
      onRetry={vi.fn()}
    />
  );
}

describe("media editor", () => {
  it("optically aligns the leading media action without shrinking its target", () => {
    expect(mediaCss).toContain(
      "margin-inline-start: calc(0px - var(--space-3))",
    );
    expect(mediaCss).toMatch(
      /\._mediaToolbar_[\w-]+ button\s*\{[^}]*inline-size: var\(--target-size\)[^}]*block-size: var\(--target-size\)/,
    );
  });

  it("uses the solid media artwork in composer toolbars", () => {
    render(
      <MediaComposer
        disabled={false}
        items={[]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const icon = screen
      .getByRole("button", { name: "选择媒体" })
      .querySelector('svg[data-icon="media-picker"]');
    expect(icon).toHaveAttribute("fill", "currentColor");
    expect(icon?.querySelectorAll("path")).toHaveLength(2);
  });

  it("uses the shared compact removal artwork on media previews", () => {
    render(
      <MediaComposer
        disabled={false}
        items={[item("00")]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const remove = screen.getByRole("button", { name: "移除第 1 张图片" });
    expect(remove.querySelector('svg[data-icon="media-remove"]')).not.toBeNull();
    expect(remove.querySelector('svg[data-icon="media-remove"]')).toHaveAttribute(
      "fill",
      "currentColor",
    );
    expect(
      remove.querySelector('svg[data-icon="media-remove"] use'),
    ).toHaveAttribute("href", "/icons/ui-symbols-v3.svg#media-remove");
    expect(remove).not.toHaveTextContent("×");
    expect(mediaCss).toMatch(
      /\._editorRemove_[\w-]+\s*\{[^}]*background:\s*rgb\(15 20 25 \/ 75%\)[^}]*backdrop-filter:\s*blur\(4px\)/,
    );
    expect(mediaCss).toMatch(
      /\._editorRemove_[\w-]+:hover:not\(:disabled\)\s*\{[^}]*background:\s*rgb\(15 20 25 \/ 55%\)/,
    );
    expect(mediaCss).toMatch(
      /\._editorRemove_[\w-]+\s*\{[^}]*transition:\s*background-color 140ms ease/,
    );
  });

  it("preserves duplicate selections as two ordered usages", () => {
    const onAdd = vi.fn();
    render(
      <MediaComposer
        disabled={false}
        items={[]}
        onAdd={onAdd}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    const duplicate = new File(["same"], "same.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: { files: [duplicate, duplicate] },
    });
    expect(onAdd).toHaveBeenCalledWith([duplicate, duplicate]);
  });

  it("supports keyboard-usable ordering and explicit alt modes", () => {
    const onChange = vi.fn();
    const items = [item("00"), item("01")];
    render(
      <MediaComposer
        disabled={false}
        items={items}
        onAdd={vi.fn()}
        onChange={onChange}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    fireEvent.click(screen.getAllByRole("button", { name: /下移/ })[0]);
    expect(
      onChange.mock.calls[0][0].map((value: ComposerMediaItem) => value.key),
    ).toEqual(["01", "00"]);
    fireEvent.click(screen.getAllByLabelText("纯装饰")[0]);
    expect(onChange.mock.calls[1][0][0].altMode).toBe("decorative");
  });

  it("shows determinate upload progress and retry without blocking remove", () => {
    render(
      <MediaComposer
        disabled={false}
        items={[
          item("00"),
          { ...item("01"), state: "uploading", progress: 47 },
          { ...item("02"), state: "failed", error: "网络中断" },
        ]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("progressbar", { name: "第 2 张上传进度" }),
    ).toHaveValue(47);
    expect(screen.getByRole("button", { name: /重试上传/ })).toBeEnabled();
    expect(screen.getAllByRole("button", { name: /移除/ })[1]).toBeEnabled();
  });

  it("reserves a stable processing tile for browser-undecodable media", () => {
    render(
      <MediaComposer
        disabled={false}
        items={[
          {
            ...item("00"),
            file: new File(["heic"], "camera.heic", { type: "image/heic" }),
            mediaId: null,
            previewUrl: "",
            originalUrl: "",
            mimeType: "image/heic",
            state: "uploading",
            progress: 38,
          },
        ]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.getByText("正在处理 38%")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "预览第 1 张图片，共 1 张" }),
    ).toHaveAttribute("data-preview-pending");
    expect(screen.getByRole("button", { name: /移除/ })).toBeEnabled();
    expect(mediaCss).toMatch(
      /\[data-preview-pending\]\s*\{[^}]*aspect-ratio:\s*4\s*\/\s*3[^}]*min-block-size:\s*12rem/,
    );
  });

  it("sizes a single preview from its intrinsic ratio without exposed gutters", () => {
    const { container } = render(
      <MediaComposer
        disabled={false}
        items={[{ ...item("00"), width: 1200, height: 1600 }]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const slot = container.querySelector<HTMLElement>("[data-media-slot]");
    const image = container.querySelector("[data-media-slot] img");
    expect(slot?.style.getPropertyValue("--editor-media-ratio")).toBe(
      "1200 / 1600",
    );
    expect(slot?.style.getPropertyValue("--editor-media-max-width")).toBe(
      "24rem",
    );
    expect(image).toHaveAttribute("width", "1200");
    expect(image).toHaveAttribute("height", "1600");
    expect(mediaCss).toMatch(
      /\[data-media-count="1"\]\s*\{[^}]*display:\s*flex[^}]*justify-content:\s*center/,
    );
    expect(mediaCss).toMatch(
      /\[data-media-count="1"\]\s+\._mediaEditorItem_[\w-]+\s*\{[^}]*aspect-ratio:\s*var\(--editor-media-ratio,\s*4\s*\/\s*3\)[^}]*max-block-size:\s*32rem/,
    );
  });

  it("opens attached images in a keyboard-navigable preview and restores focus", async () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(360);
    render(<FocusHarness initial={[item("00"), item("01")]} />);
    const trigger = screen.getByRole("button", {
      name: "预览第 1 张图片，共 2 张",
    });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog");
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-360px");
    expect(dialog.closest("[data-base-ui-portal]")?.parentElement).toBe(
      document.body,
    );
    expect(dialog.querySelector("img")).toHaveAttribute(
      "src",
      "/api/v1/media/00/preview",
    );
    expect(screen.getByRole("link", { name: "打开原图" })).toHaveAttribute(
      "href",
      "/api/v1/media/00",
    );
    fireEvent.keyDown(dialog, { key: "ArrowRight" });
    await waitFor(() =>
      expect(dialog.querySelector("img")).toHaveAttribute(
        "src",
        "/api/v1/media/01/preview",
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "关闭图片预览" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.documentElement.style.overflowY).toBe("");
    expect(document.body.style.position).toBe("");
    expect(window.scrollTo).toHaveBeenCalledWith(0, 360);
  });

  it("opens a selected local file through its browser object URL", async () => {
    render(
      <FocusHarness
        initial={[
          {
            ...item("00"),
            file: new File(["image"], "local.png", { type: "image/png" }),
            localPreviewUrl: "blob:http://localhost/local-original",
            mediaId: null,
            previewUrl: "",
            originalUrl: "",
            state: "queued",
          },
        ]}
      />,
    );

    fireEvent.click(
      screen.getByRole("button", {
        name: "预览第 1 张图片，共 1 张",
      }),
    );

    expect(
      await screen.findByRole("link", { name: "打开原图" }),
    ).toHaveAttribute("href", "blob:http://localhost/local-original");
  });

  it.each([
    { name: "first", remove: 0, expected: "01" },
    { name: "middle", remove: 1, expected: "02" },
    { name: "last", remove: 2, expected: "01" },
  ])(
    "restores focus after removing the $name item",
    async ({ remove, expected }) => {
      render(<FocusHarness initial={[item("00"), item("01"), item("02")]} />);
      fireEvent.click(screen.getAllByRole("button", { name: /移除/ })[remove]);
      await waitFor(() =>
        expect(document.activeElement).toHaveAttribute(
          "data-media-key",
          expected,
        ),
      );
    },
  );

  it("returns focus to the picker after removing the final item", async () => {
    render(<FocusHarness initial={[item("00")]} />);
    fireEvent.click(screen.getByRole("button", { name: /移除/ }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "选择媒体" })).toHaveFocus(),
    );
  });

  it("keeps the departing item mounted while the remaining items reflow", () => {
    const { container } = render(
      <FocusHarness initial={[item("00"), item("01"), item("02")]} />,
    );

    fireEvent.click(screen.getAllByRole("button", { name: /移除/ })[1]);
    expect(
      Array.from(
        container.querySelectorAll(
          '[data-media-slot]:not([data-media-state="departing"])',
        ),
      ).map((node) => node.getAttribute("data-media-slot")),
    ).toEqual(["00", "02"]);
    const departing = container.querySelector('[data-media-state="departing"]');
    expect(departing).toHaveAttribute("data-media-slot", "01");
    expect(departing).toHaveAttribute("aria-hidden", "true");
    expect(
      container.querySelector('[aria-label="待发布图片，按发布顺序"]'),
    ).toHaveAttribute("data-media-count", "2");
  });

  it("keeps focus with the moved usage after keyboard reordering", async () => {
    render(<FocusHarness initial={[item("00"), item("01")]} />);
    fireEvent.click(screen.getAllByRole("button", { name: /下移/ })[0]);
    await waitFor(() =>
      expect(document.activeElement).toHaveAttribute("data-media-key", "00"),
    );
  });
});
