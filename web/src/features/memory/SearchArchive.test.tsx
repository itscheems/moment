import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "../../app/App";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const site = {
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

function post(id: string, content: string) {
  return {
    id,
    content_md: content,
    content_html: `<p>${content}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    media: [],
  };
}

const first = post("01K2ABCDEF0123456789ABCDE0", "旧城雨落在街口");
const second = post("01K2ABCDEF0123456789ABCDE1", "旧城雨后的第二页");

function install(handler: (url: string) => Response | Promise<Response>) {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === "/api/v1/site") return json(site);
    if (url === "/api/v1/owner/status") {
      return json(
        {
          error: {
            code: "UNAUTHORIZED",
            message: "guest",
            details: {},
            request_id: "req_owner",
          },
        },
        401,
      );
    }
    return handler(url);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

describe("search and archive memory layer", () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
    window.history.replaceState({}, "", "/");
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("opens search with / while guarding inputs and IME composition", async () => {
    install(() => json({ data: { items: [] }, meta: { next_cursor: null } }));
    render(<App />);

    fireEvent.keyDown(window, { key: "/", isComposing: true });
    expect(window.location.pathname).toBe("/");
    fireEvent.keyDown(window, { key: "/" });
    await waitFor(() => expect(window.location.pathname).toBe("/search"));
    const input = await screen.findByPlaceholderText("中文、英文、数字或短语");
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(input).toHaveFocus();
    fireEvent.change(input, { target: { value: "a" } });
    fireEvent.keyDown(input, { key: "/" });
    expect(input).toHaveValue("a");
    expect(window.location.pathname).toBe("/search");
  });

  it("keeps Unicode query/date state, renders safe snippets, and restores cursor pages", async () => {
    window.history.replaceState(
      {},
      "",
      "/search?q=%E6%97%A7%E5%9F%8E%E9%9B%A8",
    );
    let searchRequests = 0;
    install((url) => {
      if (url.startsWith("/api/v1/search?")) {
        searchRequests += 1;
        const continued = url.includes("cursor=cursor-one");
        return json({
          data: {
            items: [
              {
                ...(continued ? second : first),
                snippet: continued
                  ? "第二页仍有旧城雨"
                  : '<img src="x" onerror="alert(1)"> 旧城雨',
              },
            ],
            year_counts: [
              { year: 2026, count: 2 },
              { year: 2024, count: 1 },
            ],
          },
          meta: { next_cursor: continued ? null : "cursor-one" },
        });
      }
      if (url === `/api/v1/posts/${second.id}`) {
        return json({ data: second, meta: { request_id: "req_post" } });
      }
      return json({}, 404);
    });
    const view = render(<App />);

    await waitFor(() =>
      expect(screen.getByText("旧城雨落在街口")).toBeInTheDocument(),
    );
    expect(
      screen.getByText('<img src="x" onerror="alert(1)">', { exact: false }),
    ).toBeInTheDocument();
    expect(view.container.querySelector('img[src="x"]')).toBeNull();
    expect(
      screen.getByText("旧城雨", { selector: "mark" }),
    ).toBeInTheDocument();
    expect(screen.getAllByText(/2026年8月10日/).length).toBeGreaterThan(0);
    expect(screen.getByRole("status")).toHaveTextContent("找到 3 条");

    const yearNavigation = screen.getByRole("navigation", {
      name: "按年份筛选搜索结果",
    });
    fireEvent.click(
      within(yearNavigation).getByRole("link", { name: "2026 2" }),
    );
    await waitFor(() => {
      expect(window.location.search).toContain("q=%E6%97%A7%E5%9F%8E%E9%9B%A8");
      expect(window.location.search).toContain("from=2026-01-01");
      expect(window.location.search).toContain("to=2027-01-01");
    });

    fireEvent.click(screen.getByRole("button", { name: "加载更早的结果" }));
    expect(await screen.findByText("旧城雨后的第二页")).toBeInTheDocument();
    expect(window.location.search).toContain("loaded_through=cursor-one");
    const requestsBeforeRefresh = searchRequests;

    view.unmount();
    render(<App />);
    await waitFor(() =>
      expect(screen.getByText("旧城雨后的第二页")).toBeInTheDocument(),
    );
    expect(searchRequests).toBe(requestsBeforeRefresh + 2);

    vi.spyOn(window, "scrollY", "get").mockReturnValue(880);
    const secondPost = screen
      .getByText("旧城雨后的第二页")
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

    fireEvent.click(screen.getByRole("link", { name: "返回时间流" }));
    await waitFor(() => expect(window.location.pathname).toBe("/search"));
    await waitFor(() =>
      expect(screen.getByText("旧城雨后的第二页")).toBeInTheDocument(),
    );
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(window.location.search).toContain("loaded_through=cursor-one");
    const restoredTrigger = screen
      .getByText("旧城雨后的第二页")
      .closest<HTMLElement>("[data-post-id]")
      ?.querySelector<HTMLAnchorElement>('[data-focus-key$=":open-post"]');
    expect(restoredTrigger).toHaveFocus();
    expect(window.scrollTo).toHaveBeenCalledWith({
      top: 880,
      behavior: "auto",
    });
  });

  it("guides empty and failed searches without erasing the query", async () => {
    window.history.replaceState({}, "", "/search?q=a");
    let fail = false;
    install((url) => {
      if (url.startsWith("/api/v1/search?")) {
        if (fail) {
          return json(
            {
              error: {
                code: "INTERNAL",
                message: "failed",
                details: {},
                request_id: "req_search",
              },
            },
            500,
          );
        }
        return json({
          data: { items: [], year_counts: [] },
          meta: { next_cursor: null },
        });
      }
      return json({}, 404);
    });
    const view = render(<App />);
    expect(
      await screen.findByRole("heading", { name: "这段时间里没有匹配" }),
    ).toBeInTheDocument();
    expect(window.location.search).toBe("?q=a");

    view.unmount();
    fail = true;
    render(<App />);
    expect(
      await screen.findByRole("heading", { name: "这次没有找回结果" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("searchbox")).toHaveValue("a");
    expect(window.location.search).toBe("?q=a");
  });

  it("navigates year, month, and day counts and preserves the day URL across history", async () => {
    window.history.replaceState({}, "", "/archive");
    install((url) => {
      if (url === "/api/v1/archive/months") {
        return json({
          data: {
            items: [
              { year: 2026, month: 8, count: 2 },
              { year: 2025, month: 12, count: 1 },
            ],
          },
          meta: {},
        });
      }
      if (url === "/api/v1/archive/month?month=2026-08") {
        return json({
          data: { month: "2026-08", days: [{ day: 10, count: 2 }] },
          meta: {},
        });
      }
      if (url.startsWith("/api/v1/posts?")) {
        return json({
          data: { items: [first] },
          meta: { next_cursor: null },
        });
      }
      return json({}, 404);
    });
    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "2026" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByText("8 月").closest("a")!);
    expect(
      await screen.findByRole("heading", { name: "2026 年 8 月" }),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("link", { name: "2026-08-10，2 条 Moment" }),
    );
    expect(await screen.findByText("旧城雨落在街口")).toBeInTheDocument();
    expect(window.location.search).toContain("year=2026");
    expect(window.location.search).toContain("month=2026-08");
    expect(window.location.search).toContain("from=2026-08-10");
    expect(window.location.search).toContain("to=2026-08-11");

    fireEvent.click(screen.getByRole("link", { name: "清除日期" }));
    expect(
      await screen.findByRole("heading", { name: "2026 年 8 月" }),
    ).toBeInTheDocument();
    act(() => window.history.back());
    await waitFor(() =>
      expect(window.location.search).toContain("from=2026-08-10"),
    );
    expect(await screen.findByText("旧城雨落在街口")).toBeInTheDocument();
  });
});
