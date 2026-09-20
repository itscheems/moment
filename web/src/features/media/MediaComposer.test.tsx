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

import { mediaPosterUrl, mediaPreviewUrl } from "../../api/mediaUrls";
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
    processingPosterUrl: null,
    mediaId: `01K2ABCDEF0123456789ABCD${key}`,
    previewUrl: mediaPreviewUrl(key),
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
  it("uses consistent composer tool geometry and leading alignment", () => {
    expect(mediaCss).toContain(
      "margin-inline-start: calc(0px - var(--space-3))",
    );
    expect(mediaCss).toMatch(
      /\._compactComposer_[\w-]+ \._mediaToolbar_[\w-]+\s*\{[^}]*margin-inline-start: calc\(0px - var\(--space-2\)\);/,
    );
    expect(mediaCss).toMatch(
      /\._mediaToolbar_[\w-]+ button\s*\{[^}]*inline-size: 2\.25rem;[^}]*block-size: 2\.25rem;[^}]*min-inline-size: 2\.25rem;[^}]*min-block-size: 2\.25rem;/,
    );
    expect(mediaCss).toMatch(
      /\._mediaToolbar_[\w-]+ button svg\s*\{[^}]*inline-size: 1\.453125rem;[^}]*block-size: 1\.453125rem;/,
    );
  });

  it("keeps composer tools neutral with hover and reduced-motion states", () => {
    const buttonBlock = mediaCss.match(
      /\._mediaToolbar_[\w-]+ button\s*\{([^}]*)\}/,
    )?.[1];
    expect(buttonBlock).toContain("color: #829aab");
    expect(buttonBlock).not.toContain("var(--accent)");
    expect(buttonBlock).toContain("transform 0.15s ease");
    expect(buttonBlock).toContain("background-color 0.2s ease");
    expect(buttonBlock).toContain("filter 0.15s ease");
    expect(mediaCss).toMatch(
      /\._mediaToolbar_[\w-]+ button:hover:not\(:disabled\),[\s\S]*?button\[data-popup-open\]\s*\{[^}]*color: #829aab;[^}]*background: rgb\(15 20 25 \/ 10%\);[^}]*transform: scale\(1\.12\);/,
    );
    expect(mediaCss).toMatch(
      /\._mediaToolbar_[\w-]+ button:active:not\(:disabled\)\s*\{[^}]*background: rgb\(15 20 25 \/ 7%\);[^}]*filter: brightness\(0\.7\);[^}]*transform: scale\(1\.12\);/,
    );
    expect(mediaCss).toMatch(
      /@media \(prefers-reduced-motion: reduce\)[\s\S]*?\._mediaToolbar_[\w-]+ button:hover:not\(:disabled\),[\s\S]*?transform: none;/,
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
    expect(
      remove.querySelector('svg[data-icon="media-remove"]'),
    ).not.toBeNull();
    expect(
      remove.querySelector('svg[data-icon="media-remove"]'),
    ).toHaveAttribute("fill", "currentColor");
    expect(
      remove.querySelector('svg[data-icon="media-remove"] use'),
    ).toHaveAttribute("href", "/icons/ui-symbols-v3.svg#media-remove");
    expect(remove).not.toHaveTextContent("×");
    expect(mediaCss).toMatch(
      /\._mediaRemoveControl_[\w-]+\s*\{[^}]*background:\s*rgb\(15 20 25 \/ 75%\)[^}]*backdrop-filter:\s*blur\(4px\)/,
    );
    expect(mediaCss).toMatch(
      /\._mediaRemoveControl_[\w-]+:hover:not\(:disabled\)\s*\{[^}]*background:\s*rgb\(15 20 25 \/ 55%\)/,
    );
    expect(mediaCss).toMatch(
      /\._mediaRemoveControl_[\w-]+\s*\{[^}]*transition:\s*background-color 140ms ease/,
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

  // One quiet "Add description" action sits under the pictures. This used to be a
  // <details> whose `border-block` drew a hairline above *and* below itself,
  // with the toolbar's own rule right under that -- three lines stacked in
  // the gap between the picture and the toolbar.
  it("offers one borderless description action under the pictures", () => {
    render(
      <MediaComposer
        disabled={false}
        items={[item("00"), item("01")]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    const describe = screen.getByRole("button", { name: /添加描述/ });
    expect(describe).toHaveTextContent("2 项未描述");
    expect(document.querySelector("details")).toBeNull();
    expect(mediaCss).not.toMatch(
      /\._mediaDescribe_[\w-]+\s*\{[^}]*border(-block)?:[^};]*solid/s,
    );

    fireEvent.click(describe);
    expect(
      screen.getByRole("heading", { name: "编辑图片描述" }),
    ).toBeInTheDocument();
    // One picture per page, not a stack of every item's controls at once.
    expect(screen.getAllByLabelText("填写描述")).toHaveLength(1);
  });

  // The media editor describes one picture at a time and pages between
  // them. The sheet used to list all nine items, which asked the writer to
  // hold the running order in their head and buried the picture each
  // description was about.
  it("pages the description sheet one picture at a time", () => {
    render(<FocusHarness initial={[item("00"), item("01"), item("02")]} />);

    fireEvent.click(screen.getByRole("button", { name: /添加描述/ }));
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    const railPage = (label: string) =>
      screen.getByRole("button", { name: label }).getAttribute("aria-current");
    expect(railPage("第 1 张图片")).toBe("true");
    const previous = screen.getByRole("button", { name: "上一张图片" });
    const next = screen.getByRole("button", { name: "下一张图片" });
    expect(previous).toBeDisabled();

    fireEvent.click(next);
    expect(screen.getByText("2 / 3")).toBeInTheDocument();
    expect(railPage("第 2 张图片")).toBe("true");
    expect(railPage("第 1 张图片")).toBeNull();
    expect(previous).toBeEnabled();

    // The rail reaches any picture without paging past the ones between.
    fireEvent.click(screen.getByRole("button", { name: "第 1 张图片" }));
    expect(screen.getByText("1 / 3")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "第 3 张图片" }));
    expect(screen.getByText("3 / 3")).toBeInTheDocument();
    expect(next).toBeDisabled();
  });

  // The action counts what is missing, so pressing it should land on
  // something missing rather than on page one every time.
  it("opens the sheet on the first picture still without a description", () => {
    render(
      <FocusHarness
        initial={[
          { ...item("00"), altMode: "decorative" },
          { ...item("01"), altMode: "provided", altText: "写过了" },
          item("02"),
        ]}
      />,
    );

    const describe = screen.getByRole("button", { name: /添加描述/ });
    expect(describe).toHaveTextContent("1 项未描述");
    fireEvent.click(describe);
    expect(screen.getByText("3 / 3")).toBeInTheDocument();
    expect(screen.getByLabelText("暂未描述")).toBeChecked();
  });

  // Measured at 390px: the default header paddings wrapped 完成 onto two
  // lines and pushed 填写描述 onto a row of its own.
  it("keeps the sheet header and alt modes on one line on a phone", () => {
    expect(mediaCss).toMatch(
      /\._describeDone_[\w-]+\s*\{[^}]*white-space: nowrap;/s,
    );
    const narrow = mediaCss.slice(
      mediaCss.indexOf("@media (max-width: 519px)"),
    );
    expect(narrow).toMatch(/\._describeHeader_[\w-]+\s*\{[^}]*padding-inline/s);
    expect(narrow).toMatch(/\._altModes_[\w-]+ label\s*\{[^}]*padding-inline/s);
  });

  // A page pinned to an index would swap pictures underneath the writer the
  // moment the order changed; it follows the item it was opened on.
  it("keeps the open page on its own picture across a reorder", () => {
    render(<FocusHarness initial={[item("00"), item("01")]} />);

    fireEvent.click(screen.getByRole("button", { name: /添加描述/ }));
    fireEvent.click(screen.getByLabelText("填写描述"));
    fireEvent.change(screen.getByPlaceholderText("简要描述图片内容"), {
      target: { value: "第一张" },
    });
    expect(screen.getByText("1 / 2")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /下移/ }));
    expect(screen.getByText("2 / 2")).toBeInTheDocument();
    expect(screen.getByPlaceholderText("简要描述图片内容")).toHaveValue(
      "第一张",
    );
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

    fireEvent.click(screen.getByRole("button", { name: /添加描述/ }));
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
    expect(screen.getByRole("status")).toHaveTextContent("上传 47%");
    expect(mediaCss).toContain("inset-block-start: 50%");
    expect(mediaCss).toContain("backdrop-filter: blur(8px)");
  });

  it("shows a local video cover with processing feedback instead of a blank tile", () => {
    const video = {
      ...item("00"),
      file: new File(["video"], "climb.mov", { type: "video/quicktime" }),
      localPreviewUrl: "blob:http://localhost/local-video",
      processingPosterUrl: "blob:http://localhost/server-video-poster",
      mediaId: null,
      previewUrl: "",
      originalUrl: "",
      mimeType: "video/quicktime",
      previewMimeType: "",
      state: "uploading" as const,
      progress: 99,
    };
    const { container } = render(
      <MediaComposer
        disabled={false}
        items={[video]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.getByText("正在生成视频封面")).toBeInTheDocument();
    expect(screen.getByText("climb.mov")).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent(
      "封面已就绪 · 正在处理视频",
    );
    const preview = container.querySelector("img");
    expect(preview).not.toBeNull();
    expect(preview).toHaveAttribute(
      "src",
      "blob:http://localhost/server-video-poster",
    );
    expect(container.querySelector("video")).toBeNull();
    expect(container.querySelector("[data-video-frame]")).toHaveAttribute(
      "data-video-frame",
      "ready",
    );
    expect(
      container.querySelector("[class*='processingSpinner']"),
    ).not.toBeNull();
    expect(mediaCss).toContain("transform: translateZ(0) rotate(360deg)");
    expect(mediaCss).not.toContain(
      '.mediaUploadStatus[data-processing="true"] .uploadProgressRing svg',
    );
  });

  it("keeps the server poster as the editor cover after video processing", () => {
    const readyVideo = {
      ...item("00"),
      mimeType: "video/quicktime",
      previewMimeType: "video/mp4",
    };
    const { container } = render(
      <MediaComposer
        disabled={false}
        items={[readyVideo]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    expect(container.querySelector("video")).toBeNull();
    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      mediaPosterUrl("01K2ABCDEF0123456789ABCD00"),
    );
    expect(container.querySelector("[data-video-frame]")).toHaveAttribute(
      "data-video-frame",
      "ready",
    );
  });

  it("reports poster and full video processing independently", () => {
    const video = {
      ...item("00"),
      file: new File(["video"], "climb.mov", { type: "video/quicktime" }),
      localPreviewUrl: "blob:http://localhost/local-video",
      mediaId: null,
      previewUrl: "",
      originalUrl: "",
      mimeType: "video/quicktime",
      previewMimeType: "",
      state: "uploading" as const,
      progress: 99,
    };
    const { container } = render(
      <MediaComposer
        disabled={false}
        items={[video]}
        onAdd={vi.fn()}
        onChange={vi.fn()}
        onRemove={vi.fn()}
        onRetry={vi.fn()}
      />,
    );

    expect(screen.getByRole("status")).toHaveTextContent(
      "正在生成封面 · 视频处理中",
    );
    expect(container.querySelector("video")).toBeNull();
    expect(
      screen.getByRole("button", { name: /预览第 1 项视频/ }),
    ).toBeDisabled();
  });

  it("briefly confirms a completed video upload", async () => {
    const uploading = {
      ...item("00"),
      file: new File(["video"], "climb.mp4", { type: "video/mp4" }),
      localPreviewUrl: "blob:http://localhost/local-video",
      mediaId: null,
      previewUrl: "",
      originalUrl: "",
      mimeType: "video/mp4",
      previewMimeType: "",
      state: "uploading" as const,
      progress: 80,
    };
    const ready = {
      ...uploading,
      file: null,
      localPreviewUrl: null,
      mediaId: "01K2ABCDEF0123456789ABCD00",
      previewUrl: "/api/v1/media/00/preview",
      originalUrl: "/api/v1/media/00",
      previewMimeType: "video/mp4",
      state: "ready" as const,
      progress: 100,
    };
    const props = {
      disabled: false,
      onAdd: vi.fn(),
      onChange: vi.fn(),
      onRemove: vi.fn(),
      onRetry: vi.fn(),
    };
    const { rerender } = render(
      <MediaComposer {...props} items={[uploading]} />,
    );

    rerender(<MediaComposer {...props} items={[ready]} />);

    expect(await screen.findByRole("status")).toHaveTextContent("视频已上传");
    expect(
      screen.getByRole("status").querySelector('svg[data-icon="check"]'),
    ).not.toBeNull();
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
    const remove = screen.getByRole("button", { name: "移除第 1 张图片" });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog");
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    expect(dialog.closest("[data-base-ui-portal]")?.parentElement).toBe(
      document.body,
    );
    expect(dialog.querySelector("img")).toHaveAttribute(
      "src",
      mediaPreviewUrl("00"),
    );
    expect(screen.getByRole("link", { name: "打开原图" })).toHaveAttribute(
      "href",
      "/api/v1/media/00",
    );
    const close = screen.getByRole("button", { name: "关闭图片预览" });
    expect(
      close.querySelector('svg[data-icon="media-remove"]'),
    ).toHaveAttribute("fill", "currentColor");
    expect(
      close.querySelector('svg[data-icon="media-remove"] use'),
    ).toHaveAttribute("href", "/icons/ui-symbols-v3.svg#media-remove");
    expect(close).not.toHaveTextContent("×");
    const sharedRemovalClass = Array.from(remove.classList).find((className) =>
      className.includes("mediaRemoveControl"),
    );
    expect(sharedRemovalClass).toBeTruthy();
    expect(close).toHaveClass(sharedRemovalClass!);
    fireEvent.keyDown(dialog, { key: "ArrowRight" });
    await waitFor(() =>
      expect(dialog.querySelector("img")).toHaveAttribute(
        "src",
        mediaPreviewUrl("01"),
      ),
    );
    fireEvent.click(close);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
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

  // Reordering lives in the description sheet, and the sheet traps focus:
  // handing focus to the tile behind it would have it pulled straight back.
  // The press keeps its own control -- here the moved item reaches the end,
  // so its 下移 is disabled and focus lands on its 上移, still the moved item.
  it("keeps focus with the moved usage after keyboard reordering", async () => {
    render(<FocusHarness initial={[item("00"), item("01")]} />);
    fireEvent.click(screen.getByRole("button", { name: /添加描述/ }));
    fireEvent.click(screen.getAllByRole("button", { name: /下移/ })[0]);
    await waitFor(() =>
      expect(document.activeElement).toHaveAttribute("data-media-key", "00"),
    );
    expect(document.activeElement).toHaveAttribute("data-media-action", "up");
  });
});
