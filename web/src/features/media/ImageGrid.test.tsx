import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { PublicMediaUsage } from "../../api/public";
import { ImageGrid } from "./ImageGrid";

class TestIntersectionObserver {
  static instances: TestIntersectionObserver[] = [];

  readonly root = null;
  readonly rootMargin: string;
  readonly thresholds: readonly number[];
  private readonly callback: IntersectionObserverCallback;
  private target: Element | null = null;

  constructor(
    callback: IntersectionObserverCallback,
    options: IntersectionObserverInit = {},
  ) {
    this.callback = callback;
    this.rootMargin = options.rootMargin ?? "0px";
    this.thresholds = Array.isArray(options.threshold)
      ? options.threshold
      : [options.threshold ?? 0];
    TestIntersectionObserver.instances.push(this);
  }

  observe = (target: Element) => {
    this.target = target;
  };

  unobserve = () => undefined;
  disconnect = () => undefined;
  takeRecords = () => [];

  setIntersecting(isIntersecting: boolean) {
    if (!this.target) throw new Error("observer has no target");
    this.callback(
      [
        {
          isIntersecting,
          target: this.target,
          intersectionRatio: isIntersecting ? 1 : 0,
        } as IntersectionObserverEntry,
      ],
      this as unknown as IntersectionObserver,
    );
  }
}

afterEach(() => {
  cleanup();
  TestIntersectionObserver.instances = [];
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function media(count: number): PublicMediaUsage[] {
  return Array.from({ length: count }, (_, position) => ({
    mediaId: `01K2ABCDEF0123456789ABCD${String(position).padStart(2, "0")}`,
    position,
    mimeType: "image/jpeg",
    altText:
      position === 0 ? null : position === 1 ? "" : `第 ${position + 1} 张描述`,
    width: 1_200 + position,
    height: 800 + position,
  }));
}

function video(): PublicMediaUsage {
  return {
    mediaId: "01K2ABCDEF0123456789ABCDV0",
    position: 0,
    mimeType: "video/mp4",
    altText: "一段测试视频",
    width: 1_920,
    height: 1_080,
  };
}

describe("public image grid", () => {
  it.each([0, 1, 2, 3, 4, 9])(
    "renders the deliberate %i-image layout",
    (count) => {
      const { container } = render(<ImageGrid media={media(count)} />);
      if (count === 0) {
        expect(container).toBeEmptyDOMElement();
        return;
      }
      const group = screen.getByRole("group", { name: `${count} 张图片` });
      expect(group).toHaveAttribute("data-media-count", String(count));
      expect(group.querySelectorAll("img")).toHaveLength(count);
      for (const image of group.querySelectorAll("img")) {
        expect(image).toHaveAttribute("loading", "lazy");
        expect(image.getAttribute("src")).toMatch(/\/preview$/);
      }
    },
  );

  it("renders all nullable alt states without disguising missing as decorative", () => {
    render(<ImageGrid media={media(3)} />);
    const images = screen
      .getByRole("group", { name: "3 张图片" })
      .querySelectorAll("img");
    expect(images[0]).toHaveAttribute(
      "alt",
      "未提供图片描述，第 1 张，共 3 张",
    );
    expect(images[1]).toHaveAttribute("alt", "");
    expect(images[2]).toHaveAttribute("alt", "第 3 张描述");
    expect(
      screen.getByRole("button", {
        name: "打开第 1 张图片，共 3 张：尚未提供图片描述",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "打开第 2 张图片，共 3 张：明确标记为纯装饰",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", {
        name: "打开第 3 张图片，共 3 张：第 3 张描述",
      }),
    ).toBeInTheDocument();
  });

  it("reserves the stored single-image geometry before load", () => {
    const { container } = render(<ImageGrid media={media(1)} />);
    const tile = screen.getByRole("button", {
      name: "打开第 1 张图片，共 1 张：尚未提供图片描述",
    });
    const image = screen.getByRole("img", { name: /未提供图片描述/ });

    expect(image).toHaveAttribute("width", "1200");
    expect(image).toHaveAttribute("height", "800");
    expect(tile).toHaveStyle({
      "--single-media-ratio": "1200 / 800",
      "--single-media-max-width": "51rem",
    });

    fireEvent.load(image);
    expect(tile).toHaveStyle({
      "--single-media-ratio": "1200 / 800",
      "--single-media-max-width": "51rem",
    });
    expect(container.querySelector('[data-media-order="1"]')).toBeNull();
  });

  it.each([1, 2, 3, 4])("omits order badges for %i images", (count) => {
    const { container } = render(<ImageGrid media={media(count)} />);
    expect(container.querySelectorAll("[data-media-order]")).toHaveLength(0);
  });

  it.each([5, 6, 7, 8, 9])("keeps order badges for %i images", (count) => {
    const { container } = render(<ImageGrid media={media(count)} />);
    const badges = container.querySelectorAll("[data-media-order]");
    expect(badges).toHaveLength(count);
    expect(Array.from(badges, (badge) => badge.textContent)).toEqual(
      Array.from({ length: count }, (_, index) =>
        String(index + 1).padStart(2, "0"),
      ),
    );
  });

  it("keeps a useful fallback when a preview is missing", () => {
    render(<ImageGrid media={media(1)} />);
    fireEvent.error(screen.getByRole("img", { name: /未提供图片描述/ }));
    expect(screen.getByText("图片暂不可用")).toBeInTheDocument();
  });

  it("attaches video resources near the viewport and releases them after leaving", () => {
    vi.useFakeTimers();
    vi.stubGlobal("IntersectionObserver", TestIntersectionObserver);
    const pause = vi
      .spyOn(HTMLMediaElement.prototype, "pause")
      .mockImplementation(() => undefined);
    render(<ImageGrid media={[video()]} />);

    const element = screen.getByLabelText("一段测试视频");
    expect(screen.getByRole("status", { name: "视频正在加载" })).toBeVisible();
    expect(element).not.toHaveAttribute("src");
    expect(element).not.toHaveAttribute("poster");
    expect(element).toHaveAttribute("preload", "none");
    expect(TestIntersectionObserver.instances[0].rootMargin).toBe("600px 0px");

    act(() => TestIntersectionObserver.instances[0].setIntersecting(true));
    expect(element).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCDV0/preview",
    );
    expect(element).toHaveAttribute(
      "poster",
      "/api/v1/media/01K2ABCDEF0123456789ABCDV0/poster",
    );
    expect(element).toHaveAttribute("preload", "metadata");

    Object.defineProperty(element, "duration", {
      configurable: true,
      value: 120,
    });
    fireEvent.loadedMetadata(element);
    const playButton = screen.getByRole("button", { name: "播放视频" });
    const playIcon = playButton.querySelector('[data-icon="video-play"]');
    expect(playIcon?.querySelectorAll("path")).toHaveLength(2);
    expect(playIcon?.querySelector("use")).toBeNull();
    expect(screen.getByText("2:00")).toBeVisible();

    act(() => TestIntersectionObserver.instances[0].setIntersecting(false));
    expect(pause).toHaveBeenCalled();
    expect(element).toHaveAttribute("src");
    act(() => vi.advanceTimersByTime(3_000));
    expect(element).not.toHaveAttribute("src");
    expect(element).not.toHaveAttribute("poster");
  });

  it("attaches detail video resources immediately", () => {
    vi.stubGlobal("IntersectionObserver", TestIntersectionObserver);
    render(<ImageGrid eagerVideo media={[video()]} />);

    expect(screen.getByLabelText("一段测试视频")).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCDV0/preview",
    );
    expect(TestIntersectionObserver.instances).toHaveLength(0);
  });

  it("starts inline video playback in one action", async () => {
    const play = vi
      .spyOn(HTMLMediaElement.prototype, "play")
      .mockResolvedValue(undefined);
    render(<ImageGrid eagerVideo media={[video()]} />);

    const element = screen.getByLabelText("一段测试视频");
    fireEvent.loadedMetadata(element);
    fireEvent.click(screen.getByRole("button", { name: "播放视频" }));
    expect(play).toHaveBeenCalledOnce();
    fireEvent.playing(element);

    await waitFor(() => expect(element).toHaveAttribute("controls"));
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("does not carry a failed preview across reordered or replaced usages", () => {
    const initial = media(2);
    const { rerender } = render(<ImageGrid media={initial} />);
    fireEvent.error(screen.getByRole("img", { name: /未提供图片描述/ }));
    expect(screen.getByText("图片暂不可用")).toBeInTheDocument();

    rerender(
      <ImageGrid
        media={[
          { ...initial[1], position: 0 },
          {
            mediaId: "01K2ABCDEF0123456789ABCD09",
            position: 1,
            altText: "替换后的图片",
            mimeType: "image/jpeg",
            width: 1_000,
            height: 750,
          },
        ]}
      />,
    );

    expect(screen.queryByText("图片暂不可用")).toBeNull();
    expect(
      screen.getByRole("group", { name: "2 张图片" }).querySelectorAll("img"),
    ).toHaveLength(2);
    expect(screen.getByRole("img", { name: "替换后的图片" })).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCD09/preview",
    );
  });

  it("opens on preview, links to the original, and restores focus", async () => {
    render(<ImageGrid media={media(1)} />);
    const trigger = screen.getByRole("button", {
      name: "打开第 1 张图片，共 1 张：尚未提供图片描述",
    });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog");
    expect(dialog.querySelector("img")).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCD00/preview",
    );
    expect(
      dialog.querySelector(
        'img[src="/api/v1/media/01K2ABCDEF0123456789ABCD00"]',
      ),
    ).toBeNull();
    expect(screen.getByRole("link", { name: "打开原图" })).toHaveAttribute(
      "href",
      "/api/v1/media/01K2ABCDEF0123456789ABCD00",
    );
    expect(screen.getByRole("link", { name: "打开原图" })).toHaveAttribute(
      "target",
      "_blank",
    );
    expect(dialog.querySelector("img")).toHaveAttribute(
      "src",
      "/api/v1/media/01K2ABCDEF0123456789ABCD00/preview",
    );

    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
  });
});
