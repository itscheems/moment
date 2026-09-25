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

function post(id: string, content: string) {
  return {
    id,
    kind: "moment",
    content_md: content,
    article: null,
    content_html: `<p>${content}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    reposted_at: null,
    visibility: "everyone",
    timeline_item_kind: "original",
    quoted_post: null,
    comment_count: 0,
    media: [],
    link_cards: [],
  };
}

const first = post("01K2ABCDEF0123456789ABCDE0", "旧城雨落在街口");
const second = post("01K2ABCDEF0123456789ABCDE1", "旧城雨后的第二页");

function paragraphText(text: string) {
  return (_content: string, element: Element | null) =>
    element?.tagName === "P" && element.textContent === text;
}

function install(
  handler: (url: string) => Response | Promise<Response>,
  { signedIn = false }: { signedIn?: boolean } = {},
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === "/api/v1/site") return json(site);
    if (url === "/api/v1/owner/status") {
      if (signedIn) {
        return json({
          data: {
            initialized: true,
            password_enabled: false,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00Z",
          },
          meta: { request_id: "req_owner" },
        });
      }
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
    vi.useRealTimers();
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
    // Replying needs an account, so the composer only appears for a signed-in
    // browser; without this the dialog would offer sign-in instead.
    window.sessionStorage.setItem(
      "moment.owner.session.v1",
      JSON.stringify({
        csrf_token: "csrf-reply",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    install(
      (url) => {
        if (!url.startsWith("/api/v1/search?")) return json({}, 404);
        return json({
          data: {
            items: [{ ...first, snippet: "雨" }],
            year_counts: [{ year: 2026, count: 1 }],
          },
          meta: { next_cursor: null, request_id: "req_search" },
        });
      },
      { signedIn: true },
    );
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

  // ---- Archive ----
  //
  // The archive is the timeline with a calendar beside it: the list is on
  // screen from the start, a day is where the list starts rather than what it
  // is filtered to, and the calendar pages on its own without moving the list.
  // Today is pinned so the calendar's ceiling -- the site's current month --
  // does not drift with the clock these tests run under.

  const archiveMonths = {
    data: {
      items: [
        { year: 2026, month: 8, count: 2 },
        { year: 2025, month: 12, count: 1 },
      ],
    },
    meta: { request_id: "req_archive" },
  };
  const august = {
    data: { month: "2026-08", days: [{ day: 10, count: 2 }] },
    meta: { request_id: "req_archive" },
  };
  const december = {
    data: { month: "2025-12", days: [{ day: 31, count: 1 }] },
    meta: { request_id: "req_archive" },
  };
  // 16:20 UTC on the 30th is 00:20 on the 31st in Shanghai: the byline has to
  // say the site's day.
  const lastYear = {
    ...post("01K2ABCDEF0123456789ABCDE2", "去年最后一天的雨"),
    created_at: "2025-12-30T16:20:00.000Z",
    updated_at: "2025-12-30T16:20:00.000Z",
    published_at: "2025-12-30T16:20:00.000Z",
  };

  // Published inside August, reposted on the 20th: the server windows rows by
  // publication, so this comes back for a day before the 20th, dated by its
  // repost. Under no position it files under the 20th; under a position
  // before the 20th it is dropped.
  const repostedLater = {
    ...post("01K2ABCDEF0123456789ABCDE3", "旧帖，后来转发"),
    created_at: "2026-08-01T01:00:00.000Z",
    updated_at: "2026-08-01T01:00:00.000Z",
    published_at: "2026-08-01T01:00:00.000Z",
    reposted_at: "2026-08-20T01:00:00.000Z",
    timeline_item_kind: "repost",
  };

  // The timeline as the server answers it: newest first by appearance, an
  // anchored page reading back from the end of its day, and prev_cursor
  // walking forward from it until nothing is left.
  function timelinePage(
    items: unknown[],
    cursors: { next?: string | null; prev?: string | null } = {},
  ) {
    return json({
      data: { items },
      meta: {
        next_cursor: cursors.next ?? null,
        prev_cursor: cursors.prev ?? null,
      },
    });
  }

  function installArchive() {
    return install((url) => {
      if (url === "/api/v1/archive/months") return json(archiveMonths);
      if (url === "/api/v1/archive/month?month=2026-08") return json(august);
      if (url === "/api/v1/archive/month?month=2025-12") return json(december);
      if (!url.startsWith("/api/v1/posts?")) return json({}, 404);
      const query = new URLSearchParams(url.slice(url.indexOf("?")));
      switch (query.get("cursor")) {
        case "newer-after-0810":
          return timelinePage([repostedLater], {
            next: "older-from-0820",
            prev: "newer-more",
          });
        case "newer-after-1231":
          return timelinePage([repostedLater, first], {
            next: "older-from-0810",
          });
        case "newer-more":
          return timelinePage([]);
        case null:
          break;
        default:
          return json({}, 404);
      }
      switch (query.get("anchor")) {
        case "2026-08-10":
          return timelinePage([first, lastYear], { prev: "newer-after-0810" });
        case "2025-12-31":
          return timelinePage([lastYear], { prev: "newer-after-1231" });
        case null:
          return timelinePage([repostedLater, first, lastYear], {
            prev: "head-newer",
          });
        default:
          return json({}, 404);
      }
    });
  }

  function pinToday() {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-09-18T04:00:00Z"));
  }

  function postRequests(fetcher: ReturnType<typeof install>): string[] {
    return fetcher.mock.calls
      .map(([input]) => String(input))
      .filter((url) => url.startsWith("/api/v1/posts?"));
  }

  it("opens the archive on the list, dated by site days, with the calendar on the newest month that holds posts", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    render(<App />);

    // The rows carry their own dates, in the site's zone -- which lands only
    // once the site's zone is known, so it is what the test waits for.
    expect(
      await screen.findByRole("article", { name: /2025年12月31日 00:20/ }),
    ).toBeInTheDocument();
    expect(screen.getByText("旧城雨落在街口")).toBeInTheDocument();
    // No heading over each day: the byline already says it.
    expect(
      screen.queryByRole("heading", { name: /周[一二三四五六日]/ }),
    ).toBeNull();
    // With no position, a repost is on the list at its repost.
    expect(screen.getByText("旧帖，后来转发")).toBeInTheDocument();
    // No position: the list starts at the newest post, with no window and
    // nothing above it to load.
    expect(postRequests(fetcher)).toEqual(["/api/v1/posts?limit=20"]);
    expect(
      screen.queryByRole("button", { name: "加载更新的 Moment" }),
    ).toBeNull();

    // August, not the empty current month.
    expect(
      screen.getByRole("button", { name: "2026年8月，选择年月" }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("link", { name: "8月10日，2 条 Moment" }),
    ).toHaveAttribute("href", "/archive?day=2026-08-10");
    expect(screen.getByLabelText("8月11日，没有 Moment").tagName).toBe("SPAN");
    expect(screen.getByText("本月 2 条")).toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "回到最新" })).toBeNull();

    // Neighbouring months with nothing in them cost no request.
    const monthRequests = fetcher.mock.calls
      .map(([input]) => String(input))
      .filter((url) => url.startsWith("/api/v1/archive/month?"));
    expect(monthRequests).toEqual(["/api/v1/archive/month?month=2026-08"]);
  });

  it("starts the list at a picked day, reads back past it, and keeps the day across history", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    render(<App />);

    fireEvent.click(
      await screen.findByRole("link", { name: "8月10日，2 条 Moment" }),
    );
    expect(window.location.search).toBe("?day=2026-08-10");
    // The list opens at the end of the day and reads both ways from it: the
    // rows on and before it, then the first page after it.
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/posts?limit=20&anchor=2026-08-10",
      ),
    );
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/posts?limit=20&cursor=newer-after-0810",
      ),
    );
    expect(
      await screen.findByRole("article", { name: /2025年12月31日 00:20/ }),
    ).toBeInTheDocument();
    expect(screen.getByText("旧城雨落在街口")).toBeInTheDocument();
    // The repost from the 20th stands above the day, where it appeared.
    const rows = screen
      .getAllByRole("article")
      .map((article) => article.textContent ?? "");
    expect(
      rows.findIndex((text) => text.includes("旧帖，后来转发")),
    ).toBeLessThan(rows.findIndex((text) => text.includes("旧城雨落在街口")));
    // Nothing is windowed by publication in either direction.
    expect(
      postRequests(fetcher).some(
        (url) => url.includes("from=") || url.includes("to="),
      ),
    ).toBe(false);
    expect(
      screen.getByRole("link", { name: "8月10日，2 条 Moment，已选择" }),
    ).toHaveAttribute("aria-current", "date");

    // More after the day loads from the top, the way more before it loads
    // from the bottom, and records its progress the same way.
    fireEvent.click(
      await screen.findByRole("button", { name: "加载更新的 Moment" }),
    );
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/posts?limit=20&cursor=newer-more",
      ),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "加载更新的 Moment" }),
      ).toBeNull(),
    );
    expect(window.location.search).toContain("loaded_newer=newer-more");

    fireEvent.click(screen.getByRole("link", { name: "回到最新" }));
    await waitFor(() => expect(window.location.search).toBe(""));
    expect(screen.queryByRole("link", { name: "回到最新" })).toBeNull();

    // Back replays both directions to where they were.
    act(() => window.history.back());
    await waitFor(() =>
      expect(window.location.search).toContain("day=2026-08-10"),
    );
    expect(
      await screen.findByRole("link", {
        name: "8月10日，2 条 Moment，已选择",
      }),
    ).toBeInTheDocument();
    expect(await screen.findByText("旧城雨落在街口")).toBeInTheDocument();
  });

  it("jumps years and months from the picker, and draws empty months as places rather than controls", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    render(<App />);

    fireEvent.click(
      await screen.findByRole("button", { name: "2026年8月，选择年月" }),
    );
    const years = await screen.findByRole("group", { name: "年份" });
    expect(within(years).getByRole("button", { name: /2026/ })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    expect(
      within(years).getByRole("button", { name: /2025/ }),
    ).toHaveTextContent("1 条");
    expect(
      screen.getByRole("link", { name: "2026年8月，2 条 Moment" }),
    ).toHaveAttribute("aria-current", "date");
    expect(screen.getByLabelText("2026年7月，没有 Moment").tagName).toBe(
      "SPAN",
    );

    // A year moves the calendar to its newest month and leaves the picker up.
    fireEvent.click(within(years).getByRole("button", { name: /2025/ }));
    expect(
      await screen.findByRole("button", { name: "2025年12月，选择年月" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("list", { name: "2025 年的月份" }),
    ).toBeInTheDocument();
    expect(window.location.search).toBe("");

    // A month moves the list and closes the picker.
    fireEvent.click(
      screen.getByRole("link", { name: "2025年12月，1 条 Moment" }),
    );
    expect(window.location.search).toBe("?month=2025-12");
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/posts?limit=20&anchor=2025-12-31",
      ),
    );
    await waitFor(() =>
      expect(screen.queryByRole("group", { name: "年份" })).toBeNull(),
    );
    expect(
      await screen.findByRole("link", { name: "12月31日，1 条 Moment" }),
    ).toBeInTheDocument();
  });

  it("pages the calendar within the archive's span without moving the list", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    render(<App />);
    await screen.findByRole("link", { name: "8月10日，2 条 Moment" });
    const requestsBefore = fetcher.mock.calls.length;

    // Forward to the site's current month, September, and no further.
    fireEvent.click(screen.getByRole("button", { name: "下个月" }));
    expect(
      screen.getByRole("button", { name: "2026年9月，选择年月" }),
    ).toBeInTheDocument();
    expect(screen.getByText("这个月没有 Moment")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "下个月" })).toBeDisabled();

    // Back through the empty months to the oldest one, and no further.
    const previous = screen.getByRole("button", { name: "上个月" });
    for (let step = 0; step < 9; step += 1) fireEvent.click(previous);
    expect(
      screen.getByRole("button", { name: "2025年12月，选择年月" }),
    ).toBeInTheDocument();
    expect(previous).toBeDisabled();
    expect(
      await screen.findByRole("link", { name: "12月31日，1 条 Moment" }),
    ).toBeInTheDocument();

    // The list never moved, and empty months cost nothing.
    expect(window.location.search).toBe("");
    const requests = fetcher.mock.calls
      .slice(requestsBefore)
      .map(([input]) => String(input));
    expect(requests).toEqual(["/api/v1/archive/month?month=2025-12"]);
  });

  it("opens the links the archive used to write on their day, and refuses broken ones", async () => {
    pinToday();
    window.history.replaceState(
      {},
      "",
      "/archive?year=2026&month=2026-08&from=2026-08-10&to=2026-08-11",
    );
    const fetcher = installArchive();
    const { unmount } = render(<App />);
    expect(
      await screen.findByRole("link", {
        name: "8月10日，2 条 Moment，已选择",
      }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/posts?limit=20&anchor=2026-08-10",
      ),
    );

    unmount();
    window.history.replaceState({}, "", "/archive?day=2026-02-30");
    render(<App />);
    expect(await screen.findByRole("alert")).toHaveTextContent("归档链接无效");
    expect(screen.getByRole("link", { name: "回到归档" })).toHaveAttribute(
      "href",
      "/archive",
    );
    expect(screen.queryByRole("button", { name: /选择年月/ })).toBeNull();
  });

  it("stands the calendar in the right rail where the shell draws one", async () => {
    pinToday();
    vi.stubGlobal(
      "matchMedia",
      vi.fn((query: string) => ({
        matches: query === "(min-width: 1280px)",
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    );
    window.history.replaceState({}, "", "/archive");
    installArchive();
    render(<App />);

    const rail = screen.getByRole("complementary", { name: "记忆导航" });
    expect(
      await within(rail).findByRole("button", { name: "2026年8月，选择年月" }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByRole("main")).queryByRole("button", {
        name: "2026年8月，选择年月",
      }),
    ).toBeNull();
  });
});
