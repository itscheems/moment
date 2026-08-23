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
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  PublicApi,
  type PublicComment,
  type PublicCommentAuthor,
  type SiteIdentity,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { OwnerProvider } from "../owner/OwnerProvider";
import { CommentForm } from "./CommentComposer";
import { CommentItem } from "./CommentItem";
import styles from "./Comments.module.css";

const postId = "01K2ABCDEF0123456789ABCDE0";
const rootId = "01K2ABCDEF0123456789ABCDE1";
const replyId = "01K2ABCDEF0123456789ABCDE2";
const siblingId = "01K2ABCDEF0123456789ABCDE3";
const offPageId = "01K2ABCDEF0123456789ABCDE4";
const ownerId = "11d003f31a23262ea0f6cbe9857981a1";
const strangerId = "22e114g42b34373fb1g7dcfa968a92b2";
const sessionKey = "moment.owner.session.v1";

const site: SiteIdentity = {
  ownerId,
  username: "itscheems",
  // Deliberately different from the author projection below. Reading the site
  // for an owner comment is the bug being removed, so the two have to disagree
  // for any of these tests to prove anything.
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
  userId: ownerId,
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
    authorKind: "owner",
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

/**
 * jsdom does no layout, so `scrollHeight` and `clientHeight` are both 0 there
 * and the component would never see an overflow. These stubs let a test say how
 * tall a given body box is; everything downstream of the measurement — the
 * control, the aria wiring, the focus handling — is then exercised for real.
 * The rendered-height judgement itself was verified in Chrome against the real
 * corpus, which is the only place it can be.
 */
const measured = new Map<string, { scroll: number; client: number }>();

function measure(id: string, scroll: number, client = 200) {
  measured.set(id, { scroll, client });
}

function stubLayout() {
  for (const property of ["scrollHeight", "clientHeight"] as const) {
    Object.defineProperty(HTMLElement.prototype, property, {
      configurable: true,
      get(this: HTMLElement) {
        const entry = measured.get(this.id);
        if (!entry) return 0;
        return property === "scrollHeight" ? entry.scroll : entry.client;
      },
    });
  }
}

class FakeResizeObserver implements ResizeObserver {
  static instances = new Set<FakeResizeObserver>();

  constructor(private readonly callback: ResizeObserverCallback) {
    FakeResizeObserver.instances.add(this);
  }

  observe() {}

  unobserve() {}

  disconnect() {
    FakeResizeObserver.instances.delete(this);
  }

  static notify() {
    for (const observer of FakeResizeObserver.instances) {
      observer.callback([], observer);
    }
  }
}

function renderItem(
  target: PublicComment,
  others: PublicComment[] = [],
  overrides: {
    owner?: boolean;
    pinned?: boolean;
    replyRoot?: PublicComment;
    canReply?: boolean;
  } = {},
) {
  const focusTarget = createRef<HTMLElement>();
  return render(
    <NavigationProvider>
      <ol>
        {[target, ...others].map((current) => (
          <CommentItem
            comment={current}
            focusTarget={focusTarget}
            key={current.id}
            onDeleted={() => {}}
            onPinChange={() => {}}
            onReply={() => {}}
            owner={overrides.owner ?? false}
            pinned={overrides.pinned ?? false}
            replyCount={0}
            canReply={overrides.canReply ?? true}
            replyRoot={overrides.replyRoot ?? null}
            site={site}
          />
        ))}
      </ol>
    </NavigationProvider>,
  );
}

function bodyOf(commentId: string) {
  const element = document.getElementById(`comment-${commentId}-body`);
  if (!element) throw new Error(`no body for ${commentId}`);
  return element;
}

beforeEach(() => {
  measured.clear();
  FakeResizeObserver.instances.clear();
  stubLayout();
  vi.stubGlobal("ResizeObserver", FakeResizeObserver);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
  for (const property of ["scrollHeight", "clientHeight"] as const) {
    Reflect.deleteProperty(HTMLElement.prototype, property);
  }
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
    expect(screen.getByText("所有者")).toBeInTheDocument();
  });

  it("renders a legacy guest row as plain text with no profile and no badge", () => {
    renderItem(
      comment(rootId, "很久以前的留言", {
        authorKind: "guest",
        authorName: "路过的人",
        author: guestAuthor,
      }),
    );

    expect(screen.getByText("路过的人")).toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
    expect(screen.queryByText("所有者")).not.toBeInTheDocument();
    expect(screen.queryByText(/^@/)).not.toBeInTheDocument();
  });

  it("decides the badge on the account id, not on author_kind", () => {
    // Same verdict as the retired axis today, and a different one the moment a
    // second account exists — which is the whole reason for moving it.
    const { unmount } = renderItem(
      comment(rootId, "所有者写的", { authorKind: "guest" }),
    );
    expect(screen.getByText("所有者")).toBeInTheDocument();
    unmount();

    renderItem(
      comment(rootId, "别人写的", {
        authorKind: "owner",
        author: strangerAuthor,
      }),
    );
    expect(screen.queryByText("所有者")).not.toBeInTheDocument();
  });

  it("does not lend the site identity to an account that is not the owner", () => {
    renderItem(comment(rootId, "别人写的", { author: strangerAuthor }));

    // Linked, because the account has a profile...
    expect(screen.getByRole("link", { name: "别人" })).toHaveAttribute(
      "href",
      "/someone",
    );
    expect(screen.getByRole("link", { name: "@someone" })).toBeInTheDocument();
    // ...but nothing of this site's is borrowed for it. The face is the
    // author's own media, never the site avatar, and there is no preview card
    // trigger, which would show the owner's cover and bio under this name.
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

  // The server already resolves and ships avatar_media_id for every author.
  // Deciding the face on "is this the site owner" discarded it for everybody
  // else, so every member wore a letter beside their own picture.
  it("draws a member's own avatar rather than their initial", () => {
    renderItem(comment(rootId, "别人写的", { author: strangerAuthor }));

    expect(screen.queryByText("别")).not.toBeInTheDocument();
    const avatar = screen.getByRole("link", { name: "查看 别人 的个人资料" });
    expect(avatar).toHaveAttribute("href", "/someone");
    expect(avatar.querySelector("img")).toBeInTheDocument();
  });

  it("falls back to the initial for a member who has no picture", () => {
    renderItem(
      comment(rootId, "别人写的", {
        author: { ...strangerAuthor, avatarMediaId: null },
      }),
    );

    expect(document.querySelector("img")).toBeNull();
    expect(screen.getByText("别")).toBeInTheDocument();
  });
});

describe("long comment folding", () => {
  it("clips every body and offers the control only where something is hidden", () => {
    const long = comment(rootId, "很长的一段");
    const short = comment(replyId, "短", { parentId: rootId });
    measure(`comment-${rootId}-body`, 983);
    measure(`comment-${replyId}-body`, 23, 23);
    renderItem(long, [short]);

    expect(bodyOf(rootId)).toHaveClass(styles.commentBodyClipped);
    expect(bodyOf(replyId)).toHaveClass(styles.commentBodyClipped);
    expect(screen.getAllByRole("button", { name: "展开全文" })).toHaveLength(1);
  });

  // The fade is what the clip looks like when it has something to hide. Bound
  // to the clip instead, a body that fits inside the maximum had its last line
  // faded out anyway -- reading as cut off, with nothing offered to open it.
  it("fades only the body that has something hidden behind the fade", () => {
    const long = comment(rootId, "很长的一段");
    const short = comment(replyId, "短", { parentId: rootId });
    measure(`comment-${rootId}-body`, 983);
    measure(`comment-${replyId}-body`, 23, 23);
    renderItem(long, [short]);

    expect(bodyOf(rootId)).toHaveClass(styles.commentBodyFaded);
    expect(bodyOf(replyId)).not.toHaveClass(styles.commentBodyFaded);
  });

  it("takes the fade away with the clip when the body is opened", () => {
    measure(`comment-${rootId}-body`, 983);
    renderItem(comment(rootId, "很长的一段"));

    expect(bodyOf(rootId)).toHaveClass(styles.commentBodyFaded);
    fireEvent.click(screen.getByRole("button", { name: "展开全文" }));
    expect(bodyOf(rootId)).not.toHaveClass(styles.commentBodyFaded);
  });

  it("wires the control to the body it controls and reverses cleanly", () => {
    measure(`comment-${rootId}-body`, 983);
    renderItem(comment(rootId, "很长的一段"));

    const toggle = screen.getByRole("button", { name: "展开全文" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveAttribute("aria-controls", `comment-${rootId}-body`);

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(toggle).toHaveTextContent("收起");
    expect(bodyOf(rootId)).not.toHaveClass(styles.commentBodyClipped);

    fireEvent.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    expect(toggle).toHaveTextContent("展开全文");
    expect(bodyOf(rootId)).toHaveClass(styles.commentBodyClipped);
  });

  it("opens the body when focus lands on a link the clip is hiding", () => {
    measure(`comment-${rootId}-body`, 983);
    renderItem(
      comment(rootId, "", {
        contentMd: "见 [文档](https://example.com)",
        contentHtml: '<p>见 <a href="https://example.com">文档</a></p>\n',
      }),
    );

    const hidden = screen.getByRole("link", { name: "文档" });
    expect(bodyOf(rootId)).toHaveClass(styles.commentBodyClipped);
    fireEvent.focus(hidden);
    expect(bodyOf(rootId)).not.toHaveClass(styles.commentBodyClipped);
  });

  it("folds the body only, leaving media and location whole", () => {
    measure(`comment-${rootId}-body`, 983);
    renderItem(
      comment(rootId, "很长的一段", {
        location: { name: "陆悦天地", displayName: "陆悦天地" },
        media: [
          {
            mediaId: "01M08A31N0WNK66SC39ZAQFK6P",
            position: 0,
            altText: null,
            mimeType: "image/png",
            width: 800,
            height: 600,
          },
        ],
      }),
    );

    const body = bodyOf(rootId);
    const location = screen.getByLabelText("地点：陆悦天地");
    expect(body.contains(location)).toBe(false);
    const image = document.querySelector("img");
    expect(image).not.toBeNull();
    expect(body.contains(image)).toBe(false);
  });

  it("re-measures when the box changes instead of trusting the first reading", () => {
    measure(`comment-${rootId}-body`, 100);
    renderItem(comment(rootId, "刚好放得下"));
    expect(screen.queryByRole("button", { name: "展开全文" })).toBeNull();

    // The same comment in a narrower column: whether it folds is a property of
    // the rendered box, not of the text.
    measure(`comment-${rootId}-body`, 320);
    act(() => FakeResizeObserver.notify());
    expect(
      screen.getByRole("button", { name: "展开全文" }),
    ).toBeInTheDocument();

    measure(`comment-${rootId}-body`, 100);
    act(() => FakeResizeObserver.notify());
    expect(screen.queryByRole("button", { name: "展开全文" })).toBeNull();
  });

  it("keeps the reader in place when collapsing pulls the page up", () => {
    measure(`comment-${rootId}-body`, 983);
    renderItem(comment(rootId, "很长的一段"));
    const toggle = screen.getByRole("button", { name: "展开全文" });
    fireEvent.click(toggle);

    const header = document.querySelector("header");
    const scrollIntoView = vi.fn();
    Object.defineProperty(header, "scrollIntoView", {
      configurable: true,
      value: scrollIntoView,
    });
    Object.defineProperty(header, "getBoundingClientRect", {
      configurable: true,
      value: () => ({ top: -420 }) as DOMRect,
    });

    fireEvent.click(toggle);
    expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" });
  });

  it("leaves a comment alone when collapsing costs the reader nothing", () => {
    measure(`comment-${rootId}-body`, 983);
    renderItem(comment(rootId, "很长的一段"));
    const toggle = screen.getByRole("button", { name: "展开全文" });
    fireEvent.click(toggle);

    const header = document.querySelector("header");
    const scrollIntoView = vi.fn();
    Object.defineProperty(header, "scrollIntoView", {
      configurable: true,
      value: scrollIntoView,
    });

    fireEvent.click(toggle);
    expect(scrollIntoView).not.toHaveBeenCalled();
  });
});

describe("the reply target prefix", () => {
  const sibling = comment(siblingId, "先来的兄弟回复", { parentId: rootId });

  function replyNaming(targetId: string, author = ownerAuthor) {
    return comment(replyId, "接着说", {
      parentId: rootId,
      replyTo: { commentId: targetId, author },
    });
  }

  it("names the target when the reply answers a sibling", () => {
    renderItem(replyNaming(siblingId), [sibling]);

    expect(screen.getByText("回复")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "@itscheems" }),
    ).toBeInTheDocument();
  });

  it("stays quiet when the reply answers the root its position already names", () => {
    renderItem(replyNaming(rootId), [comment(rootId, "楼主")]);

    expect(screen.queryByText("回复")).not.toBeInTheDocument();
  });

  it("stays quiet when the comment names nobody", () => {
    renderItem(comment(replyId, "接着说", { parentId: rootId }));

    expect(screen.queryByText("回复")).not.toBeInTheDocument();
  });

  it("focuses and highlights a target that is on the page", () => {
    renderItem(replyNaming(siblingId), [sibling]);
    const target = document.querySelector(`[data-comment-id="${siblingId}"]`);

    fireEvent.click(screen.getByRole("button", { name: "@itscheems" }));

    expect(target).toHaveFocus();
    expect(target).toHaveAttribute("data-comment-flash", "true");
  });

  it("degrades to plain text when the target is not on the page", () => {
    renderItem(replyNaming(offPageId));

    const prefix = document.querySelector(`.${styles.commentReplyTo}`);
    expect(prefix).toHaveTextContent("回复 @itscheems");
    expect(prefix?.querySelector("button")).toBeNull();
  });

  it("falls back to the stored name for a legacy guest target", () => {
    // Not `@undefined`, and not a control either: a row with no account has no
    // handle to print and nothing to open.
    renderItem(replyNaming(siblingId, guestAuthor), [sibling]);

    expect(screen.getByText("路过的人")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /路过的人/ })).toBeNull();
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
        author_kind: "owner",
        author_name: "Owner",
        author: {
          user_id: ownerId,
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
      if (url === "/api/v1/owner/status") {
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
        <OwnerProvider>
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
        </OwnerProvider>
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

// A signed-out reader has no reply button, because the prompt above the thread
// already says why once and repeating it on every row is noise. What must not
// survive is the row it used to sit in: an empty action bar still claims its
// full target height, and a band of blank space under every comment reads as
// something failing to load rather than as nothing being on offer.
describe("comment action bar", () => {
  it("renders no action bar when there is nothing to reply to", () => {
    // A reply whose root is not on the page: the action has no target, so it
    // has no meaning either, and an empty bar would leave dead space.
    renderItem(comment(replyId, "孤儿回复", { parentId: offPageId }));

    expect(document.querySelector("footer")).toBeNull();
    expect(
      screen.queryByRole("button", { name: /^回复/ }),
    ).not.toBeInTheDocument();
  });

  // Being signed out is not the same as having nothing to reply to. The action
  // still means something, so it stays and explains itself when pressed.
  it("keeps the reply action while signed out and answers with a sign-in card", async () => {
    const root = comment(rootId, "顶级回应");
    renderItem(root, [], { replyRoot: root, canReply: false });

    const action = screen.getByRole("button", { name: /^回复/ });
    expect(action).toBeInTheDocument();

    fireEvent.click(action);
    const prompt = await screen.findByRole("dialog");
    // jsdom does not run the popup's entry transition, so presence is the
    // meaningful assertion here; the rendered card is checked in a browser.
    expect(within(prompt).getByText("登录后回复")).toBeInTheDocument();
    expect(within(prompt).getByRole("link", { name: "登录" })).toHaveAttribute(
      "href",
      "/login",
    );
  });

  it("keeps the reply action when a reply target is available", () => {
    const root = comment(rootId, "顶级回应");
    renderItem(root, [], { replyRoot: root });

    const footer = document.querySelector("footer");
    expect(footer).not.toBeNull();
    expect(
      within(footer as HTMLElement).getByRole("button", { name: /^回复/ }),
    ).toBeInTheDocument();
  });
});
