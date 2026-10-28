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
import { useRef, useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { mediaPreviewUrl } from "../../api/mediaUrls";
import {
  PublicApi,
  PublicApiError,
  parseComment,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { useLoginNavigation } from "../auth/loginNavigation";
import { NavigationProvider, useNavigation } from "../../app/navigation";
import { SessionProvider, useSession } from "../auth/SessionProvider";
import { CommentThread } from "./CommentThread";
import type { DiscussionChange } from "./discussionChanges";
import commentsCss from "./Comments.module.css?inline";
import { makeAccountLimits } from "../../test/accountFixtures";

const postId = "01K2ABCDEF0123456789ABCDE0";
const rootId = "01K2ABCDEF0123456789ABCDE1";
const replyId = "01K2ABCDEF0123456789ABCDE2";
const createdId = "01K2ABCDEF0123456789ABCDE3";
const sessionKey = "moment.auth.session.v2";

const site: SiteIdentity = {
  profileUserId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "乔乔的 Moment",
  bio: "沿着日常留下坐标",
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
  author: {
    userId: "00000000000000000000000000000000",
    username: "owner",
    displayName: "Moment",
    avatarMediaId: null,
    profileUrl: "/owner",
  },
  id: postId,
  kind: "moment",
  contentMd: "一段需要回应的 Moment",
  article: null,
  contentHtml: "<p>一段需要回应的 Moment</p>\n",
  createdAt: "2026-08-10T01:00:00Z",
  updatedAt: "2026-08-10T01:00:00Z",
  publishedAt: "2026-08-10T01:00:00Z",
  repostedAt: null,
  visibility: "everyone",
  timelineItemKind: "original",
  commentCount: 0,
  media: [],
  linkCards: [],
};

function comment(
  id: string,
  content: string,
  options: {
    parentId?: string | null;
    canPublish?: boolean;
    authorName?: string;
    createdAt?: string;
    media?: Array<{
      media_id: string;
      position: number;
      alt_text: string | null;
      mime_type?: string;
      width: number;
      height: number;
    }>;
    location?: { name: string; display_name: string } | null;
  } = {},
) {
  const parentId = options.parentId ?? null;
  const authorName =
    options.authorName ?? (options.canPublish ? "Owner" : "访客");
  return {
    id,
    post_id: postId,
    parent_id: parentId,
    depth: parentId ? 1 : 0,

    author_name: authorName,
    author: {
      user_id: options.canPublish ? site.profileUserId : null,
      username: options.canPublish ? site.username : null,
      display_name: options.canPublish ? site.displayName : authorName,
      avatar_media_id: null,
      profile_url: options.canPublish ? `/${site.username}` : null,
    },
    reply_to: null,
    reply_count: 0,
    content_md: content,
    content_html: content ? `<p>${content}</p>\n` : "",
    created_at: options.createdAt ?? "2026-08-10T01:03:00Z",
    media: (options.media ?? []).map((usage) => ({
      ...usage,
      mime_type: usage.mime_type ?? "image/png",
    })),
    location: options.location ?? null,
  };
}

class FakeCommentUploadRequest extends EventTarget {
  static last: FakeCommentUploadRequest | null = null;

  readonly upload = new EventTarget();
  method = "";
  url = "";
  withCredentials = false;
  status = 0;
  responseText = "";
  responseType: XMLHttpRequestResponseType = "";
  sent: Document | XMLHttpRequestBodyInit | null = null;

  constructor() {
    super();
    FakeCommentUploadRequest.last = this;
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader() {}

  getResponseHeader(name: string) {
    return name.toLowerCase() === "content-type" ? "application/json" : null;
  }

  send(body: Document | XMLHttpRequestBodyInit | null) {
    this.sent = body;
  }

  abort() {
    this.dispatchEvent(new Event("abort"));
    this.dispatchEvent(new Event("loadend"));
  }

  fail() {
    this.dispatchEvent(new Event("error"));
    this.dispatchEvent(new Event("loadend"));
  }

  respond(body: unknown) {
    this.status = 201;
    this.responseText = JSON.stringify(body);
    this.dispatchEvent(new Event("load"));
    this.dispatchEvent(new Event("loadend"));
  }
}

class FakeCommentResizeObserver implements ResizeObserver {
  static instances = new Set<FakeCommentResizeObserver>();

  constructor(private readonly callback: ResizeObserverCallback) {
    FakeCommentResizeObserver.instances.add(this);
  }

  observe() {}

  unobserve() {}

  disconnect() {
    FakeCommentResizeObserver.instances.delete(this);
  }

  static notify() {
    for (const observer of FakeCommentResizeObserver.instances) {
      observer.callback([], observer);
    }
  }

  static reset() {
    FakeCommentResizeObserver.instances.clear();
  }
}

function measuredRect(height: number): DOMRect {
  return {
    bottom: height,
    height,
    left: 0,
    right: 640,
    top: 0,
    width: 640,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  };
}

const root = comment(rootId, "顶级回应");
const reply = comment(replyId, "已有一级回复", {
  parentId: rootId,
  canPublish: true,
  createdAt: "2026-08-10T01:04:00Z",
});

function json(body: unknown, status = 200, headers?: HeadersInit): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", ...headers },
  });
}

function success(
  data: unknown,
  meta: Record<string, unknown> = {},
  status = 200,
) {
  const thread =
    typeof data === "object" &&
    data !== null &&
    !Array.isArray(data) &&
    "items" in data;
  const canonicalData = thread
    ? { pinned_comment_id: null, pinned: null, ...data }
    : data;
  return json(
    {
      data: canonicalData,
      meta: thread
        ? { request_id: "req_test", next_cursor: null, ...meta }
        : { request_id: "req_test", ...meta },
    },
    status,
  );
}

function failure(code: string, status: number, details = {}) {
  return json(
    {
      error: {
        code,
        message: "stable code wins",
        details,
        request_id: "req_error",
      },
    },
    status,
  );
}

function unauthorized() {
  return failure("UNAUTHORIZED", 401);
}

function ownerStatus() {
  return success({
    initialized: true,
    password_enabled: false,
    session_epoch: 1,
    initialized_at: "2026-08-01T00:00:00Z",
  });
}

function preauthenticate() {
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({
      csrf_token: "csrf-old",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
}

function accountSessionEnvelope(username = "owner") {
  return {
    data: {
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: {
        user_id: "00000000000000000000000000000000",
        username,
        display_name: "乔乔",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: `/${username}`,
        status: "active",
        username_chosen: true,
      },
      limits: makeAccountLimits(),
    },
    meta: { request_id: "req_account_session" },
  };
}

function signedOutEnvelope() {
  return {
    error: {
      code: "UNAUTHORIZED",
      message: "auth required",
      details: {},
      request_id: "req_auth",
    },
  };
}

function installApi(
  handler: (
    url: string,
    init: RequestInit | undefined,
  ) => Response | Promise<Response> | undefined,
) {
  let cookieAuthenticated = Boolean(window.sessionStorage.getItem(sessionKey));
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const response = await handler(url, init);
      if (
        url === "/api/v1/login/session" &&
        init?.method === "POST" &&
        response?.ok
      )
        cookieAuthenticated = true;
      if (response) return response;
      if (url === "/api/v1/account/session") {
        return cookieAuthenticated
          ? json(accountSessionEnvelope())
          : json(signedOutEnvelope(), 401);
      }
      if (url === "/api/v1/status") {
        // SessionProvider throws away a stored session the moment status answers
        // 401, so a preauthenticated test has to be answered as signed in
        // unless it deliberately handles this route itself.
        return window.sessionStorage.getItem(sessionKey)
          ? ownerStatus()
          : unauthorized();
      }
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

let completeStandaloneSignIn: () => Promise<void>;

function ExpireOwnerControl() {
  const auth = useSession();
  const navigation = useNavigation();
  const { requestLogin } = useLoginNavigation();
  // Complete the same provider exchange as the standalone page; this fixture
  // exercises retained comment state rather than rendering the application router.
  completeStandaloneSignIn = async () => {
    await auth.login("verified-page-token", "token");
    const target =
      new URLSearchParams(window.location.search).get("return") ?? "/";
    navigation.replace(target);
  };
  return (
    <button
      onClick={() => {
        auth.expireIfInvalidSession(
          new PublicApiError("expired", {
            code: "UNAUTHORIZED",
            status: 401,
          }),
        );
        requestLogin({ replace: true });
      }}
      type="button"
    >
      使会话过期
    </button>
  );
}

function renderThread(
  currentSite = site,
  withExpireControl = false,
  onCountChange?: (count: number) => void,
) {
  return render(
    <NavigationProvider>
      <SessionProvider>
        {withExpireControl ? <ExpireOwnerControl /> : null}
        <CommentThread
          api={new PublicApi()}
          onCountChange={onCountChange}
          postId={postId}
          post={post}
          site={currentSite}
        />
      </SessionProvider>
    </NavigationProvider>,
  );
}

async function openCommentMenu() {
  const trigger = screen.getAllByRole("button", { name: "回复操作" })[0];
  await waitFor(() => expect(trigger).toBeEnabled());
  await act(async () => {
    trigger.focus();
    fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
  });
  await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "true"));
}

async function chooseFirstCommentAction(name: string) {
  await openCommentMenu();
  fireEvent.click(
    await screen.findByRole("menuitem", { name }, { timeout: 3_000 }),
  );
}

function ReplyDialogHarness() {
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const apiRef = useRef(new PublicApi());

  return (
    <>
      <button ref={triggerRef} onClick={() => setOpen(true)} type="button">
        打开回复窗口
      </button>
      <CommentThread
        api={apiRef.current}
        onReplyDialogOpenChange={setOpen}
        post={post}
        postId={postId}
        replyDialogOpen={open}
        replyDialogTriggerRef={triggerRef}
        site={site}
      />
    </>
  );
}

describe("public comment thread", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", `/m/${postId}`);
    window.sessionStorage.clear();
    FakeCommentResizeObserver.reset();
    vi.stubGlobal("ResizeObserver", FakeCommentResizeObserver);
    vi.stubGlobal(
      "requestAnimationFrame",
      vi.fn((callback: FrameRequestCallback) => {
        callback(0);
        return 1;
      }),
    );
    vi.stubGlobal("cancelAnimationFrame", vi.fn());
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("hides time sorting for an empty thread", async () => {
    installApi((url) =>
      url.endsWith("/comments") ? success({ items: [] }) : undefined,
    );
    renderThread();
    await screen.findByText("登录后回复");
    expect(
      screen.queryByRole("button", { name: /回复排序/ }),
    ).not.toBeInTheDocument();
  });

  it("requests newest first and keeps the previous view when a sort request fails", async () => {
    const oldest = comment(rootId, "early reply", {
      createdAt: "2026-08-10T01:00:00Z",
    });
    const newest = comment(replyId, "late reply", {
      createdAt: "2026-08-10T02:00:00Z",
    });
    let fail = false;
    installApi((url) => {
      if (url.includes("/comments?sort=newest"))
        return fail
          ? json(
              {
                error: {
                  code: "INVALID_ARGUMENT",
                  message: "retry",
                  request_id: "sort-error",
                  details: {},
                },
              },
              400,
            )
          : success({ items: [newest, oldest] });
      if (url.endsWith("/comments"))
        return success({ items: [oldest, newest] });
      return undefined;
    });
    renderThread();
    const trigger = await screen.findByRole("button", {
      name: "回复排序：最早",
    });
    const open = async () => {
      fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
      await screen.findByRole("menuitemradio", { name: "最新" });
    };
    await open();
    fireEvent.click(screen.getByRole("menuitemradio", { name: "最新" }));
    await screen.findByRole("button", { name: "回复排序：最新" });
    expect(
      [...document.querySelectorAll("[data-comment-id]")].map((el) =>
        el.getAttribute("data-comment-id"),
      ),
    ).toEqual([replyId, rootId]);
    fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
    fireEvent.click(await screen.findByRole("menuitemradio", { name: "最早" }));
    await screen.findByRole("button", { name: "回复排序：最早" });
    fail = true;
    await open();
    fireEvent.click(screen.getByRole("menuitemradio", { name: "最新" }));
    await screen.findByRole("alert");
    expect(trigger).toHaveTextContent("最早");
    expect(
      [...document.querySelectorAll("[data-comment-id]")].map((el) =>
        el.getAttribute("data-comment-id"),
      ),
    ).toEqual([rootId, replyId]);
  });

  it("loads nothing while inactive and preserves the draft and loaded thread when resumed", async () => {
    preauthenticate();
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root, reply] });
      }
    });
    const api = new PublicApi();
    const show = (active: boolean) => (
      <NavigationProvider>
        <SessionProvider>
          <CommentThread
            active={active}
            api={api}
            postId={postId}
            post={post}
            site={site}
          />
        </SessionProvider>
      </NavigationProvider>
    );
    const mounted = render(show(false));
    expect(screen.queryByRole("region", { name: "回复" })).toBeNull();
    expect(
      fetcher.mock.calls.filter(([url]) => String(url).endsWith("/comments")),
    ).toHaveLength(0);
    mounted.rerender(show(true));
    const form = await screen.findByRole("form", { name: "发表回复" });
    const input = within(form).getByLabelText("回复内容");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "写了一半，返回继续" } });
    fireEvent.click(within(form).getByRole("button", { name: "添加位置" }));
    expect(
      await screen.findByRole("dialog", { name: "添加位置" }),
    ).toBeVisible();
    mounted.rerender(show(false));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(form).not.toBeVisible();
    expect(form.closest("[inert]")).not.toBeNull();
    expect(screen.queryByRole("region", { name: "回复" })).toBeNull();
    mounted.rerender(show(true));
    expect(screen.getByRole("form", { name: "发表回复" })).toBe(form);
    expect(input).toHaveValue("写了一半，返回继续");
    expect(
      fetcher.mock.calls.filter(([url]) => String(url).endsWith("/comments")),
    ).toHaveLength(1);
  });

  it("closes comment moderation portals when its conversation is suspended", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root, reply] });
      }
    });
    const api = new PublicApi();
    const show = (active: boolean) => (
      <NavigationProvider>
        <SessionProvider>
          <CommentThread
            active={active}
            api={api}
            postId={postId}
            post={post}
            site={site}
          />
        </SessionProvider>
      </NavigationProvider>
    );
    const mounted = render(show(true));
    await screen.findByRole("form", { name: "发表回复" });
    await chooseFirstCommentAction("删除");
    expect(
      await screen.findByRole("dialog", { name: "删除这条回复？" }),
    ).toHaveAttribute("data-open");
    mounted.rerender(show(false));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(document.body.style.overflow).not.toBe("hidden");
    mounted.rerender(show(true));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.getByRole("form", { name: "发表回复" })).toBeVisible();
  });

  it("aborts an unfinished comment read on pause and ignores its late response", async () => {
    let answerFirst!: (response: Response) => void;
    let firstSignal: AbortSignal | undefined;
    let calls = 0;
    installApi((url, init) => {
      if (!url.endsWith("/comments")) return;
      calls++;
      if (calls === 1) {
        firstSignal = init?.signal ?? undefined;
        return new Promise<Response>((resolve) => {
          answerFirst = resolve;
        });
      }
      return success({ items: [] });
    });
    const api = new PublicApi();
    const show = (active: boolean) => (
      <NavigationProvider>
        <SessionProvider>
          <CommentThread
            active={active}
            api={api}
            postId={postId}
            post={post}
            site={site}
          />
        </SessionProvider>
      </NavigationProvider>
    );
    const mounted = render(show(true));
    await waitFor(() => expect(calls).toBe(1));
    mounted.rerender(show(false));
    expect(firstSignal?.aborted).toBe(true);
    await act(async () => answerFirst(success({ items: [root] })));
    expect(screen.queryByRole("region", { name: "回复" })).toBeNull();
    mounted.rerender(show(true));
    await waitFor(() => expect(calls).toBe(2));
    expect(await screen.findByRole("region", { name: "回复" })).toBeVisible();
    expect(document.querySelector(`[data-comment-id="${rootId}"]`)).toBeNull();
  });

  // The thread reports movement, not a total: holding one page of a
  // conversation, it cannot know how many comments the post has, and
  // announcing the size of the loaded page would overwrite the real figure.
  it("reports the comment count as a delta and renders no empty-state row", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return success(
          comment(createdId, "新回复", { canPublish: true }),
          { replayed: false },
          201,
        );
      }
    });
    const onCountChange = vi.fn();
    renderThread(site, false, onCountChange);

    const form = await screen.findByRole("form", { name: "发表回复" });
    expect(screen.queryByText("暂无回复")).not.toBeInTheDocument();
    // Nothing has changed yet, so nothing is reported.
    expect(onCountChange).not.toHaveBeenCalled();

    const content = within(form).getByLabelText("回复内容");
    fireEvent.focus(content);
    fireEvent.change(content, { target: { value: "新回复" } });
    fireEvent.click(within(form).getByRole("button", { name: "发表回复" }));
    await waitFor(() => expect(onCountChange).toHaveBeenCalledWith(1));
  });

  // A thread arrives one page at a time. The pinned conversation travels
  // beside that page rather than reordered into it, so the same root can be in
  // both and has to render once.
  it("renders the pinned conversation once and pages the rest of the roots", async () => {
    const secondRootId = "01K2ABCDEF0123456789ABCDF1";
    const pinnedRoot = comment(rootId, "被置顶的回应", { canPublish: true });
    const fetcher = installApi((url, init) => {
      if (!url.includes("/comments") || init?.method === "POST")
        return undefined;
      if (url.includes("cursor=page2")) {
        return success(
          {
            items: [comment(secondRootId, "第二页的根", { canPublish: true })],
            pinned_comment_id: rootId,
            pinned: [pinnedRoot],
          },
          { next_cursor: null },
        );
      }
      return success(
        {
          items: [pinnedRoot],
          pinned_comment_id: rootId,
          pinned: [pinnedRoot],
        },
        { next_cursor: "page2" },
      );
    });

    renderThread();

    await screen.findByText("被置顶的回应");
    expect(screen.getAllByText("被置顶的回应")).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: "加载更多" }));
    await screen.findByText("第二页的根");
    expect(screen.getAllByText("被置顶的回应")).toHaveLength(1);
    expect(
      fetcher.mock.calls.some(([url]) => String(url).includes("cursor=page2")),
    ).toBe(true);
    // The cursor is spent, so the control retires rather than looping.
    expect(
      screen.queryByRole("button", { name: "加载更多" }),
    ).not.toBeInTheDocument();
  });

  // A cursor is spent only when a page comes back for it. Marking it as used
  // before the request would turn an ordinary network failure into a permanent
  // "cycle detected", leaving the retry button unable to retry.
  it("automatically retries a failed page while keeping an unsent reply intact", async () => {
    preauthenticate();
    const secondRootId = "01K2ABCDEF0123456789ABCDF3";
    let attempts = 0;
    installApi((url, init) => {
      if (!url.includes("/comments") || init?.method === "POST")
        return undefined;
      if (url.includes("cursor=page2")) {
        attempts += 1;
        if (attempts === 1) return json({}, 503);
        return success(
          {
            items: [comment(secondRootId, "终于来了", { canPublish: true })],
            pinned_comment_id: null,
            pinned: null,
          },
          { next_cursor: null },
        );
      }
      return success(
        {
          items: [comment(rootId, "第一页", { canPublish: true })],
          pinned_comment_id: null,
          pinned: null,
        },
        { next_cursor: "page2" },
      );
    });

    renderThread();

    await screen.findByText("第一页");
    const draft = screen.getByLabelText("回复内容");
    fireEvent.focus(draft);
    fireEvent.change(draft, { target: { value: "这段还没发出去" } });
    fireEvent.click(screen.getByRole("button", { name: "加载更多" }));

    await screen.findByText("正在重新获取更多回复…");
    // The already-loaded page is never discarded by a failed continuation.
    expect(screen.getByText("第一页")).toBeInTheDocument();

    await screen.findByText("终于来了");
    expect(draft).toBeInTheDocument();
    expect(draft).toHaveValue("这段还没发出去");
    expect(screen.queryByText("这段还没发出去")).toBe(draft);
    expect(attempts).toBe(2);
    expect(
      screen.queryByRole("button", { name: "重试" }),
    ).not.toBeInTheDocument();
  });

  // A root reports its true reply total, so the branch can offer exactly the
  // ones the reader has not seen instead of a total they can already count.
  it("offers a root's unread replies and appends them behind the loaded ones", async () => {
    const laterReplyId = "01K2ABCDEF0123456789ABCDF2";
    const root = {
      ...comment(rootId, "有很多回复", { canPublish: true }),
      reply_count: 2,
    };
    const firstReply = comment(replyId, "第一条回复", {
      parentId: rootId,
      canPublish: true,
    });
    const fetcher = installApi((url, init) => {
      if (init?.method === "POST") return undefined;
      if (url.includes("/replies")) {
        // A reply page is its own shape: items and nothing else. Routing it
        // through success() would attach the thread-only pinned fields.
        return json({
          data: {
            items: [
              comment(laterReplyId, "第二条回复", {
                parentId: rootId,
                canPublish: true,
              }),
            ],
          },
          meta: { request_id: "req_test", next_cursor: null },
        });
      }
      if (url.includes("/comments")) {
        return success({
          items: [{ ...root, replies_next_cursor: "replies2" }, firstReply],
          pinned_comment_id: null,
          pinned: null,
        });
      }
      return undefined;
    });

    renderThread();

    const branch = await screen.findByRole("button", {
      name: "展开其余 1 条回复",
    });
    expect(branch.closest("[data-comment-id]")).toHaveAttribute(
      "data-comment-id",
      replyId,
    );
    fireEvent.click(branch);

    await screen.findByText("第二条回复");
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes(`/comments/${rootId}/replies?cursor=replies2`),
      ),
    ).toBe(true);
    // Nothing left to offer once the counted replies are all present.
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /展开其余/ }),
      ).not.toBeInTheDocument(),
    );
    // The newly loaded reply lands behind the one already shown, keeping the
    // root immediately followed by its own replies in creation order.
    const ordered = Array.from(
      document.querySelectorAll<HTMLElement>("[data-comment-id]"),
    ).map((node) => node.dataset.commentId);
    expect(ordered).toEqual([rootId, replyId, laterReplyId]);
    const requestsBefore = fetcher.mock.calls.filter(([url]) =>
      String(url).includes("/replies"),
    ).length;
    expect(
      screen.getByRole("button", { name: "收起" }).closest("[data-comment-id]"),
    ).toHaveAttribute("data-comment-id", laterReplyId);
    fireEvent.click(screen.getByRole("button", { name: "收起" }));
    // Exit keeps the row mounted briefly, but it must stop accepting focus
    // immediately and disappear once its height transition completes.
    expect(
      screen.getByText("第二条回复").closest("[data-comment-id]"),
    ).toHaveAttribute("inert");
    await waitFor(() =>
      expect(screen.queryByText("第二条回复")).not.toBeInTheDocument(),
    );
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "展开其余 1 条回复" }),
      ).toHaveFocus(),
    );
    fireEvent.click(screen.getByRole("button", { name: "展开其余 1 条回复" }));
    expect(screen.getByText("第二条回复")).toBeInTheDocument();
    expect(
      fetcher.mock.calls.filter(([url]) => String(url).includes("/replies"))
        .length,
    ).toBe(requestsBefore);
  });

  it("collapses while the first reply page is still pending", async () => {
    installApi((url) => {
      if (url.includes("/replies")) return new Promise<Response>(() => {});
      if (url.includes("/comments"))
        return success({
          items: [
            {
              ...comment(rootId, "楼层"),
              reply_count: 2,
              replies_next_cursor: "replies2",
            },
            comment(replyId, "预览回复", { parentId: rootId }),
          ],
        });
      return undefined;
    });
    renderThread();
    fireEvent.click(
      await screen.findByRole("button", { name: "展开其余 1 条回复" }),
    );
    fireEvent.click(screen.getByRole("button", { name: "收起" }));
    await waitFor(() =>
      expect(
        document.documentElement.style.getPropertyValue("overflow-anchor"),
      ).toBe(""),
    );
    expect(
      screen.queryByRole("button", { name: "收起" }),
    ).not.toBeInTheDocument();
    expect(
      screen.getByText("预览回复").closest("[data-comment-id]"),
    ).toHaveFocus();
  });

  it("loads the first continuation even when a newer detail reply is already cached", async () => {
    const oldSecondId = "01K2ABCDEF0123456789ABCDE4";
    const serverRoot = {
      ...comment(rootId, "原有楼层"),
      reply_count: 2,
      replies_next_cursor: "first-unread",
    };
    const first = comment(replyId, "第一条旧回复", {
      parentId: rootId,
      canPublish: true,
    });
    const fromDetail = parseComment(
      comment(createdId, "刚从详情发表的回复", {
        parentId: rootId,
        canPublish: true,
        createdAt: "2026-08-10T01:06:00Z",
      }),
    );
    const fetcher = installApi((url, init) => {
      if (init?.method === "POST") return undefined;
      if (url.includes("/replies"))
        return json({
          data: {
            items: [
              comment(oldSecondId, "第二条旧回复", {
                parentId: rootId,
                canPublish: true,
                createdAt: "2026-08-10T01:04:00Z",
              }),
            ],
          },
          meta: { request_id: "test", next_cursor: null },
        });
      if (url.includes("/comments"))
        return success({ items: [serverRoot, first] });
      return undefined;
    });
    function CachedThread() {
      const api = useRef(new PublicApi());
      const [change, setChange] = useState<DiscussionChange | null>(null);
      return (
        <>
          <button
            onClick={() =>
              setChange({
                kind: "created",
                comment: fromDetail,
                source: "other-detail",
                revision: 1,
              })
            }
          >
            模拟详情发表回复
          </button>
          <CommentThread
            api={api.current}
            postId={postId}
            post={post}
            site={site}
            change={change}
          />
        </>
      );
    }
    render(
      <NavigationProvider>
        <SessionProvider>
          <CachedThread />
        </SessionProvider>
      </NavigationProvider>,
    );
    await screen.findByText("第一条旧回复");
    fireEvent.click(screen.getByRole("button", { name: "模拟详情发表回复" }));
    fireEvent.click(screen.getByRole("button", { name: "展开其余 2 条回复" }));
    await screen.findByText("第二条旧回复");
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes("/replies?cursor=first-unread"),
      ),
    ).toBe(true);
    expect(screen.getByText("刚从详情发表的回复")).toBeVisible();
  });

  it("recovers a nested reply page automatically without reloading its root", async () => {
    preauthenticate();
    let replyAttempts = 0;
    let rootRequests = 0;
    const laterReplyId = "01K2ABCDEF0123456789ABCDF2";
    const root = {
      ...comment(rootId, "有很多回复", { canPublish: true }),
      reply_count: 2,
    };
    const firstReply = comment(replyId, "第一条回复", {
      parentId: rootId,
      canPublish: true,
    });
    const fetcher = installApi((url, init) => {
      if (init?.method === "POST") return undefined;
      if (url.includes("/replies")) {
        replyAttempts += 1;
        if (replyAttempts === 1) return json({}, 503);
        // A reply page is its own shape: items and nothing else. Routing it
        // through success() would attach the thread-only pinned fields.
        return json({
          data: {
            items: [
              comment(laterReplyId, "第二条回复", {
                parentId: rootId,
                canPublish: true,
              }),
            ],
          },
          meta: { request_id: "req_test", next_cursor: null },
        });
      }
      if (url.includes("/comments")) {
        rootRequests += 1;
        return success({
          items: [{ ...root, replies_next_cursor: "replies2" }, firstReply],
          pinned_comment_id: null,
          pinned: null,
        });
      }
      return undefined;
    });

    renderThread();

    const branch = await screen.findByRole("button", {
      name: "展开其余 1 条回复",
    });
    fireEvent.click(branch);

    await screen.findByText("正在重新获取回复…");
    const draft = screen.getByLabelText("回复内容");
    draft.focus();
    fireEvent.change(draft, { target: { value: "继续写我的回复" } });
    expect(screen.getByText("第一条回复")).toBeInTheDocument();
    await screen.findByText("第二条回复");
    expect(draft).toHaveFocus();
    expect(draft).toHaveValue("继续写我的回复");
    expect(replyAttempts).toBe(2);
    expect(rootRequests).toBe(1);
    expect(
      fetcher.mock.calls.some(([url]) =>
        String(url).includes(`/comments/${rootId}/replies?cursor=replies2`),
      ),
    ).toBe(true);
    // Nothing left to offer once the counted replies are all present.
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: /展开其余/ }),
      ).not.toBeInTheDocument(),
    );
    // The newly loaded reply lands behind the one already shown, keeping the
    // root immediately followed by its own replies in creation order.
    const ordered = Array.from(
      document.querySelectorAll<HTMLElement>("[data-comment-id]"),
    ).map((node) => node.dataset.commentId);
    expect(ordered).toEqual([rootId, replyId, laterReplyId]);
  });

  it("expands the detail reply composer once and keeps it open after blur", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
    });
    renderThread();

    const form = await screen.findByRole("form", { name: "发表回复" });
    const content = within(form).getByLabelText("回复内容");
    const frame = form.closest<HTMLElement>("[data-comment-layout-frame]");
    const initialReply = within(form).getByRole("button", { name: "回复" });
    expect(frame).toHaveAttribute("data-comment-top-level", "true");
    expect(frame).toHaveAttribute("data-comment-layout-state", "collapsed");
    expect(initialReply).toBeVisible();
    expect(initialReply).toBeDisabled();
    // The toolbar is folded away, not unmounted: out of sight, out of the
    // accessibility tree, and out of reach of the keyboard and pointer.
    const foldedPicker = form.querySelector('button[aria-label="选择媒体"]');
    expect(foldedPicker).not.toBeVisible();
    expect(foldedPicker?.closest("[inert]")).not.toBeNull();
    expect(foldedPicker?.closest("[aria-hidden='true']")).not.toBeNull();
    // The 「正在回复」 line is folded away rather than absent, so it can grow
    // in with the composer; folded, it is out of the accessibility tree.
    const replyTarget = within(form).getByText(/正在回复/);
    expect(replyTarget.closest("[aria-hidden='true']")).not.toBeNull();
    expect(replyTarget).not.toBeVisible();

    fireEvent.focus(content);

    expect(frame).toHaveAttribute("data-comment-layout-state", "expanded");
    expect(initialReply).not.toBeInTheDocument();
    expect(replyTarget).toHaveTextContent("正在回复 @owner");
    expect(replyTarget.closest("[aria-hidden='true']")).toBeNull();
    expect(replyTarget).toBeVisible();
    const mediaPicker = within(form).getByRole("button", {
      name: "选择媒体",
    });
    expect(mediaPicker).toBeVisible();
    expect(mediaPicker).not.toHaveAttribute("title");
    const locationPicker = within(form).getByRole("button", {
      name: "添加位置",
    });
    expect(locationPicker).toBeVisible();
    expect(locationPicker).toBeEnabled();
    expect(locationPicker).not.toHaveAttribute("title");
    expect(locationPicker).not.toHaveTextContent("位置");

    fireEvent.keyDown(document, { key: "Tab" });
    act(() => mediaPicker.focus());
    const mediaTooltip = await screen.findByRole("tooltip");
    expect(mediaTooltip).toHaveTextContent("媒体");
    expect(form).not.toContainElement(mediaTooltip);
    act(() => mediaPicker.blur());

    fireEvent.blur(content, { relatedTarget: document.body });
    expect(frame).toHaveAttribute("data-comment-layout-state", "expanded");
    expect(within(form).getByText(/正在回复/)).toHaveTextContent(
      "正在回复 @owner",
    );
    expect(
      within(form).getByRole("button", { name: "选择媒体" }),
    ).toBeVisible();
    expect(
      within(form).getByRole("button", { name: "发表回复" }),
    ).toBeVisible();
  });

  it("preserves inline reply transition declarations", () => {
    expect(commentsCss).toMatch(
      /\._formSurface_[\w-]+\._inlineTopLevelForm_[\w-]+\s*\{[^}]*border-block-end:\s*1px solid var\(--border-subtle\);[^}]*padding:\s*0\.25rem var\(--space-4\) 0\.75rem;/s,
    );
    // The folded geometry — the placeholder on the centre line of a 3.5rem
    // row under 0.25rem of form padding, the avatar 0.875rem down — comes
    // from one field box shared by both states plus the paddings, margins
    // and grid rows that transition to the open geometry, not from fixed
    // heights that would snap.
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._commentForm_[\w-]+ textarea\s*\{[^}]*min-block-size:\s*3\.5rem;[^}]*border:\s*0\.125rem solid transparent;[^}]*padding-block:\s*0\.75rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+\._commentCollapsed_[\w-]+ \._commentForm_[\w-]+\s*\{[^}]*padding-block-start:\s*0\.25rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+\._commentCollapsed_[\w-]+ \._commentForm_[\w-]+ textarea\s*\{[^}]*max-block-size:\s*3\.5rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._commentForm_[\w-]+\s*\{[^}]*transition:\s*padding-block-start var\(--motion-content\)/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._composerAvatar_[\w-]+\s*\{[^}]*margin-block-start:\s*0\.875rem;[^}]*transition:\s*margin-block-start var\(--motion-content\)/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+\._commentExpanded_[\w-]+ \._composerAvatar_[\w-]+\s*\{[^}]*margin-block-start:\s*2rem;/s,
    );
    for (const row of ["replyTargetRow", "toolbarRow"]) {
      expect(commentsCss).toMatch(
        new RegExp(
          `\\._${row}_[\\w-]+\\s*\\{[^}]*grid-template-rows:\\s*1fr;[^}]*transition:\\s*grid-template-rows var\\(--motion-content\\)`,
          "s",
        ),
      );
      expect(commentsCss).toMatch(
        new RegExp(
          `\\._${row}Collapsed_[\\w-]+\\s*\\{[^}]*grid-template-rows:\\s*0fr;[^}]*visibility:\\s*hidden;`,
          "s",
        ),
      );
    }
    // The frame follows the content exactly while a fold runs inside it.
    expect(commentsCss).toMatch(
      /\._commentLayoutFrame_[\w-]+\[data-comment-layout-motion="fold"\]\s*\{\s*transition:\s*none;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._collapsedReplyAction_[\w-]+\s*\{[^}]*inline-size:\s*4\.671875rem;[^}]*block-size:\s*2\.25rem;[^}]*color:\s*var\(--button-primary-contrast\);[^}]*background:\s*var\(--button-primary-disabled\);[^}]*opacity:\s*0\.25;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._commentMediaComposer_[\w-]+\s*\{[^}]*margin-block-start:\s*0;[^}]*animation:\s*none;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._commentMediaComposer_[\w-]+ > div\s*\{[^}]*block-size:\s*3rem;[^}]*min-block-size:\s*3rem;[^}]*align-items:\s*flex-end;[^}]*padding-block-end:\s*0\.125rem;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:disabled\s*\{[^}]*opacity:\s*0\.25;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+\s*\{[^}]*color:\s*var\(--button-primary-contrast\);[^}]*background:\s*var\(--button-primary\);[^}]*transition:\s*box-shadow 0\.2s;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:hover:not\(:disabled\)\s*\{[^}]*background:\s*var\(--button-primary-hover\);/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:disabled\s*\{[^}]*color:\s*var\(--button-primary-contrast\);[^}]*background:\s*var\(--button-primary-disabled\);[^}]*opacity:\s*0\.25;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+ \._primaryAction_[\w-]+:active:not\(:disabled\)\s*\{[^}]*background:\s*var\(--button-primary-pressed\);[^}]*transform:\s*none;/s,
    );
    expect(commentsCss).toMatch(
      /\._inlineTopLevelForm_[\w-]+[\s\S]*?\._commentMediaComposer_[\w-]+[\s\S]*?button[\s\S]*?svg\s*\{[^}]*inline-size:\s*1\.453125rem;[^}]*block-size:\s*1\.453125rem;/s,
    );
    expect(commentsCss).toMatch(
      /@media \(hover: hover\)[\s\S]*?button:hover:not\(:disabled\)\s*\{[^}]*color:\s*#829aab;[^}]*background:\s*rgb\(15 20 25 \/ 10%\);[^}]*transform:\s*scale\(1\.12\);/s,
    );
    expect(commentsCss).toMatch(
      /button:active:not\(:disabled\)\s*\{[^}]*background:\s*rgb\(15 20 25 \/ 7%\);[^}]*filter:\s*brightness\(0\.7\);[^}]*transform:\s*scale\(1\.12\);/s,
    );
  });

  it("stores and renders an owner reply location as structured post-style data", async () => {
    preauthenticate();
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return success(
          comment(createdId, "", {
            canPublish: true,
            location: { name: "书房", display_name: "书房" },
          }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    const form = await screen.findByRole("form", { name: "发表回复" });
    const content = within(form).getByLabelText("回复内容");
    fireEvent.focus(content);
    const locationPicker = within(form).getByRole("button", {
      name: "添加位置",
    });
    expect(locationPicker).toBeEnabled();

    fireEvent.click(locationPicker);
    const dialog = await screen.findByRole("dialog", { name: "添加位置" });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "手动添加地点" }),
    );
    fireEvent.change(within(dialog).getByLabelText("地点名称"), {
      target: { value: "书房" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "添加" }));

    expect(content).toHaveValue("");
    expect(
      within(form).getByRole("button", { name: "更改位置：书房" }),
    ).toBeEnabled();
    const submit = within(form).getByRole("button", { name: "发表回复" });
    expect(submit).toBeEnabled();
    fireEvent.click(submit);
    await waitFor(() =>
      expect(
        fetcher.mock.calls.filter(
          ([url, init]) =>
            String(url).endsWith("/comments") && init?.method === "POST",
        ),
      ).toHaveLength(1),
    );
    const mutation = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url).endsWith("/comments") && init?.method === "POST",
    );
    expect(JSON.parse(String(mutation?.[1]?.body))).toEqual({
      content_md: "",
      parent_id: null,
      // A top-level comment answers nobody in particular.
      reply_to_comment_id: null,
      media: [],
      location: {
        name: "书房",
        display_name: "书房",
        country_code: "",
        country: "",
        region: "",
        city: "",
        district: "",
        category: "",
        provider: "manual",
        provider_place_id: "",
        latitude: null,
        longitude: null,
        coordinate_system: null,
      },
    });
    expect(await screen.findByLabelText("地点：书房")).toBeInTheDocument();
  });

  it("retargets the composer's measured block size as activation and media change its real layout", async () => {
    preauthenticate();
    let measuredHeight = 74.625;
    const nativeRect = HTMLElement.prototype.getBoundingClientRect;
    const rectSpy = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockImplementation(function (this: HTMLElement) {
        return this.dataset.commentLayoutContent === "true"
          ? measuredRect(measuredHeight)
          : nativeRect.call(this);
      });
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
    });
    renderThread();

    const form = await screen.findByRole("form", { name: "发表回复" });
    const content = within(form).getByLabelText("回复内容");
    const frame = form.closest<HTMLElement>("[data-comment-layout-frame]");
    expect(frame?.style.blockSize).toBe("74.625px");

    measuredHeight = 254.625;
    fireEvent.focus(content);
    expect(frame?.style.blockSize).toBe("254.625px");

    measuredHeight = 337.25;
    fireEvent.change(within(form).getByLabelText("媒体文件"), {
      target: {
        files: [new File(["image"], "layout.png", { type: "image/png" })],
      },
    });
    expect(frame?.style.blockSize).toBe("337.25px");

    measuredHeight = 361.75;
    act(() => FakeCommentResizeObserver.notify());
    expect(frame?.style.blockSize).toBe("361.75px");
    rectSpy.mockRestore();
  });

  it("offers sign-in instead of a composer while signed out", async () => {
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root, reply] });
      }
    });
    renderThread();

    expect(await screen.findByText("登录后回复")).toBeVisible();
    expect(screen.getByRole("link", { name: "登录" })).toHaveAttribute(
      "href",
      `/login?return=${encodeURIComponent(`/m/${postId}`)}`,
    );
    expect(
      screen.queryByRole("form", { name: "发表回复" }),
    ).not.toBeInTheDocument();
    expect(screen.queryByLabelText("回复内容")).not.toBeInTheDocument();

    // Reading keeps working, and the per-row reply actions stay in place. They
    // are the affordance for the thing a reader wants to do; removing them
    // would leave a page full of replies with no visible way to make one, and
    // the notice above the list is easy to have scrolled past by the time the
    // reader reaches the comment they mean to answer.
    expect(screen.getByText("顶级回应")).toBeInTheDocument();
    expect(screen.getByText("已有一级回复")).toBeInTheDocument();
    const replyActions = screen.getAllByRole("button", { name: /^回复 / });
    expect(replyActions).toHaveLength(2);

    fireEvent.click(replyActions[0]);
    expect(window.location.pathname).toBe("/login");
    expect(new URLSearchParams(window.location.search).get("return")).toBe(
      `/m/${postId}`,
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    // Pressing it must not open a composer it cannot submit.
    expect(screen.queryByLabelText("回复内容")).not.toBeInTheDocument();
  });

  it("tells an account still waiting on approval why it cannot reply", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url === "/api/v1/account/session") {
        return json({
          data: {
            expires_at: "2999-08-11T01:00:00.000Z",
            csrf_token: "csrf-from-cookie",
            account: {
              user_id: "1".repeat(32),
              username: "linlin",
              display_name: "林林",
              bio: "",
              avatar_media_id: null,
              cover_media_id: null,
              role: "member",
              profile_url: "/linlin",
              status: "pending",
              username_chosen: true,
            },
            limits: makeAccountLimits(),
          },
          meta: { request_id: "req_pending" },
        });
      }
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root] });
      }
    });
    renderThread();

    // Signed in, and still refused by the server. Finding that out after
    // typing a reply is the worst possible moment, so the composer never
    // appears -- and neither does a login link, which would send somebody who
    // is already signed in around a loop.
    expect(await screen.findByText("正在等待管理员批准")).toBeVisible();
    expect(
      screen.getByText("你的账户正在等待管理员批准，批准后就能留言。"),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("回复内容")).not.toBeInTheDocument();
    expect(screen.queryByRole("link", { name: "登录" })).toBeNull();

    fireEvent.click(screen.getAllByRole("button", { name: /^回复 / })[0]);
    const prompt = await screen.findByRole("dialog");
    expect(within(prompt).getByText("正在等待管理员批准")).toBeInTheDocument();
    expect(within(prompt).queryByRole("link", { name: "登录" })).toBeNull();
  });

  it("offers sign-in inside the reply dialog while signed out", async () => {
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
    });
    render(
      <NavigationProvider>
        <SessionProvider>
          <ReplyDialogHarness />
        </SessionProvider>
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "打开回复窗口" }));
    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(new URLSearchParams(window.location.search).get("return")).toBe(
      `/m/${postId}`,
    );
  });

  it("keeps layout motion bounded and removes it for reduced-motion users", () => {
    expect(commentsCss).toContain(
      "transition: block-size var(--motion-content) var(--ease-out)",
    );
    expect(commentsCss).toMatch(
      /@media \(prefers-reduced-motion: reduce\)[\s\S]*commentLayoutFrame[\s\S]*transition: none/,
    );
    expect(commentsCss).not.toContain("cubic-bezier(0.16, 1.3, 0.3, 1)");
  });

  it("renders the action reply as an independent modal composer", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
    });
    render(
      <NavigationProvider>
        <SessionProvider>
          <CommentThread
            api={new PublicApi()}
            onReplyDialogOpenChange={vi.fn()}
            post={post}
            postId={postId}
            replyDialogOpen
            site={site}
          />
        </SessionProvider>
      </NavigationProvider>,
    );

    const dialog = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    const context = within(dialog).getByRole("article", {
      name: "Moment发布的 Moment",
    });
    const form = within(dialog).getByRole("form", { name: "发表回复" });
    expect(within(context).getByText("一段需要回应的 Moment")).toBeVisible();
    expect(within(context).getByText(/正在回复/)).toHaveTextContent(
      "正在回复 @owner",
    );
    expect(within(form).queryByText(/正在回复/)).not.toBeInTheDocument();
    expect(
      within(dialog).getByRole("button", { name: "选择媒体" }),
    ).toBeVisible();
    expect(within(dialog).getByLabelText("回复内容")).toHaveAttribute(
      "id",
      "dialog-top-level-comment-content",
    );
    expect(document.getElementById("top-level-comment-content")).not.toBe(
      within(dialog).getByLabelText("回复内容"),
    );
    expect(within(dialog).getByLabelText("回复内容")).toHaveFocus();
    expect(
      within(dialog).getByRole("button", { name: "关闭回复窗口" }),
    ).toBeInTheDocument();
  });

  it("keeps owner location selection available in dialog and inline comment replies", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root] });
      }
    });
    render(
      <NavigationProvider>
        <SessionProvider>
          <ReplyDialogHarness />
        </SessionProvider>
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "打开回复窗口" }));
    const dialog = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    expect(
      within(dialog).getByRole("button", { name: "添加位置" }),
    ).toBeEnabled();
    fireEvent.click(
      within(dialog).getByRole("button", { name: "关闭回复窗口" }),
    );

    const rootArticle = await screen.findByRole("article", {
      name: "访客的回复",
    });
    fireEvent.click(
      within(rootArticle).getByRole("button", { name: "回复 访客" }),
    );
    const replyForm = screen.getByRole("form", { name: "回复 访客" });
    const locationPicker = within(replyForm).getByRole("button", {
      name: "添加位置",
    });
    expect(locationPicker).toBeEnabled();
    fireEvent.click(locationPicker);
    const locationDialog = await screen.findByRole("dialog", {
      name: "添加位置",
    });
    fireEvent.click(
      within(locationDialog).getByRole("button", { name: "手动添加地点" }),
    );
    fireEvent.change(within(locationDialog).getByLabelText("地点名称"), {
      target: { value: "书房" },
    });
    fireEvent.click(
      within(locationDialog).getByRole("button", { name: "添加" }),
    );
    expect(
      within(replyForm).getByRole("button", { name: "更改位置：书房" }),
    ).toBeVisible();
  });

  it("keeps the reply dialog avatars on one mobile alignment rail", () => {
    expect(commentsCss).toMatch(
      /\._replyContext_[\w-]+::after\s*\{[^}]*inset-inline-start: calc\(\s*var\(--space-4\) \+ \(var\(--target-size\) \/ 2\) - 1px\s*\)/,
    );
    expect(commentsCss).toMatch(
      /@media \(max-width: 719px\)[\s\S]*\._replyDialogPopup_[\w-]+ > \._formSurface_[\w-]+[^{}]*\{[^}]*padding-inline: var\(--space-4\)/,
    );
    expect(commentsCss).toMatch(
      /\._replyContextTarget_[\w-]+\s*\{[^}]*margin: var\(--space-3\) 0 var\(--space-1\)/,
    );
  });

  it("locks the document and restores one reply trigger through every dismissal path", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
    });
    vi.spyOn(window, "scrollY", "get").mockReturnValue(420);
    render(
      <NavigationProvider>
        <SessionProvider>
          <ReplyDialogHarness />
        </SessionProvider>
      </NavigationProvider>,
    );

    const trigger = screen.getByRole("button", { name: "打开回复窗口" });
    trigger.focus();
    fireEvent.click(trigger);
    let dialog = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    let textarea =
      within(dialog).getByLabelText<HTMLTextAreaElement>("回复内容");
    expect(textarea).toHaveFocus();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    fireEvent.change(textarea, { target: { value: "保留到再次打开" } });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "关闭回复窗口" }),
    );
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.documentElement.style.overflow).toBe("");

    fireEvent.click(trigger);
    dialog = await screen.findByRole("dialog", { name: "回复这条 Moment" });
    textarea = within(dialog).getByLabelText<HTMLTextAreaElement>("回复内容");
    expect(textarea).toHaveFocus();
    expect(textarea).toHaveValue("保留到再次打开");
    await waitFor(() =>
      expect(textarea.selectionStart).toBe(textarea.value.length),
    );
    expect(textarea.selectionEnd).toBe(textarea.value.length);
    fireEvent.keyDown(dialog, { key: "Escape" });
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    dialog = await screen.findByRole("dialog", { name: "回复这条 Moment" });
    const backdrop = dialog.parentElement?.previousElementSibling;
    expect(backdrop).toHaveAttribute("role", "presentation");
    fireEvent.mouseDown(backdrop!);
    fireEvent.click(backdrop!);
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(
      screen.queryByRole("dialog", { name: "回复这条 Moment" }),
    ).toBeNull();
  });

  it("keeps a flat one-level presentation and reuses the reply dialog for a child", async () => {
    preauthenticate();
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root, reply] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return success(
          comment(createdId, "新一级回复", {
            parentId: rootId,
            canPublish: true,
            createdAt: "2026-08-10T01:05:00Z",
          }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    const existingReply = await screen.findByText("已有一级回复");
    expect(existingReply.closest("li")).toHaveAttribute("data-depth", "1");
    expect(screen.getAllByRole("button", { name: /^回复 / })).toHaveLength(2);

    const replyArticle = screen.getByRole("article", {
      name: "Moment的回复",
    });
    fireEvent.click(
      within(replyArticle).getByRole("button", {
        name: "回复 Moment",
      }),
    );
    const replyForm = screen.getByRole("form", {
      name: "回复 @owner",
    });
    expect(screen.getAllByRole("form", { name: "回复 @owner" })).toHaveLength(
      1,
    );
    expect(replyForm.closest('[role="dialog"]')).toHaveAccessibleName(
      "回复这条 Moment",
    );
    // Previously this read "@Moment" — the display name wearing an @ as though
    // it were a handle, from the site overlay this branch retired everywhere
    // else. The handle is what the reply is actually recorded against.
    expect(replyForm).toHaveTextContent("正在回复 @owner");
    fireEvent.change(within(replyForm).getByLabelText("回复内容"), {
      target: { value: "新一级回复" },
    });
    fireEvent.click(
      within(replyForm).getByRole("button", { name: "发表回复" }),
    );

    await waitFor(() =>
      expect(
        fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
      ).toHaveLength(1),
    );
    const createdReply = await screen.findByText("新一级回复");
    expect(createdReply).toBeInTheDocument();
    await waitFor(() => expect(createdReply.closest("li")).toHaveFocus());
    const mutation = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url).endsWith("/comments") && init?.method === "POST",
    );
    // Where the reply sits and what it answers are recorded separately: it
    // hangs off the root, but it answers the sibling the reader clicked. That
    // difference is the whole reason for the second field, and it is the case
    // where the rendered prefix has something to say.
    expect(JSON.parse(String(mutation?.[1]?.body))).toEqual({
      content_md: "新一级回复",
      parent_id: rootId,
      reply_to_comment_id: replyId,
      media: [],
      location: null,
    });
    expect(
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-comment-id]"),
      ).map((item) => [item.dataset.commentId, item.dataset.depth]),
    ).toEqual([
      [rootId, "0"],
      [replyId, "1"],
      [createdId, "1"],
    ]);
  });

  it("retargets the measured layout when a submission error is revealed", async () => {
    preauthenticate();
    let measuredHeight = 254.625;
    const nativeRect = HTMLElement.prototype.getBoundingClientRect;
    const rectSpy = vi
      .spyOn(HTMLElement.prototype, "getBoundingClientRect")
      .mockImplementation(function (this: HTMLElement) {
        return this.dataset.commentLayoutContent === "true"
          ? measuredRect(measuredHeight)
          : nativeRect.call(this);
      });
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return failure("COMMENT_REJECTED", 422);
      }
    });
    renderThread();

    const form = await screen.findByRole("form", { name: "发表回复" });
    fireEvent.change(within(form).getByLabelText("回复内容"), {
      target: { value: "保留这段文字" },
    });
    const frame = form.closest<HTMLElement>("[data-comment-layout-frame]");
    expect(frame?.style.blockSize).toBe("254.625px");

    measuredHeight = 318.375;
    fireEvent.click(within(form).getByRole("button", { name: "发表回复" }));
    expect(await within(form).findByRole("alert")).toHaveTextContent(
      "防滥用检查",
    );
    expect(frame?.style.blockSize).toBe("318.375px");
    rectSpy.mockRestore();
  });

  it("uploads, previews, and publishes an ordered image from the reply composer", async () => {
    preauthenticate();
    vi.stubGlobal("XMLHttpRequest", FakeCommentUploadRequest);
    const mediaId = "01K2ABCDEF0123456789ABCDE4";
    const usage = {
      media_id: mediaId,
      position: 0,
      alt_text: "night sky",
      width: 10,
      height: 10,
    };
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return success(
          comment(createdId, "带图回应", { canPublish: true, media: [usage] }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    await screen.findByRole("form", { name: "发表回复" });
    fireEvent.change(screen.getByLabelText("回复内容"), {
      target: { value: "带图回应" },
    });
    const file = new File(["image"], "night.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: { files: [file] },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add descriptions" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Description" }), {
      target: { value: "night sky" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));

    await waitFor(() => expect(FakeCommentUploadRequest.last).not.toBeNull());
    const upload = FakeCommentUploadRequest.last;
    expect(upload?.method).toBe("POST");
    expect(upload?.url).toBe("/api/v1/media");
    expect(upload?.sent).toBeInstanceOf(FormData);
    upload?.respond({
      data: {
        media: {
          id: mediaId,
          sha256: "a".repeat(64),
          original_name: "night.png",
          mime_type: "image/png",
          byte_size: 5,
          width: 10,
          height: 10,
          created_at: "2026-08-10T01:02:03.000Z",
          unreferenced_since: "2026-08-10T01:02:03.000Z",
          animated_original: false,
        },
        preview: {
          recipe_version: "preview-v1",
          sha256: "b".repeat(64),
          mime_type: "image/jpeg",
          width: 10,
          height: 10,
          animated_original: false,
        },
        original_url: `/api/v1/media/${mediaId}`,
        preview_url: mediaPreviewUrl(mediaId),
      },
      meta: { request_id: "req_upload" },
    });

    await screen.findByText("带图回应");
    const mutation = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url).endsWith("/comments") && init?.method === "POST",
    );
    expect(JSON.parse(String(mutation?.[1]?.body)).media).toEqual([
      { media_id: mediaId, position: 0, alt_text: "night sky" },
    ]);
    expect(
      screen.getByRole("button", {
        name: "打开第 1 张图片，共 1 张：night sky",
      }),
    ).toBeInTheDocument();
  });

  it("keeps reply text and selected media through upload failure and retry", async () => {
    preauthenticate();
    vi.stubGlobal("XMLHttpRequest", FakeCommentUploadRequest);
    const mediaId = "01K2ABCDEF0123456789ABCDE4";
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return success(
          comment(createdId, "失败后重试", {
            canPublish: true,
            media: [
              {
                media_id: mediaId,
                position: 0,
                alt_text: null,
                width: 10,
                height: 10,
              },
            ],
          }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    await screen.findByRole("form", { name: "发表回复" });
    fireEvent.change(screen.getByLabelText("回复内容"), {
      target: { value: "失败后重试" },
    });
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: {
        files: [new File(["image"], "retry.png", { type: "image/png" })],
      },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));
    await waitFor(() => expect(FakeCommentUploadRequest.last).not.toBeNull());
    const failedUpload = FakeCommentUploadRequest.last;
    failedUpload?.fail();

    expect(await screen.findByText("重试上传", { exact: false })).toBeVisible();
    expect(screen.getByLabelText("回复内容")).toHaveValue("失败后重试");
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(0);

    fireEvent.click(screen.getByText("重试上传", { exact: false }));
    await waitFor(() =>
      expect(FakeCommentUploadRequest.last).not.toBe(failedUpload),
    );
    const retriedUpload = FakeCommentUploadRequest.last;
    expect(retriedUpload?.sent).toBeInstanceOf(FormData);
    retriedUpload?.respond({
      data: {
        media: {
          id: mediaId,
          sha256: "a".repeat(64),
          original_name: "retry.png",
          mime_type: "image/png",
          byte_size: 5,
          width: 10,
          height: 10,
          created_at: "2026-08-10T01:02:03.000Z",
          unreferenced_since: "2026-08-10T01:02:03.000Z",
          animated_original: false,
        },
        preview: {
          recipe_version: "preview-v1",
          sha256: "b".repeat(64),
          mime_type: "image/jpeg",
          width: 10,
          height: 10,
          animated_original: false,
        },
        original_url: `/api/v1/media/${mediaId}`,
        preview_url: mediaPreviewUrl(mediaId),
      },
      meta: { request_id: "req_upload_retry" },
    });
    // The retry offer is what the tile shows while an upload is broken, so
    // its disappearance is the visible proof the retry landed.
    await waitFor(() =>
      expect(screen.queryByText("重试上传", { exact: false })).toBeNull(),
    );
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));
    expect(await screen.findByText("失败后重试")).toBeInTheDocument();
  });

  it("keeps existing responses readable when new comments are disabled", async () => {
    installApi((url) => {
      if (url.endsWith("/comments")) return success({ items: [root, reply] });
    });
    renderThread({ ...site, commentsEnabled: false });

    expect(await screen.findByText("顶级回应")).toBeInTheDocument();
    expect(screen.getByText("已有一级回复")).toBeInTheDocument();
    expect(screen.getByText("回复已关闭")).toBeInTheDocument();
    expect(
      screen.queryByRole("form", { name: "发表回复" }),
    ).not.toBeInTheDocument();
  });

  it("guards a pending create and reconciles the confirmed comment only once", async () => {
    preauthenticate();
    let resolveCreate!: (response: Response) => void;
    const pendingCreate = new Promise<Response>((resolve) => {
      resolveCreate = resolve;
    });
    const fetcher = installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return pendingCreate;
      }
    });
    renderThread();

    await screen.findByRole("form", { name: "发表回复" });
    const contentField = screen.getByLabelText("回复内容");
    fireEvent.focus(contentField);
    const initiallyDisabled = screen.getByRole("button", {
      name: "发表回复",
    });
    expect(initiallyDisabled).toBeDisabled();
    expect(initiallyDisabled).not.toHaveAttribute("data-pending");
    expect(initiallyDisabled).not.toHaveAttribute("aria-busy");
    expect(commentsCss).toContain("cursor: not-allowed");
    expect(commentsCss).toContain("cursor: progress");
    expect(commentsCss).not.toContain("cursor: wait");
    fireEvent.change(contentField, {
      target: { value: "只发表一次" },
    });
    const submit = screen.getByRole("button", { name: "发表回复" });
    fireEvent.click(submit);
    fireEvent.click(submit);
    expect(submit).toBeDisabled();
    expect(submit).toHaveAttribute("data-pending", "true");
    expect(submit).toHaveAttribute("aria-busy", "true");
    expect(
      fetcher.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(1);

    resolveCreate(
      success(
        comment(createdId, "只发表一次", { canPublish: true }),
        { replayed: false },
        201,
      ),
    );
    expect(await screen.findAllByText("只发表一次")).toHaveLength(1);
  });

  it("keeps text and existing responses when the server closes comments during submit", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [root] });
      }
      if (url.endsWith("/comments") && init?.method === "POST") {
        return failure("COMMENTS_DISABLED", 403);
      }
    });
    renderThread();

    await screen.findByText("顶级回应");
    fireEvent.change(screen.getByLabelText("回复内容"), {
      target: { value: "关闭前写下" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "回复刚刚被关闭",
    );
    expect(screen.getByLabelText("回复内容")).toHaveValue("关闭前写下");
    expect(screen.getByText("顶级回应")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "发表回复" })).toBeDisabled();
  });

  it("reuses an owner idempotency key after a network failure", async () => {
    preauthenticate();
    let submissions = 0;
    const keys: string[] = [];
    installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({ items: [] });
      }
      if (url.includes("/posts/") && init?.method === "POST") {
        submissions += 1;
        keys.push(new Headers(init.headers).get("Idempotency-Key") ?? "");
        if (submissions === 1) throw new TypeError("offline");
        return success(
          comment(createdId, "Owner 重试", { canPublish: true }),
          { replayed: false },
          201,
        );
      }
    });
    renderThread();

    await screen.findByRole("form", { name: "发表回复" });
    expect(screen.queryByLabelText("显示名")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("回复内容"), {
      target: { value: "Owner 重试" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("文字已保留");
    fireEvent.click(screen.getByRole("button", { name: "发表回复" }));

    expect(await screen.findByText("Owner 重试")).toBeInTheDocument();
    expect(keys).toHaveLength(2);
    expect(keys[1]).toBe(keys[0]);
  });

  it.each([
    {
      name: "top-level comment",
      formName: "发表回复",
      fieldName: "回复内容",
      submitName: "发表回复",
      content: "过期前写好的顶级回应",
      parentId: null,
      openReply: false,
    },
    {
      name: "reply",
      formName: "回复 访客",
      fieldName: "回复内容",
      submitName: "发表回复",
      content: "过期前写好的一级回复",
      parentId: rootId,
      openReply: true,
    },
  ])(
    "reauthenticates a pre-expired $name without losing or duplicating its draft",
    async ({
      formName,
      fieldName,
      submitName,
      content,
      parentId,
      openReply,
    }) => {
      preauthenticate();
      const createTokens: Array<string | null> = [];
      const createBodies: Array<Record<string, unknown>> = [];
      installApi((url, init) => {
        if (url === "/api/v1/status") {
          return success({
            initialized: true,
            password_enabled: false,
            session_epoch: 1,
            initialized_at: "2026-08-01T00:00:00Z",
          });
        }
        if (url.endsWith("/comments") && init?.method !== "POST") {
          return success({ items: [root] });
        }
        if (url === "/api/v1/login/session" && init?.method === "POST") {
          return success({
            csrf_token: "csrf-new",
            expires_at: "2999-08-11T01:00:00Z",
          });
        }
        if (url.includes("/posts/") && init?.method === "POST") {
          createTokens.push(new Headers(init.headers).get("X-CSRF-Token"));
          createBodies.push(JSON.parse(String(init.body)));
          return success(
            comment(createdId, content, { canPublish: true, parentId }),
            { replayed: false },
            201,
          );
        }
      });
      renderThread(site, true);

      await screen.findByText("顶级回应");
      if (openReply) {
        fireEvent.click(screen.getByRole("button", { name: "回复 访客" }));
      }
      let form = screen.getByRole("form", { name: formName });
      let field = within(form).getByRole("textbox", { name: fieldName });
      fireEvent.change(field, { target: { value: content } });
      // This harness control simulates expiration outside the modal.
      fireEvent.click(screen.getByText("使会话过期"));

      expect(field).toHaveValue(content);
      if (!openReply)
        expect(
          within(form.parentElement!).getByRole("alert"),
        ).toHaveTextContent("文字已保留");
      expect(screen.queryByLabelText("API 令牌")).toBeNull();
      expect(window.location.pathname).toBe("/login");
      expect(new URLSearchParams(window.location.search).get("return")).toBe(
        `/m/${postId}`,
      );
      await act(() => completeStandaloneSignIn());
      form = await screen.findByRole("form", { name: formName });
      field = within(form).getByRole("textbox", { name: fieldName });
      await waitFor(() =>
        expect(
          within(form).getByRole("button", { name: submitName }),
        ).toBeEnabled(),
      );
      expect(field).toHaveValue(content);
      expect(field).toHaveFocus();
      fireEvent.click(within(form).getByRole("button", { name: submitName }));

      await waitFor(() => expect(createTokens).toHaveLength(1));
      expect(await screen.findByText(content)).toBeInTheDocument();
      expect(createTokens).toEqual(["csrf-new"]);
      expect(createBodies).toEqual([
        {
          content_md: content,
          parent_id: parentId,
          // Both cases answer the comment they hang off — the top-level one
          // answers nobody, the reply answers the root it was opened from —
          // so the target tracks parent_id here rather than diverging from it.
          reply_to_comment_id: parentId,
          media: [],
          location: null,
        },
      ]);
    },
  );

  it("cascades a confirmed root deletion and restores focus to the thread heading", async () => {
    preauthenticate();
    vi.spyOn(window, "scrollY", "get").mockReturnValue(615);
    installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE") {
        return success({ items: [root, reply] });
      }
      if (url.endsWith(`/comments/${rootId}`)) {
        return success(root, {
          deleted: true,
          dry_run: false,
          replies_affected: 1,
        });
      }
    });
    renderThread();

    await screen.findByText("已有一级回复");
    expect(screen.getAllByText("作者")).toHaveLength(1);
    expect(screen.getByText("Moment")).toBeInTheDocument();
    await chooseFirstCommentAction("删除");
    expect(await screen.findByRole("dialog")).toHaveTextContent(
      "下面的 1 条回复",
    );
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    fireEvent.click(screen.getByRole("button", { name: "删除回复及其对话" }));

    await waitFor(() =>
      expect(screen.queryByText("已有一级回复")).not.toBeInTheDocument(),
    );
    expect(screen.queryByText("顶级回应")).not.toBeInTheDocument();
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "回复" })).toHaveFocus(),
    );
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");
  });

  it("moves a pinned root with its replies as one group and restores chronology after unpin", async () => {
    preauthenticate();
    const earlierRoot = comment(createdId, "更早的顶级回应", {
      createdAt: "2026-08-10T01:02:00Z",
    });
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (
        url.endsWith(`/comments/${rootId}/pin`) &&
        init?.method === "DELETE"
      ) {
        return success({ post_id: postId, pinned_comment_id: null });
      }
      if (url.endsWith("/comments") && init?.method !== "POST") {
        return success({
          items: [earlierRoot, root, reply],
          pinned_comment_id: rootId,
        });
      }
    });
    renderThread();

    await screen.findByText("已有一级回复");
    const order = () =>
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-comment-id]"),
      ).map((item) => item.dataset.commentId);
    expect(order()).toEqual([rootId, replyId, createdId]);
    expect(screen.getByText("已置顶")).toBeVisible();

    await chooseFirstCommentAction("取消置顶");
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("回到这段对话中的原始时间位置");
    fireEvent.click(within(dialog).getByRole("button", { name: "取消置顶" }));

    await waitFor(() => expect(order()).toEqual([createdId, rootId, replyId]));
    expect(screen.queryByText("已置顶")).not.toBeInTheDocument();
    expect(
      fetcher.mock.calls.some(
        ([url, init]) =>
          String(url).endsWith(`/comments/${rootId}/pin`) &&
          init?.method === "DELETE",
      ),
    ).toBe(true);
  });

  it("keeps the delete dialog mounted through standalone reauthentication and retries with new CSRF", async () => {
    preauthenticate();
    let deletes = 0;
    const deleteTokens: Array<string | null> = [];
    installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE") {
        return success({ items: [root] });
      }
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        return success({
          csrf_token: "csrf-new",
          expires_at: "2999-08-11T01:00:00Z",
        });
      }
      if (url.endsWith(`/comments/${rootId}`)) {
        deletes += 1;
        deleteTokens.push(new Headers(init?.headers).get("X-CSRF-Token"));
        return deletes === 1
          ? unauthorized()
          : success(root, {
              deleted: true,
              dry_run: false,
              replies_affected: 0,
            });
      }
    });
    renderThread(site, true);

    await screen.findByText("顶级回应");
    await chooseFirstCommentAction("删除");
    fireEvent.click(await screen.findByRole("button", { name: "删除回复" }));
    expect(screen.queryByLabelText("API 令牌")).toBeNull();
    await act(() => completeStandaloneSignIn());
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "删除回复" }),
      ).toBeEnabled(),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "删除回复" }));
    await waitFor(() =>
      expect(screen.queryByText("顶级回应")).not.toBeInTheDocument(),
    );
    expect(deleteTokens).toEqual(["csrf-from-cookie", "csrf-new"]);
  });

  it("requires standalone sign-in before opening an expired delete action", async () => {
    preauthenticate();
    const deleteTokens: Array<string | null> = [];
    installApi((url, init) => {
      if (url === "/api/v1/status") {
        return success({
          initialized: true,
          password_enabled: false,
          session_epoch: 1,
          initialized_at: "2026-08-01T00:00:00Z",
        });
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE") {
        return success({ items: [root] });
      }
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        return success({
          csrf_token: "csrf-new",
          expires_at: "2999-08-11T01:00:00Z",
        });
      }
      if (url.endsWith(`/comments/${rootId}`)) {
        deleteTokens.push(new Headers(init?.headers).get("X-CSRF-Token"));
        return success(root, {
          deleted: true,
          dry_run: false,
          replies_affected: 0,
        });
      }
    });
    renderThread(site, true);

    await screen.findByText("顶级回应");
    // This harness control simulates expiration outside the modal.
    fireEvent.click(screen.getByText("使会话过期"));
    await chooseFirstCommentAction("删除");
    expect(window.location.pathname).toBe("/login");
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByLabelText("API 令牌")).toBeNull();
    await act(() => completeStandaloneSignIn());
    await chooseFirstCommentAction("删除");
    const dialog = await screen.findByRole("dialog");
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "删除回复" }),
      ).toBeEnabled(),
    );
    expect(dialog).toBeInTheDocument();
    fireEvent.click(within(dialog).getByRole("button", { name: "删除回复" }));

    await waitFor(() =>
      expect(screen.queryByText("顶级回应")).not.toBeInTheDocument(),
    );
    expect(deleteTokens).toEqual(["csrf-new"]);
    await waitFor(() =>
      expect(screen.getByRole("heading", { name: "回复" })).toHaveFocus(),
    );
  });
  it("allows an active member to delete their own reply without offering pin controls", async () => {
    preauthenticate();
    const own = {
      ...comment(rootId, "我的回复", { canPublish: true }),
      reply_count: 0,
    };
    installApi((url, init) => {
      if (url === "/api/v1/account/session") {
        const payload = accountSessionEnvelope();
        payload.data.account.role = "member";
        return json(payload);
      }
      if (url.endsWith("/comments") && init?.method !== "DELETE")
        return success({ items: [own] });
      if (url.endsWith(`/comments/${rootId}`) && init?.method === "DELETE")
        return success(own, {
          deleted: true,
          dry_run: false,
          replies_affected: 0,
        });
    });
    renderThread();
    await screen.findByText("我的回复");
    await openCommentMenu();
    expect(
      await screen.findByRole("menuitem", { name: "删除" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: "置顶回复" })).toBeNull();
    fireEvent.click(screen.getByRole("menuitem", { name: "删除" }));
    fireEvent.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: "删除回复",
      }),
    );
    await waitFor(() => expect(screen.queryByText("我的回复")).toBeNull());
  });
  it("isolates retained reply attachments by target and preserves them while the media viewer is inactive", async () => {
    preauthenticate();
    installApi((url, init) => {
      if (url.endsWith("/comments") && init?.method !== "POST")
        return success({ items: [root, comment(createdId, "另一条根评论")] });
    });
    const api = new PublicApi();
    const show = (active: boolean) => (
      <NavigationProvider>
        <SessionProvider>
          <CommentThread
            active={active}
            api={api}
            post={post}
            postId={postId}
            site={site}
            instanceId="media-test"
          />
        </SessionProvider>
      </NavigationProvider>
    );
    const view = render(show(true));
    await screen.findByText("顶级回应");
    const rowA = document.querySelector(
      '[data-comment-id="' + rootId + '"]',
    ) as HTMLElement;
    fireEvent.click(within(rowA).getByRole("button", { name: "回复 访客" }));
    const first = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    fireEvent.change(within(first).getByLabelText("回复内容"), {
      target: { value: "A 的草稿" },
    });
    fireEvent.change(within(first).getByLabelText("媒体文件"), {
      target: {
        files: [new File(["image"], "kept.png", { type: "image/png" })],
      },
    });
    expect(
      within(first).getByRole("button", { name: "移除第 1 张图片" }),
    ).toBeInTheDocument();
    fireEvent.click(
      within(first).getByRole("button", { name: "关闭回复窗口" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    const rowB = document.querySelector(
      '[data-comment-id="' + createdId + '"]',
    ) as HTMLElement;
    fireEvent.click(within(rowB).getByRole("button", { name: "回复 访客" }));
    const second = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    expect(within(second).getByLabelText("回复内容")).toHaveValue("");
    expect(
      within(second).queryByRole("button", { name: "移除第 1 张图片" }),
    ).toBeNull();
    fireEvent.click(
      within(second).getByRole("button", { name: "关闭回复窗口" }),
    );
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    fireEvent.click(within(rowA).getByRole("button", { name: "回复 访客" }));
    const restored = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    expect(within(restored).getByLabelText("回复内容")).toHaveValue("A 的草稿");
    expect(
      within(restored).getByRole("button", { name: "移除第 1 张图片" }),
    ).toBeInTheDocument();
    view.rerender(show(false));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    view.rerender(show(true));
    fireEvent.click(within(rowA).getByRole("button", { name: "回复 访客" }));
    const resumed = await screen.findByRole("dialog", {
      name: "回复这条 Moment",
    });
    expect(within(resumed).getByLabelText("回复内容")).toHaveValue("A 的草稿");
    expect(
      within(resumed).getByRole("button", { name: "移除第 1 张图片" }),
    ).toBeInTheDocument();
  });
});
