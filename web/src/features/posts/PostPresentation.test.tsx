import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { PublicPost, SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { PostPresentation } from "./PostPresentation";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
});

const site: SiteIdentity = {
  title: "乔乔的 Moment",
  bio: "一些正在发生的事",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  commentsEnabled: true,
  instanceId: null,
};

function makePost(contentMd: string, contentHtml: string): PublicPost {
  return {
    id: "01K2ABCDEF0123456789ABCDE0",
    contentMd,
    contentHtml,
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
    media: [],
  };
}

describe("shared post presentation", () => {
  it("renders accepted HTML, absolute time, and a long-form timeline affordance", () => {
    const longText = "一段很长的记忆。".repeat(70);
    render(
      <NavigationProvider>
        <PostPresentation
          now={new Date("2026-08-10T02:00:00.000Z")}
          post={makePost(longText, `<p>${longText}</p>`)}
          site={site}
        />
      </NavigationProvider>,
    );

    expect(screen.getByText(longText)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "阅读全文" })).toHaveAttribute(
      "href",
      "/m/01K2ABCDEF0123456789ABCDE0",
    );
    expect(screen.getByRole("link", { name: "阅读全文" })).toHaveAttribute(
      "data-focus-key",
      "01K2ABCDEF0123456789ABCDE0:read-more",
    );
    expect(screen.getByRole("link", { name: "1小时前" })).toHaveAttribute(
      "title",
      expect.stringContaining("2026"),
    );
    expect(
      screen.getByRole("link", { name: "打开完整 Moment" }),
    ).toHaveTextContent("回应");
  });

  it("shows the full body and explicit absolute publication time in detail", () => {
    render(
      <NavigationProvider>
        <PostPresentation
          detail
          post={makePost("# 标题", "<h1>标题</h1><p>完整正文</p>")}
          site={site}
        />
      </NavigationProvider>,
    );
    expect(screen.getByRole("heading", { name: "标题" })).toBeInTheDocument();
    expect(screen.getByText("完整正文")).toBeInTheDocument();
    expect(screen.getByText("发布于").parentElement).toHaveTextContent("2026");
    expect(
      screen.queryByRole("link", { name: "阅读全文" }),
    ).not.toBeInTheDocument();
  });

  it("opens the detail from the post surface without hijacking post actions", () => {
    window.scrollTo = vi.fn();
    render(
      <NavigationProvider>
        <PostPresentation
          post={makePost("整行可打开", "<p>整行可打开</p>")}
          site={site}
        />
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByText("整行可打开"));
    expect(window.location.pathname).toBe("/m/01K2ABCDEF0123456789ABCDE0");
  });

  it("copies the canonical public URL and shows a confirmation", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal(
      "navigator",
      Object.assign(Object.create(window.navigator), {
        clipboard: { writeText },
        share: undefined,
      }),
    );
    render(
      <NavigationProvider>
        <PostPresentation
          post={makePost("可分享", "<p>可分享</p>")}
          site={{ ...site, publicBaseUrl: "https://moment.example" }}
        />
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "分享这条 Moment" }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(
        "https://moment.example/m/01K2ABCDEF0123456789ABCDE0",
      );
    });
    expect(screen.getByRole("status")).toHaveTextContent("链接已复制");
  });
});
