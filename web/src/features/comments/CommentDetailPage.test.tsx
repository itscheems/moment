import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PublicApi,
  type SiteIdentity,
  type PublicPost,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { SessionProvider } from "../auth/SessionProvider";
import { CommentDetailPage } from "./CommentDetailPage";
import type { DiscussionChange } from "./discussionChanges";

const postId = "01K2ABCDEF0123456789ABCDE0";
const rootId = "01K2ABCDEF0123456789ABCDE1";
const selectedId = "01K2ABCDEF0123456789ABCDE2";
const targetId = "01K2ABCDEF0123456789ABCDE3";
const answerId = "01K2ABCDEF0123456789ABCDE4";
const secondId = "01K2ABCDEF0123456789ABCDE5";
const author = {
  user_id: "0123456789abcdef0123456789abcdef",
  username: "alice",
  display_name: "Alice",
  avatar_media_id: null,
  profile_url: "/alice",
};
const replyAuthor = {
  ...author,
  user_id: "1123456789abcdef0123456789abcdef",
  username: "bobby",
  display_name: "Bob",
  profile_url: "/bobby",
};
const publicAuthor = {
  userId: author.user_id,
  username: author.username,
  displayName: author.display_name,
  avatarMediaId: null,
  profileUrl: author.profile_url,
};
const site: SiteIdentity = {
  profileUserId: author.user_id,
  username: "alice",
  displayName: "Alice",
  title: "Moment",
  bio: "",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};
const post: PublicPost = {
  id: postId,
  kind: "moment",
  author: publicAuthor,
  contentMd: "原始帖子内容",
  contentHtml: "<p>原始帖子内容</p>",
  article: null,
  createdAt: "2026-08-10T01:00:00Z",
  updatedAt: "2026-08-10T01:00:00Z",
  publishedAt: "2026-08-10T01:00:00Z",
  repostedAt: null,
  visibility: "everyone",
  timelineItemKind: "original",
  commentCount: 5,
  media: [],
  linkCards: [],
};

function wire(
  id: string,
  text: string,
  target: string | null = null,
  parent: string | null = rootId,
) {
  return {
    id,
    post_id: postId,
    parent_id: parent,
    depth: parent ? 1 : 0,
    author_name: id === targetId ? "Bob" : "Alice",
    author: id === targetId ? replyAuthor : author,
    reply_to: target
      ? {
          comment_id: target,
          author: target === targetId ? replyAuthor : author,
        }
      : null,
    content_md: text,
    content_html: "<p>" + text + "</p>",
    created_at: "2026-08-10T01:03:00Z",
    media: [],
    location: null,
    reply_count: parent ? 0 : 3,
  };
}
function response(data: unknown, cursor: string | null = null) {
  return new Response(
    JSON.stringify({
      data,
      meta: { request_id: "detail_test", next_cursor: cursor },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}
function setup(
  options: { scope?: "thread" | "direct"; missing?: boolean } = {},
) {
  const target = wire(targetId, "被回复者的评论", rootId);
  const selected = wire(selectedId, "当前评论全文", targetId);
  const answer = wire(answerId, "直接回答当前评论", selectedId);
  const root = wire(rootId, "整层主评论", null, null);
  const fetcher = vi.fn<typeof fetch>(async (input) => {
    const url = String(input);
    if (url.includes("/replies"))
      return response({ items: [answer] }, "next-page");
    if (options.missing)
      return new Response(
        JSON.stringify({
          error: { code: "COMMENT_NOT_FOUND", message: "missing", details: {} },
        }),
        { status: 404 },
      );
    return response({
      comment: options.scope === "thread" ? root : selected,
      reply_target: options.scope === "thread" ? null : target,
      root_comment_id: rootId,
      pinned_comment_id: null,
    });
  });
  const api = new PublicApi(fetcher);
  const mutation = vi.fn();
  const props = {
    api,
    post,
    site,
    commentId: options.scope === "thread" ? rootId : selectedId,
    scope: options.scope ?? ("direct" as const),
    onMutation: mutation,
    onCountChange: vi.fn(),
  };
  const wrapper = (active = true, change: DiscussionChange | null = null) => (
    <NavigationProvider>
      <SessionProvider>
        <CommentDetailPage {...props} active={active} change={change} />
      </SessionProvider>
    </NavigationProvider>
  );
  return { fetcher, props, wrapper, mutation };
}

beforeEach(() => {
  localStorage.clear();
  window.history.replaceState(
    {},
    "",
    "/m/" + postId + "/comments/" + selectedId,
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            error: { code: "UNAUTHORIZED", message: "signed out", details: {} },
          }),
          { status: 401, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
  vi.spyOn(window, "scrollTo").mockImplementation(() => {});
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(),
  });
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("focused comment reading", () => {
  it("loads a deep permalink with the post, immediate target and only its direct replies", async () => {
    const { fetcher, wrapper } = setup();
    render(wrapper());
    await screen.findByText("当前评论全文");
    expect(
      within(screen.getByRole("region", { name: "原帖引用" })).getByText(
        "原始帖子内容",
      ),
    ).toBeVisible();
    expect(
      within(screen.getByRole("region", { name: "被回复评论引用" })).getByText(
        "被回复者的评论",
      ),
    ).toBeVisible();
    expect(
      within(screen.getByRole("list", { name: "直接回复列表" })).getByText(
        "直接回答当前评论",
      ),
    ).toBeVisible();
    expect(screen.getByRole("link", { name: "查看整层讨论" })).toHaveAttribute(
      "href",
      "/m/" + postId + "/comments/" + rootId + "?view=thread",
    );
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).endsWith("/replies?scope=direct"),
      ),
    ).toBe(true);
    expect(
      fetcher.mock.calls.some(([url]) => String(url).endsWith("/comments")),
    ).toBe(false);
  });
  it("pages forward, folds and reopens cached replies, including after suspension", async () => {
    const { fetcher, wrapper } = setup();
    const view = render(wrapper());
    await screen.findByText("当前评论全文");
    fetcher.mockImplementationOnce(async () =>
      response({
        items: [
          {
            ...wire(secondId, "第二批回复", selectedId),
            created_at: "2026-08-10T01:04:00Z",
          },
        ],
      }),
    );
    fireEvent.click(screen.getByRole("button", { name: "加载更多回复" }));
    await screen.findByText("第二批回复");
    const loadedCalls = fetcher.mock.calls.length;
    fireEvent.click(screen.getByRole("button", { name: "收起回复" }));
    expect(screen.queryByText("第二批回复")).not.toBeInTheDocument();
    view.rerender(wrapper(false));
    view.rerender(wrapper(true));
    fireEvent.click(screen.getByRole("button", { name: "展开已读取的回复" }));
    expect(screen.getByText("第二批回复")).toBeVisible();
    expect(fetcher.mock.calls.length).toBe(loadedCalls);
  });
  it("reads a whole floor using the existing thread scope", async () => {
    const { fetcher, wrapper } = setup({ scope: "thread" });
    render(wrapper());
    await screen.findByText("整层主评论");
    expect(
      screen.queryByRole("region", { name: "被回复评论引用" }),
    ).not.toBeInTheDocument();
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).endsWith("/replies?scope=thread"),
      ),
    ).toBe(true);
    expect(screen.getByRole("link", { name: "只看直接回复" })).toHaveAttribute(
      "href",
      "/m/" + postId + "/comments/" + rootId,
    );
  });
  it("shows a missing comment without rendering unavailable context", async () => {
    const { wrapper } = setup({ missing: true });
    render(wrapper());
    await screen.findByText("这条评论已不存在或不可访问。");
    expect(
      screen
        .getByText("这条评论已不存在或不可访问。")
        .closest('[role="status"]'),
    ).toHaveFocus();
    expect(
      screen.queryByRole("region", { name: "被回复评论引用" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "返回原帖" })).toHaveAttribute(
      "href",
      "/m/" + postId,
    );
  });
  it("clears an externally deleted target instead of retaining a stale quote", async () => {
    const { wrapper } = setup();
    const view = render(wrapper());
    await screen.findByText("当前评论全文");
    const context = await new PublicApi(
      vi.fn(async () =>
        response({
          comment: wire(targetId, "被回复者的评论", rootId),
          reply_target: wire(rootId, "root", null, null),
          root_comment_id: rootId,
          pinned_comment_id: null,
        }),
      ) as typeof fetch,
    ).commentContext(postId, targetId);
    view.rerender(
      wrapper(true, {
        kind: "deleted",
        comment: context.comment,
        repliesAffected: 0,
        source: "other",
        revision: 1,
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("region", { name: "被回复评论引用" }),
      ).not.toBeInTheDocument(),
    );
    expect(screen.getByText(/未记录具体回复对象/)).toBeVisible();
    expect(screen.getByText("当前评论全文")).toBeVisible();
  });
});
