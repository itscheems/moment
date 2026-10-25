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
import { makeAccountLimits } from "../../test/accountFixtures";

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
      direct_reply_count: 1,
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
  sessionStorage.clear();
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

function currentPost() {
  const element = screen.getByText("当前评论全文").closest("article");
  if (!element)
    throw new Error("Current comment is not using the post renderer");
  return within(element);
}

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
      within(screen.getByRole("region", { name: "原帖引用" })).getByRole(
        "link",
        { name: "打开这条 Moment 详情" },
      ),
    ).toHaveAttribute("href", "/m/" + postId);
    expect(
      within(screen.getByRole("region", { name: "被回复评论引用" })).getByRole(
        "link",
        { name: "打开这条 Moment 详情" },
      ),
    ).toHaveAttribute("href", "/m/" + postId + "/comments/" + targetId);
    expect(
      screen.queryByRole("link", { name: "返回原帖" }),
    ).not.toBeInTheDocument();
    expect(
      within(await screen.findByRole("list")).getByText("直接回答当前评论"),
    ).toBeVisible();
    expect(
      currentPost().getByRole("button", { name: "分享这条 Moment" }),
    ).toBeVisible();
    expect(screen.queryByText("直接回复")).not.toBeInTheDocument();
    expect(screen.queryByText("查看整层讨论")).not.toBeInTheDocument();
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).endsWith("/replies?scope=direct"),
      ),
    ).toBe(true);
    expect(
      fetcher.mock.calls.some(([url]) => String(url).endsWith("/comments")),
    ).toBe(false);
  });
  it("renders direct replies as the first level of the selected conversation", async () => {
    const { wrapper } = setup();
    render(wrapper());
    const answer = await screen.findByText("直接回答当前评论");
    expect(answer.closest("li")).toHaveAttribute("data-depth", "0");
    expect(answer.closest("li")).toHaveAttribute(
      "data-presentation",
      "direct-reply",
    );
    expect(screen.getByText("当前评论全文").closest("article")).toHaveAttribute(
      "data-post-id",
      selectedId,
    );
  });
  it("uses the selected comment author for reply badges rather than the original post author", async () => {
    const { fetcher, wrapper } = setup();
    fetcher.mockImplementation(async (input) =>
      String(input).includes("/replies")
        ? response({
            items: [
              {
                ...wire(answerId, "原帖作者的回答", selectedId),
                reply_to: { comment_id: selectedId, author: replyAuthor },
              },
            ],
          })
        : response({
            comment: {
              ...wire(selectedId, "当前评论全文", targetId),
              author: replyAuthor,
              author_name: "Bob",
            },
            reply_target: wire(targetId, "被回复者的评论", rootId),
            root_comment_id: rootId,
            pinned_comment_id: null,
            direct_reply_count: 1,
          }),
    );
    render(wrapper());
    await screen.findByText("原帖作者的回答");
    expect(
      within(screen.getByRole("list")).queryByText("作者"),
    ).not.toBeInTheDocument();
  });
  it.each([false, true])(
    "reuses the reply dialog and preserves the selected conversation when replying to a child: %s",
    async (answerChild) => {
      const { wrapper, mutation } = setup();
      const target = answerChild ? answerId : selectedId;
      vi.stubGlobal(
        "fetch",
        vi.fn(async (input, init) => {
          if (String(input) === "/api/v1/account/session")
            return response({
              expires_at: "2999-08-11T01:00:00Z",
              csrf_token: "test-csrf",
              account: {
                ...author,
                bio: "",
                cover_media_id: null,
                role: "member",
                status: "active",
                username_chosen: true,
              },
              limits: makeAccountLimits(),
            });
          expect(init?.method).toBe("POST");
          expect(JSON.parse(String(init?.body)).reply_to_comment_id).toBe(
            target,
          );
          return new Response(
            JSON.stringify({
              data: wire(secondId, "刚发表的回复", target),
              meta: { request_id: "created", replayed: false },
            }),
            { status: 201, headers: { "Content-Type": "application/json" } },
          );
        }),
      );
      render(wrapper());
      const inline = await screen.findByRole("textbox", { name: "回复内容" });
      expect(inline.closest("[data-comment-layout-frame]")).toHaveAttribute(
        "data-comment-layout-state",
        "collapsed",
      );
      if (answerChild) {
        fireEvent.click(
          within(screen.getByRole("list")).getByRole("button", {
            name: "回复 Alice",
          }),
        );
      } else {
        fireEvent.click(
          currentPost().getByRole("button", { name: /回复这条 Moment/ }),
        );
      }
      const dialog = await screen.findByRole("dialog", {
        name: "回复这条 Moment",
      });
      const dialogBox = within(dialog).getByRole("textbox", {
        name: "回复内容",
      });
      fireEvent.change(dialogBox, { target: { value: "刚发表的回复" } });
      fireEvent.click(within(dialog).getByRole("button", { name: "发表回复" }));
      await waitFor(() => expect(mutation).toHaveBeenCalled());
      expect(screen.queryByText("查看刚发布的回复")).not.toBeInTheDocument();
      const list = within(screen.getByRole("list"));
      if (answerChild)
        expect(list.queryByText("刚发表的回复")).not.toBeInTheDocument();
      else expect(list.getByText("刚发表的回复")).toBeVisible();
    },
  );
  it("pages through the normal comment section and retains it after suspension", async () => {
    const { fetcher, wrapper } = setup();
    const view = render(wrapper());
    await screen.findByText("直接回答当前评论");
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
    fireEvent.click(screen.getByRole("button", { name: "加载更多" }));
    await screen.findByText("第二批回复");
    const loadedCalls = fetcher.mock.calls.length;
    view.rerender(wrapper(false));
    view.rerender(wrapper(true));
    expect(screen.getByText("第二批回复")).toBeVisible();
    expect(fetcher.mock.calls.length).toBe(loadedCalls);
  });
  it("reuses the normal post share menu for the comment body", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal(
      "navigator",
      Object.assign(Object.create(window.navigator), {
        clipboard: { writeText },
      }),
    );
    const { wrapper } = setup();
    render(wrapper());
    await screen.findByText("当前评论全文");
    const shareTrigger = currentPost().getByRole("button", {
      name: "分享这条 Moment",
    });
    shareTrigger.focus();
    fireEvent.keyDown(shareTrigger, { key: "ArrowDown" });
    await waitFor(() =>
      expect(
        screen.getByRole("menuitem", { name: "生成分享图片" }),
      ).toBeVisible(),
    );
    expect(screen.getByRole("menuitem", { name: "复制链接" })).toBeVisible();
    fireEvent.click(screen.getByRole("menuitem", { name: "复制链接" }));
    await waitFor(() =>
      expect(writeText).toHaveBeenCalledWith(
        window.location.origin + "/m/" + postId + "/comments/" + selectedId,
      ),
    );
  });
  it("shows a missing comment without rendering unavailable context", async () => {
    const { wrapper } = setup({ missing: true });
    render(wrapper());
    await screen.findByText("这条评论已不存在或不可访问。");
    await waitFor(() =>
      expect(
        screen
          .getByText("这条评论已不存在或不可访问。")
          .closest('[role="status"]'),
      ).toHaveFocus(),
    );
    expect(
      screen.queryByRole("region", { name: "被回复评论引用" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("link", { name: "返回原帖" })).toHaveAttribute(
      "href",
      "/m/" + postId,
    );
  });
  it("clears an externally deleted target instead of retaining a stale quote", async () => {
    const { wrapper, fetcher } = setup();
    const view = render(wrapper());
    await screen.findByText("当前评论全文");
    const context = await new PublicApi(
      vi.fn(async () =>
        response({
          comment: wire(targetId, "被回复者的评论", rootId),
          reply_target: wire(rootId, "root", null, null),
          direct_reply_count: 1,
          root_comment_id: rootId,
          pinned_comment_id: null,
        }),
      ) as typeof fetch,
    ).commentContext(postId, targetId);
    fetcher.mockImplementation(async (input) =>
      String(input).includes("/replies")
        ? response({ items: [] })
        : response({
            comment: wire(selectedId, "当前评论全文", null),
            reply_target: null,
            root_comment_id: rootId,
            pinned_comment_id: null,
            direct_reply_count: 0,
          }),
    );
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
    expect(screen.queryByText(/未记录具体回复对象/)).not.toBeInTheDocument();
    expect(screen.getByText("当前评论全文")).toBeVisible();
  });
});
