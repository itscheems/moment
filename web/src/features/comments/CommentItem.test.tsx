import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { createRef } from "react";
import defaultAvatarUrl from "../../assets/default-avatar.png";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  PublicApi,
  type PublicComment,
  type PublicCommentAuthor,
  type SiteIdentity,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { SessionProvider } from "../auth/SessionProvider";
import { CommentForm } from "./CommentComposer";
import { CommentItem } from "./CommentItem";
import styles from "./Comments.module.css";
import postStyles from "../posts/PostPresentation.module.css";

const postId = "01K2ABCDEF0123456789ABCDE0";
const rootId = "01K2ABCDEF0123456789ABCDE1";
const replyId = "01K2ABCDEF0123456789ABCDE2";
const siblingId = "01K2ABCDEF0123456789ABCDE3";
const offPageId = "01K2ABCDEF0123456789ABCDE4";
const profileUserId = "11d003f31a23262ea0f6cbe9857981a1";
const strangerId = "22e114g42b34373fb1g7dcfa968a92b2";
const sessionKey = "moment.auth.session.v2";

const site: SiteIdentity = {
  profileUserId,
  username: "itscheems",
  displayName: "Moment 站点",
  title: "乔乔的 Moment",
  bio: "沿着日常留下坐标",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: "01M08A31N0WNK66SC39ZAQFK6M",
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

const ownerAuthor: PublicCommentAuthor = {
  userId: profileUserId,
  username: "itscheems",
  displayName: "Cheems",
  avatarMediaId: "01M08A31N0WNK66SC39ZAQFK6M",
  profileUrl: "/itscheems",
};

const guestAuthor: PublicCommentAuthor = {
  userId: null,
  username: null,
  displayName: "路过的人",
  avatarMediaId: null,
  profileUrl: null,
};

const strangerAuthor: PublicCommentAuthor = {
  userId: strangerId,
  username: "someone",
  displayName: "别人",
  avatarMediaId: "01M08A31N0WNK66SC39ZAQFK6N",
  profileUrl: "/someone",
};

function comment(
  id: string,
  content: string,
  options: Partial<PublicComment> = {},
): PublicComment {
  const parentId = options.parentId ?? null;
  return {
    id,
    postId,
    parentId,
    depth: parentId === null ? 0 : 1,

    authorName: "Owner",
    author: ownerAuthor,
    replyTo: null,
    contentMd: content,
    contentHtml: content ? `<p>${content}</p>\n` : "",
    createdAt: "2026-08-10T01:03:00Z",
    media: [],
    location: null,
    replyCount: 0,
    repliesNextCursor: null,
    ...options,
  };
}

function renderItem(
  target: PublicComment,
  others: PublicComment[] = [],
  overrides: {
    canModerate?: boolean;
    postAuthorUserId?: string;
    pinned?: boolean;
    replyRoot?: PublicComment;
    canReply?: boolean;
  } = {},
) {
  const focusTarget = createRef<HTMLElement>();
  return render(
    <NavigationProvider>
      <SessionProvider>
        <ol>
          {[target, ...others].map((current) => (
            <CommentItem
              comment={current}
              focusTarget={focusTarget}
              key={current.id}
              onDeleted={() => {}}
              onPinChange={() => {}}
              onReply={() => {}}
              canModerate={overrides.canModerate ?? false}
              pinned={overrides.pinned ?? false}
              replyCount={0}
              canReply={overrides.canReply ?? true}
              replyRootId={overrides.replyRoot?.id ?? null}
              site={site}
              postAuthorUserId={overrides.postAuthorUserId ?? profileUserId}
            />
          ))}
        </ol>
      </SessionProvider>
    </NavigationProvider>,
  );
}

function bodyOf(commentId: string) {
  const element = document.querySelector(
    `[data-comment-id="${commentId}"] .ProseMirror`,
  );
  if (!element) throw new Error(`no body for ${commentId}`);
  return element;
}
beforeEach(() => {
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
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

describe("comment identity", () => {
  it("signs a comment with the resolved author rather than the site identity", () => {
    renderItem(comment(rootId, "顶级回应"));

    const name = screen.getByRole("link", { name: "Cheems" });
    const handle = screen.getByRole("link", { name: "@itscheems" });
    expect(name).toHaveAttribute("href", "/itscheems");
    expect(handle).toHaveAttribute("href", "/itscheems");
    expect(screen.queryByText("Moment 站点")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Cheems的回复")).toBeInTheDocument();
    expect(screen.getByText("作者")).toBeInTheDocument();
  });

  it("renders a legacy guest row as plain text with no profile and no badge", () => {
    renderItem(
      comment(rootId, "很久以前的留言", {
        authorName: "路过的人",
        author: guestAuthor,
      }),
    );

    expect(screen.getByText("路过的人")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByText("作者")).not.toBeInTheDocument();
    expect(screen.queryByText(/^@/)).not.toBeInTheDocument();
  });

  it("marks the current post author and never a different account", () => {
    // Same verdict as the retired axis today, and a different one the moment a
    // second account exists — which is the whole reason for moving it.
    const { unmount } = renderItem(comment(rootId, "作者写的", {}));
    expect(screen.getByText("作者")).toBeInTheDocument();
    unmount();

    renderItem(
      comment(rootId, "别人写的", {
        author: strangerAuthor,
      }),
    );
    expect(screen.queryByText("作者")).not.toBeInTheDocument();
  });

  it("does not lend the site identity to an account that is not the owner", () => {
    renderItem(comment(rootId, "别人写的", { author: strangerAuthor }));

    // Linked, because the account has a profile...
    expect(screen.getByRole("link", { name: "别人" })).toHaveAttribute(
      "href",
      "/someone",
    );
    expect(screen.getByRole("link", { name: "@someone" })).toBeInTheDocument();
    const avatar = document.querySelector("img");
    expect(avatar).toHaveAttribute(
      "src",
      expect.stringContaining(strangerAuthor.avatarMediaId as string),
    );
    expect(avatar).not.toHaveAttribute(
      "src",
      expect.stringContaining(site.avatarMediaId as string),
    );
  });

  it("draws a member's own avatar rather than their initial", () => {
    renderItem(comment(rootId, "别人写的", { author: strangerAuthor }));

    expect(screen.queryByText("别")).not.toBeInTheDocument();
    const avatar = screen.getByRole("link", { name: "查看 别人 的个人资料" });
    expect(avatar).toHaveAttribute("href", "/someone");
    expect(avatar.querySelector("img")).toBeInTheDocument();
  });

  it("uses the default avatar image for a member who has no picture", () => {
    renderItem(
      comment(rootId, "别人写的", {
        author: { ...strangerAuthor, avatarMediaId: null },
      }),
    );

    expect(document.querySelector("img")).toHaveAttribute(
      "src",
      defaultAvatarUrl,
    );
    expect(screen.queryByText("别")).not.toBeInTheDocument();
  });
});

it("marks the post author even when that account is not the site's display account", () => {
  const view = renderItem(
    comment(rootId, "实际作者", { author: strangerAuthor }),
    [],
    { postAuthorUserId: strangerId },
  );
  expect(screen.getByText("作者")).toBeVisible();
  view.unmount();
  renderItem(comment(rootId, "展示账号写的"), [], {
    postAuthorUserId: strangerId,
  });
  expect(screen.queryByText("作者")).toBeNull();
});

describe("long comment folding", () => {
  it("uses the post preview and reveals complete paragraphs in place", () => {
    const text = "这是开头。".repeat(12),
      tail = "后续评论正文".repeat(40);
    renderItem(
      comment(rootId, text + "\n\n" + tail, {
        contentHtml: `<p>${text}</p><p>${tail}</p>`,
      }),
    );
    expect(bodyOf(rootId)).toHaveTextContent(text);
    expect(bodyOf(rootId)).not.toHaveTextContent(tail);
    const more = screen.getByRole("button", { name: "显示更多" });
    fireEvent.click(more);
    expect(bodyOf(rootId)).toHaveTextContent(tail);
    expect(screen.queryByRole("button", { name: "显示更多" })).toBeNull();
    expect(screen.queryByRole("button", { name: "收起" })).toBeNull();
    expect(screen.queryByRole("button", { name: "展开全文" })).toBeNull();
  });
  it("does not offer Show more for short comments", () => {
    renderItem(comment(rootId, "简短回复"));
    expect(bodyOf(rootId)).toHaveTextContent("简短回复");
    expect(screen.queryByRole("button", { name: "显示更多" })).toBeNull();
  });
  it("uses the existing hard clip for one long paragraph, with no fade", () => {
    renderItem(comment(rootId, "长段落".repeat(160)));
    expect(bodyOf(rootId)).toHaveClass(postStyles.clippedPreview);
    fireEvent.click(screen.getByRole("button", { name: "显示更多" }));
    expect(bodyOf(rootId)).not.toHaveClass(postStyles.clippedPreview);
  });
  it("keeps media and location outside the preview", () => {
    renderItem(
      comment(rootId, "正文".repeat(200), {
        location: { name: "陆悦天地", displayName: "陆悦天地" },
        media: [
          {
            mediaId: "01M08A31N0WNK66SC39ZAQFK6P",
            position: 0,
            altText: "评论图片",
            mimeType: "image/png",
            width: 800,
            height: 600,
          },
        ],
      }),
    );
    expect(
      bodyOf(rootId).contains(screen.getByLabelText("地点：陆悦天地")),
    ).toBe(false);
    expect(bodyOf(rootId).contains(screen.getByAltText("评论图片"))).toBe(
      false,
    );
  });
});

describe("the reply target identity", () => {
  it("distinguishes matching display names by username and profile", () => {
    renderItem(
      comment(replyId, "同名用户之间的回复", {
        parentId: rootId,
        author: { ...strangerAuthor, displayName: "Cheems" },
        replyTo: { commentId: rootId, author: ownerAuthor },
      }),
    );
    const header = document.querySelector(
      "[data-comment-id='" + replyId + "'] header",
    ) as HTMLElement;
    expect(within(header).getByText("@someone")).toHaveAttribute(
      "href",
      "/someone",
    );
    expect(within(header).queryByText("@itscheems")).toBeNull();
    expect(
      within(header)
        .getAllByRole("link", { name: "Cheems" })
        .map((link) => link.getAttribute("href")),
    ).toEqual(["/someone", "/itscheems"]);
  });
  function replyNaming(targetId: string, author = ownerAuthor) {
    return comment(replyId, "接着说", {
      parentId: rootId,
      replyTo: { commentId: targetId, author },
    });
  }
  it.each([rootId, siblingId, offPageId])(
    "shows the target's name and profile even when it is the root or off-page: %s",
    (targetId) => {
      renderItem(replyNaming(targetId));
      const target = document.querySelector(
        `.${styles.commentReplyTo}`,
      ) as HTMLElement;
      expect(target.textContent).toBe("回复Cheems");
      expect(
        within(target).getByRole("link", { name: "Cheems" }),
      ).toHaveAttribute("href", "/itscheems");
      expect(target.querySelector("img")).toBeNull();
    },
  );
  it("omits a reply label when no specific target is recorded", () => {
    renderItem(comment(replyId, "接着说", { parentId: rootId }));
    expect(screen.queryByText("楼内回复")).not.toBeInTheDocument();
    expect(document.querySelector(`.${styles.commentReplyTo}`)).toBeNull();
    expect(screen.getByText("接着说")).toBeInTheDocument();
  });
  it("preserves a historical signature without inventing an account", () => {
    renderItem(replyNaming(siblingId, guestAuthor));
    const target = document.querySelector(
      `.${styles.commentReplyTo}`,
    ) as HTMLElement;
    expect(target).toHaveTextContent("路过的人");
    expect(target.querySelector("a")).toBeNull();
  });
});

describe("the composer's reply target", () => {
  function installApi(handler: (url: string, init?: RequestInit) => Response) {
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) =>
        handler(String(input), init),
    );
    vi.stubGlobal("fetch", fetcher);
    return fetcher;
  }

  function createdBody(id: string) {
    return JSON.stringify({
      data: {
        id,
        post_id: postId,
        parent_id: rootId,
        depth: 1,

        author_name: "Owner",
        author: {
          user_id: profileUserId,
          username: "itscheems",
          display_name: "Cheems",
          avatar_media_id: null,
          profile_url: "/itscheems",
        },
        reply_to: { comment_id: siblingId, author: null },
        content_md: "接着说",
        content_html: "<p>接着说</p>\n",
        created_at: "2026-08-10T01:05:00Z",
        media: [],
        location: null,
        reply_count: 0,
      },
      meta: { request_id: "req_test", replayed: false },
    });
  }

  async function renderComposer(replyTargetId: string) {
    window.sessionStorage.setItem(
      sessionKey,
      JSON.stringify({
        csrf_token: "csrf-test",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    const posted: Array<Record<string, unknown>> = [];
    installApi((url, init) => {
      if (url === "/api/v1/status") {
        return new Response(
          JSON.stringify({
            data: {
              initialized: true,
              password_enabled: false,
              session_epoch: 1,
              initialized_at: "2026-08-01T00:00:00Z",
            },
            meta: { request_id: "req_test" },
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        );
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        posted.push(JSON.parse(String(init.body)) as Record<string, unknown>);
        return new Response(createdBody(replyId), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        });
      }
      return new Response("{}", { status: 404 });
    });

    render(
      <NavigationProvider>
        <SessionProvider>
          <CommentForm
            api={new PublicApi()}
            commentsEnabled
            content="接着说"
            onClosed={() => {}}
            onCreated={() => {}}
            parentId={rootId}
            postId={postId}
            replyTargetAuthor="Cheems"
            replyTargetId={replyTargetId}
            setContent={() => {}}
            site={site}
          />
        </SessionProvider>
      </NavigationProvider>,
    );
    return posted;
  }

  it("persists the comment the reply answers instead of only announcing it", async () => {
    const posted = await renderComposer(siblingId);
    const form = await screen.findByRole("form", { name: "回复 Cheems" });

    await act(async () => {
      fireEvent.submit(form);
    });

    expect(posted).toHaveLength(1);
    expect(posted[0]).toMatchObject({
      parent_id: rootId,
      reply_to_comment_id: siblingId,
    });
  });

  it("answers the root when the reply names no other comment", async () => {
    const posted = await renderComposer(rootId);
    const form = await screen.findByRole("form", { name: "回复 Cheems" });

    await act(async () => {
      fireEvent.submit(form);
    });

    expect(posted[0]).toMatchObject({ reply_to_comment_id: rootId });
  });
});

describe("comment action bar", () => {
  it("keeps sharing available without a reply target", () => {
    // Sharing remains available even when this embedding has no reply target.
    renderItem(comment(replyId, "孤儿回复", { parentId: offPageId }));

    expect(screen.queryByRole("button", { name: /^回复/ })).toBeNull();
    expect(
      screen.getByRole("button", { name: "分享 Cheems 的评论" }),
    ).toBeVisible();
  });

  // Being signed out is not the same as having nothing to reply to. The action
  // still means something, so it stays and explains itself when pressed.
  it("keeps a child reply count visible while signed out", () => {
    const root = comment(rootId, "楼层");
    renderItem(
      comment(replyId, "已有回复", { parentId: rootId, replyCount: 3 }),
      [],
      { replyRoot: root, canReply: false },
    );
    expect(
      within(
        screen.getByRole("button", { name: "回复 Cheems，3 条回复" }),
      ).getByText("3"),
    ).toBeVisible();
  });
  it("keeps the reply action while signed out and answers with a sign-in card", async () => {
    const root = comment(rootId, "顶级回应");
    renderItem(root, [], { replyRoot: root, canReply: false });

    const action = screen.getByRole("button", { name: /^回复/ });
    expect(action).toBeInTheDocument();

    fireEvent.click(action);
    expect(window.location.pathname).toBe("/login");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(
      new URLSearchParams(window.location.search).get("return"),
    ).toBeTruthy();
  });

  it("keeps the reply action when a reply target is available", () => {
    const root = comment(rootId, "顶级回应");
    renderItem(root, [], { replyRoot: root });

    const footer = document.querySelector("[data-post-actions]");
    expect(footer).not.toBeNull();
    expect(
      within(footer as HTMLElement).getByRole("button", { name: /^回复/ }),
    ).toBeInTheDocument();
  });
});
