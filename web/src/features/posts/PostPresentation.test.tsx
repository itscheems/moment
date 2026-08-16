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
import { FeedbackProvider } from "../../components/Feedback";
import { OwnerProvider } from "../owner/OwnerProvider";
import { PostPresentation } from "./PostPresentation";
import postCss from "./PostPresentation.module.css?inline";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.history.replaceState({}, "", "/");
  Object.defineProperty(window, "scrollY", {
    configurable: true,
    value: 0,
  });
});

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "乔乔的 Moment",
  bio: "一些正在发生的事",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

function makePost(
  contentMd: string,
  contentHtml: string,
  commentCount = 0,
): PublicPost {
  return {
    id: "01K2ABCDEF0123456789ABCDE0",
    contentMd,
    contentHtml,
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
    repostedAt: null,
    timelineItemKind: "original",
    commentCount,
    media: [],
  };
}

describe("shared post presentation", () => {
  it("places repost attribution above an aligned avatar and byline row", () => {
    const reposted = {
      ...makePost("再次分享", "<p>再次分享</p>"),
      repostedAt: "2026-08-10T02:00:00.000Z",
      timelineItemKind: "repost" as const,
    };
    const { rerender } = render(
      <NavigationProvider>
        <PostPresentation post={reposted} site={site} />
      </NavigationProvider>,
    );

    const timelineLabel = screen.getByText("已转发").parentElement;
    const timelineArticle = timelineLabel?.closest("article");
    expect(timelineLabel?.parentElement).toBe(timelineArticle);
    expect(timelineArticle?.children.item(1)).toBe(timelineLabel);

    rerender(
      <NavigationProvider>
        <PostPresentation detail post={reposted} site={site} />
      </NavigationProvider>,
    );
    const detailLabel = screen.getByText("已转发").parentElement;
    const detailArticle = detailLabel?.closest("article");
    expect(detailLabel?.parentElement).toBe(detailArticle);
    expect(detailArticle?.firstElementChild).toBe(detailLabel);
    expect(postCss).toMatch(
      /\._repostedLabel_[^{]+\{[^}]*grid-column:\s*1\s*\/\s*-1;[^}]*grid-template-columns:\s*3rem\s+minmax\(0,\s*1fr\);/s,
    );
  });

  it("renders a quoted Moment as a nested interactive card", () => {
    render(
      <NavigationProvider>
        <PostPresentation
          post={{
            ...makePost("补充说明", "<p>补充说明</p>"),
            quotedPost: {
              unavailable: false,
              id: "01K2ABCDEF0123456789ABCDE9",
              contentMd: "原始内容",
              contentHtml: "<p>原始内容</p>",
              publishedAt: "2026-08-09T01:00:00.000Z",
              media: [],
              location: null,
            },
          }}
          site={site}
        />
      </NavigationProvider>,
    );

    const quote = screen.getByRole("link", { name: "打开被引用的 Moment" });
    expect(quote).toHaveTextContent("原始内容");
    fireEvent.click(quote);
    expect(window.location.pathname).toBe("/m/01K2ABCDEF0123456789ABCDE9");
    expect(postCss).toContain("#f7f7f7");
    expect(postCss).toContain("#efefef");
    expect(postCss).toMatch(
      /\._quoteCard_[^{]+\{[^}]*background:\s*transparent;/s,
    );
    expect(postCss).toMatch(
      /\._quoteCard_[^:]+:hover,[\s\S]*?background:\s*#efefef;/,
    );
  });

  it("keeps a quote wrapper when its source is deleted", () => {
    render(
      <NavigationProvider>
        <PostPresentation
          post={{
            ...makePost("引用外层仍然存在", "<p>引用外层仍然存在</p>"),
            quotedPost: { unavailable: true },
          }}
          site={site}
        />
      </NavigationProvider>,
    );

    expect(screen.getByText("引用外层仍然存在")).toBeInTheDocument();
    expect(screen.getByText("This post is unavailable.")).toHaveAttribute(
      "role",
      "status",
    );
    expect(postCss).toMatch(
      /\._quoteUnavailable_[^{]+\{[^}]*background:\s*#efefef;/s,
    );
  });

  it("renders accepted HTML, absolute time, and a long-form timeline affordance", async () => {
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
      "01K2ABCDEF0123456789ABCDE0:original:read-more",
    );
    const time = screen.getByRole("link", { name: "1小时前" });
    expect(time).not.toHaveAttribute("title");
    fireEvent.focus(time);
    expect(await screen.findByText("2026年8月10日 09:00")).toBeInTheDocument();
    expect(
      screen.getByRole("link", { name: "打开完整 Moment" }),
    ).toContainElement(document.querySelector('svg[data-icon="reply"]'));
  });

  it("shows the full body and explicit absolute publication time in detail", () => {
    const onReply = vi.fn();
    render(
      <NavigationProvider>
        <PostPresentation
          detail
          onReply={onReply}
          post={makePost("# 标题", "<h1>标题</h1><p>完整正文</p>")}
          site={site}
        />
      </NavigationProvider>,
    );
    expect(screen.getByRole("heading", { name: "标题" })).toBeInTheDocument();
    expect(screen.getByText("完整正文")).toBeInTheDocument();
    expect(screen.getByText("发布于").parentElement).toHaveTextContent("2026");
    fireEvent.click(screen.getByRole("button", { name: "回复这条 Moment" }));
    expect(onReply).toHaveBeenCalledTimes(1);
    const article = screen.getByText("完整正文").closest("article");
    const body = article?.querySelector("[class*='detailBody']");
    expect(body).toContainElement(screen.getByText("完整正文"));
    expect(body).toHaveAttribute("data-content-density", "short");
    expect(
      screen.queryByRole("link", { name: "阅读全文" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("条回复")).not.toBeInTheDocument();
  });

  it("uses a restrained density for long detail reading", () => {
    const longText = "一段需要慢慢读完的长内容。".repeat(60);
    render(
      <NavigationProvider>
        <PostPresentation
          detail
          post={makePost(longText, `<p>${longText}</p>`)}
          site={site}
        />
      </NavigationProvider>,
    );

    expect(
      screen.getByText(longText).closest("[data-content-density]"),
    ).toHaveAttribute("data-content-density", "long");
    expect(postCss).toContain("font-size: 1.375rem");
    expect(postCss).toContain("font-size: 1.1875rem");
    expect(postCss).toContain("font-size: 1.25rem");
    expect(postCss).toContain("font-size: 1.125rem");
  });

  it("keeps the selected place name primary and discloses its address on focus or press", async () => {
    render(
      <NavigationProvider>
        <PostPresentation
          post={{
            ...makePost("带地点", "<p>带地点</p>"),
            location: {
              name: "静安寺",
              displayName: "静安寺，静安区，上海市，中国",
            },
          }}
          site={site}
        />
      </NavigationProvider>,
    );

    const location = screen.getByRole("button", {
      name: "地点：静安寺。查看详细地址",
    });
    expect(location).toHaveTextContent("静安寺");
    expect(location).not.toHaveTextContent("静安区");
    expect(location).not.toHaveAttribute("title");

    fireEvent.click(location);
    await waitFor(() =>
      expect(screen.getByText("静安寺，静安区，上海市，中国")).toBeVisible(),
    );
  });

  it("keeps manual locations compact when the name is already the full label", () => {
    render(
      <NavigationProvider>
        <PostPresentation
          post={{
            ...makePost("手动地点", "<p>手动地点</p>"),
            location: { name: "书房", displayName: "书房" },
          }}
          site={site}
        />
      </NavigationProvider>,
    );

    expect(screen.getByLabelText("地点：书房")).toHaveTextContent("书房");
    expect(
      screen.queryByRole("button", { name: /地点：书房/ }),
    ).not.toBeInTheDocument();
  });

  it("shows reply counts only when they are non-zero", () => {
    const { rerender } = render(
      <NavigationProvider>
        <PostPresentation
          post={makePost("有回复", "<p>有回复</p>", 3)}
          site={site}
        />
      </NavigationProvider>,
    );
    const timelineAction = screen.getByRole("link", {
      name: "打开完整 Moment，3 条回复",
    });
    expect(timelineAction).toHaveTextContent("3");

    rerender(
      <NavigationProvider>
        <PostPresentation
          detail
          post={makePost("有回复", "<p>有回复</p>", 3)}
          site={site}
        />
      </NavigationProvider>,
    );
    expect(screen.getByRole("link", { name: "3条回复" })).toHaveAttribute(
      "href",
      "#comments",
    );
  });

  it("optically aligns the reply glyph without shrinking its hit target", () => {
    render(
      <NavigationProvider>
        <PostPresentation
          onReply={() => undefined}
          post={makePost("对齐回复动作", "<p>对齐回复动作</p>")}
          site={site}
        />
      </NavigationProvider>,
    );

    const reply = screen.getByRole("button", { name: "回复这条 Moment" });
    expect(reply.className).toContain("replyAction");
    expect(postCss).toMatch(
      /\._replyAction_[\w-]+\s*\{[^}]*margin-inline-start: calc\(0px - var\(--space-3\)\)/,
    );
    expect(postCss).toMatch(
      /\._postAction_[\w-]+\s*\{[^}]*inline-size: 2\.75rem[^}]*block-size: 2\.75rem/,
    );
  });

  it("aligns timeline and detail actions with sharing at the far edge", () => {
    expect(postCss).toMatch(/\._postActions_[\w-]+\s*\{[^}]*inline-size: 100%/);
    expect(postCss).toMatch(
      /\._postActions_[\w-]+\s*\{[^}]*grid-template-columns: 2\.75rem 2\.75rem minmax\(0, 1fr\) 2\.75rem/,
    );
    expect(postCss).toMatch(/\._shareAction_[\w-]+\s*\{[^}]*grid-column: 4/);
    expect(postCss).toMatch(
      /\._detail_[\w-]+ \._postActions_[\w-]+\s*\{[^}]*max-inline-size: none/,
    );
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

  it("exposes a native keyboard detail link before the card actions", () => {
    render(
      <NavigationProvider>
        <PostPresentation
          post={makePost("键盘打开", "<p>键盘打开</p>")}
          site={site}
        />
      </NavigationProvider>,
    );

    const link = screen.getByRole("link", { name: "打开这条 Moment 详情" });
    expect(link).toHaveAttribute("href", "/m/01K2ABCDEF0123456789ABCDE0");
    expect(link).toHaveAttribute(
      "data-focus-key",
      "01K2ABCDEF0123456789ABCDE0:original:surface-link",
    );
    link.focus();
    expect(link).toHaveFocus();
    fireEvent.click(link);
    expect(window.location.pathname).toBe("/m/01K2ABCDEF0123456789ABCDE0");
  });

  it("keeps every share action inside the portal without navigating the card", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    const share = vi.fn().mockResolvedValue(undefined);
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    vi.stubGlobal(
      "navigator",
      Object.assign(Object.create(window.navigator), {
        clipboard: { writeText },
        share,
      }),
    );
    Object.defineProperty(window, "scrollY", {
      configurable: true,
      value: 420,
    });
    render(
      <FeedbackProvider>
        <NavigationProvider>
          <PostPresentation
            post={makePost("可分享", "<p>可分享</p>")}
            site={{ ...site, publicBaseUrl: "https://moment.example" }}
          />
        </NavigationProvider>
      </FeedbackProvider>,
    );

    const trigger = screen.getByRole("button", { name: "分享这条 Moment" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("menuitem", { name: "复制链接" }));

    await waitFor(() => {
      expect(writeText).toHaveBeenCalledWith(
        "https://moment.example/m/01K2ABCDEF0123456789ABCDE0",
      );
    });
    expect(screen.getByRole("dialog", { name: "链接已复制" })).toBeVisible();
    expect(window.location.pathname).toBe("/");
    expect(window.scrollY).toBe(420);
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("menuitem", { name: "系统分享" }));
    await waitFor(() => expect(share).toHaveBeenCalledTimes(1));
    expect(window.location.pathname).toBe("/");
    expect(window.scrollY).toBe(420);
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "在新标签页打开" }),
    );
    expect(open).toHaveBeenCalledWith(
      "https://moment.example/m/01K2ABCDEF0123456789ABCDE0",
      "_blank",
      "noopener,noreferrer",
    );
    expect(window.location.pathname).toBe("/");
    expect(window.scrollY).toBe(420);
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it("keeps a detail share menu on the content side of its trigger", async () => {
    render(
      <NavigationProvider>
        <PostPresentation
          detail
          post={makePost("分享详情", "<p>分享详情</p>")}
          site={site}
        />
      </NavigationProvider>,
    );

    const trigger = screen.getByRole("button", { name: "分享这条 Moment" });
    expect(trigger.querySelector('svg[data-icon="share"]')).toBeInTheDocument();
    fireEvent.click(trigger);

    const menu = await screen.findByRole("menu", { hidden: true });
    expect(menu.parentElement).toHaveAttribute("data-align", "start");
  });

  it("opens the site profile preview from the author identity", async () => {
    render(
      <NavigationProvider>
        <PostPresentation
          post={makePost("查看作者", "<p>查看作者</p>")}
          site={site}
        />
      </NavigationProvider>,
    );

    fireEvent.focus(
      screen.getByRole("link", {
        name: "查看 Moment 的个人资料",
      }),
    );

    expect(
      await screen.findByRole("heading", { name: "Moment" }),
    ).toBeInTheDocument();
    expect(screen.getByText("一些正在发生的事")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "查看个人资料" })).toHaveAttribute(
      "href",
      "/@owner",
    );
  });

  it("confirms and publishes a singleton profile pin from the owner menu", async () => {
    window.sessionStorage.setItem(
      "moment.owner.session.v1",
      JSON.stringify({
        csrf_token: "csrf-pin",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    const requests: Array<{ url: string; method: string }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        requests.push({ url, method: init?.method ?? "GET" });
        if (url === "/api/v1/owner/status") {
          return new Response(
            JSON.stringify({
              data: {
                initialized: true,
                password_enabled: false,
                session_epoch: 1,
                initialized_at: "2026-08-01T00:00:00Z",
              },
              meta: { request_id: "req_status" },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        if (
          url === "/api/v1/owner/posts/01K2ABCDEF0123456789ABCDE0/pin" &&
          init?.method === "PUT"
        ) {
          return new Response(
            JSON.stringify({
              data: {
                owner_id: site.ownerId,
                username: site.username,
                display_name: site.displayName,
                title: site.title,
                bio: site.bio,
                timezone: site.timezone,
                public_base_url: null,
                avatar_media_id: null,
                cover_media_id: null,
                pinned_post_id: "01K2ABCDEF0123456789ABCDE0",
                comments_enabled: true,
              },
              meta: { request_id: "req_pin" },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response("{}", { status: 404 });
      }),
    );
    const onSiteChanged = vi.fn();
    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <PostPresentation
              onSiteChanged={onSiteChanged}
              owner
              post={makePost("可置顶", "<p>可置顶</p>")}
              site={site}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "更多操作" }));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "置顶到个人资料" }),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("heading", { name: "置顶这段 Moment？" }),
      ).toBeVisible(),
    );
    fireEvent.click(screen.getByRole("button", { name: "置顶" }));

    await waitFor(() => expect(onSiteChanged).toHaveBeenCalledTimes(1));
    expect(onSiteChanged.mock.calls[0]?.[0]).toMatchObject({
      pinnedPostId: "01K2ABCDEF0123456789ABCDE0",
    });
    expect(requests).toContainEqual({
      url: "/api/v1/owner/posts/01K2ABCDEF0123456789ABCDE0/pin",
      method: "PUT",
    });
    expect(screen.getByText("已置顶到个人资料")).toBeInTheDocument();
    window.sessionStorage.removeItem("moment.owner.session.v1");
  });

  it("reposts from the owner toolbar and exposes the green active state", async () => {
    window.sessionStorage.setItem(
      "moment.owner.session.v1",
      JSON.stringify({
        csrf_token: "csrf-repost",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    const requests: Array<{ url: string; method: string }> = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        requests.push({ url, method: init?.method ?? "GET" });
        if (url === "/api/v1/owner/status") {
          return new Response(
            JSON.stringify({
              data: {
                initialized: true,
                password_enabled: false,
                session_epoch: 1,
                initialized_at: "2026-08-01T00:00:00Z",
              },
              meta: { request_id: "req_status" },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        if (
          url === "/api/v1/owner/posts/01K2ABCDEF0123456789ABCDE0/repost" &&
          init?.method === "PUT"
        ) {
          return new Response(
            JSON.stringify({
              data: {
                id: "01K2ABCDEF0123456789ABCDE0",
                content_md: "可转发",
                content_html: "<p>可转发</p>",
                created_at: "2026-08-10T01:00:00.000Z",
                updated_at: "2026-08-10T02:00:00.000Z",
                published_at: "2026-08-10T01:00:00.000Z",
                reposted_at: "2026-08-10T02:00:00.000Z",
                timeline_item_kind: "original",
                quoted_post: null,
                comment_count: 0,
                media: [],
              },
              meta: { request_id: "req_repost" },
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          );
        }
        return new Response("{}", { status: 404 });
      }),
    );

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <PostPresentation
              owner
              post={makePost("可转发", "<p>可转发</p>")}
              site={site}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    const repost = await screen.findByRole("button", {
      name: "转发这条 Moment",
    });
    fireEvent.click(repost);
    fireEvent.click(await screen.findByRole("menuitem", { name: "转发" }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "转发这条 Moment" }),
      ).toHaveAttribute("aria-pressed", "true"),
    );
    expect(requests).toContainEqual({
      url: "/api/v1/owner/posts/01K2ABCDEF0123456789ABCDE0/repost",
      method: "PUT",
    });
    expect(screen.getByText("已转发到时间流顶部")).toBeInTheDocument();
    expect(postCss).toContain("#07ba7c");
    expect(postCss).toContain("#e3f1eb");
    fireEvent.click(screen.getByRole("button", { name: "转发这条 Moment" }));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "引用 Moment" }),
    );
    expect(window.location.pathname).toBe("/owner/compose");
    expect(new URLSearchParams(window.location.search).get("quote")).toBe(
      "01K2ABCDEF0123456789ABCDE0",
    );
    window.sessionStorage.removeItem("moment.owner.session.v1");
  });
});
