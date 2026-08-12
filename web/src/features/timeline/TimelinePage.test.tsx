import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { PublicApi, type SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { TimelinePage } from "./TimelinePage";

const site: SiteIdentity = {
  title: "乔乔的 Moment",
  bio: "一些正在发生的事",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
};

function post(id: string, content: string) {
  return {
    id,
    content_md: content,
    content_html: `<p>${content}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    comment_count: 0,
    media: [],
  };
}

function page(items: unknown[], nextCursor: string | null): Response {
  return new Response(
    JSON.stringify({
      data: { items },
      meta: { next_cursor: nextCursor, request_id: "req_posts" },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

function staleCursor(): Response {
  return new Response(
    JSON.stringify({
      error: {
        code: "INVALID_ARGUMENT",
        message: "cursor is stale",
        details: { field: "cursor" },
        request_id: "req_stale",
      },
    }),
    { status: 400, headers: { "Content-Type": "application/json" } },
  );
}

function renderTimeline(api: PublicApi, initialSearch = "") {
  return render(
    <NavigationProvider>
      <TimelinePage api={api} initialSearch={initialSearch} site={site} />
    </NavigationProvider>,
  );
}

describe("public timeline", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    vi.stubGlobal("scrollTo", vi.fn());
    let nextFrame = 0;
    const frames = new Map<number, number>();
    vi.stubGlobal(
      "requestAnimationFrame",
      vi.fn((callback: FrameRequestCallback) => {
        const id = ++nextFrame;
        const timer = window.setTimeout(() => {
          frames.delete(id);
          callback(0);
        }, 0);
        frames.set(id, timer);
        return id;
      }),
    );
    vi.stubGlobal(
      "cancelAnimationFrame",
      vi.fn((id: number) => {
        const timer = frames.get(id);
        if (timer !== undefined) window.clearTimeout(timer);
        frames.delete(id);
      }),
    );
  });

  afterEach(async () => {
    await new Promise<void>((resolve) => window.setTimeout(resolve, 0));
    cleanup();
    vi.unstubAllGlobals();
  });

  it("continues the opaque cursor in server order and focuses new content", async () => {
    const first = post("01K2ABCDEF0123456789ABCDE0", "最近的一段");
    const second = post("01K2ABCDEF0123456789ABCDE1", "更早的一段");
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("cursor=cursor-one")
        ? page([second], null)
        : page([first], "cursor-one"),
    );
    renderTimeline(new PublicApi(fetcher as typeof fetch), "?from=2026-08-01");

    expect(await screen.findByText("最近的一段")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    const older = await screen.findByText("更早的一段");
    expect(older).toBeInTheDocument();
    await waitFor(() => expect(older.closest("article")).toHaveFocus());
    expect(fetcher).toHaveBeenLastCalledWith(
      "/api/v1/posts?limit=20&cursor=cursor-one&from=2026-08-01",
      expect.any(Object),
    );
    expect(window.location.search).toContain("loaded_through=cursor-one");
    expect(screen.getByText("已经走到这段时间流的起点")).toBeInTheDocument();
  });

  it("replays the server cursor chain on refresh and restores scroll", async () => {
    const first = post("01K2ABCDEF0123456789ABCDE0", "最近的一段");
    const second = post("01K2ABCDEF0123456789ABCDE1", "更早的一段");
    window.history.replaceState(
      { momentScrollY: 720 },
      "",
      "/?loaded_through=cursor-one",
    );
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("cursor=cursor-one")
        ? page([second], null)
        : page([first], "cursor-one"),
    );

    renderTimeline(
      new PublicApi(fetcher as typeof fetch),
      window.location.search,
    );

    expect(await screen.findByText("更早的一段")).toBeInTheDocument();
    expect(screen.getByText("最近的一段")).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(fetcher).toHaveBeenNthCalledWith(
      2,
      "/api/v1/posts?limit=20&cursor=cursor-one",
      expect.any(Object),
    );
    await waitFor(() =>
      expect(window.scrollTo).toHaveBeenCalledWith({
        top: 720,
        behavior: "auto",
      }),
    );
  });

  it("rejects an unreachable, duplicate, or cyclic restoration cursor", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "不会部分显示");
    const unreachable = vi.fn(async () => page([item], null));
    const { unmount } = renderTimeline(
      new PublicApi(unreachable as typeof fetch),
      "?loaded_through=missing",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这次收到的内容无法显示",
    );
    expect(screen.queryByText("不会部分显示")).not.toBeInTheDocument();

    unmount();
    const duplicateParameter = vi.fn(async () => page([item], null));
    const duplicateView = renderTimeline(
      new PublicApi(duplicateParameter as typeof fetch),
      "?loaded_through=one&loaded_through=two",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这次收到的内容无法显示",
    );
    expect(duplicateParameter).not.toHaveBeenCalled();

    duplicateView.unmount();
    let cyclicRequest = 0;
    const cyclic = vi.fn(async () => {
      cyclicRequest += 1;
      return page(
        [
          cyclicRequest === 1
            ? item
            : post("01K2ABCDEF0123456789ABCDE1", "循环中的下一条"),
        ],
        "loop",
      );
    });
    renderTimeline(
      new PublicApi(cyclic as typeof fetch),
      "?loaded_through=missing",
    );
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这次收到的内容无法显示",
    );
    expect(cyclic).toHaveBeenCalledTimes(2);
  });

  it("retries an initial failure without losing the deliberate error state", async () => {
    let attempt = 0;
    const fetcher = vi.fn(async () => {
      attempt += 1;
      if (attempt === 1) throw new TypeError("offline");
      return page([post("01K2ABCDEF0123456789ABCDE0", "重新取回")], null);
    });
    renderTimeline(new PublicApi(fetcher as typeof fetch));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "无法加载时间流",
    );
    fireEvent.click(screen.getByRole("button", { name: "重试" }));
    expect(await screen.findByText("重新取回")).toBeInTheDocument();
  });

  it("does not append a duplicate returned by a continued page", async () => {
    const original = post("01K2ABCDEF0123456789ABCDE0", "只出现一次");
    let attempt = 0;
    const fetcher = vi.fn(async () => {
      attempt += 1;
      return attempt === 1 ? page([original], "next") : page([original], null);
    });
    renderTimeline(new PublicApi(fetcher as typeof fetch));
    await screen.findByText("只出现一次");
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "下一页数据不完整",
    );
    expect(screen.getAllByText("只出现一次")).toHaveLength(1);
  });

  it("clears stale archive progress without losing the selected day", async () => {
    const first = post("01K2ABCDEF0123456789ABCDE0", "当天第一条");
    window.history.replaceState(
      {},
      "",
      "/archive?year=2026&month=2026-08&from=2026-08-10&to=2026-08-11&loaded_through=stale",
    );
    let firstPageRequests = 0;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes("cursor=stale")) return staleCursor();
      firstPageRequests += 1;
      return page([first], "stale");
    });
    renderTimeline(
      new PublicApi(fetcher as typeof fetch),
      window.location.search,
    );

    const reset = await screen.findByRole("button", {
      name: "从第一页重新加载",
    });
    fireEvent.click(reset);
    expect(await screen.findByText("当天第一条")).toBeInTheDocument();
    expect(firstPageRequests).toBe(2);
    expect(window.location.pathname).toBe("/archive");
    expect(window.location.search).toContain("year=2026");
    expect(window.location.search).toContain("month=2026-08");
    expect(window.location.search).toContain("from=2026-08-10");
    expect(window.location.search).toContain("to=2026-08-11");
    expect(window.location.search).not.toContain("loaded_through");
  });

  it.each([
    {
      failure: "self-loop",
      items: [post("01K2ABCDEF0123456789ABCDE1", "不应追加")],
      nextCursor: "cursor-one",
    },
    {
      failure: "empty drift",
      items: [],
      nextCursor: "cursor-two",
    },
    { failure: "stale", items: [], nextCursor: null },
  ])(
    "recovers a continued timeline cursor $failure from the first page",
    async ({ failure, items, nextCursor }) => {
      const first = post("01K2ABCDEF0123456789ABCDE0", "最近的一段");
      const recovered = post("01K2ABCDEF0123456789ABCDE2", "恢复后的下一页");
      let continuedRequests = 0;
      const fetcher = vi.fn(async (input: RequestInfo | URL) => {
        if (!String(input).includes("cursor=cursor-one")) {
          return page([first], "cursor-one");
        }
        continuedRequests += 1;
        if (continuedRequests > 1) return page([recovered], null);
        return failure === "stale" ? staleCursor() : page(items, nextCursor);
      });
      renderTimeline(new PublicApi(fetcher as typeof fetch));
      await screen.findByText("最近的一段");
      fireEvent.click(
        screen.getByRole("button", { name: "加载更早的 Moment" }),
      );

      const reset = await screen.findByRole("button", {
        name: "从第一页重新加载",
      });
      expect(screen.queryByText("不应追加")).not.toBeInTheDocument();
      expect(window.location.search).not.toContain("loaded_through");

      fireEvent.click(reset);
      await waitFor(() =>
        expect(
          screen.getByRole("button", { name: "加载更早的 Moment" }),
        ).toBeInTheDocument(),
      );
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
      fireEvent.click(
        screen.getByRole("button", { name: "加载更早的 Moment" }),
      );
      expect(await screen.findByText("恢复后的下一页")).toBeInTheDocument();
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    },
  );

  it("tracks continued timeline cursors and rejects a later cycle", async () => {
    const first = post("01K2ABCDEF0123456789ABCDE0", "最近的一段");
    const second = post("01K2ABCDEF0123456789ABCDE1", "第二页");
    const third = post("01K2ABCDEF0123456789ABCDE2", "不应追加的循环页");
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const cursor = new URL(
        String(input),
        "http://moment.invalid",
      ).searchParams.get("cursor");
      if (cursor === "cursor-one") return page([second], "cursor-two");
      if (cursor === "cursor-two") return page([third], "cursor-one");
      return page([first], "cursor-one");
    });
    renderTimeline(new PublicApi(fetcher as typeof fetch));
    await screen.findByText("最近的一段");
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    await screen.findByText("第二页");
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));

    expect(
      await screen.findByRole("button", { name: "从第一页重新加载" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("不应追加的循环页")).not.toBeInTheDocument();
    expect(window.location.search).toContain("loaded_through=cursor-one");
  });

  it("renders a useful empty state", async () => {
    renderTimeline(
      new PublicApi(vi.fn(async () => page([], null)) as typeof fetch),
    );
    expect(
      await screen.findByRole("heading", { name: "还没有 Moment" }),
    ).toBeInTheDocument();
  });
});
