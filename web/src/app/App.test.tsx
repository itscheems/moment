import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

class FakeVisualViewport extends EventTarget {
  height: number;
  width: number;
  offsetLeft = 0;
  offsetTop = 0;
  pageLeft = 0;
  pageTop = 0;
  scale = 1;
  onresize: ((this: VisualViewport, event: Event) => unknown) | null = null;
  onscroll: ((this: VisualViewport, event: Event) => unknown) | null = null;

  constructor(width: number, height: number) {
    super();
    this.width = width;
    this.height = height;
  }
}

function mediaQuery(matches: boolean, media: string): MediaQueryList {
  return {
    matches,
    media,
    onchange: null,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    addListener: vi.fn(),
    removeListener: vi.fn(),
    dispatchEvent: vi.fn(),
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const siteEnvelope = {
  data: {
    owner_id: "00000000000000000000000000000000",
    username: "owner",
    display_name: "乔乔",
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
    comment_count: 0,
    media: [],
  };
}

function timelineEnvelope(items: unknown[] = []) {
  return {
    data: { items },
    meta: { next_cursor: null, request_id: "req_posts" },
  };
}

function ownerStatusEnvelope() {
  return {
    data: {
      initialized: true,
      password_enabled: false,
      session_epoch: 1,
      initialized_at: "2026-08-01T00:00:00Z",
    },
    meta: { request_id: "req_owner" },
  };
}

function preauthenticate() {
  window.sessionStorage.setItem(
    "moment.owner.session.v1",
    JSON.stringify({
      csrf_token: "csrf-from-session",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
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
    window.sessionStorage.clear();
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
    expect(screen.getByRole("link", { name: "发布" })).toHaveAttribute(
      "href",
      "/owner/compose",
    );
    expect(
      screen
        .getByRole("link", { name: "发布" })
        .querySelector('svg[data-icon="compose"]'),
    ).toBeInTheDocument();
    const desktopNavigation = screen.getByRole("navigation", {
      name: "主要导航",
    });
    const more = screen.getAllByRole("button", { name: "更多" })[0]!;
    const publish = screen.getByRole("link", { name: "发布" });
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

  it("starts the timeline while site identity is still pending and does not refetch after identity arrives", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "身份之前已经可读");
    let resolveSite: ((response: Response) => void) | null = null;
    let siteSettled = false;
    let timelineRequests = 0;
    const delayedSite = new Promise<Response>((resolve) => {
      resolveSite = resolve;
    });
    preauthenticate();
    installApi(async (url) => {
      if (url === "/api/v1/site") return delayedSite;
      if (url === "/api/v1/owner/status") {
        return json(ownerStatusEnvelope());
      }
      if (url.startsWith("/api/v1/posts?")) {
        timelineRequests += 1;
        return json(timelineEnvelope([item]));
      }
      return json({}, 404);
    });

    render(<App />);
    await waitFor(() =>
      expect(screen.getByText("身份之前已经可读")).toBeInTheDocument(),
    );
    expect(
      document.querySelector('section[aria-label="发布 Moment"]'),
    ).not.toBeInTheDocument();
    expect(resolveSite).not.toBeNull();
    expect(siteSettled).toBe(false);
    expect(timelineRequests).toBe(1);

    await act(async () => {
      siteSettled = true;
      resolveSite?.(json(siteEnvelope));
      await delayedSite;
    });
    await waitFor(() => expect(document.title).toBe("乔乔的 Moment"));
    await waitFor(() =>
      expect(
        document.querySelector('section[aria-label="发布 Moment"]'),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("身份之前已经可读")).toBeInTheDocument();
    expect(timelineRequests).toBe(1);
  });

  it("keeps independently loaded content readable while identity retry repairs the shell", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "资料失败也能阅读");
    let siteRequests = 0;
    let timelineRequests = 0;
    preauthenticate();
    installApi(async (url) => {
      if (url === "/api/v1/site") {
        siteRequests += 1;
        if (siteRequests === 1) throw new Error("site unavailable");
        return json(siteEnvelope);
      }
      if (url === "/api/v1/owner/status") {
        return json(ownerStatusEnvelope());
      }
      if (url.startsWith("/api/v1/posts?")) {
        timelineRequests += 1;
        return json(timelineEnvelope([item]));
      }
      return json({}, 404);
    });

    render(<App />);
    await waitFor(() =>
      expect(screen.getByText("资料失败也能阅读")).toBeInTheDocument(),
    );
    const identityError = await screen.findByRole("alert");
    expect(identityError).toHaveTextContent(
      "站点资料没有加载完成，暂时使用默认身份。",
    );
    expect(
      document.querySelector('section[aria-label="发布 Moment"]'),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", {
        name: "重试资料",
      }),
    );

    await waitFor(() => expect(document.title).toBe("乔乔的 Moment"));
    await waitFor(() =>
      expect(
        document.querySelector('section[aria-label="发布 Moment"]'),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("资料失败也能阅读")).toBeInTheDocument();
    expect(siteRequests).toBe(2);
    expect(timelineRequests).toBe(1);
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

  it("uses five distinct mobile destinations and omits the inline owner composer", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "手机首屏的第一条");
    preauthenticate();
    vi.mocked(window.matchMedia).mockImplementation((query) =>
      mediaQuery(query === "(max-width: 719px)", query),
    );
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url === "/api/v1/owner/status") {
        return json(ownerStatusEnvelope());
      }
      if (url.startsWith("/api/v1/posts?")) {
        return json(timelineEnvelope([item]));
      }
      return json({}, 404);
    });

    const { container } = render(<App />);
    await screen.findByText("手机首屏的第一条");
    const article = container.querySelector(
      `article[data-post-id="${item.id}"]`,
    );
    const mobileNavigation = container.querySelector<HTMLElement>(
      'nav[aria-label="移动导航"]',
    );

    expect(article).not.toBeNull();
    expect(
      container.querySelector('section[aria-label="发布 Moment"]'),
    ).toBeNull();
    expect(
      Array.from(mobileNavigation?.querySelectorAll("a") ?? [], (link) =>
        link.getAttribute("aria-label"),
      ),
    ).toEqual(["时间流", "搜索", "归档", "发布", "所有者"]);
    expect(
      mobileNavigation?.querySelector('a[aria-label="发布"]'),
    ).toHaveAttribute("href", "/owner/compose");
    expect(
      mobileNavigation?.querySelector('a[aria-label="所有者"]'),
    ).toHaveAttribute("href", "/owner/settings");
    expect(
      mobileNavigation?.querySelector('a[aria-label="个人资料"]'),
    ).toBeNull();
  });

  it("hides the mobile bar on deterministic visual-viewport contraction without moving composer focus", async () => {
    const viewport = new FakeVisualViewport(390, 844);
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    vi.stubGlobal("innerHeight", 844);
    vi.stubGlobal("visualViewport", viewport);
    vi.mocked(window.matchMedia).mockImplementation((query) =>
      mediaQuery(query === "(max-width: 719px)", query),
    );
    installApi(async (url) => {
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url === "/api/v1/owner/status") {
        return json(ownerStatusEnvelope());
      }
      return json({}, 404);
    });

    const { container } = render(<App />);
    const editor = await screen.findByLabelText("此刻想到什么？");
    const mobileNavigation = container.querySelector<HTMLElement>(
      'nav[aria-label="移动导航"]',
    );
    editor.focus();
    expect(editor).toHaveFocus();
    expect(mobileNavigation).not.toHaveAttribute("data-viewport-obscured");

    act(() => {
      viewport.height = 480;
      viewport.dispatchEvent(new Event("resize"));
    });
    expect(mobileNavigation).toHaveAttribute("data-viewport-obscured", "true");
    expect(editor).toHaveFocus();

    act(() => {
      viewport.height = 844;
      viewport.dispatchEvent(new Event("resize"));
    });
    expect(mobileNavigation).not.toHaveAttribute("data-viewport-obscured");
    expect(editor).toHaveFocus();
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
    const publish = screen.getByRole("link", { name: "发布" });
    vi.spyOn(window, "scrollX", "get").mockReturnValue(12);
    vi.spyOn(window, "scrollY", "get").mockReturnValue(640);

    publish.focus();
    fireEvent.click(publish);
    expect(window.location.pathname).toBe("/owner/compose");
    expect(
      await screen.findByRole("dialog", { name: "发布 Moment" }),
    ).toBeInTheDocument();
    expect(document.querySelector("main h1")).toHaveTextContent("时间流");
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.documentElement.style.overscrollBehavior).toBe("none");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-640px");
    expect(document.body.style.left).toBe("-12px");

    fireEvent.keyDown(screen.getByRole("dialog", { name: "发布 Moment" }), {
      key: "Escape",
    });
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(publish).toHaveFocus();
    expect(document.documentElement.style.overflowY).toBe("");
    expect(document.documentElement.style.overscrollBehavior).toBe("");
    expect(document.body.style.position).toBe("");
    expect(window.scrollTo).toHaveBeenCalledWith(12, 640);
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
        name: "乔乔 的个人资料",
      }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 1, name: "乔乔" }),
    ).toBeInTheDocument();
    expect(await screen.findByText("个人资料里的 Moment")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { level: 2, name: "乔乔" }),
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

    const openLink = screen.getByRole("link", {
      name: "打开这条 Moment 详情",
    });
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
      '[data-focus-key$=":surface-link"]',
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
