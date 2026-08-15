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
    cover_media_id: null,
    pinned_post_id: null,
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
    reposted_at: null,
    comment_count: 0,
    media: [],
  };
}

const first = post("01K2ABCDEF0123456789ABCDE0", "旧城雨落在街口");
const second = post("01K2ABCDEF0123456789ABCDE1", "旧城雨后的第二页");

function paragraphText(text: string) {
  return (_content: string, element: Element | null) =>
    element?.tagName === "P" && element.textContent === text;
}

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
    const input = await screen.findByPlaceholderText("搜索 Moment");
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(input).toHaveFocus();
    fireEvent.change(input, { target: { value: "a" } });
    fireEvent.keyDown(input, { key: "/" });
    expect(input).toHaveValue("a");
    expect(window.location.pathname).toBe("/search");
  });

  it("keeps clear and submit inside one pill and clears results, URL, and focus state", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      return json({
        data: {
          items: [{ ...first, snippet: "雨" }],
          year_counts: [{ year: 2026, count: 1 }],
        },
        meta: { next_cursor: null, request_id: "req_search" },
      });
    });
    render(<App />);

    await screen.findByText(paragraphText("旧城雨落在街口"));
    const input = screen.getByRole("searchbox");
    const control = input.closest("[data-search-control]");
    expect(control).toContainElement(
      screen.getByRole("button", { name: "清除搜索内容" }),
    );
    expect(control).toContainElement(
      screen.getByRole("button", { name: "搜索" }),
    );

    fireEvent.click(screen.getByRole("button", { name: "清除搜索内容" }));
    await waitFor(() => expect(window.location.search).toBe(""));
    const cleared = await screen.findByRole("searchbox");
    expect(cleared).toHaveValue("");
    await waitFor(() => expect(cleared).toHaveFocus());
    expect(
      screen.queryByText(paragraphText("旧城雨落在街口")),
    ).not.toBeInTheDocument();
  });

  it("opens the reply dialog from a search result without leaving search", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      return json({
        data: {
          items: [{ ...first, snippet: "雨" }],
          year_counts: [{ year: 2026, count: 1 }],
        },
        meta: { next_cursor: null, request_id: "req_search" },
      });
    });
    render(<App />);

    await screen.findByText(paragraphText("旧城雨落在街口"));
    const trigger = screen.getByRole("button", { name: "回复这条 Moment" });
    const initialUrl = window.location.href;
    fireEvent.click(trigger);

    expect(
      screen.getByRole("dialog", { name: "回复这条 Moment" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByLabelText("回复内容")).toHaveFocus(),
    );
    expect(window.location.href).toBe(initialUrl);

    fireEvent.click(screen.getByRole("button", { name: "关闭回复窗口" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "回复这条 Moment" }),
      ).not.toBeInTheDocument(),
    );
    expect(trigger).toHaveFocus();
  });

  it("uses Escape to clear, close filters, then release focus", async () => {
    window.history.replaceState({}, "", "/search");
    install(() => json({}, 404));
    render(<App />);

    const input = await screen.findByRole("searchbox");
    fireEvent.change(input, { target: { value: "暂存搜索" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(input).toHaveValue("");
    expect(input).toHaveFocus();

    fireEvent.click(screen.getByRole("button", { name: "日期" }));
    expect(document.querySelector("#search-date-filters")).not.toHaveAttribute(
      "hidden",
    );
    input.focus();
    fireEvent.keyDown(input, { key: "Escape" });
    expect(document.querySelector("#search-date-filters")).toHaveAttribute(
      "hidden",
    );
    expect(input).toHaveFocus();

    fireEvent.keyDown(input, { key: "Escape" });
    expect(input).not.toHaveFocus();
  });

  it("treats the selected end date as an inclusive natural day", async () => {
    window.history.replaceState({}, "", "/search");
    let requestedSearch = "";
    install((url) => {
      if (url.startsWith("/api/v1/search?")) {
        requestedSearch = url;
        return json({
          data: { items: [], year_counts: [] },
          meta: { next_cursor: null, request_id: "req_search" },
        });
      }
      return json({}, 404);
    });
    render(<App />);

    const now = new Date();
    const year = now.getFullYear();
    const month = now.getMonth() + 1;
    const day = now.getDate();
    const selected = `${year}-${String(month).padStart(2, "0")}-${String(day).padStart(2, "0")}`;
    const next = new Date(Date.UTC(year, month - 1, day + 1));
    const exclusiveEnd = [
      next.getUTCFullYear(),
      String(next.getUTCMonth() + 1).padStart(2, "0"),
      String(next.getUTCDate()).padStart(2, "0"),
    ].join("-");
    const dateLabel = `${year}年${month}月${day}日`;

    fireEvent.change(await screen.findByRole("searchbox"), {
      target: { value: "雨" },
    });
    fireEvent.click(screen.getByRole("button", { name: "日期" }));
    fireEvent.click(screen.getByRole("button", { name: "开始日期：选择日期" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "开始日期" })).getByRole(
        "button",
        { name: dateLabel },
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "结束日期：选择日期" }));
    fireEvent.click(
      within(screen.getByRole("dialog", { name: "结束日期" })).getByRole(
        "button",
        { name: dateLabel },
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "应用" }));

    await waitFor(() => expect(requestedSearch).not.toBe(""));
    expect(window.location.search).toContain(`from=${selected}`);
    expect(window.location.search).toContain(`to=${exclusiveEnd}`);
    expect(requestedSearch).toContain(`from=${selected}`);
    expect(requestedSearch).toContain(`to=${exclusiveEnd}`);
    expect(
      screen.getByText(`${dateLabel} 至 ${dateLabel}`),
    ).toBeInTheDocument();
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
          meta: {
            next_cursor: continued ? null : "cursor-one",
            request_id: "req_search",
          },
        });
      }
      if (url === `/api/v1/posts/${second.id}`) {
        return json({ data: second, meta: { request_id: "req_post" } });
      }
      return json({}, 404);
    });
    const view = render(<App />);

    await waitFor(() =>
      expect(
        screen.getByText(paragraphText("旧城雨落在街口")),
      ).toBeInTheDocument(),
    );
    expect(screen.getAllByText(paragraphText("旧城雨落在街口"))).toHaveLength(
      1,
    );
    expect(
      screen.queryByText('<img src="x" onerror="alert(1)">', { exact: false }),
    ).not.toBeInTheDocument();
    expect(view.container.querySelector('img[src="x"]')).toBeNull();
    expect(
      screen.getByText("旧城雨", { selector: "mark" }),
    ).toBeInTheDocument();
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
    expect(
      await screen.findByText(paragraphText("旧城雨后的第二页")),
    ).toBeInTheDocument();
    expect(window.location.search).toContain("loaded_through=cursor-one");
    const requestsBeforeRefresh = searchRequests;

    view.unmount();
    render(<App />);
    await waitFor(() =>
      expect(
        screen.getByText(paragraphText("旧城雨后的第二页")),
      ).toBeInTheDocument(),
    );
    expect(searchRequests).toBe(requestsBeforeRefresh + 2);

    vi.spyOn(window, "scrollY", "get").mockReturnValue(880);
    const secondPost = screen
      .getByText(paragraphText("旧城雨后的第二页"))
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

    fireEvent.click(screen.getByRole("link", { name: "返回时间流" }));
    await waitFor(() => expect(window.location.pathname).toBe("/search"));
    await waitFor(() =>
      expect(
        screen.getByText(paragraphText("旧城雨后的第二页")),
      ).toBeInTheDocument(),
    );
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(window.location.search).toContain("loaded_through=cursor-one");
    const restoredTrigger = screen
      .getByText(paragraphText("旧城雨后的第二页"))
      .closest<HTMLElement>("[data-post-id]")
      ?.querySelector<HTMLAnchorElement>('[data-focus-key$=":surface-link"]');
    expect(restoredTrigger).toHaveFocus();
    expect(window.scrollTo).toHaveBeenCalledWith({
      top: 880,
      behavior: "auto",
    });
  });

  it("automatically continues search once while keeping the searchbox focused", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
    let intersect: IntersectionObserverCallback | null = null;
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        constructor(callback: IntersectionObserverCallback) {
          intersect = callback;
        }
        observe = vi.fn();
        disconnect = vi.fn();
        unobserve = vi.fn();
        takeRecords = vi.fn(() => []);
        root = null;
        rootMargin = "800px 0px";
        thresholds = [0];
      },
    );
    let requests = 0;
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      requests += 1;
      const continued = url.includes("cursor=cursor-one");
      return json({
        data: {
          items: [{ ...(continued ? second : first), snippet: "雨" }],
          year_counts: [{ year: 2026, count: 2 }],
        },
        meta: {
          next_cursor: continued ? null : "cursor-one",
          request_id: "req_search",
        },
      });
    });
    render(<App />);

    await screen.findByText(paragraphText("旧城雨落在街口"));
    const searchbox = screen.getByRole("searchbox");
    searchbox.focus();
    await waitFor(() => expect(intersect).toBeTypeOf("function"));
    const callback = intersect as unknown as IntersectionObserverCallback;
    act(() => {
      callback(
        [{ isIntersecting: true } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
      callback(
        [{ isIntersecting: true } as IntersectionObserverEntry],
        {} as IntersectionObserver,
      );
    });

    expect(
      await screen.findByText(paragraphText("旧城雨后的第二页")),
    ).toBeInTheDocument();
    expect(requests).toBe(2);
    expect(searchbox).toHaveFocus();
    expect(window.location.search).toContain("loaded_through=cursor-one");
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
          meta: { next_cursor: null, request_id: "req_search" },
        });
      }
      return json({}, 404);
    });
    const view = render(<App />);
    expect(
      await screen.findByRole("heading", { name: "没有找到 Moment" }),
    ).toBeInTheDocument();
    expect(window.location.search).toBe("?q=a");

    view.unmount();
    fail = true;
    render(<App />);
    expect(
      await screen.findByRole("heading", { name: "搜索失败" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("searchbox")).toHaveValue("a");
    expect(window.location.search).toBe("?q=a");
  });

  it("clears a stale continued search cursor while preserving deliberate filters", async () => {
    window.history.replaceState(
      {},
      "",
      "/search?q=%E9%9B%A8&from=2026-01-01&to=2027-01-01",
    );
    let firstPageRequests = 0;
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      if (url.includes("cursor=stale")) {
        return json(
          {
            error: {
              code: "INVALID_ARGUMENT",
              message: "cursor is stale",
              details: { field: "cursor" },
              request_id: "req_stale",
            },
          },
          400,
        );
      }
      firstPageRequests += 1;
      return json({
        data: {
          items: [{ ...first, snippet: "雨" }],
          year_counts: [{ year: 2026, count: 1 }],
        },
        meta: { next_cursor: "stale", request_id: "req_search" },
      });
    });
    render(<App />);

    await screen.findByText(paragraphText("旧城雨落在街口"));
    fireEvent.click(screen.getByRole("button", { name: "加载更早的结果" }));
    const reset = await screen.findByRole("button", {
      name: "从第一页重新加载",
    });
    fireEvent.click(reset);

    await waitFor(() => expect(firstPageRequests).toBe(2));
    expect(window.location.search).toContain("q=%E9%9B%A8");
    expect(window.location.search).toContain("from=2026-01-01");
    expect(window.location.search).toContain("to=2027-01-01");
    expect(window.location.search).not.toContain("loaded_through");
    expect(screen.getByRole("searchbox")).toHaveValue("雨");
  });

  it("recovers an unreachable saved search position from the first page", async () => {
    window.history.replaceState(
      {},
      "",
      "/search?q=%E9%9B%A8&from=2026-01-01&to=2027-01-01&loaded_through=missing",
    );
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      return json({
        data: {
          items: [{ ...first, snippet: "雨" }],
          year_counts: [{ year: 2026, count: 1 }],
        },
        meta: { next_cursor: null, request_id: "req_search" },
      });
    });
    render(<App />);

    expect(
      await screen.findByRole("heading", { name: "搜索失败" }),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "从第一页重新加载" }));
    await waitFor(() =>
      expect(
        screen.getByText(paragraphText("旧城雨落在街口")),
      ).toBeInTheDocument(),
    );
    expect(window.location.search).not.toContain("loaded_through");
    expect(window.location.search).toContain("from=2026-01-01");
    expect(window.location.search).toContain("to=2027-01-01");
  });

  it.each([
    {
      name: "self-loop",
      items: [{ ...second, snippet: "雨" }],
      nextCursor: "cursor-one",
    },
    { name: "empty drift", items: [], nextCursor: "cursor-two" },
  ])(
    "rejects a continued search cursor $name",
    async ({ items, nextCursor }) => {
      window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
      install((url) => {
        if (!url.startsWith("/api/v1/search?")) return json({}, 404);
        const continued = url.includes("cursor=cursor-one");
        return json({
          data: {
            items: continued ? items : [{ ...first, snippet: "雨" }],
            year_counts: [{ year: 2026, count: 2 }],
          },
          meta: {
            next_cursor: continued ? nextCursor : "cursor-one",
            request_id: "req_search",
          },
        });
      });
      render(<App />);
      await screen.findByText(paragraphText("旧城雨落在街口"));
      fireEvent.click(screen.getByRole("button", { name: "加载更早的结果" }));

      expect(
        await screen.findByRole("button", { name: "从第一页重新加载" }),
      ).toBeInTheDocument();
      expect(
        screen.queryByText(paragraphText("旧城雨后的第二页")),
      ).not.toBeInTheDocument();
      expect(window.location.search).not.toContain("loaded_through");
    },
  );

  it("tracks continued search cursors and rejects a later cycle", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
    const third = post("01K2ABCDEF0123456789ABCDE2", "不应追加的循环页");
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      const cursor = new URL(url, "http://moment.invalid").searchParams.get(
        "cursor",
      );
      const item = cursor === "cursor-one" ? second : cursor ? third : first;
      return json({
        data: {
          items: [{ ...item, snippet: "雨" }],
          year_counts: [{ year: 2026, count: 3 }],
        },
        meta: {
          next_cursor:
            cursor === "cursor-one"
              ? "cursor-two"
              : cursor === "cursor-two"
                ? "cursor-one"
                : "cursor-one",
          request_id: "req_search",
        },
      });
    });
    render(<App />);
    await screen.findByText(paragraphText("旧城雨落在街口"));
    fireEvent.click(screen.getByRole("button", { name: "加载更早的结果" }));
    await screen.findByText(paragraphText("旧城雨后的第二页"));
    fireEvent.click(screen.getByRole("button", { name: "加载更早的结果" }));

    expect(
      await screen.findByRole("button", { name: "从第一页重新加载" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(paragraphText("不应追加的循环页")),
    ).not.toBeInTheDocument();
    expect(window.location.search).toContain("loaded_through=cursor-one");
  });

  it("fails closed without rewriting malformed URL bytes", async () => {
    install(() => json({}, 404));
    window.history.replaceState({}, "", "/search?q=%E0%A4%A");
    const view = render(<App />);
    expect(
      await screen.findByRole("heading", { name: "搜索链接无效" }),
    ).toBeInTheDocument();
    expect(window.location.search).toBe("?q=%E0%A4%A");

    view.unmount();
    window.history.replaceState({}, "", "/archive?month=%ED%A0%80");
    render(<App />);
    expect(
      await screen.findByRole("heading", { name: "归档链接无效" }),
    ).toBeInTheDocument();
    expect(window.location.search).toBe("?month=%ED%A0%80");
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
          meta: { request_id: "req_archive" },
        });
      }
      if (url === "/api/v1/archive/month?month=2026-08") {
        return json({
          data: { month: "2026-08", days: [{ day: 10, count: 2 }] },
          meta: { request_id: "req_archive" },
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
