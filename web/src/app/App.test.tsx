import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const siteEnvelope = {
  data: {
    title: "乔乔的 Moment",
    bio: "沿着日常留下坐标",
    timezone: "Asia/Shanghai",
    public_base_url: null,
    avatar_media_id: null,
    cover_media_id: null,
    pinned_post_id: null,
    comments_enabled: true,
  },
  meta: { request_id: "req_site" },
};

function post(id: string, content: string, html = `<p>${content}</p>`) {
  return {
    id,
    content_md: content,
    content_html: html,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    media: [],
  };
}

function timelineEnvelope(items: unknown[] = []) {
  return {
    data: { items },
    meta: { next_cursor: null, request_id: "req_posts" },
  };
}

function installApi(
  handler: (url: string) => Promise<Response> = async (url) => {
    if (url === "/api/v1/site") return json(siteEnvelope);
    return json(timelineEnvelope());
  },
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL) =>
    handler(String(input)),
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

describe("App shell and public routes", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    window.localStorage.clear();
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
    installApi();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("keeps the public timeline central with real site identity and landmarked navigation", async () => {
    const { container } = render(<App />);

    expect(screen.getByRole("main")).toHaveAttribute("id", "main-content");
    expect(screen.getByRole("main")).toHaveAttribute("tabindex", "-1");
    expect(
      screen.getByRole("navigation", { name: "主要导航" }),
    ).toBeInTheDocument();
    const mobileNavigation = container.querySelector<HTMLElement>(
      'nav[aria-label="移动导航"]',
    );
    expect(mobileNavigation).toBeInTheDocument();
    expect(document.querySelector("main h1")).toHaveTextContent("时间流");
    expect(
      mobileNavigation?.querySelector('a[aria-label="发布"]'),
    ).toHaveAttribute("href", "/owner/compose");
    expect(screen.getByRole("link", { name: "发布 Moment" })).toHaveAttribute(
      "href",
      "/owner/compose",
    );
    expect(
      screen
        .getByRole("link", { name: "发布 Moment" })
        .querySelector('svg[data-icon="compose"]'),
    ).toBeInTheDocument();
    const desktopNavigation = screen.getByRole("navigation", {
      name: "主要导航",
    });
    const more = screen.getAllByRole("button", { name: "更多" })[0]!;
    const publish = screen.getByRole("link", { name: "发布 Moment" });
    expect(desktopNavigation).toContainElement(more);
    expect(
      more.compareDocumentPosition(publish) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
    expect(
      await screen.findByRole("heading", { name: "还没有 Moment" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("乔乔的 Moment").length).toBeGreaterThan(0);
    expect(document.title).toBe("乔乔的 Moment");
  });

  it("keeps Owner access in the mobile account menu and restores trigger focus", async () => {
    render(<App />);
    const trigger = document.querySelector<HTMLButtonElement>(
      'button[aria-label="打开账户菜单"]',
    );
    expect(trigger).not.toBeNull();

    trigger!.focus();
    fireEvent.click(trigger!);
    const menu = await screen.findByRole("menu", { hidden: true });
    expect(
      screen.getByRole("menuitem", { name: "个人资料" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "所有者登录" }),
    ).toBeInTheDocument();

    fireEvent.keyDown(menu, { key: "Escape" });
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger!);
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "所有者登录" }),
    );
    await waitFor(() => expect(window.location.pathname).toBe("/owner/login"));
  });

  it("opens desktop Publish as a route-backed dialog and restores shell focus", async () => {
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 720px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    render(<App />);
    const publish = screen.getByRole("link", { name: "发布 Moment" });

    publish.focus();
    fireEvent.click(publish);
    expect(window.location.pathname).toBe("/owner/compose");
    expect(
      await screen.findByRole("dialog", { name: "发布 Moment" }),
    ).toBeInTheDocument();
    expect(document.querySelector("main h1")).toHaveTextContent("时间流");

    fireEvent.click(screen.getByRole("button", { name: "关闭发布窗口" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(publish).toHaveFocus();
  });

  it("replaces discovery placeholders with recoverable URL-state errors", async () => {
    window.history.replaceState({}, "", "/search?state=loading");
    const { unmount } = render(<App />);
    expect(screen.getByRole("heading", { name: "搜索" })).toBeInTheDocument();
    expect(
      await screen.findByRole("heading", { name: "搜索链接无效" }),
    ).toBeInTheDocument();

    unmount();
    window.history.replaceState({}, "", "/archive?state=error");
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent("归档链接无效");
  });

  it("opens display controls from More and persists color and background", async () => {
    render(<App />);
    const triggers = screen.getAllByRole("button", { name: "更多" });
    fireEvent.click(triggers[0]);

    fireEvent.click(await screen.findByRole("menuitem", { name: /显示/ }));
    fireEvent.click(await screen.findByRole("button", { name: "粉色" }));
    fireEvent.click(await screen.findByRole("button", { name: /^纯黑/ }));
    await waitFor(() =>
      expect(document.documentElement).toHaveAttribute(
        "data-theme",
        "lights-out",
      ),
    );
    expect(document.documentElement).toHaveAttribute("data-accent", "pink");
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("lights-out");
    expect(window.localStorage.getItem("moment.accent.v1")).toBe("pink");
  });

  it("gives active navigation a semantic current-page state", () => {
    window.history.replaceState({}, "", "/archive");
    render(<App />);
    expect(screen.getAllByRole("link", { name: "归档" })[0]).toHaveAttribute(
      "aria-current",
      "page",
    );
  });

  it("renders the public profile as a first-class route", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "个人资料里的 Moment");
    window.history.replaceState({}, "", "/profile");
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url.startsWith("/api/v1/posts?")) {
        return json(timelineEnvelope([item]));
      }
      return json({}, 404);
    });

    render(<App />);

    expect(
      await screen.findByRole("region", {
        name: "乔乔的 Moment 的个人资料",
      }),
    ).toBeInTheDocument();
    expect(await screen.findByText("个人资料里的 Moment")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 1, name: "乔乔的 Moment" }),
    ).toBeInTheDocument();
    expect(
      screen.getAllByRole("link", { name: "个人资料" })[0],
    ).toHaveAttribute("aria-current", "page");
    expect(screen.queryByText("私人的公开时间流")).not.toBeInTheDocument();
  });

  it("opens detail and returns to the preserved timeline without refetching it", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "可返回的 Moment");
    let timelineRequests = 0;
    const fetcher = installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url.startsWith("/api/v1/posts?")) {
        timelineRequests += 1;
        return json(timelineEnvelope([item]));
      }
      if (url === `/api/v1/posts/${item.id}`) {
        return json({ data: item, meta: { request_id: "req_post" } });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByText("可返回的 Moment");
    vi.spyOn(window, "scrollY", "get").mockReturnValue(640);

    const openLink = screen.getByRole("link", { name: "打开完整 Moment" });
    openLink.focus();
    fireEvent.click(openLink);
    expect(
      await screen.findByRole("heading", { name: "Moment" }),
    ).toBeInTheDocument();
    expect(window.location.pathname).toBe(`/m/${item.id}`);
    const backLink = screen.getByRole("link", { name: "返回时间流" });
    backLink.focus();
    fireEvent.click(backLink);

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.getByRole("heading", { name: "时间流" })).toBeInTheDocument();
    expect(timelineRequests).toBe(1);
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(openLink).toHaveFocus();
    expect(fetcher).toHaveBeenCalledWith(
      `/api/v1/posts/${item.id}`,
      expect.any(Object),
    );
    await waitFor(() =>
      expect(window.scrollTo).toHaveBeenCalledWith({
        top: 640,
        behavior: "auto",
      }),
    );
  });

  it("returns to the exact trigger on a previously loaded page", async () => {
    const first = post("01K2ABCDEF0123456789ABCDE0", "最近的一段");
    const second = post("01K2ABCDEF0123456789ABCDE1", "第二页的一段");
    let timelineRequests = 0;
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url.startsWith("/api/v1/posts?")) {
        timelineRequests += 1;
        return url.includes("cursor=cursor-one")
          ? json(timelineEnvelope([second]))
          : json({
              data: { items: [first] },
              meta: { next_cursor: "cursor-one", request_id: "req_posts" },
            });
      }
      if (url === `/api/v1/posts/${second.id}`) {
        return json({ data: second, meta: { request_id: "req_post" } });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByText("最近的一段");
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    await screen.findByText("第二页的一段");
    expect(window.location.search).toContain("loaded_through=cursor-one");

    vi.spyOn(window, "scrollY", "get").mockReturnValue(920);
    const secondPost = screen
      .getByText("第二页的一段")
      .closest<HTMLElement>("[data-post-id]");
    const trigger = secondPost?.querySelector<HTMLAnchorElement>(
      '[data-focus-key$=":open-post"]',
    );
    expect(trigger).toBeTruthy();
    trigger?.focus();
    fireEvent.click(trigger!);
    expect(
      await screen.findByRole("heading", { name: "Moment" }),
    ).toBeInTheDocument();

    const backLink = screen.getByRole("link", { name: "返回时间流" });
    backLink.focus();
    fireEvent.click(backLink);
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );

    expect(window.location.search).toContain("loaded_through=cursor-one");
    expect(timelineRequests).toBe(2);
    expect(trigger).toHaveFocus();
    expect(window.scrollTo).toHaveBeenCalledWith({
      top: 920,
      behavior: "auto",
    });
  });

  it("renders a tailored direct-detail 404", async () => {
    window.history.replaceState({}, "", "/m/01K2ABCDEF0123456789ABCDE0");
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      return json(
        {
          error: {
            code: "POST_NOT_FOUND",
            message: "missing",
            details: {},
            request_id: "req_missing",
          },
        },
        404,
      );
    });
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这个 Moment 不在这里",
    );
  });

  it("recovers a directly opened detail route and rejects malformed post data", async () => {
    const item = post(
      "01K2ABCDEF0123456789ABCDE0",
      "直接打开的正文",
      "<p>直接打开的正文</p>",
    );
    window.history.replaceState({}, "", `/m/${item.id}`);
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      return json({ data: item, meta: { request_id: "req_post" } });
    });
    const { unmount } = render(<App />);
    expect(await screen.findByText("直接打开的正文")).toBeInTheDocument();

    unmount();
    window.history.replaceState({}, "", `/m/${item.id}`);
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      const malformed: Partial<typeof item> = { ...item };
      delete malformed.content_html;
      return json({ data: malformed, meta: { request_id: "req_bad" } });
    });
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这次收到的内容无法显示",
    );
  });
});
