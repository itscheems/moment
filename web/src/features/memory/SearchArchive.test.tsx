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

import i18n, { LANGUAGE_STORAGE_KEY } from "../../i18n";

import { App } from "../../app/App";
import { makeAccountSession } from "../../test/accountFixtures";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const site = {
  data: {
    profile_user_id: "00000000000000000000000000000000",
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
    if (url.startsWith("/api/v1/users/search?"))
      return json({ data: { items: [], next_offset: null }, meta: {} });
    if (url === "/api/v1/site") return json(site);
    if (url === "/api/v1/account/session") {
      if (signedIn) {
        return json({
          data: makeAccountSession(),
          meta: { request_id: "req_account_session" },
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
    const input = await screen.findByPlaceholderText("搜索");
    await new Promise<void>((resolve) =>
      window.requestAnimationFrame(() => resolve()),
    );
    expect(input).toHaveFocus();
    fireEvent.change(input, { target: { value: "a" } });
    fireEvent.keyDown(input, { key: "/" });
    expect(input).toHaveValue("a");
    expect(window.location.pathname).toBe("/search");
  });

  it.each(["/search", "/search?f=user", "/search?q=river"])(
    "keeps the input unfocused on entry and tab changes at %s",
    async (path) => {
      window.history.replaceState({}, "", path);
      install(() =>
        json({
          data: { items: [], year_counts: [], next_offset: null },
          meta: { next_cursor: null },
        }),
      );
      render(<App />);
      const input = await screen.findByRole("combobox");
      expect(input).not.toHaveFocus();
      expect(input).toHaveAttribute("aria-expanded", "false");
      for (const name of ["用户", "最新", "用户", "最新"]) {
        fireEvent.click(screen.getByRole("link", { name }));
        const nextInput = await screen.findByRole("combobox");
        expect(nextInput).not.toHaveFocus();
        expect(nextInput).toHaveAttribute("aria-expanded", "false");
      }
    },
  );

  it.each([
    "/search",
    "/search?f=user",
    "/account/notifications",
    "/account/notifications/mentions",
  ])("shows the shared recommendations in the rail at %s", async (path) => {
    window.history.replaceState({}, "", path);
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 988px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    install(
      (url) => {
        if (url.startsWith("/api/v1/users/")) {
          const username = url.split("/").at(-1)!;
          return json({
            data: {
              user_id:
                username === "itscheems" ? "2".repeat(32) : "3".repeat(32),
              username,
              display_name: "Recommended account",
              bio: "",
              avatar_media_id: null,
              cover_media_id: null,
              profile_url: `/${username}`,
            },
            meta: {},
          });
        }
        return json({}, 404);
      },
      { signedIn: path.startsWith("/account/") },
    );
    render(<App />);
    const rail = await screen.findByRole("complementary", { name: "记忆导航" });
    const card = await within(rail).findByRole("region", { name: "推荐关注" });
    expect(within(card).getAllByRole("listitem")).toHaveLength(2);
    expect(within(rail).queryByRole("combobox") !== null).toBe(
      path.startsWith("/account/"),
    );
  });

  it("clears the draft while retaining committed results and opens recent searches", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
    install(() =>
      json({
        data: {
          items: [{ ...first, snippet: "雨" }],
          year_counts: [{ year: 2026, count: 1 }],
        },
        meta: { next_cursor: null, request_id: "req_search" },
      }),
    );
    render(<App />);
    await screen.findByText(paragraphText("旧城雨落在街口"));
    const input = screen.getByRole("combobox");
    expect(input.closest("[data-search-control]")).toContainElement(
      screen.getByRole("button", { name: "清除搜索内容" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "清除搜索内容" }));
    expect(input).toHaveValue("");
    expect(window.location.search).toBe("?q=%E9%9B%A8");
    fireEvent.keyDown(input, { key: "Escape" });
    expect(
      screen.getByText(paragraphText("旧城雨落在街口")),
    ).toBeInTheDocument();
  });

  it.each([
    "/",
    `/m/${first.id}`,
    `/m/${first.id}/comments/01K2ABCDEF0123456789ABCDE3?view=thread`,
  ])("returns to %s after repeated search tab changes", async (source) => {
    window.localStorage.setItem(LANGUAGE_STORAGE_KEY, "en");
    await i18n.changeLanguage("en");
    window.history.replaceState({}, "", source);
    window.history.pushState({ momentInternal: true }, "", "/search?q=river");
    const historyLength = window.history.length;
    install(() =>
      json({
        data: { items: [], year_counts: [] },
        meta: { next_cursor: null, request_id: "req_search" },
      }),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "No Moments found" });
    for (let repeat = 0; repeat < 3; repeat += 1) {
      fireEvent.click(screen.getByRole("link", { name: "People" }));
      await screen.findByRole("heading", { name: "No people found" });
      fireEvent.click(screen.getByRole("link", { name: "Latest" }));
      await screen.findByRole("heading", { name: "No Moments found" });
    }
    expect(window.history.length).toBe(historyLength);
    fireEvent.click(screen.getByRole("button", { name: "Back to timeline" }));
    await waitFor(() =>
      expect(window.location.pathname + window.location.search).toBe(source),
    );
    act(() => window.history.forward());
    await screen.findByRole("heading", { name: "No Moments found" });
    expect(window.location.pathname + window.location.search).toBe(
      "/search?q=river",
    );
  });

  it("shows a real search hit that carries nullable post fields", async () => {
    window.history.replaceState({}, "", "/search?q=smoke");
    const hit = post("01K2ABCDEF0123456789ABCDE0", "registration smoke");
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      return json({
        data: {
          items: [
            {
              ...hit,
              edited_at: null,
              map_card: null,
              location: null,
              snippet: "smoke",
            },
          ],
          year_counts: [{ year: 2026, count: 1 }],
        },
        meta: { next_cursor: null, request_id: "req_search" },
      });
    });
    render(<App />);

    expect(
      await screen.findByText(paragraphText("registration smoke")),
    ).toBeInTheDocument();
    expect(screen.queryByText("正在重新获取搜索结果…")).toBeNull();
  });

  it("opens the reply dialog from a search result without leaving search", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
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

    await screen.findByLabelText("Writer 的账户菜单");
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

  it("uses Escape to dismiss suggestions without discarding the draft", async () => {
    window.history.replaceState({}, "", "/search");
    install(() => json({}, 404));
    render(<App />);
    const input = await screen.findByRole("combobox");
    act(() => input.focus());
    fireEvent.change(input, { target: { value: "暂存搜索" } });
    fireEvent.keyDown(input, { key: "Escape" });
    expect(input).toHaveValue("暂存搜索");
    expect(input).toHaveFocus();
    expect(input).toHaveAttribute("aria-expanded", "false");
  });

  it("uses latest and people navigation with no date or year controls", async () => {
    window.history.replaceState({}, "", "/search?q=river");
    install(() =>
      json({
        data: { items: [], year_counts: [] },
        meta: { next_cursor: null, request_id: "req_search" },
      }),
    );
    render(<App />);
    await screen.findByRole("heading", { name: "没有找到 Moment" });
    expect(screen.queryByRole("button", { name: "日期" })).toBeNull();
    expect(
      screen.queryByRole("navigation", { name: "按年份筛选搜索结果" }),
    ).toBeNull();
    expect(screen.queryByText("按文字或日期查找 Moment")).toBeNull();
    fireEvent.click(screen.getByRole("link", { name: "用户" }));
    await waitFor(() => expect(window.location.search).toBe("?q=river&f=user"));
    expect(
      await screen.findByRole("heading", { name: "没有找到用户" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "用户" })).toHaveAttribute(
      "aria-current",
      "page",
    );
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
        expect(
          new URL(url, "http://moment.invalid").searchParams.get("sort"),
        ).toBe("latest");
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
    expect(screen.queryByRole("region", { name: "搜索概览" })).toBeNull();

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
    const searchbox = screen.getByRole("combobox");
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
      await screen.findByText("正在重新获取搜索结果…"),
    ).toBeInTheDocument();
    expect(screen.getByRole("combobox")).toHaveValue("a");
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
    await screen.findByText("正在重新获取后续结果…");
    expect(
      screen.queryByRole("button", { name: "从第一页重新加载" }),
    ).toBeNull();

    await waitFor(() => expect(firstPageRequests).toBe(2));
    expect(window.location.search).toContain("q=%E9%9B%A8");
    expect(window.location.search).toContain("from=2026-01-01");
    expect(window.location.search).toContain("to=2027-01-01");
    expect(window.location.search).not.toContain("loaded_through");
    expect(screen.getByRole("combobox")).toHaveValue("雨");
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

    await waitFor(() =>
      expect(
        screen.getByText(paragraphText("旧城雨落在街口")),
      ).toBeInTheDocument(),
    );
    expect(window.location.search).not.toContain("loaded_through");
    expect(window.location.search).toContain("from=2026-01-01");
    expect(window.location.search).toContain("to=2027-01-01");
  });

  it("accepts changing search totals while merging valid cursor pages", async () => {
    window.history.replaceState({}, "", "/search?q=%E9%9B%A8");
    install((url) => {
      if (!url.startsWith("/api/v1/search?")) return json({}, 404);
      const continued = url.includes("cursor=next");
      return json({
        data: {
          items: [{ ...(continued ? second : first), snippet: "雨" }],
          year_counts: [{ year: 2026, count: continued ? 3 : 2 }],
        },
        meta: {
          next_cursor: continued ? null : "next",
          request_id: "changed-total",
        },
      });
    });
    render(<App />);
    await screen.findByText(paragraphText("旧城雨落在街口"));
    fireEvent.click(screen.getByRole("button", { name: "加载更早的结果" }));
    expect(
      await screen.findByText(paragraphText("旧城雨后的第二页")),
    ).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "搜索概览" })).toBeNull();
    expect(screen.queryByText("搜索失败")).toBeNull();
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
        await screen.findByText("正在重新获取后续结果…"),
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
      await screen.findByText("正在重新获取后续结果…"),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(paragraphText("不应追加的循环页")),
    ).not.toBeInTheDocument();
    expect(window.location.search).toContain("loaded_through=cursor-one");
  });

  it("fails closed without rewriting malformed URL bytes", async () => {
    install(() => json({}, 404), { signedIn: true });
    window.history.replaceState({}, "", "/search?q=%E0%A4%A");
    const view = render(<App />);
    await screen.findByLabelText("Writer 的账户菜单");
    expect(
      await screen.findByRole("heading", { name: "搜索链接无效" }),
    ).toBeInTheDocument();
    expect(window.location.search).toBe("?q=%E0%A4%A");

    view.unmount();
    window.history.replaceState({}, "", "/archive?month=%ED%A0%80");
    await renderArchive();
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

  function installArchive(headItems?: unknown[]) {
    const handler = (url: string) => {
      if (url === "/api/v1/archive/months") return json(archiveMonths);
      if (url === "/api/v1/archive/month?month=2026-08") return json(august);
      if (url === "/api/v1/archive/month?month=2025-12") return json(december);
      if (!url.startsWith("/api/v1/archive/posts?")) return json({}, 404);
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
          return timelinePage([first, lastYear], {
            prev: "newer-after-0810",
          });
        case "2025-12-31":
          return timelinePage([lastYear], { prev: "newer-after-1231" });
        case null:
          return timelinePage(headItems ?? [repostedLater, first, lastYear], {
            prev: "head-newer",
          });
        default:
          return json({}, 404);
      }
    };
    return install(handler, { signedIn: true });
  }

  function pinToday() {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(new Date("2026-09-18T04:00:00Z"));
  }

  function postRequests(fetcher: ReturnType<typeof install>): string[] {
    return fetcher.mock.calls
      .map(([input]) => String(input))
      .filter((url) => url.startsWith("/api/v1/archive/posts?"));
  }

  async function renderArchive() {
    const view = render(<App />);
    await screen.findByLabelText("Writer 的账户菜单");
    return view;
  }

  async function findCalendarDay(
    name: string,
    waitOptions?: { timeout: number },
  ) {
    const calendar = await screen.findByLabelText("归档日历");
    return within(calendar).findByRole("link", { name }, waitOptions);
  }

  it("redirects a guest archive visit to sign-in with its date preserved", async () => {
    window.history.replaceState({}, "", "/archive?day=2026-08-10");
    install(() => json({}, 404));
    render(<App />);

    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    expect(new URLSearchParams(window.location.search).get("return")).toBe(
      "/archive?day=2026-08-10",
    );
    expect(screen.queryByLabelText("归档日历")).not.toBeInTheDocument();
  });

  it("opens the archive with a confirmed account session", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    render(<App />);

    expect(
      await screen.findByLabelText("Writer 的账户菜单"),
    ).toBeInTheDocument();
    expect(await screen.findByLabelText("归档日历")).toBeInTheDocument();
    expect(fetcher.mock.calls.map(([url]) => String(url))).toContain(
      "/api/v1/account/session",
    );
    expect(fetcher.mock.calls.map(([url]) => String(url))).not.toContain(
      "/api/v1/status",
    );
  });

  it("loads the account archive once after session restoration finishes", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    const fetch = fetcher.getMockImplementation()!;
    let release: (() => void) | undefined;
    const pendingSession = new Promise<void>((resolve) => {
      release = resolve;
    });
    fetcher.mockImplementation(async (input) => {
      if (String(input) === "/api/v1/account/session") await pendingSession;
      return fetch(input);
    });
    render(<App />);

    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).startsWith("/api/v1/archive/"),
      ),
    ).toBe(false);
    await act(async () => release?.());
    expect(await findCalendarDay("8月10日，2 条 Moment")).toBeInTheDocument();
    expect(postRequests(fetcher)).toEqual(["/api/v1/archive/posts?limit=20"]);
    expect(
      fetcher.mock.calls.filter(
        ([url]) => String(url) === "/api/v1/archive/months",
      ),
    ).toHaveLength(1);
  });

  it("waits for an unavailable account session to recover before loading archive data", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    const fetch = fetcher.getMockImplementation()!;
    let sessionChecks = 0;
    fetcher.mockImplementation(async (input) => {
      if (String(input) === "/api/v1/account/session" && ++sessionChecks === 1)
        return json({}, 503);
      return fetch(input);
    });
    render(<App />);

    const retry = await screen.findByRole("button", { name: "重试检查" });
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).startsWith("/api/v1/archive/"),
      ),
    ).toBe(false);
    fireEvent.click(retry);
    expect(await findCalendarDay("8月10日，2 条 Moment")).toBeInTheDocument();
    expect(postRequests(fetcher)).toEqual(["/api/v1/archive/posts?limit=20"]);
    expect(window.location.pathname).toBe("/archive");
  });

  it("recovers archive month and day counts while preserving the visible list", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    let months = 0;
    let days = 0;
    install(
      (url) => {
        if (url === "/api/v1/archive/months") {
          months += 1;
          return months === 1 ? json({}, 503) : json(archiveMonths);
        }
        if (url === "/api/v1/archive/month?month=2026-08") {
          days += 1;
          return days === 1 ? json({}, 503) : json(august);
        }
        if (url.startsWith("/api/v1/archive/posts?"))
          return timelinePage([first]);
        return json({}, 404);
      },
      { signedIn: true },
    );
    await renderArchive();
    expect(await screen.findByText("旧城雨落在街口")).toBeInTheDocument();
    expect(
      await findCalendarDay("8月10日，2 条 Moment", { timeout: 2500 }),
    ).toHaveAttribute("href", "/archive?day=2026-08-10");
    expect(screen.getByText("旧城雨落在街口")).toBeInTheDocument();
    expect(months).toBe(2);
    expect(days).toBe(2);
    expect(window.location.pathname).toBe("/archive");
  });

  it("opens the archive on the list, dated by site days, with the calendar on the newest month that holds posts", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    await renderArchive();

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
    expect(postRequests(fetcher)).toEqual(["/api/v1/archive/posts?limit=20"]);
    expect(
      screen.queryByRole("button", { name: "加载更新的 Moment" }),
    ).toBeNull();

    // August, not the empty current month.
    expect(
      screen.getByRole("button", { name: "2026年8月，选择年月" }),
    ).toBeInTheDocument();
    expect(await findCalendarDay("8月10日，2 条 Moment")).toHaveAttribute(
      "href",
      "/archive?day=2026-08-10",
    );
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
    const fetcher = installArchive([repostedLater]);
    await renderArchive();

    fireEvent.click(await findCalendarDay("8月10日，2 条 Moment"));
    expect(window.location.search).toBe("?day=2026-08-10");
    // The list opens at the end of the day and reads both ways from it: the
    // rows on and before it, then the first page after it.
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/archive/posts?limit=20&anchor=2026-08-10",
      ),
    );
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/archive/posts?limit=20&cursor=newer-after-0810",
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
    const newerButton = await screen.findByRole("button", {
      name: "加载更新的 Moment",
    });
    await waitFor(() => expect(newerButton).toBeEnabled());
    fireEvent.click(newerButton);
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/archive/posts?limit=20&cursor=newer-more",
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
      await findCalendarDay("8月10日，2 条 Moment，已选择"),
    ).toBeInTheDocument();
    expect(await screen.findByText("旧城雨落在街口")).toBeInTheDocument();
  });

  it("keeps the selected day's rows and requests when the same date is clicked again", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive?day=2026-08-10");
    const fetcher = installArchive();
    await renderArchive();

    const selectedDay = await findCalendarDay("8月10日，2 条 Moment，已选择");
    const landingRow = (await screen.findByText("旧城雨落在街口")).closest(
      "article",
    )!;
    await waitFor(() =>
      expect(landingRow).toHaveAttribute("data-archive-target"),
    );
    expect(
      screen.getByText("旧帖，后来转发").closest("article"),
    ).not.toHaveAttribute("data-archive-target");

    const requests = postRequests(fetcher);
    fireEvent.click(selectedDay);
    expect(landingRow).toBeInTheDocument();
    expect(screen.getByText("旧城雨落在街口").closest("article")).toBe(
      landingRow,
    );
    expect(postRequests(fetcher)).toEqual(requests);
    expect(window.location.search).toBe("?day=2026-08-10");
  });

  it("slides to an already loaded day without replacing the list or fetching it again", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    await renderArchive();
    const day = await findCalendarDay("8月10日，2 条 Moment");
    const row = (await screen.findByText("旧城雨落在街口")).closest("article");
    const requests = postRequests(fetcher);
    fireEvent.click(day);
    await waitFor(() =>
      expect(window.location.search).toContain("day=2026-08-10"),
    );
    expect(screen.getByText("旧城雨落在街口").closest("article")).toBe(row);
    expect(postRequests(fetcher)).toEqual(requests);
  });

  it("retains the current list while a missing archive window loads", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive([repostedLater]);
    await renderArchive();
    const day = await findCalendarDay("8月10日，2 条 Moment");
    const row = screen.getByText("旧帖，后来转发").closest("article");
    let release: (() => void) | undefined;
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const fetch = fetcher.getMockImplementation()!;
    let started = false;
    fetcher.mockImplementation(async (input) => {
      if (String(input).includes("anchor=2026-08-10")) {
        started = true;
        await gate;
      }
      return fetch(input);
    });
    fireEvent.click(day);
    await waitFor(() => expect(started).toBe(true));
    expect(row).toBeInTheDocument();
    expect(screen.queryByText("旧城雨落在街口")).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("link", { name: "8月10日，2 条 Moment，已选择" }),
    );
    expect(row).toBeInTheDocument();
    expect(
      postRequests(fetcher).filter((url) => url.includes("anchor=2026-08-10")),
    ).toHaveLength(1);
    release?.();
    expect(await screen.findByText("旧城雨落在街口")).toBeInTheDocument();
  });

  it("jumps years and months from the picker, and draws empty months as places rather than controls", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    await renderArchive();

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
    const requestsBeforeMonth = postRequests(fetcher);
    const cachedRow = screen.getByText("去年最后一天的雨").closest("article");
    fireEvent.click(
      screen.getByRole("link", { name: "2025年12月，1 条 Moment" }),
    );
    expect(window.location.search).toBe("?month=2025-12");
    expect(postRequests(fetcher)).toEqual(requestsBeforeMonth);
    expect(screen.getByText("去年最后一天的雨").closest("article")).toBe(
      cachedRow,
    );
    await waitFor(() =>
      expect(screen.queryByRole("group", { name: "年份" })).toBeNull(),
    );
    expect(await findCalendarDay("12月31日，1 条 Moment")).toBeInTheDocument();
  });

  it("pages the calendar within the archive's span without moving the list", async () => {
    pinToday();
    window.history.replaceState({}, "", "/archive");
    const fetcher = installArchive();
    await renderArchive();
    await findCalendarDay("8月10日，2 条 Moment");
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
    expect(await findCalendarDay("12月31日，1 条 Moment")).toBeInTheDocument();

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
    const { unmount } = await renderArchive();
    expect(
      await findCalendarDay("8月10日，2 条 Moment，已选择"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(postRequests(fetcher)).toContain(
        "/api/v1/archive/posts?limit=20&anchor=2026-08-10",
      ),
    );

    unmount();
    window.history.replaceState({}, "", "/archive?day=2026-02-30");
    await renderArchive();
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
        matches: query === "(min-width: 988px)",
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      })),
    );
    window.history.replaceState({}, "", "/archive");
    installArchive();
    await renderArchive();

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
