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
    expect(screen.getByRole("heading", { name: "时间流" })).toBeInTheDocument();
    expect(
      mobileNavigation?.querySelector('a[aria-label="发布"]'),
    ).toHaveAttribute("href", "/owner/compose");
    expect(screen.getByRole("link", { name: "发布 Moment" })).toHaveAttribute(
      "href",
      "/owner/compose",
    );
    expect(
      await screen.findByRole("heading", { name: "第一段记忆还没写下" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText("乔乔的 Moment").length).toBeGreaterThan(0);
    expect(document.title).toBe("乔乔的 Moment");
  });

  it("keeps placeholder loading and error examples for later route slices", async () => {
    window.history.replaceState({}, "", "/search?state=loading");
    const { unmount } = render(<App />);
    expect(screen.getByRole("heading", { name: "搜索" })).toBeInTheDocument();
    expect(await screen.findByLabelText("正在加载时间流")).toHaveAttribute(
      "aria-busy",
      "true",
    );

    unmount();
    window.history.replaceState({}, "", "/archive?state=error");
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这次没有取回时间流",
    );
  });

  it("opens the accessible Base UI theme menu and persists an override", async () => {
    render(<App />);
    const triggers = screen.getAllByRole("button", { name: "选择主题" });
    fireEvent.click(triggers[0]);

    const dark = await screen.findByRole("menuitemradio", { name: "深色" });
    fireEvent.click(dark);
    await waitFor(() =>
      expect(document.documentElement).toHaveAttribute("data-theme", "dark"),
    );
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("dark");
    await waitFor(() => expect(triggers[0]).toHaveFocus());
  });

  it("gives active navigation a semantic current-page state", () => {
    window.history.replaceState({}, "", "/archive");
    render(<App />);
    expect(screen.getAllByRole("link", { name: "归档" })[0]).toHaveAttribute(
      "aria-current",
      "page",
    );
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
    fireEvent.click(screen.getByRole("link", { name: "返回时间流" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.getByRole("heading", { name: "时间流" })).toBeInTheDocument();
    expect(timelineRequests).toBe(1);
    await waitFor(() => expect(openLink).toHaveFocus());
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
