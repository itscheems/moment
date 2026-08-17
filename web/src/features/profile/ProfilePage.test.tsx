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

import { PublicApi, type SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import { OwnerProvider } from "../owner/OwnerProvider";
import { announceOwnerPost } from "../owner/postEvents";
import { ProfilePage } from "./ProfilePage";

const pinnedId = "01K2ABCDEF0123456789ABCDE0";
const otherId = "01K2ABCDEF0123456789ABCDE1";

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "Moment",
  bio: "",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: pinnedId,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: "2026-08-01T09:08:00Z",
};

function post(id: string, text: string) {
  return {
    id,
    content_md: text,
    content_html: `<p>${text}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    reposted_at: null,
    timeline_item_kind: "original",
    quoted_post: null,
    comment_count: 0,
    media: [],
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function page(items: unknown[], nextCursor: string | null): Response {
  return json({
    data: { items },
    meta: { next_cursor: nextCursor, request_id: "req_posts" },
  });
}

beforeEach(() => {
  window.history.replaceState({}, "", "/@owner");
  vi.stubGlobal("scrollTo", vi.fn());
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

describe("profile page", () => {
  it("adds a newly published owner Moment without reloading the profile", async () => {
    const existing = post(otherId, "原有内容");
    const fetcher = vi.fn(async () => page([existing], null));
    const api = new PublicApi(fetcher as typeof fetch);

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={api}
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    expect(await screen.findByText("原有内容")).toBeInTheDocument();
    act(() => {
      announceOwnerPost({
        kind: "created",
        post: {
          id: pinnedId,
          contentMd: "刚刚发布的引用",
          contentHtml: "<p>刚刚发布的引用</p>",
          createdAt: "2026-08-16T02:00:00.000Z",
          updatedAt: "2026-08-16T02:00:00.000Z",
          publishedAt: "2026-08-16T02:00:00.000Z",
          repostedAt: null,
          timelineItemKind: "original",
          quotedPost: null,
          commentCount: 0,
          media: [],
        },
      });
    });

    expect(screen.getByText("刚刚发布的引用")).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("inserts a linked repost above the original from the profile action", async () => {
    const original = post(otherId, "个人资料里的原帖");
    const repostedAt = "2026-08-16T06:00:00.000Z";
    window.sessionStorage.setItem(
      "moment.owner.session.v1",
      JSON.stringify({
        csrf_token: "csrf-repost",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/owner/status") {
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
        if (
          url === `/api/v1/owner/posts/${otherId}/repost` &&
          init?.method === "PUT"
        ) {
          return json({
            data: {
              ...original,
              updated_at: repostedAt,
              reposted_at: repostedAt,
            },
            meta: { request_id: "req_repost" },
          });
        }
        return json({}, 404);
      }),
    );

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={
                new PublicApi(
                  vi.fn(async () => page([original], null)) as typeof fetch,
                )
              }
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    await screen.findByText("个人资料里的原帖");
    fireEvent.click(
      await screen.findByRole("button", { name: "转发这条 Moment" }),
    );
    fireEvent.click(await screen.findByRole("menuitem", { name: "转发" }));

    await waitFor(() =>
      expect(
        Array.from(
          document.querySelectorAll<HTMLElement>("[data-timeline-item-key]"),
        ).map((element) => element.dataset.timelineItemKey),
      ).toEqual([`${otherId}:repost`, `${otherId}:original`]),
    );
    expect(screen.getByText("已转发")).toBeInTheDocument();
  });

  it("opens the reply dialog from a profile Moment and restores the trigger", async () => {
    const item = post(otherId, "从个人资料直接回复");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL) =>
        String(input) === "/api/v1/owner/status"
          ? json(
              {
                error: {
                  code: "UNAUTHORIZED",
                  message: "guest",
                  details: {},
                  request_id: "req_owner",
                },
              },
              401,
            )
          : json({}, 404),
      ),
    );
    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={
                new PublicApi(
                  vi.fn(async () => page([item], null)) as typeof fetch,
                )
              }
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    expect(await screen.findByText("从个人资料直接回复")).toBeInTheDocument();
    const trigger = screen.getByRole("button", { name: "回复这条 Moment" });
    const initialUrl = window.location.href;
    fireEvent.click(trigger);

    const dialog = screen.getByRole("dialog", { name: "回复这条 Moment" });
    expect(dialog).toHaveTextContent("从个人资料直接回复");
    await waitFor(() =>
      expect(screen.getByLabelText("回复内容")).toHaveFocus(),
    );
    expect(window.location.href).toBe(initialUrl);
    expect(document.documentElement.style.overflowY).toBe("hidden");

    fireEvent.click(screen.getByRole("button", { name: "关闭回复窗口" }));
    await waitFor(() => expect(dialog).not.toBeInTheDocument());
    expect(trigger).toHaveFocus();
    expect(document.documentElement.style.overflowY).toBe("");
  });

  it("renders the singleton pin first without duplicating its timeline row", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json({
          data: {
            initialized: true,
            password_enabled: false,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00Z",
          },
          meta: { request_id: "req_status" },
        }),
      ),
    );
    const api = new PublicApi(async (input) => {
      const url = String(input);
      if (url.startsWith("/api/v1/posts?")) {
        return json({
          data: {
            items: [post(pinnedId, "置顶内容"), post(otherId, "普通内容")],
          },
          meta: { next_cursor: null },
        });
      }
      if (url === `/api/v1/posts/${pinnedId}`) {
        return json({ data: post(pinnedId, "置顶内容"), meta: {} });
      }
      return json({}, 404);
    });

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage api={api} onSiteSaved={vi.fn()} site={site} />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    await waitFor(() =>
      expect(screen.getAllByText("置顶内容")).toHaveLength(1),
    );
    expect(
      screen.getByRole("heading", { level: 3, name: "Moment" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("navigation", { name: "个人资料内容" }),
    ).not.toBeInTheDocument();
    expect(screen.getByText("按发布时间排列")).toBeInTheDocument();
    expect(screen.queryByText("媒体与归档")).not.toBeInTheDocument();
    expect(screen.getByText("@owner", { selector: "p" })).toBeInTheDocument();
    expect(screen.getByText("2026年8月加入")).toHaveAttribute(
      "datetime",
      site.instanceInitializedAt,
    );
    expect(
      screen.queryByRole("link", { name: "查看归档" }),
    ).not.toBeInTheDocument();
    const timezone = screen.getByLabelText(/时区：.*（Asia\/Shanghai）/);
    expect(timezone).toHaveAttribute("title", "Asia/Shanghai");
    expect(timezone).not.toHaveTextContent(/^Asia\/Shanghai$/);
    expect(screen.getByText("已置顶")).toBeInTheDocument();
    const pinned = screen.getByText("置顶内容").closest("article");
    const normal = screen.getByText("普通内容").closest("article");
    expect(
      pinned && normal
        ? pinned.compareDocumentPosition(normal) &
            Node.DOCUMENT_POSITION_FOLLOWING
        : 0,
    ).toBeTruthy();
  });

  it("explains an empty public profile without presenting a fake tab", async () => {
    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={
                new PublicApi(vi.fn(async () => page([], null)) as typeof fetch)
              }
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    expect(
      await screen.findByText(/这个个人资料还没有公开 Moment/),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("link", { name: "发布第一条 Moment" }),
    ).not.toBeInTheDocument();
  });

  it("offers the owner a first-publish action from an empty profile", async () => {
    window.sessionStorage.setItem(
      "moment.owner.session.v1",
      JSON.stringify({
        csrf_token: "csrf-profile",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        json({
          data: {
            initialized: true,
            password_enabled: false,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00Z",
          },
          meta: { request_id: "req_status" },
        }),
      ),
    );
    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={
                new PublicApi(vi.fn(async () => page([], null)) as typeof fetch)
              }
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    expect(
      await screen.findByRole("link", { name: "发布第一条 Moment" }),
    ).toHaveAttribute("href", "/owner/compose");
  });

  it("keeps loaded posts visible when an append fails and retries in place", async () => {
    const first = post(pinnedId, "已经读到这里");
    const second = post(otherId, "重试后接上的内容");
    let continued = 0;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes("cursor=cursor-one")) {
        continued += 1;
        if (continued === 1) throw new TypeError("offline");
        return page([second], null);
      }
      return page([first], "cursor-one");
    });

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={new PublicApi(fetcher as typeof fetch)}
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    expect(await screen.findByText("已经读到这里")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "加载更早的 Moment" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "已保留当前 Moment",
    );
    expect(screen.getByText("已经读到这里")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "重试加载" }));
    expect(await screen.findByText("重试后接上的内容")).toBeInTheDocument();
    expect(screen.getByText("已经读到这里")).toBeInTheDocument();
    expect(window.location.search).toContain("loaded_through=cursor-one");
  });

  it("replays saved profile pages before restoring the history entry", async () => {
    window.history.replaceState(
      { momentScrollY: 640 },
      "",
      "/@owner?loaded_through=cursor-one",
    );
    const first = post(pinnedId, "资料页第一页");
    const second = post(otherId, "资料页第二页");
    const fetcher = vi.fn(async (input: RequestInfo | URL) =>
      String(input).includes("cursor=cursor-one")
        ? page([second], null)
        : page([first], "cursor-one"),
    );

    render(
      <FeedbackProvider>
        <OwnerProvider>
          <NavigationProvider>
            <ProfilePage
              api={new PublicApi(fetcher as typeof fetch)}
              onSiteSaved={vi.fn()}
              site={{ ...site, pinnedPostId: null }}
            />
          </NavigationProvider>
        </OwnerProvider>
      </FeedbackProvider>,
    );

    expect(await screen.findByText("资料页第二页")).toBeInTheDocument();
    expect(screen.getByText("资料页第一页")).toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
    await waitFor(() =>
      expect(window.scrollTo).toHaveBeenCalledWith({
        top: 640,
        behavior: "auto",
      }),
    );
  });
});
