import { makeAccountSession } from "../../test/accountFixtures";
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

import { PublicApi, type SiteIdentity } from "../../api/public";
import { mediaPosterUrl, mediaPreviewUrl } from "../../api/mediaUrls";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import { SessionProvider } from "../auth/SessionProvider";
import { TimelinePage } from "./TimelinePage";

const site: SiteIdentity = {
  profileUserId: "00000000000000000000000000000000",
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

function post(id: string, content: string) {
  return {
    author: {
      user_id: "00000000000000000000000000000000",
      username: "owner",
      display_name: "Moment",
      avatar_media_id: null,
      profile_url: "/owner",
    },
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

function ownerStatus(): Response {
  return new Response(
    JSON.stringify({
      data: {
        initialized: true,
        password_enabled: false,
        session_epoch: 1,
        initialized_at: "2026-08-01T00:00:00Z",
      },
      meta: { request_id: "req_owner" },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

function renderTimeline(
  api: PublicApi,
  initialSearch = "",
  canPublish = false,
) {
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <TimelinePage
          api={api}
          initialSearch={initialSearch}
          canPublish={canPublish}
          site={site}
        />
      </NavigationProvider>
    </FeedbackProvider>,
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
    window.sessionStorage.clear();
    vi.unstubAllGlobals();
  });

  it("adds a linked repost at the top, keeps the original, and removes the projection on undo", async () => {
    const id = "01K2ABCDEF0123456789ABCDE0";
    const original = post(id, "一条会被转发的 Moment");
    const repostedAt = "2026-08-16T06:00:00.000Z";
    let active = false;
    window.sessionStorage.setItem(
      "moment.auth.session.v2",
      JSON.stringify({
        csrf_token: "csrf-repost",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/account/session")
          return new Response(
            JSON.stringify({
              data: {
                preferences: { ui_locale: null },
                csrf_token: "csrf-repost",
                expires_at: "2999-08-11T01:00:00.000Z",
                account: {
                  user_id: site.profileUserId,
                  username: site.username,
                  display_name: site.displayName,
                  bio: site.bio,
                  avatar_media_id: null,
                  cover_media_id: null,
                  role: "admin",
                  status: "active",
                  username_chosen: true,
                  profile_url: "/owner",
                },
                limits: {
                  profile_picture_bytes: 8388608,
                  display_name_length: 80,
                  bio_length: 500,
                },
              },
              meta: { request_id: "session" },
            }),
            { headers: { "Content-Type": "application/json" } },
          );
        if (url === `/api/v1/posts/${id}/repost`) {
          active = init?.method === "PUT";
          return new Response(
            JSON.stringify({
              data: {
                ...original,
                updated_at: repostedAt,
                reposted_at: active ? repostedAt : null,
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
        <SessionProvider>
          <NavigationProvider>
            <TimelinePage
              api={
                new PublicApi(
                  vi.fn(async () => page([original], null)) as typeof fetch,
                )
              }
              initialSearch=""
              canPublish
              site={site}
            />
          </NavigationProvider>
        </SessionProvider>
      </FeedbackProvider>,
    );

    await screen.findByText("一条会被转发的 Moment");
    const originalAction = await screen.findByRole("button", {
      name: "转发这条 Moment",
    });
    fireEvent.click(originalAction);
    fireEvent.click(await screen.findByRole("menuitem", { name: "转发" }));

    await waitFor(() =>
      expect(
        Array.from(
          document.querySelectorAll<HTMLElement>("[data-timeline-item-key]"),
        ).map((element) => element.dataset.timelineItemKey),
      ).toEqual([`${id}:repost`, `${id}:original`]),
    );
    expect(screen.getByText("已转发")).toBeInTheDocument();
    expect(
      screen
        .getAllByRole("button", { name: "转发这条 Moment" })
        .every((button) => button.getAttribute("aria-pressed") === "true"),
    ).toBe(true);
    expect(
      screen
        .getAllByRole("button", { name: "转发这条 Moment" })
        .every((button) => within(button).getByText("1")),
    ).toBe(true);

    fireEvent.click(
      screen.getAllByRole("button", { name: "转发这条 Moment" })[0],
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: "取消转发" }));
    await waitFor(() =>
      expect(
        Array.from(
          document.querySelectorAll<HTMLElement>("[data-timeline-item-key]"),
        ).map((element) => element.dataset.timelineItemKey),
      ).toEqual([`${id}:original`]),
    );
    expect(active).toBe(false);
    expect(
      within(
        screen.getByRole("button", { name: "转发这条 Moment" }),
      ).queryByText("1"),
    ).not.toBeInTheDocument();
  });

  it("attaches the lead video poster immediately and keeps later videos lazy", async () => {
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        readonly root = null;
        readonly rootMargin = "600px 0px";
        readonly thresholds = [0.01];
        observe = vi.fn();
        unobserve = vi.fn();
        disconnect = vi.fn();
        takeRecords = () => [];
      },
    );
    const firstMediaId = "01K2ABCDEF0123456789ABCDE1";
    const secondMediaId = "01K2ABCDEF0123456789ABCDE2";
    const first = {
      ...post("01K2ABCDEF0123456789ABCDE0", "首屏视频"),
      media: [
        {
          media_id: firstMediaId,
          position: 0,
          alt_text: "首屏视频画面",
          mime_type: "video/mp4",
          width: 1080,
          height: 1920,
        },
      ],
    };
    const second = {
      ...post("01K2ABCDEF0123456789ABCDE3", "下方视频"),
      media: [
        {
          media_id: secondMediaId,
          position: 0,
          alt_text: "下方视频画面",
          mime_type: "video/mp4",
          width: 1080,
          height: 1920,
        },
      ],
    };

    renderTimeline(
      new PublicApi(
        vi.fn(async () => page([first, second], null)) as typeof fetch,
      ),
    );

    const leadVideo = await screen.findByLabelText("首屏视频画面");
    const laterVideo = screen.getByLabelText("下方视频画面");
    expect(leadVideo.closest("[data-media-count]")).toHaveAttribute(
      "data-compact-tall-video",
      "true",
    );
    expect(laterVideo.closest("[data-media-count]")).toHaveAttribute(
      "data-compact-tall-video",
      "true",
    );
    expect(leadVideo).toHaveAttribute("src", mediaPreviewUrl(firstMediaId));
    expect(leadVideo).toHaveAttribute("poster", mediaPosterUrl(firstMediaId));
    expect(
      document.querySelector(`img[src="${mediaPosterUrl(firstMediaId)}"]`),
    ).not.toBeNull();
    expect(laterVideo).not.toHaveAttribute("src");
    expect(laterVideo).not.toHaveAttribute("poster");
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

  it("loads an intersecting continuation once without stealing focus", async () => {
    let intersect: IntersectionObserverCallback | null = null;
    const observe = vi.fn();
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        constructor(callback: IntersectionObserverCallback) {
          intersect = callback;
        }
        observe = observe;
        disconnect = vi.fn();
        unobserve = vi.fn();
        takeRecords = vi.fn(() => []);
        root = null;
        rootMargin = "800px 0px";
        thresholds = [0];
      },
    );
    const first = post("01K2ABCDEF0123456789ABCDE0", "可见的一段");
    const second = post("01K2ABCDEF0123456789ABCDE1", "自动接上的一段");
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("cursor=cursor-one")
        ? page([second], null)
        : page([first], "cursor-one"),
    );
    renderTimeline(new PublicApi(fetcher as typeof fetch));

    await screen.findByText("可见的一段");
    await waitFor(() => expect(observe).toHaveBeenCalled());
    const focusTarget = screen.getByRole("link", {
      name: "打开这条 Moment 详情",
    });
    focusTarget.focus();
    const callback = intersect as unknown as IntersectionObserverCallback;
    expect(callback).toBeTypeOf("function");
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

    expect(await screen.findByText("自动接上的一段")).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(focusTarget).toHaveFocus();
    expect(window.location.search).toContain("loaded_through=cursor-one");
  });

  it("opens the reply dialog from a timeline bubble without navigating", async () => {
    // Replying needs an account, so the composer only appears for a signed-in
    // browser; without this the dialog would offer sign-in instead.
    window.sessionStorage.setItem(
      "moment.auth.session.v2",
      JSON.stringify({
        csrf_token: "csrf-reply",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    const item = post("01ARZ3NDEKTSV4RRFFQ69G5FAV", "直接在时间流回复");
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input) === "/api/v1/status"
        ? ownerStatus()
        : String(input) === "/api/v1/account/session"
          ? new Response(
              JSON.stringify({
                data: makeAccountSession(),
                meta: { request_id: "reply-session" },
              }),
              { headers: { "Content-Type": "application/json" } },
            )
          : String(input).endsWith("/reply-context")
            ? new Response(
                JSON.stringify({
                  data: {
                    subject: { type: "post", id: item.id },
                    participants: ["owner", "maple"].map((username, index) => ({
                      user_id: String(index + 1).repeat(32),
                      username,
                      display_name: username,
                      avatar_media_id: null,
                      required: index === 0,
                    })),
                  },
                  meta: { request_id: "reply-context" },
                }),
                { headers: { "Content-Type": "application/json" } },
              )
            : page([item], null),
    );
    vi.stubGlobal("fetch", fetcher);
    vi.spyOn(window, "scrollY", "get").mockReturnValue(360);
    render(
      <NavigationProvider>
        <SessionProvider>
          <TimelinePage
            api={new PublicApi(fetcher as typeof fetch)}
            initialSearch=""
            site={site}
          />
        </SessionProvider>
      </NavigationProvider>,
    );

    const trigger = await screen.findByRole("button", {
      name: "回复这条 Moment",
    });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    expect(window.location.pathname).toBe("/");
    expect(within(dialog).getByLabelText("回复内容")).toHaveFocus();
    expect(await within(dialog).findByText(/正在回复/)).toBeVisible();
    await within(dialog).findByText(/@maple/);
    expect(within(dialog).getByLabelText("回复内容")).toHaveValue("");
    expect(within(dialog).queryByRole("checkbox")).not.toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.top).toBe("");

    fireEvent.click(
      within(dialog).getByRole("button", { name: "关闭回复窗口" }),
    );
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(window.location.pathname).toBe("/");
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
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

  it("shows valid content and clears a saved position that no longer matches page boundaries", async () => {
    window.history.replaceState({}, "", "/?loaded_through=old-boundary");
    const item = post("01K2ABCDEF0123456789ABCDE0", "内容仍然有效");
    const fetcher = vi.fn(async () => page([item], null));
    renderTimeline(
      new PublicApi(fetcher as typeof fetch),
      window.location.search,
    );
    expect(await screen.findByText("内容仍然有效")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(window.location.search).not.toContain("loaded_through");
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("merges an overlapping continued page without hiding the existing timeline", async () => {
    const first = post("01K2ABCDEF0123456789ABCDE0", "重叠的记录");
    const second = post("01K2ABCDEF0123456789ABCDE1", "更早的记录");
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("cursor=")
        ? page([first, second], null)
        : page([first], "next"),
    );
    renderTimeline(new PublicApi(fetcher as typeof fetch));
    await screen.findByText("重叠的记录");
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    expect(await screen.findByText("更早的记录")).toBeInTheDocument();
    expect(screen.getAllByText("重叠的记录")).toHaveLength(1);
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("drops unusable saved progress and restarts a cyclic restoration without looping", async () => {
    const item = post("01K2ABCDEF0123456789ABCDE0", "不会部分显示");
    const unreachable = vi.fn(async () => page([item], null));
    const { unmount } = renderTimeline(
      new PublicApi(unreachable as typeof fetch),
      "?loaded_through=missing",
    );
    expect(await screen.findByText("不会部分显示")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();

    unmount();
    const duplicateParameter = vi.fn(async () => page([item], null));
    const duplicateView = renderTimeline(
      new PublicApi(duplicateParameter as typeof fetch),
      "?loaded_through=one&loaded_through=two",
    );
    expect(await screen.findByText("不会部分显示")).toBeInTheDocument();
    expect(duplicateParameter).toHaveBeenCalledTimes(1);

    duplicateView.unmount();
    const cyclic = vi.fn(async (input: RequestInfo | URL) => {
      return page(
        [
          !String(input).includes("cursor=loop")
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
    expect(await screen.findByText("不会部分显示")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
    expect(cyclic).toHaveBeenCalledTimes(3);
  });

  it("automatically recovers an initial failure without a retry action", async () => {
    let attempt = 0;
    const fetcher = vi.fn(async () => {
      attempt += 1;
      if (attempt === 1) throw new TypeError("offline");
      return page([post("01K2ABCDEF0123456789ABCDE0", "重新取回")], null);
    });
    renderTimeline(new PublicApi(fetcher as typeof fetch));

    expect(await screen.findByText("正在重新获取内容…")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
    expect(await screen.findByText("重新取回")).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("deduplicates a row that overlaps with a continued page", async () => {
    const original = post("01K2ABCDEF0123456789ABCDE0", "只出现一次");
    let attempt = 0;
    const fetcher = vi.fn(async () => {
      attempt += 1;
      return attempt === 1 ? page([original], "next") : page([original], null);
    });
    renderTimeline(new PublicApi(fetcher as typeof fetch));
    await screen.findByText("只出现一次");
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    expect(screen.queryByRole("alert")).toBeNull();
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

      await screen.findByText("正在重新获取后续内容…");
      expect(screen.queryByText("不应追加")).not.toBeInTheDocument();
      expect(window.location.search).not.toContain("loaded_through");

      await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(3));
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
      await screen.findByText("正在重新获取后续内容…"),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "从第一页重新加载" }),
    ).toBeNull();
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
    expect(screen.getByText(/这里还没有公开 Moment/)).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "发布第一条 Moment" }),
    ).not.toBeInTheDocument();
  });

  it("offers the owner a direct first-publish action", async () => {
    renderTimeline(
      new PublicApi(vi.fn(async () => page([], null)) as typeof fetch),
      "",
      true,
    );
    expect(
      await screen.findByRole("link", { name: "发布第一条 Moment" }),
    ).toHaveAttribute("href", "/compose");
  });
});
