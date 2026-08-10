import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { PublicPost, SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { PostPresentation } from "./PostPresentation";

afterEach(cleanup);

const site: SiteIdentity = {
  title: "乔乔的 Moment",
  bio: "一些正在发生的事",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  commentsEnabled: true,
};

function makePost(contentMd: string, contentHtml: string): PublicPost {
  return {
    id: "01K2ABCDEF0123456789ABCDE0",
    contentMd,
    contentHtml,
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
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
    expect(screen.getByText("08.10")).toHaveAccessibleName(
      expect.stringContaining("2026"),
    );
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
});
