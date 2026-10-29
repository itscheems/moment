import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  createEvent,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { mediaPreviewUrl } from "../../api/mediaUrls";
import { App } from "../../app/App";
import composerStyles from "../../components/Forms.module.css";
import {
  COMPOSE_DRAFT_CLEARANCE_SCHEMA,
  COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
  COMPOSE_DRAFT_CLEARED_EVENT,
  COMPOSE_DRAFT_CLEARANCE_CHANNEL,
  composeDraftClearAllKey,
  composeDraftClearanceKey,
  composeDraftKey,
  loadComposeDraft,
} from "./draft";
import { keepDraft, listKeptDrafts } from "./drafts";
import { makeAccountLimits } from "../../test/accountFixtures";

const instance =
  "0123456789abcdef0123456789abcdef.00000000000000000000000000000000";
const sessionKey = "moment.auth.session.v2";

class FakeBroadcastChannel {
  static channels = new Map<string, Set<FakeBroadcastChannel>>();
  static posted: unknown[] = [];

  readonly name: string;
  private readonly listeners = new Set<
    (event: MessageEvent<unknown>) => void
  >();

  constructor(name: string) {
    this.name = name;
    const peers = FakeBroadcastChannel.channels.get(name) ?? new Set();
    peers.add(this);
    FakeBroadcastChannel.channels.set(name, peers);
  }

  addEventListener(
    type: string,
    listener: (event: MessageEvent<unknown>) => void,
  ) {
    if (type === "message") this.listeners.add(listener);
  }

  removeEventListener(
    type: string,
    listener: (event: MessageEvent<unknown>) => void,
  ) {
    if (type === "message") this.listeners.delete(listener);
  }

  postMessage(data: unknown) {
    FakeBroadcastChannel.posted.push(data);
    for (const peer of FakeBroadcastChannel.channels.get(this.name) ?? []) {
      if (peer === this) continue;
      queueMicrotask(() => peer.receive(data));
    }
  }

  close() {
    FakeBroadcastChannel.channels.get(this.name)?.delete(this);
    this.listeners.clear();
  }

  private receive(data: unknown) {
    const event = new MessageEvent("message", { data });
    for (const listener of this.listeners) listener(event);
  }

  static reset() {
    for (const peers of FakeBroadcastChannel.channels.values()) {
      for (const peer of peers) peer.close();
    }
    FakeBroadcastChannel.channels.clear();
    FakeBroadcastChannel.posted = [];
  }
}

class ControlledUploadRequest extends EventTarget {
  static requests: ControlledUploadRequest[] = [];

  readonly upload = new EventTarget();
  readonly headers = new Map<string, string>();
  method = "";
  url = "";
  withCredentials = false;
  status = 0;
  responseText = "";
  sent: Document | XMLHttpRequestBodyInit | null = null;
  aborted = false;

  constructor() {
    super();
    ControlledUploadRequest.requests.push(this);
  }

  open(method: string, url: string) {
    this.method = method;
    this.url = url;
  }

  setRequestHeader(name: string, value: string) {
    this.headers.set(name, value);
  }

  getResponseHeader(name: string) {
    return name.toLowerCase() === "content-type" ? "application/json" : null;
  }

  send(body: Document | XMLHttpRequestBodyInit | null) {
    this.sent = body;
  }

  abort() {
    this.aborted = true;
    this.dispatchEvent(new Event("abort"));
    this.dispatchEvent(new Event("loadend"));
  }

  fail() {
    this.dispatchEvent(new Event("error"));
    this.dispatchEvent(new Event("loadend"));
  }

  succeed(body: unknown) {
    this.status = 201;
    this.responseText = JSON.stringify(body);
    this.dispatchEvent(new Event("load"));
    this.dispatchEvent(new Event("loadend"));
  }

  static reset() {
    ControlledUploadRequest.requests = [];
  }
}

const uploadEnvelope = {
  data: {
    media: {
      id: "01K2ABCDEF0123456789ABCDE7",
      sha256: "a".repeat(64),
      original_name: "rain.png",
      mime_type: "image/png",
      byte_size: 5,
      width: 800,
      height: 600,
      created_at: "2026-08-10T01:02:03.000Z",
      unreferenced_since: "2026-08-10T01:02:03.000Z",
      animated_original: false,
    },
    preview: {
      recipe_version: "preview-v1",
      sha256: "b".repeat(64),
      mime_type: "image/png",
      width: 800,
      height: 600,
      animated_original: false,
    },
    original_url: "/api/v1/media/01K2ABCDEF0123456789ABCDE7",
    preview_url: mediaPreviewUrl("01K2ABCDEF0123456789ABCDE7"),
  },
  meta: { request_id: "req_upload" },
};

function clearanceMessage(clearance: Record<string, unknown>) {
  return {
    schema: "moment.compose-clearance-message.v1",
    sender_id: "f".repeat(32),
    clearance,
  };
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const siteEnvelope = {
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
  meta: { instance_id: instance.split(".")[0], request_id: "req_site" },
};

function post(
  content = "刚写下的 Moment",
  id = "01K2ABCDEF0123456789ABCDE0",
  media: Array<{
    media_id: string;
    position: number;
    alt_text: string | null;
    mime_type?: string;
    width: number;
    height: number;
  }> = [],
) {
  return {
    author: {
      user_id: "00000000000000000000000000000000",
      username: "owner",
      display_name: "乔乔",
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
    media: media.map((usage) => ({
      ...usage,
      mime_type: usage.mime_type ?? "image/png",
    })),
    link_cards: [],
  };
}

function ownerStatus() {
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

function account(overrides: Record<string, unknown> = {}) {
  const merged = {
    user_id: "00000000000000000000000000000000",
    username: "owner",
    display_name: "乔乔",
    bio: "",
    avatar_media_id: null,
    cover_media_id: null,
    role: "admin",
    status: "active",
    username_chosen: true,
    ...overrides,
  };
  return { ...merged, profile_url: `/${String(merged.username)}` };
}

function accountSession(overrides: Record<string, unknown> = {}) {
  return json({
    data: {
      preferences: { ui_locale: null },
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: account(overrides),
      limits: makeAccountLimits(),
    },
    meta: { request_id: "req_account_session" },
  });
}

function unauthorized() {
  return json(
    {
      error: {
        code: "UNAUTHORIZED",
        message: "auth required",
        details: {},
        request_id: "req_auth",
      },
    },
    401,
  );
}

function csrfInvalid() {
  return json(
    {
      error: {
        code: "CSRF_INVALID",
        message: "csrf required",
        details: {},
        request_id: "req_csrf",
      },
    },
    403,
  );
}

function preauthenticate() {
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({
      csrf_token: "csrf-from-session",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
}

async function openMomentActions(trigger?: HTMLElement) {
  const button =
    trigger ?? (await screen.findByRole("button", { name: "更多操作" }));
  await waitFor(() => expect(button).toBeEnabled());
  await act(async () => {
    button.focus();
    fireEvent.keyDown(button, { key: "ArrowDown", code: "ArrowDown" });
    fireEvent.keyUp(button, { key: "ArrowDown", code: "ArrowDown" });
  });
  await screen.findByRole("menu");
}

async function keepDraftAndResumeNavigation() {
  const dialog = await screen.findByRole("dialog", { name: "退出发布？" });
  fireEvent.click(
    within(dialog).getByRole("button", { name: "保留草稿并退出" }),
  );
}

function installApi(
  override?: (
    url: string,
    init: RequestInit | undefined,
  ) => Promise<Response> | Response | undefined,
) {
  let cookieAuthenticated = Boolean(window.sessionStorage.getItem(sessionKey));
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const response = await override?.(url, init);
      if (
        url === "/api/v1/login/session" &&
        init?.method === "POST" &&
        response?.ok
      )
        cookieAuthenticated = true;
      if (response) return response;
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url === "/api/v1/status") return ownerStatus();
      if (url === "/api/v1/account/session") {
        // The server answers this from the session cookie; a stored session is
        // this suite's stand-in for a browser that holds one.
        return cookieAuthenticated ? accountSession() : unauthorized();
      }
      if (url === "/api/v1/login/oauth/providers") {
        return json({
          data: { providers: [] },
          meta: { request_id: "req_providers" },
        });
      }
      if (url.startsWith("/api/v1/posts?")) {
        return json({
          data: { items: [] },
          meta: { next_cursor: null, request_id: "req_posts" },
        });
      }
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

describe("owner composing flow", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
    window.localStorage.clear();
    window.sessionStorage.clear();
    FakeBroadcastChannel.reset();
    ControlledUploadRequest.reset();
    vi.stubGlobal("BroadcastChannel", FakeBroadcastChannel);
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
    vi.stubGlobal(
      "confirm",
      vi.fn(() => true),
    );
  });

  afterEach(() => {
    cleanup();
    FakeBroadcastChannel.reset();
    ControlledUploadRequest.reset();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("exchanges a bearer without storing it and restores CSRF across a refresh", async () => {
    // Composing needs a session, so a guest asking for it is sent here and
    // sent back; this is that page, with the address it would carry.
    window.history.replaceState({}, "", "/login?return=%2Fcompose");
    const bearer = "owner-bearer-never-store";
    const fetcher = installApi((url) => {
      if (url === "/api/v1/status") {
        return window.sessionStorage.getItem(sessionKey)
          ? ownerStatus()
          : unauthorized();
      }
      if (url === "/api/v1/login/session") {
        return json({
          data: {
            csrf_token: "csrf-exchanged",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: { request_id: "req_session" },
        });
      }
    });
    render(<App />);

    const token = await screen.findByLabelText("API 令牌");
    fireEvent.change(token, { target: { value: bearer } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    expect(await screen.findByLabelText("此刻想到什么？")).toBeInTheDocument();

    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-exchanged",
    );
    expect(window.sessionStorage.getItem(sessionKey)).not.toContain(bearer);
    expect(JSON.stringify(window.localStorage)).not.toContain(bearer);
    const exchange = fetcher.mock.calls.find(
      ([url]) => String(url) === "/api/v1/login/session",
    );
    expect(exchange?.[1]?.body).toBe(JSON.stringify({ token: bearer }));
  });

  it("searches and inserts the owner identity from an active @mention", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    const fetcher = installApi((url) => {
      if (url.startsWith("/api/v1/users/mention-suggestions?")) {
        return json({
          data: [
            {
              user_id: "00000000000000000000000000000000",
              username: "owner",
              display_name: "乔乔",
              avatar_media_id: null,
              profile_url: "/owner",
            },
          ],
          meta: { request_id: "req_mentions" },
        });
      }
    });
    render(<App />);

    const editor = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(editor, {
      target: { value: "今天想起 @ow", selectionStart: 9 },
    });
    const option = await screen.findByRole("option", { name: /乔乔.*@owner/ });
    const suggestions = screen.getByRole("listbox", {
      name: "提及用户建议",
    });
    expect(suggestions).toHaveAttribute("data-placement", "caret");
    expect(suggestions.parentElement).toContainElement(editor);
    expect(
      fetcher.mock.calls.some(([url]) => String(url).includes("q=ow")),
    ).toBe(true);
    fireEvent.click(option);
    expect(editor).toHaveValue("今天想起 @owner ");
    expect(
      document.querySelector('[data-mention-highlight="@owner"]'),
    ).toHaveTextContent("@owner");
  });

  // A stranded draft used to be offered back as a notice above the
  // composer: a second place drafts lived, drawn across the composer's own
  // layout. It goes straight into 草稿 now, where the rest of them are.
  it("files a draft stranded by a reload into 草稿 without asking", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "刷新前留下的草稿",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi();
    render(<App />);

    expect(await screen.findByLabelText("此刻想到什么？")).toHaveValue("");
    expect(screen.queryByText("发现一段未发布草稿")).toBeNull();
    await waitFor(() =>
      expect(listKeptDrafts(instance).map((draft) => draft.contentMd)).toEqual([
        "刷新前留下的草稿",
      ]),
    );
    // The recovery slot has handed it over rather than holding a copy.
    expect(loadComposeDraft(instance)).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: /草稿/ }));
    fireEvent.click(await screen.findByText("刷新前留下的草稿"));
    await waitFor(() =>
      expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
        "刷新前留下的草稿",
      ),
    );
  });

  // An old draft that is already filed keeps its place in the list rather
  // than being refiled and reading as though it were just written.
  it("does not refile a stranded draft that 草稿 already holds", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    const kept = keepDraft(
      instance,
      "上周写的",
      new Date(Date.now() - 7 * 24 * 60 * 60 * 1000),
    );
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "上周写的",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi();
    render(<App />);

    await screen.findByLabelText("此刻想到什么？");
    await waitFor(() => expect(loadComposeDraft(instance)).toBeNull());
    expect(listKeptDrafts(instance)).toEqual([kept]);
  });

  it("gives the recovery slot to new text typed over a filed draft", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "较早的草稿",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi();
    render(<App />);

    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "直接输入的新文字" },
    });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "直接输入的新文字",
    );
    // The older text was filed on the way in, not overwritten.
    expect(listKeptDrafts(instance).map((draft) => draft.contentMd)).toEqual([
      "较早的草稿",
    ]);
  });

  it("debounces the local draft, guards duplicate submit, and clears only after success", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    let resolveCreate!: (response: Response) => void;
    const createResponse = new Promise<Response>((resolve) => {
      resolveCreate = resolve;
    });
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return createResponse;
      }
    });
    render(<App />);
    const textarea = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(textarea, { target: { value: "十秒内写下这一刻" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "十秒内写下这一刻",
    );

    const publish = screen.getByRole("button", { name: "发布" });
    fireEvent.click(publish);
    fireEvent.click(publish);
    fireEvent.keyDown(textarea, { key: "Enter", ctrlKey: true });
    expect(
      fetcher.mock.calls.filter(
        ([url, init]) =>
          String(url) === "/api/v1/posts" && init?.method === "POST",
      ),
    ).toHaveLength(1);

    resolveCreate(json({ data: post("十秒内写下这一刻"), meta: {} }, 201));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(window.confirm).not.toHaveBeenCalled();
    expect(loadComposeDraft(instance)).toBeNull();
    await waitFor(
      () => expect(screen.getByText("十秒内写下这一刻")).toBeInTheDocument(),
      { timeout: 3_000 },
    );
    const published = screen.getByRole("dialog", {
      name: "Moment 已发布",
    });
    expect(
      within(published).getByRole("button", { name: "查看 Moment" }),
    ).toBeInTheDocument();
    fireEvent.click(
      within(published).getByRole("button", { name: "查看 Moment" }),
    );
    expect(window.location.pathname).toBe("/m/01K2ABCDEF0123456789ABCDE0");
  });

  it("turns an empty quote into a repost instead of creating an empty wrapper", async () => {
    preauthenticate();
    const quotedId = "01K2ABCDEF0123456789ABCDE9";
    window.history.replaceState({}, "", `/compose?quote=${quotedId}`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${quotedId}` && !init?.method) {
        return json({
          data: post("原始 Moment", quotedId),
          meta: { request_id: "req_quote" },
        });
      }
      if (
        url === `/api/v1/posts/${quotedId}/repost` &&
        init?.method === "PUT"
      ) {
        return json({
          data: {
            ...post("原始 Moment", quotedId),
            reposted_at: "2026-08-10T02:00:00.000Z",
            visibility: "everyone",
            timeline_item_kind: "original",
          },
          meta: { request_id: "req_repost" },
        });
      }
    });
    render(<App />);

    expect(await screen.findByText("原始 Moment")).toBeInTheDocument();
    const publish = screen.getByRole("button", { name: "发布" });
    expect(publish).toBeEnabled();
    fireEvent.click(publish);

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(
      fetcher.mock.calls.filter(
        ([url, init]) =>
          String(url) === `/api/v1/posts/${quotedId}/repost` &&
          init?.method === "PUT",
      ),
    ).toHaveLength(1);
    expect(
      fetcher.mock.calls.some(
        ([url, init]) =>
          String(url) === "/api/v1/posts" && init?.method === "POST",
      ),
    ).toBe(false);
    expect(screen.getByText("已转发到时间流顶部")).toBeInTheDocument();
  });

  it("keeps a quote with commentary as a new nested Moment", async () => {
    preauthenticate();
    const quotedId = "01K2ABCDEF0123456789ABCDE9";
    window.history.replaceState({}, "", `/compose?quote=${quotedId}`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${quotedId}` && !init?.method) {
        return json({
          data: post("原始 Moment", quotedId),
          meta: { request_id: "req_quote" },
        });
      }
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return json(
          {
            data: {
              ...post("我的补充", "01K2ABCDEF0123456789ABCDE8"),
              quoted_post: {
                author: {
                  user_id: "00000000000000000000000000000000",
                  username: "owner",
                  display_name: "乔乔",
                  avatar_media_id: null,
                  profile_url: "/owner",
                },
                unavailable: false,
                article: null,
                id: quotedId,
                kind: "moment",
                content_md: "原始 Moment",
                content_html: "<p>原始 Moment</p>",
                published_at: "2026-08-10T01:00:00.000Z",
                location: null,
                media: [],
              },
            },
            meta: { request_id: "req_create_quote" },
          },
          201,
        );
      }
    });
    render(<App />);

    expect(await screen.findByText("原始 Moment")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("此刻想到什么？"), {
      target: { value: "我的补充" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    const create = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url) === "/api/v1/posts" && init?.method === "POST",
    );
    expect(JSON.parse(String(create?.[1]?.body))).toMatchObject({
      content_md: "我的补充",
      quoted_post_id: quotedId,
    });
  });

  it.each(["", "我的评论补充"])(
    "publishes a comment quote with body %j through the normal composer",
    async (content) => {
      preauthenticate();
      const originalId = "01K2ABCDEF0123456789ABCDE9",
        commentId = "01K2ABCDEF0123456789ABCDE7";
      window.history.replaceState(
        {},
        "",
        `/compose?quoteComment=${commentId}&quotePost=${originalId}`,
      );
      const comment = {
        id: commentId,
        post_id: originalId,
        parent_id: null,
        depth: 0,
        author_name: "乔乔",
        author: post().author,
        reply_to: null,
        content_md: "被引用评论",
        content_html: "<p>被引用评论</p>\n",
        created_at: "2026-08-10T01:00:00Z",
        media: [],
        reply_count: 0,
      };
      const fetcher = installApi((url, init) => {
        if (url === `/api/v1/posts/${originalId}` && !init?.method)
          return json({ data: post("原帖", originalId), meta: {} });
        if (
          url === `/api/v1/posts/${originalId}/comments/${commentId}` &&
          !init?.method
        )
          return json({
            data: {
              comment,
              reply_target: null,
              direct_reply_count: 0,
              root_comment_id: commentId,
              pinned_comment_id: null,
            },
            meta: {},
          });
        if (url === "/api/v1/posts" && init?.method === "POST")
          return json(
            {
              data: {
                ...post(content),
                content_html: content ? `<p>${content}</p>` : "",
                quoted_post: {
                  id: commentId,
                  comment_post_id: originalId,
                  author: post().author,
                  unavailable: false,
                  kind: "moment",
                  article: null,
                  content_md: comment.content_md,
                  content_html: comment.content_html,
                  published_at: comment.created_at,
                  media: [],
                  location: null,
                  map_card: null,
                },
              },
              meta: {},
            },
            201,
          );
      });
      render(<App />);
      expect(await screen.findByText("被引用评论")).toBeInTheDocument();
      if (content)
        fireEvent.change(screen.getByLabelText("此刻想到什么？"), {
          target: { value: content },
        });
      fireEvent.click(screen.getByRole("button", { name: "发布" }));
      await waitFor(() => expect(window.location.pathname).toBe("/"));
      const call = fetcher.mock.calls.find(
        ([url, init]) =>
          String(url) === "/api/v1/posts" && init?.method === "POST",
      );
      expect(JSON.parse(String(call?.[1]?.body))).toMatchObject({
        content_md: content,
        quoted_comment_id: commentId,
      });
      expect(JSON.parse(String(call?.[1]?.body))).not.toHaveProperty(
        "quoted_post_id",
      );
      expect(
        fetcher.mock.calls.some(([url]) => String(url).endsWith("/repost")),
      ).toBe(false);
    },
  );

  it("reuses an uploaded media ID when post submission fails and retries", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    let creates = 0;
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        creates += 1;
        if (creates === 1) {
          return json(
            {
              error: {
                code: "INTERNAL",
                message: "temporary",
                details: {},
                request_id: "req_post_failed",
              },
            },
            500,
          );
        }
        return json(
          {
            data: post("雨落下来", "01K2ABCDEF0123456789ABCDE0", [
              {
                media_id: uploadEnvelope.data.media.id,
                position: 0,
                alt_text: null,
                width: uploadEnvelope.data.media.width,
                height: uploadEnvelope.data.media.height,
              },
            ]),
            meta: { request_id: "req_post_created", replayed: false },
          },
          201,
        );
      }
    });
    render(<App />);

    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "雨落下来" },
    });
    const file = new File(["image"], "rain.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: { files: [file] },
    });
    await waitFor(() =>
      expect(ControlledUploadRequest.requests).toHaveLength(1),
    );
    ControlledUploadRequest.requests[0].succeed(uploadEnvelope);
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "发布" })).toBeEnabled(),
    );
    fireEvent.click(screen.getByRole("button", { name: "发布" }));
    expect(await screen.findByText(/操作没有完成/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "发布" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(ControlledUploadRequest.requests).toHaveLength(1);
    const createCalls = fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url) === "/api/v1/posts" && init?.method === "POST",
    );
    expect(createCalls).toHaveLength(2);
    for (const [, init] of createCalls) {
      expect(JSON.parse(String(init?.body)).media).toEqual([
        {
          media_id: uploadEnvelope.data.media.id,
          position: 0,
          alt_text: null,
        },
      ]);
    }
  });

  it("keeps text paste native and manages image or mixed clipboard ingress", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    installApi();
    render(<App />);

    const textarea = await screen.findByLabelText("此刻想到什么？");
    const textPaste = createEvent.paste(textarea, {
      bubbles: true,
      cancelable: true,
      clipboardData: {
        files: [],
        getData: () => "只粘贴文字",
        types: ["text/plain"],
      },
    });
    fireEvent(textarea, textPaste);
    expect(textPaste.defaultPrevented).toBe(false);
    expect(screen.queryByText("1 / 9")).not.toBeInTheDocument();

    const image = new File(["image"], "clipboard.png", {
      type: "image/png",
    });
    const imagePaste = createEvent.paste(textarea, {
      bubbles: true,
      cancelable: true,
      clipboardData: {
        files: [image],
        getData: () => "",
        types: ["Files"],
      },
    });
    fireEvent(textarea, imagePaste);
    expect(imagePaste.defaultPrevented).toBe(true);
    expect(
      await screen.findByRole("button", { name: "预览第 1 张图片，共 1 张" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("1 / 9")).not.toBeInTheDocument();

    const mixedImage = new File(["image"], "mixed.png", {
      type: "image/png",
    });
    const mixedPaste = createEvent.paste(textarea, {
      bubbles: true,
      cancelable: true,
      clipboardData: {
        files: [mixedImage],
        getData: () => "不要重复插入这段伴随文字",
        types: ["text/plain", "Files"],
      },
    });
    fireEvent(textarea, mixedPaste);
    expect(mixedPaste.defaultPrevented).toBe(true);
    expect(textarea).toHaveValue("");
    expect(
      await screen.findByRole("button", { name: "预览第 2 张图片，共 2 张" }),
    ).toBeInTheDocument();

    const overLimitPaste = createEvent.paste(textarea, {
      bubbles: true,
      cancelable: true,
      clipboardData: {
        files: Array.from(
          { length: 8 },
          (_, index) =>
            new File(["image"], `overflow-${index}.png`, {
              type: "image/png",
            }),
        ),
        getData: () => "",
        types: ["Files"],
      },
    });
    fireEvent(textarea, overLimitPaste);
    expect(overLimitPaste.defaultPrevented).toBe(true);
    expect(
      await screen.findByRole("button", { name: "预览第 9 张图片，共 9 张" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("9 / 9")).not.toBeInTheDocument();
    expect(
      screen.getByText("每条Moment最多附加 9 张图片；多出的文件没有加入。"),
    ).toBeInTheDocument();

    const rejectedMixedPaste = createEvent.paste(textarea, {
      bubbles: true,
      cancelable: true,
      clipboardData: {
        files: [new File(["image"], "tenth.png", { type: "image/png" })],
        getData: () => "仍由浏览器处理的文字",
        types: ["text/plain", "Files"],
      },
    });
    fireEvent(textarea, rejectedMixedPaste);
    expect(rejectedMixedPaste.defaultPrevented).toBe(false);
    expect(
      screen.getByRole("button", { name: "预览第 9 张图片，共 9 张" }),
    ).toBeInTheDocument();
  });

  it("publishes a Moment that contains only an image", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return json(
          {
            data: post("", "01K2ABCDEF0123456789ABCDE8", [
              {
                media_id: uploadEnvelope.data.media.id,
                position: 0,
                alt_text: null,
                width: uploadEnvelope.data.media.width,
                height: uploadEnvelope.data.media.height,
              },
            ]),
            meta: { request_id: "req_image_only", replayed: false },
          },
          201,
        );
      }
    });
    render(<App />);

    await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: {
        files: [new File(["image"], "only.png", { type: "image/png" })],
      },
    });
    const publish = screen.getByRole("button", { name: "发布" });
    expect(publish).toBeDisabled();
    await waitFor(() =>
      expect(ControlledUploadRequest.requests).toHaveLength(1),
    );
    ControlledUploadRequest.requests[0].succeed(uploadEnvelope);
    await waitFor(() => expect(publish).toBeEnabled());
    fireEvent.click(publish);

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    const create = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url) === "/api/v1/posts" && init?.method === "POST",
    );
    expect(JSON.parse(String(create?.[1]?.body))).toEqual({
      content_md: "",
      visibility: "everyone",
      media: [
        {
          media_id: uploadEnvelope.data.media.id,
          position: 0,
          alt_text: null,
        },
      ],
    });
  });

  it("renders an openable local preview without extending the text draft", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    installApi();
    render(
      <StrictMode>
        <App />
      </StrictMode>,
    );

    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "预览仍不进入草稿" },
    });
    const file = new File(["image"], "rain.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: { files: [file] },
    });

    await waitFor(() =>
      expect(
        document.querySelector<HTMLImageElement>(
          'ol[aria-label="待发布图片，按发布顺序"] img',
        )?.src,
      ).toMatch(/^blob:/),
    );
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(
      JSON.parse(window.localStorage.getItem(composeDraftKey(instance))!),
    ).toEqual({
      content_md: "预览仍不进入草稿",
      saved_at: expect.any(String),
    });
  });

  it("aborts removed uploads and ignores a late success without reviving the usage", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    const fetcher = installApi();
    render(<App />);

    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "不会被迟到上传污染" },
    });
    fireEvent.change(screen.getByLabelText("媒体文件"), {
      target: {
        files: [new File(["image"], "rain.png", { type: "image/png" })],
      },
    });
    await waitFor(() =>
      expect(ControlledUploadRequest.requests).toHaveLength(1),
    );
    ControlledUploadRequest.requests[0].fail();
    expect(
      await screen.findByRole("button", { name: /重试上传/ }),
    ).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: /重试上传/ }));
    await waitFor(() =>
      expect(ControlledUploadRequest.requests).toHaveLength(2),
    );
    fireEvent.click(screen.getByRole("button", { name: /移除/ }));
    expect(ControlledUploadRequest.requests[1].aborted).toBe(true);
    expect(screen.queryByText("第 1 张")).toBeNull();
    ControlledUploadRequest.requests[1].succeed(uploadEnvelope);
    await new Promise((resolve) => window.setTimeout(resolve, 0));
    expect(screen.queryByText("第 1 张")).toBeNull();
    expect(
      fetcher.mock.calls.filter(
        ([url, init]) =>
          String(url) === "/api/v1/posts" && init?.method === "POST",
      ),
    ).toHaveLength(0);
  });

  it("leaves standalone sign-in when the cookie already authenticates the browser", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/login?return=%2Fsearch%3Fq%3Dnight");
    installApi();
    render(<App />);
    await waitFor(() => expect(window.location.pathname).toBe("/search"));
    expect(window.location.search).toBe("?q=night");
    expect(screen.queryByRole("heading", { name: "Happening now" })).toBeNull();
  });

  it("keeps expired home sign-in on its own page and preserves the compose draft", async () => {
    preauthenticate();
    let reauthenticated = false;
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return unauthorized();
      }
      if (url === "/api/v1/account/session" && reauthenticated) {
        return accountSession();
      }
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        reauthenticated = true;
        return json({
          data: {
            csrf_token: "csrf-from-cookie",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: {},
        });
      }
    });
    render(<App />);
    const composer = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(composer, { target: { value: "重新登录也要留下这句话" } });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    expect(new URLSearchParams(window.location.search).get("return")).toBe("/");
    expect(screen.queryByRole("navigation", { name: "主要导航" })).toBeNull();
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "重新登录也要留下这句话",
    );
    const credential = await screen.findByLabelText("API 令牌");
    expect(window.location.pathname).toBe("/login");
    fireEvent.change(credential, { target: { value: "fresh-owner-token" } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(await screen.findByLabelText("此刻想到什么？")).toHaveValue(
      "重新登录也要留下这句话",
    );
    expect(screen.queryByLabelText("API 令牌")).toBeNull();
  });

  it("preserves input before an auth-expiry response and requires a fresh exchange", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return unauthorized();
      }
    });
    render(<App />);
    const textarea = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(textarea, { target: { value: "会话失效也不能丢" } });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "会话失效也不能丢",
    );
    expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
  });

  it("restores the compose dialog and its background after reauthentication", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/search?q=night");
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 720px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    let reauthenticated = false;
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST")
        return unauthorized();
      if (url === "/api/v1/account/session" && reauthenticated)
        return accountSession();
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        reauthenticated = true;
        return json({
          data: {
            csrf_token: "csrf-from-cookie",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: {},
        });
      }
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("link", { name: "发帖" }));
    const dialog = await screen.findByRole("dialog", { name: "发布 Moment" });
    fireEvent.change(within(dialog).getByLabelText("此刻想到什么？"), {
      target: { value: "回到原来的发布弹窗" },
    });
    fireEvent.click(within(dialog).getByRole("button", { name: "发布" }));
    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "发布 Moment" })).toBeNull(),
    );
    fireEvent.change(await screen.findByLabelText("API 令牌"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    const restored = await screen.findByRole("dialog", { name: "发布 Moment" });
    expect(within(restored).getByLabelText("此刻想到什么？")).toHaveValue(
      "回到原来的发布弹窗",
    );
    expect(window.history.state.momentOverlayFrom).toEqual({
      pathname: "/search",
      search: "?q=night",
    });
  });

  it("redirects an expired profile edit without leaving its modal over sign-in", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner");
    let reauthenticated = false;
    installApi((url, init) => {
      if (url === "/api/v1/account/profile" && init?.method === "PATCH") {
        return unauthorized();
      }
      if (url === "/api/v1/account/session" && reauthenticated)
        return accountSession();
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        reauthenticated = true;
        return json({
          data: {
            csrf_token: "csrf-from-cookie",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: {},
        });
      }
    });
    render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "编辑资料" }));
    fireEvent.change(screen.getByLabelText("显示名称"), {
      target: { value: "未保存的名字" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存" }));
    await waitFor(() => expect(window.location.pathname).toBe("/login"));
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "编辑资料" })).toBeNull(),
    );
    fireEvent.change(await screen.findByLabelText("API 令牌"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() =>
      expect(window.location.pathname).toBe("/settings/profile"),
    );
    expect(
      await screen.findByRole("dialog", { name: "编辑资料" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("显示名称")).toHaveValue("未保存的名字");
  });

  it("uses the timeline composer layout for editing an existing post", async () => {
    preauthenticate();
    const item = post("编辑原有内容");
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    installApi((url) => {
      if (url === `/api/v1/posts/${item.id}`)
        return json({ data: item, meta: {} });
    });
    render(<App />);

    const editor = await screen.findByLabelText("Moment 正文");
    const composer = screen.getByRole("region", { name: "编辑 Moment" });
    expect(composer).toHaveClass(composerStyles.inlineComposer);
    expect(composer).not.toHaveClass(composerStyles.pageComposer);
    expect(editor).toHaveAttribute("rows", "1");
    expect(
      within(composer).queryByRole("progressbar", { name: "正文字数" }),
    ).toBeNull();
    expect(
      within(composer).getByRole("button", { name: "保存更改" }),
    ).toBeInTheDocument();
  });

  it("undoes and redoes text and map removal in order, then saves the removal", async () => {
    preauthenticate();
    const item = {
      ...post("保留正文"),
      map_card: {
        places: [
          {
            name: "东京",
            display_name: "日本",
            latitude: 35.67,
            longitude: 139.76,
            coordinate_system: "wgs84",
          },
        ],
        connect_points: false,
      },
    };
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    let saved: Record<string, unknown> | undefined;
    vi.stubGlobal(
      "IntersectionObserver",
      class {
        observe() {}
        disconnect() {}
      },
    );
    installApi((url, init) => {
      if (url === `/api/v1/posts/${item.id}` && init?.method === "PATCH") {
        saved = JSON.parse(String(init.body));
        return json({ data: { ...item, map_card: null }, meta: {} });
      }
      if (url === `/api/v1/posts/${item.id}`)
        return json({ data: item, meta: {} });
    });
    render(<App />);
    const editor = await screen.findByLabelText("Moment 正文");
    fireEvent.change(editor, { target: { value: "保留正文，继续编辑" } });
    fireEvent.click(await screen.findByRole("button", { name: "移除地图" }));
    await waitFor(() => expect(editor).toHaveFocus());
    expect(screen.queryByRole("button", { name: "展开地图" })).toBeNull();
    fireEvent.keyDown(editor, { key: "z", metaKey: true });
    expect(
      screen.getByRole("button", { name: "移除地图" }),
    ).toBeInTheDocument();
    expect(editor).toHaveValue("保留正文，继续编辑");
    fireEvent.keyDown(editor, { key: "z", ctrlKey: true });
    expect(editor).toHaveValue("保留正文");
    fireEvent.keyDown(editor, { key: "z", metaKey: true, shiftKey: true });
    expect(editor).toHaveValue("保留正文，继续编辑");
    fireEvent.keyDown(editor, { key: "y", ctrlKey: true });
    expect(screen.queryByRole("button", { name: "展开地图" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));
    await waitFor(() => expect(saved?.map_card).toBeNull());
  });

  it("keeps an edited post mounted through auth expiry, re-login, and retry", async () => {
    preauthenticate();
    const item = post("编辑前", "01K2ABCDEF0123456789ABCDE8");
    let current = item;
    let patches = 0;
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    const fetcher = installApi((url, init) => {
      if (
        url === `/api/v1/posts/${item.id}` &&
        (!init?.method || init.method === "GET")
      )
        return json({ data: current, meta: {} });
      if (url === `/api/v1/posts/${item.id}` && init?.method === "PATCH") {
        patches += 1;
        if (patches === 1) return unauthorized();
        current = post("不会丢的编辑", item.id);
        return json({ data: current, meta: {} });
      }
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        return json({
          data: {
            csrf_token: "csrf-after-reauth",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: {},
        });
      }
    });
    render(<App />);

    const editor = await screen.findByLabelText("Moment 正文");
    fireEvent.change(editor, { target: { value: "不会丢的编辑" } });
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    expect(screen.getByLabelText("Moment 正文")).toHaveValue("不会丢的编辑");
    fireEvent.change(screen.getByLabelText("API 令牌"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(screen.queryByLabelText("API 令牌")).toBeNull());
    expect(screen.getByLabelText("Moment 正文")).toHaveValue("不会丢的编辑");
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() => expect(window.location.pathname).toBe(`/m/${item.id}`));
    const patchCalls = fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url) === `/api/v1/posts/${item.id}` && init?.method === "PATCH",
    );
    expect(patchCalls).toHaveLength(2);
    expect(new Headers(patchCalls[1]?.[1]?.headers).get("X-CSRF-Token")).toBe(
      "csrf-after-reauth",
    );
  });

  it("preserves a CSRF-invalid compose buffer for re-login instead of retrying forever", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    let creates = 0;
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        creates += 1;
        return creates === 1
          ? csrfInvalid()
          : json({
              data: post("跨标签页冲突也不丢", "01K2ABCDEF0123456789ABCDF0"),
              meta: {},
            });
      }
      if (url === "/api/v1/login/session" && init?.method === "POST") {
        return json({
          data: {
            csrf_token: "csrf-after-conflict",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: {},
        });
      }
    });
    render(<App />);
    const textarea = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(textarea, { target: { value: "跨标签页冲突也不丢" } });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "跨标签页冲突也不丢",
    );
    expect(creates).toBe(1);
    fireEvent.change(screen.getByLabelText("API 令牌"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() => expect(screen.queryByLabelText("API 令牌")).toBeNull());
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "跨标签页冲突也不丢",
    );
    fireEvent.click(screen.getByRole("button", { name: "发布" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(creates).toBe(2);
    const createCalls = fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url) === "/api/v1/posts" && init?.method === "POST",
    );
    expect(new Headers(createCalls[1]?.[1]?.headers).get("X-CSRF-Token")).toBe(
      "csrf-after-conflict",
    );
  });

  it("scopes global N away from editors and opens the page composer from the shell", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    inline.focus();
    fireEvent.keyDown(inline, { key: "n" });
    expect(window.location.pathname).toBe("/");

    fireEvent.keyDown(document.body, { key: "n", isComposing: true });
    expect(window.location.pathname).toBe("/");

    fireEvent.keyDown(document.body, { key: "n" });
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    expect(
      screen.getByRole("heading", { name: "发布 Moment" }),
    ).toBeInTheDocument();
  });

  it("opens the inline composer on focus with the audience pill and publishes the chosen audience", async () => {
    preauthenticate();
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return json(
          {
            data: { ...post("只给自己看"), visibility: "only_me" },
            meta: { request_id: "req_only_me", replayed: false },
          },
          201,
        );
      }
    });
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    const composer = inline.closest("section");
    // The collapsed composer does not show the audience pill.
    expect(composer).toHaveAttribute("data-expanded", "false");
    expect(
      screen.queryByRole("button", { name: "选择受众" }),
    ).not.toBeInTheDocument();

    fireEvent.focus(inline);
    expect(composer).toHaveAttribute("data-expanded", "true");
    const pill = screen.getByRole("button", { name: "选择受众" });
    expect(pill).toHaveTextContent("所有人");
    expect(pill).toHaveAttribute("aria-haspopup", "menu");

    // Leaving with nothing written closes it again; anything written keeps
    // it open.
    fireEvent.blur(inline, { relatedTarget: document.body });
    await waitFor(() =>
      expect(composer).toHaveAttribute("data-expanded", "false"),
    );
    fireEvent.focus(inline);
    fireEvent.change(inline, { target: { value: "只给自己看" } });
    fireEvent.blur(inline, { relatedTarget: document.body });
    expect(composer).toHaveAttribute("data-expanded", "true");

    fireEvent.click(screen.getByRole("button", { name: "选择受众" }));
    const card = await screen.findByRole("menu", { name: "选择受众" });
    const options = within(card).getAllByRole("menuitemradio");
    expect(options.map((option) => option.textContent)).toEqual([
      "所有人",
      "仅自己",
    ]);
    expect(options[0]).toHaveAttribute("aria-checked", "true");
    fireEvent.click(options[1]);
    await waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
    );
    expect(screen.getByRole("button", { name: "选择受众" })).toHaveTextContent(
      "仅自己",
    );

    fireEvent.keyDown(inline, { key: "Enter", metaKey: true });
    await waitFor(() =>
      expect(
        screen.getByRole("dialog", { name: "Moment 已发布" }),
      ).toBeInTheDocument(),
    );
    const create = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url) === "/api/v1/posts" && init?.method === "POST",
    );
    expect(JSON.parse(String(create?.[1]?.body))).toEqual({
      content_md: "只给自己看",
      media: [],
      visibility: "only_me",
    });
    // The next post starts public again.
    fireEvent.focus(inline);
    expect(screen.getByRole("button", { name: "选择受众" })).toHaveTextContent(
      "所有人",
    );
  });

  it("keeps an empty composer open through the reply settings portal and returns focus on selection", async () => {
    preauthenticate();
    const fetcher = installApi();
    render(<App />);
    const input = await screen.findByLabelText("此刻想到什么？");
    const composer = input.closest("section")!;
    expect(
      screen.queryByRole("button", { name: "所有人都能回复" }),
    ).not.toBeInTheDocument();
    fireEvent.focus(input);
    const trigger = screen.getByRole("button", { name: "所有人都能回复" });
    fireEvent.click(trigger);
    const menu = await screen.findByRole("menu", { name: "谁可以回复？" });
    const options = within(menu).getAllByRole("menuitemradio");
    expect(options).toHaveLength(1);
    expect(options[0]).toHaveTextContent("所有人");
    expect(options[0]).toHaveAttribute("aria-checked", "true");
    expect(composer).toHaveAttribute("data-expanded", "true");
    fireEvent.click(options[0]);
    await waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(composer).toHaveAttribute("data-expanded", "true");
    expect(input).toHaveValue("");
    expect(fetcher.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
      false,
    );

    fireEvent.click(screen.getByRole("button", { name: "选择受众" }));
    const audience = await screen.findByRole("menu", { name: "选择受众" });
    fireEvent.click(
      within(audience).getByRole("menuitemradio", { name: "仅自己" }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("button", { name: "所有人都能回复" }),
      ).not.toBeInTheDocument(),
    );
  });

  it("keeps the caret where it is for any press inside the composer that is not on a field", async () => {
    // Pressing the pill, a toolbar button or blank space must not move
    // focus: Safari would blur the textarea for a button with no
    // relatedTarget, every browser would for blank space, and an empty
    // composer would shut on that blur under the very click meant for it.
    // A press on the textarea itself, or on a notice worth selecting text
    // in, is left to the browser.
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    const composer = inline.closest("section")!;
    // Blank space in a closed composer is not an invitation: nothing opens.
    expect(fireEvent.mouseDown(composer.querySelector("form")!)).toBe(false);
    expect(composer).toHaveAttribute("data-expanded", "false");

    fireEvent.focus(inline);
    const pill = screen.getByRole("button", { name: "选择受众" });
    expect(fireEvent.mouseDown(pill)).toBe(false);
    expect(
      fireEvent.mouseDown(screen.getByRole("button", { name: "发布" })),
    ).toBe(false);
    expect(fireEvent.mouseDown(inline)).toBe(true);
    expect(composer).toHaveAttribute("data-expanded", "true");
    fireEvent.click(pill);
    const card = await screen.findByRole("menu", { name: "选择受众" });
    expect(composer).toHaveAttribute("data-expanded", "true");

    fireEvent.click(
      within(card).getByRole("menuitemradio", { name: "仅自己" }),
    );
    await waitFor(() =>
      expect(screen.queryByRole("menu")).not.toBeInTheDocument(),
    );
    await waitFor(() => expect(pill).toHaveFocus());
    await new Promise((settle) =>
      window.requestAnimationFrame(() => settle(undefined)),
    );
    expect(composer).toHaveAttribute("data-expanded", "true");
    expect(pill).toHaveTextContent("仅自己");

    // The collapsed row stays in the DOM, hidden, so it can animate shut.
    pill.blur();
    await waitFor(() =>
      expect(composer).toHaveAttribute("data-expanded", "false"),
    );
    expect(
      screen.queryByRole("button", { name: "选择受众" }),
    ).not.toBeInTheDocument();
    expect(pill).toBeInTheDocument();
    expect(pill.closest("[aria-hidden='true']")).not.toBeNull();
  });

  it("keeps an empty inline composer open while the window is in the background", async () => {
    // A window or tab going to the background blurs the field with no
    // destination and leaves it document.activeElement; coming back focuses
    // it again. Only focus actually moving elsewhere on the page — a press
    // outside, a Tab out — closes the composer.
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    const composer = inline.closest("section")!;
    act(() => inline.focus());
    expect(composer).toHaveAttribute("data-expanded", "true");

    fireEvent.blur(inline, { relatedTarget: null });
    await act(() => new Promise((settle) => window.setTimeout(settle, 5)));
    expect(composer).toHaveAttribute("data-expanded", "true");
    fireEvent.focus(inline);
    await act(() => new Promise((settle) => window.setTimeout(settle, 5)));
    expect(composer).toHaveAttribute("data-expanded", "true");

    act(() => screen.getByRole("link", { name: "主页" }).focus());
    await waitFor(() =>
      expect(composer).toHaveAttribute("data-expanded", "false"),
    );
    act(() => inline.focus());
    expect(composer).toHaveAttribute("data-expanded", "true");
    act(() => inline.blur());
    await waitFor(() =>
      expect(composer).toHaveAttribute("data-expanded", "false"),
    );
  });

  it("edits a hidden post's audience through the same pill", async () => {
    preauthenticate();
    const item = { ...post("藏起来的"), visibility: "only_me" };
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${item.id}`) {
        return json({ data: item, meta: {} });
      }
      if (url === `/api/v1/posts/${item.id}` && init?.method === "PATCH") {
        return json({ data: post("藏起来的"), meta: {} });
      }
    });
    render(<App />);
    await screen.findByLabelText("Moment 正文");
    const pill = screen.getByRole("button", { name: "选择受众" });
    expect(pill).toHaveTextContent("仅自己");
    fireEvent.click(pill);
    const card = await screen.findByRole("menu", { name: "选择受众" });
    fireEvent.click(
      within(card).getByRole("menuitemradio", { name: "所有人" }),
    );
    await waitFor(() => expect(pill).toHaveTextContent("所有人"));
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() => expect(window.location.pathname).toBe(`/m/${item.id}`));
    const patchCall = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url) === `/api/v1/posts/${item.id}` && init?.method === "PATCH",
    );
    expect(JSON.parse(String(patchCall?.[1]?.body))).toEqual({
      content_md: "藏起来的",
      media: [],
      visibility: "everyone",
    });
  });

  it("returns to the post after editing it from its page, leaving no editor in history", async () => {
    // Editing is a detour from the post: saving goes back to where the
    // reader was, so the next back leads out of the post rather than into
    // the editor again.
    preauthenticate();
    const item = post("改前", "01K2ABCDEF0123456789ABCDE9");
    let current = item;
    window.history.replaceState({}, "", `/m/${item.id}`);
    installApi((url, init) => {
      if (
        url === `/api/v1/posts/${item.id}` &&
        (!init?.method || init.method === "GET")
      )
        return json({ data: current, meta: {} });
      if (url.startsWith(`/api/v1/posts/${item.id}/comments`))
        return json({ data: { items: [] }, meta: { next_cursor: null } });
      if (url === `/api/v1/posts/${item.id}` && init?.method === "PATCH") {
        current = post("改后", item.id);
        return json({ data: current, meta: {} });
      }
    });
    render(<App />);

    await openMomentActions();
    fireEvent.click(await screen.findByRole("menuitem", { name: "编辑" }));
    const editor = await screen.findByLabelText("Moment 正文");
    expect(window.location.pathname).toBe(`/m/${item.id}/edit`);
    expect(window.history.state?.momentInternal).toBe(true);
    fireEvent.change(editor, { target: { value: "改后" } });
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() => expect(window.location.pathname).toBe(`/m/${item.id}`));
    // The entry is the one the reader started on, not a new page pushed on
    // top of the editor.
    expect(window.history.state?.momentInternal).toBeFalsy();
    expect(await screen.findByText("改后")).toBeInTheDocument();
  });

  it("keeps the inline composer geometry stable while writing", async () => {
    preauthenticate();
    installApi();
    render(<App />);

    const inline = await screen.findByLabelText("此刻想到什么？");
    expect(inline).toHaveAttribute("rows", "1");
    expect(
      screen.getByRole("button", { name: "选择媒体" }),
    ).toBeInTheDocument();

    fireEvent.focus(inline);
    expect(inline).toHaveAttribute("rows", "1");
    expect(
      screen.getByRole("button", { name: "选择媒体" }),
    ).toBeInTheDocument();

    fireEvent.change(inline, { target: { value: "正在记录" } });
    expect(inline).toHaveAttribute("rows", "1");

    fireEvent.blur(inline, { relatedTarget: null });
    await waitFor(() => expect(inline).toHaveAttribute("rows", "1"));
    expect(
      screen.getByRole("button", { name: "选择媒体" }),
    ).toBeInTheDocument();
  });

  it("keeps an unsaved page composer in place until leave is confirmed", async () => {
    preauthenticate();
    vi.spyOn(window, "scrollY", "get").mockReturnValue(510);
    window.history.replaceState({}, "", "/compose");
    installApi();
    render(<App />);
    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "还没有发布" },
    });
    fireEvent.click(
      screen.getAllByRole("link", { name: "乔乔的 Moment首页" })[0],
    );
    expect(window.location.pathname).toBe("/compose");
    const dialog = await screen.findByRole("dialog", { name: "退出发布？" });
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    expect(window.confirm).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "继续编辑" }));
    expect(window.location.pathname).toBe("/compose");
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");

    fireEvent.click(
      screen.getAllByRole("link", { name: "乔乔的 Moment首页" })[0],
    );
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    // Home's composer mounts on arrival and hands the stranded slot to 草稿.
    await waitFor(() =>
      expect(listKeptDrafts(instance).map((draft) => draft.contentMd)).toEqual([
        "还没有发布",
      ]),
    );
  });

  it("does not repeat the draft prompt after an approved inline navigation", async () => {
    preauthenticate();
    installApi();
    render(<App />);

    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "切页前保留这段草稿" },
    });
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/search"));

    fireEvent.click(screen.getAllByRole("link", { name: "归档" })[0]);
    await waitFor(() => expect(window.location.pathname).toBe("/archive"));
    expect(screen.queryByRole("dialog", { name: "退出发布？" })).toBeNull();
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("uses the in-app discard dialog when closing the desktop composer", async () => {
    preauthenticate();
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 720px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    installApi();
    render(<App />);

    fireEvent.click(await screen.findByRole("link", { name: "发帖" }));
    const composer = await screen.findByRole("dialog", {
      name: "发布 Moment",
    });
    fireEvent.change(within(composer).getByLabelText("媒体文件"), {
      target: {
        files: [new File(["image"], "draft.png", { type: "image/png" })],
      },
    });
    fireEvent.keyDown(composer, { key: "Escape" });

    expect(
      await screen.findByRole("dialog", { name: "退出发布？" }),
    ).toBeInTheDocument();
    expect(window.confirm).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "丢弃并退出" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
  });

  // The compose dialog carries the exit on one side of its header and
  // Drafts on the other; pressing Drafts turns the same dialog into the
  // list instead of stacking a second one on it.
  it("carries the exit and the drafts list in the compose dialog header", async () => {
    preauthenticate();
    // This file shares one history; without this the test inherits whatever
    // address the previous one left behind.
    window.history.replaceState({}, "", "/");
    keepDraft(
      instance,
      "上周没写完的一段",
      new Date(Date.now() - 7 * 24 * 60 * 60 * 1000),
    );
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 720px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    installApi();
    render(<App />);

    fireEvent.click(await screen.findByRole("link", { name: "发帖" }));
    const dialog = await screen.findByRole("dialog", { name: "发布 Moment" });
    const drafts = within(dialog).getByRole("button", { name: /草稿/ });
    expect(drafts).toHaveTextContent("1");
    expect(
      within(dialog).getByRole("button", { name: "关闭发布" }),
    ).toBeInTheDocument();

    fireEvent.click(drafts);
    const list = await screen.findByRole("dialog", { name: "草稿" });
    // The composer is still mounted behind the list, holding its text.
    expect(within(list).getByLabelText("此刻想到什么？")).toBeInTheDocument();

    fireEvent.click(within(list).getByText("上周没写完的一段"));
    // The dialog's composer, not home's behind it.
    await waitFor(() =>
      expect(within(dialog).getByLabelText("此刻想到什么？")).toHaveValue(
        "上周没写完的一段",
      ),
    );
    // The list has its own address, so leaving it is a navigation.
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "草稿" })).toBeNull(),
    );
    expect(window.location.pathname).toBe("/compose");
  });

  // The list is a face of the compose dialog, not a thing stacked on it:
  // both ways out of it go back to what was being written.
  it("returns the drafts list to the editor, by arrow or by pressing outside", async () => {
    preauthenticate();
    // This file shares one history; without this the test inherits whatever
    // address the previous one left behind.
    window.history.replaceState({}, "", "/");
    keepDraft(
      instance,
      "先放着的一段",
      new Date(Date.now() - 24 * 60 * 60 * 1000),
    );
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 720px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    installApi();
    render(<App />);

    fireEvent.click(await screen.findByRole("link", { name: "发帖" }));
    const dialog = await screen.findByRole("dialog", { name: "发布 Moment" });
    fireEvent.click(within(dialog).getByRole("button", { name: /草稿/ }));

    const list = await screen.findByRole("dialog", { name: "草稿" });
    // The reference's section row, with only the one kind that exists.
    expect(within(list).getByRole("tab", { name: "未发布" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // Turning the dialog over must replace these buttons, not reuse one node
    // and swap its class: a reused node runs `transition: background-color`
    // between the two pills, which painted 草稿's blue label on 编辑's black
    // ground for 200ms. Different nodes cannot bridge.
    const listBack = within(list).getByRole("button", { name: "返回发布" });
    const listPill = within(list).getByRole("button", { name: /编辑/ });
    fireEvent.click(listBack);
    expect(await screen.findByRole("dialog", { name: "发布 Moment" }));
    expect(window.location.pathname).toBe("/compose");
    expect(screen.getByRole("button", { name: "关闭发布" })).not.toBe(listBack);
    expect(screen.getByRole("button", { name: /草稿/ })).not.toBe(listPill);

    // Escape and a press on the scrim reach the dialog the same way.
    fireEvent.click(screen.getByRole("button", { name: /草稿/ }));
    await screen.findByRole("dialog", { name: "草稿" });
    fireEvent.keyDown(screen.getByRole("dialog", { name: "草稿" }), {
      key: "Escape",
    });
    expect(await screen.findByRole("dialog", { name: "发布 Moment" }));
    expect(window.location.pathname).toBe("/compose");
  });

  // Reloading the compose address used to drop to a bare compose screen with
  // nothing behind it. The reference keeps the dialog over the timeline, and
  // gives the list its own address so a reload comes back to the list.
  it("restores the compose dialog and its list from the address alone", async () => {
    preauthenticate();
    keepDraft(
      instance,
      "刷新之前存下的",
      new Date(Date.now() - 24 * 60 * 60 * 1000),
    );
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: query === "(min-width: 720px)",
      media: query,
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    installApi();

    window.history.replaceState({}, "", "/compose");
    const cold = render(<App />);
    expect(
      await screen.findByRole("dialog", { name: "发布 Moment" }),
    ).toBeInTheDocument();
    // The timeline is behind it, not a compose screen of its own.
    expect(document.querySelector("main h1")).toHaveTextContent("时间流");
    cold.unmount();

    window.history.replaceState({}, "", "/compose/drafts");
    render(<App />);
    const list = await screen.findByRole("dialog", { name: "草稿" });
    expect(within(list).getByText("刷新之前存下的")).toBeInTheDocument();
    expect(document.querySelector("main h1")).toHaveTextContent("时间流");
  });

  // The reference's edit mode: a checkbox per row, 全选, and a 删除 that is
  // out of reach until something is picked.
  it("picks and deletes drafts from the list's edit mode", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    keepDraft(
      instance,
      "第一段",
      new Date(Date.now() - 2 * 24 * 60 * 60 * 1000),
    );
    keepDraft(instance, "第二段", new Date(Date.now() - 24 * 60 * 60 * 1000));
    installApi();
    render(<App />);

    await screen.findByLabelText("此刻想到什么？");
    fireEvent.click(screen.getByRole("button", { name: /草稿/ }));
    fireEvent.click(await screen.findByRole("button", { name: "编辑" }));

    // Nothing picked: the delete is disabled, so it cannot even be pressed.
    const remove = screen.getByRole("button", { name: "删除" });
    expect(remove).toBeDisabled();
    fireEvent.click(remove);
    expect(
      screen.queryByRole("dialog", { name: "删除选中的草稿？" }),
    ).toBeNull();

    // The whole row picks it now, rather than only a 22px box inside it:
    // there is no row button left, and the row's own text names the box.
    expect(screen.queryByRole("button", { name: /^第一段/ })).toBeNull();
    const first = screen.getByRole("checkbox", { name: /第一段/ });
    fireEvent.click(first);
    expect(first).toBeChecked();
    expect(remove).toBeEnabled();

    fireEvent.click(screen.getByRole("button", { name: "全选" }));
    expect(
      screen
        .getAllByRole("checkbox")
        .every((box) => box.getAttribute("aria-checked") === "true"),
    ).toBe(true);
    expect(
      screen.getByRole("button", { name: "取消全选" }),
    ).toBeInTheDocument();

    fireEvent.click(first);
    expect(first).not.toBeChecked();
    expect(screen.getByRole("button", { name: "全选" })).toBeInTheDocument();

    fireEvent.click(remove);
    expect(
      await screen.findByRole("dialog", { name: "删除选中的草稿？" }),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByRole("dialog", { name: "删除选中的草稿？" }),
      ).toHaveFocus(),
    );
    fireEvent.click(
      within(
        screen.getByRole("dialog", { name: "删除选中的草稿？" }),
      ).getByRole("button", { name: "删除" }),
    );

    await waitFor(() =>
      expect(listKeptDrafts(instance).map((draft) => draft.contentMd)).toEqual([
        "第一段",
      ]),
    );
    // One left, and it is openable again once editing is done.
    fireEvent.click(screen.getByRole("button", { name: "完成" }));
    expect(screen.getByRole("button", { name: /^第一段/ })).toBeEnabled();
  });

  // Editing a draft used to leave the original behind: 草稿 matches on
  // content, so "aaa" edited to "aaa1" filed a second entry beside it.
  it("replaces the draft it opened rather than filing a second one", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    keepDraft(instance, "aaa", new Date(Date.now() - 24 * 60 * 60 * 1000));
    installApi();
    render(<App />);

    await screen.findByLabelText("此刻想到什么？");
    fireEvent.click(screen.getByRole("button", { name: /草稿/ }));
    fireEvent.click(await screen.findByText("aaa"));
    await waitFor(() =>
      expect(screen.getByLabelText("此刻想到什么？")).toHaveValue("aaa"),
    );
    // It is the live text now, so it is no longer waiting in the list.
    expect(listKeptDrafts(instance)).toEqual([]);

    fireEvent.change(screen.getByLabelText("此刻想到什么？"), {
      target: { value: "aaa1" },
    });
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/search"));

    expect(listKeptDrafts(instance).map((draft) => draft.contentMd)).toEqual([
      "aaa1",
    ]);
  });

  // The only copy of that text, one press away from gone.
  it("asks before deleting a draft", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    keepDraft(
      instance,
      "别手滑删了我",
      new Date(Date.now() - 24 * 60 * 60 * 1000),
    );
    installApi();
    render(<App />);

    await screen.findByLabelText("此刻想到什么？");
    fireEvent.click(screen.getByRole("button", { name: /草稿/ }));
    fireEvent.click(
      await screen.findByRole("button", { name: /删除草稿：别手滑删了我/ }),
    );
    expect(
      await screen.findByRole("dialog", { name: "删除这份草稿？" }),
    ).toBeInTheDocument();
    expect(listKeptDrafts(instance)).toHaveLength(1);

    fireEvent.click(screen.getByRole("button", { name: "删除草稿" }));
    await waitFor(() => expect(listKeptDrafts(instance)).toEqual([]));
  });

  it("files a kept draft under 草稿 and stops re-offering it as recovery", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    installApi();
    const { unmount } = render(<App />);

    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "留到以后再发" },
    });
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/search"));

    expect(listKeptDrafts(instance).map((draft) => draft.contentMd)).toEqual([
      "留到以后再发",
    ]);

    // Filed text is not lost text: the recovery notice would otherwise
    // offer the same paragraph back above a list that already holds it.
    unmount();
    window.history.replaceState({}, "", "/compose");
    render(<App />);
    expect(await screen.findByLabelText("此刻想到什么？")).toHaveValue("");
    expect(screen.queryByText("发现一段未发布草稿")).toBeNull();

    // A phone never gets the compose dialog, so the screen carries its own
    // way into 草稿; filing text that cannot be read back is a trap.
    const entry = screen.getByRole("button", { name: /草稿/ });
    expect(entry).toHaveTextContent("1");
    fireEvent.click(entry);
    fireEvent.click(await screen.findByText("留到以后再发"));
    await waitFor(() =>
      expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
        "留到以后再发",
      ),
    );
  });

  it("clears the hidden inline surface after a recovered page draft publishes", async () => {
    preauthenticate();
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "跨 surface 的恢复草稿",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return json({
          data: post("跨 surface 的恢复草稿", "01K2ABCDEF0123456789ABCDE9"),
          meta: {},
        });
      }
    });
    render(<App />);
    await screen.findByLabelText("此刻想到什么？");
    await waitFor(() => expect(listKeptDrafts(instance)).toHaveLength(1));

    fireEvent.keyDown(document.body, { key: "n" });
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    fireEvent.click(screen.getByRole("button", { name: /草稿/ }));
    fireEvent.click(await screen.findByText("跨 surface 的恢复草稿"));
    await waitFor(() =>
      expect(screen.getAllByLabelText("此刻想到什么？")[1]).toHaveValue(
        "跨 surface 的恢复草稿",
      ),
    );
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue("");
    expect(loadComposeDraft(instance)).toBeNull();
    // Publishing takes it out of 草稿 as well.
    expect(listKeptDrafts(instance)).toEqual([]);
    expect(screen.getAllByText("跨 surface 的恢复草稿")).toHaveLength(1);

    const inline = screen.getByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "发布后继续写的新草稿" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "发布后继续写的新草稿",
    );

    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(
      await screen.findByRole("dialog", { name: "退出发布？" }),
    ).toBeInTheDocument();
    expect(window.confirm).not.toHaveBeenCalled();
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("clears equal text across surfaces when storage is denied", async () => {
    preauthenticate();
    const denied = () => {
      throw new DOMException("denied");
    };
    vi.spyOn(window.localStorage, "getItem").mockImplementation(denied);
    vi.spyOn(window.localStorage, "setItem").mockImplementation(denied);
    vi.spyOn(window.localStorage, "removeItem").mockImplementation(denied);
    let creates = 0;
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        creates += 1;
        return json({
          data: post("无法落盘但已经发布", "01K2ABCDEF0123456789ABCDF1"),
          meta: {},
        });
      }
    });
    render(<App />);

    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "无法落盘但已经发布" } });
    fireEvent.keyDown(document.body, { key: "n" });
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    fireEvent.change(screen.getAllByLabelText("此刻想到什么？")[1], {
      target: { value: "无法落盘但已经发布" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue("");
    expect(screen.getByRole("button", { name: "发布" })).toBeDisabled();
    expect(screen.getAllByText("无法落盘但已经发布")).toHaveLength(1);
    expect(creates).toBe(1);
  });

  it("keeps a divergent hidden inline surface after publishing when storage is denied", async () => {
    preauthenticate();
    const denied = () => {
      throw new DOMException("denied");
    };
    vi.spyOn(window.localStorage, "getItem").mockImplementation(denied);
    vi.spyOn(window.localStorage, "setItem").mockImplementation(denied);
    vi.spyOn(window.localStorage, "removeItem").mockImplementation(denied);
    let creates = 0;
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        creates += 1;
        return json({
          data: post("B 标签页发布成功", "01K2ABCDEF0123456789ABCDF2"),
          meta: {},
        });
      }
    });
    render(<App />);

    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "A 标签页仍未发布" } });
    fireEvent.keyDown(document.body, { key: "n" });
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    fireEvent.change(screen.getAllByLabelText("此刻想到什么？")[1], {
      target: { value: "B 标签页发布成功" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "A 标签页仍未发布",
    );
    expect(screen.getAllByText("B 标签页发布成功")).toHaveLength(1);
    expect(creates).toBe(1);
  });

  it("repairs a newer inline draft after an older publish cleanup interleaves", async () => {
    preauthenticate();
    let resolveCreate!: (response: Response) => void;
    const createResponse = new Promise<Response>((resolve) => {
      resolveCreate = resolve;
    });
    installApi((url, init) => {
      if (url === "/api/v1/posts" && init?.method === "POST") {
        return createResponse;
      }
    });
    render(<App />);

    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "共同起点" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    fireEvent.keyDown(document.body, { key: "n" });
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/compose"));
    // The page composer opens empty -- what the inline box left in the
    // recovery slot went to 草稿 on its way here -- so B writes its own.
    const page = screen.getAllByLabelText("此刻想到什么？")[1];
    fireEvent.change(page, { target: { value: "B 正在发布的版本" } });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));
    const submittedDraft = loadComposeDraft(instance);
    expect(submittedDraft).toMatchObject({ contentMd: "B 正在发布的版本" });

    fireEvent.change(inline, { target: { value: "A 在等待期间写的新版本" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const activeDraft = loadComposeDraft(instance);
    expect(activeDraft).toMatchObject({
      contentMd: "A 在等待期间写的新版本",
    });
    expect(activeDraft?.savedAt).not.toBe(submittedDraft?.savedAt);

    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: submittedDraft?.contentMd,
        saved_at: submittedDraft?.savedAt,
      }),
    );
    const originalGetItem = window.localStorage.getItem.bind(
      window.localStorage,
    );
    const originalSetItem = window.localStorage.setItem.bind(
      window.localStorage,
    );
    let injectedNewerProjection = false;
    const getItem = vi
      .spyOn(window.localStorage, "getItem")
      .mockImplementation((key) => {
        const value = originalGetItem(key);
        if (
          !injectedNewerProjection &&
          key === composeDraftKey(instance) &&
          activeDraft
        ) {
          injectedNewerProjection = true;
          originalSetItem(
            key,
            JSON.stringify({
              content_md: activeDraft.contentMd,
              saved_at: activeDraft.savedAt,
            }),
          );
        }
        return value;
      });

    resolveCreate(
      json({
        data: post("B 正在发布的版本", "01K2ABCDEF0123456789ABCDF3"),
        meta: {},
      }),
    );

    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    getItem.mockRestore();
    expect(injectedNewerProjection).toBe(true);
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "A 在等待期间写的新版本",
    );
    expect(loadComposeDraft(instance)).toMatchObject({
      contentMd: "A 在等待期间写的新版本",
      fingerprint: activeDraft?.fingerprint,
      savedAt: activeDraft?.savedAt,
    });
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    await waitFor(() => expect(window.location.pathname).toBe("/search"));
    expect(screen.queryByRole("dialog", { name: "退出发布？" })).toBeNull();
  });

  it("re-saves a dirty draft when another document removes its projection", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "清理竞态里的新正文" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const removed = window.localStorage.getItem(composeDraftKey(instance));
    expect(removed).toContain("清理竞态里的新正文");

    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: removed,
        newValue: null,
      }),
    );

    expect(inline).toHaveValue("清理竞态里的新正文");
    await waitFor(
      () =>
        expect(
          window.localStorage.getItem(composeDraftKey(instance)),
        ).toContain("清理竞态里的新正文"),
      { timeout: 500 },
    );
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(
      await screen.findByRole("dialog", { name: "退出发布？" }),
    ).toBeInTheDocument();
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("does not resurrect an equal draft when marker writes fail but clearance is broadcast", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "另一标签页已经发布" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const stored = loadComposeDraft(instance);
    expect(stored).not.toBeNull();
    const originalSetItem = window.localStorage.setItem.bind(
      window.localStorage,
    );
    vi.spyOn(window.localStorage, "setItem").mockImplementation(
      (key, value) => {
        if (key === composeDraftClearanceKey(instance)) {
          throw new DOMException("quota exceeded");
        }
        originalSetItem(key, value);
      },
    );
    const clearance = {
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instance,
      draft_fingerprint: stored?.fingerprint,
      draft_saved_at: stored?.savedAt,
      reason: "published",
      cleared_at: new Date(
        Math.max(Date.now(), Date.parse(stored?.savedAt ?? "")) + 1,
      ).toISOString(),
    };
    expect(() =>
      window.localStorage.setItem(
        composeDraftClearanceKey(instance),
        JSON.stringify(clearance),
      ),
    ).toThrow();
    const peer = new FakeBroadcastChannel(COMPOSE_DRAFT_CLEARANCE_CHANNEL);
    const removed = window.localStorage.getItem(composeDraftKey(instance));

    peer.postMessage(clearanceMessage(clearance));
    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: removed,
        newValue: null,
      }),
    );

    await waitFor(() => expect(inline).toHaveValue(""));
    await new Promise((resolve) => window.setTimeout(resolve, 75));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    peer.close();
  });

  it("preserves a divergent draft when marker writes fail but clearance is broadcast", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "本标签页分叉的正文" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const stored = loadComposeDraft(instance);
    expect(stored).not.toBeNull();
    const originalSetItem = window.localStorage.setItem.bind(
      window.localStorage,
    );
    vi.spyOn(window.localStorage, "setItem").mockImplementation(
      (key, value) => {
        if (key === composeDraftClearanceKey(instance)) {
          throw new DOMException("quota exceeded");
        }
        originalSetItem(key, value);
      },
    );
    const clearance = {
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instance,
      draft_fingerprint: "2".repeat(64),
      draft_saved_at: stored?.savedAt,
      reason: "published",
      cleared_at: new Date(
        Math.max(Date.now(), Date.parse(stored?.savedAt ?? "")) + 1,
      ).toISOString(),
    };
    expect(() =>
      window.localStorage.setItem(
        composeDraftClearanceKey(instance),
        JSON.stringify(clearance),
      ),
    ).toThrow();
    const peer = new FakeBroadcastChannel(COMPOSE_DRAFT_CLEARANCE_CHANNEL);
    peer.postMessage(clearanceMessage(clearance));
    await new Promise<void>((resolve) => queueMicrotask(resolve));
    const removed = window.localStorage.getItem(composeDraftKey(instance));
    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: removed,
        newValue: null,
      }),
    );

    expect(inline).toHaveValue("本标签页分叉的正文");
    await waitFor(
      () =>
        expect(
          window.localStorage.getItem(composeDraftKey(instance)),
        ).toContain("本标签页分叉的正文"),
      { timeout: 500 },
    );
    peer.close();
  });

  it("honors a broadcast logout when its persistent marker is denied", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "退出必须清理" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const stored = loadComposeDraft(instance);
    const originalSetItem = window.localStorage.setItem.bind(
      window.localStorage,
    );
    vi.spyOn(window.localStorage, "setItem").mockImplementation(
      (key, value) => {
        if (key === composeDraftClearAllKey(instance)) {
          throw new DOMException("quota exceeded");
        }
        originalSetItem(key, value);
      },
    );
    const peer = new FakeBroadcastChannel(COMPOSE_DRAFT_CLEARANCE_CHANNEL);
    const removed = window.localStorage.getItem(composeDraftKey(instance));
    const clearance = {
      schema: COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
      instance_id: instance,
      reason: "logout",
      cleared_at: new Date(
        Math.max(Date.now(), Date.parse(stored?.savedAt ?? "")) + 1,
      ).toISOString(),
    };
    expect(() =>
      window.localStorage.setItem(
        composeDraftClearAllKey(instance),
        JSON.stringify(clearance),
      ),
    ).toThrow();
    peer.postMessage(clearanceMessage(clearance));
    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: removed,
        newValue: null,
      }),
    );

    await waitFor(() => expect(inline).toHaveValue(""));
    await new Promise((resolve) => window.setTimeout(resolve, 75));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    peer.close();
  });

  it("strictly validates and closes the dedicated clearance channel", async () => {
    preauthenticate();
    installApi();
    const view = render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "不能被伪造消息清理" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const stored = loadComposeDraft(instance);
    const peer = new FakeBroadcastChannel(COMPOSE_DRAFT_CLEARANCE_CHANNEL);
    expect(
      FakeBroadcastChannel.channels.get(COMPOSE_DRAFT_CLEARANCE_CHANNEL)?.size,
    ).toBe(2);

    peer.postMessage({
      ...clearanceMessage({
        schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
        instance_id: instance,
        draft_fingerprint: stored?.fingerprint,
        draft_saved_at: stored?.savedAt,
        reason: "published",
        cleared_at: new Date().toISOString(),
      }),
      extra: true,
    });
    peer.postMessage(
      clearanceMessage({
        schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
        instance_id: "fedcba9876543210fedcba9876543210",
        draft_fingerprint: stored?.fingerprint,
        draft_saved_at: stored?.savedAt,
        reason: "published",
        cleared_at: new Date().toISOString(),
      }),
    );
    peer.postMessage(
      clearanceMessage({
        schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
        instance_id: instance,
        draft_fingerprint: stored?.fingerprint,
        draft_saved_at: stored?.savedAt,
        reason: "published",
        cleared_at: new Date(Date.now() + 120_000).toISOString(),
      }),
    );
    await new Promise<void>((resolve) => queueMicrotask(resolve));
    expect(inline).toHaveValue("不能被伪造消息清理");

    peer.postMessage(
      clearanceMessage({
        schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
        instance_id: instance,
        draft_fingerprint: stored?.fingerprint,
        draft_saved_at: stored?.savedAt,
        reason: "published",
        cleared_at: new Date(
          Math.max(Date.now(), Date.parse(stored?.savedAt ?? "")) + 1,
        ).toISOString(),
      }),
    );
    await waitFor(() => expect(inline).toHaveValue(""));

    view.unmount();
    expect(
      FakeBroadcastChannel.channels.get(COMPOSE_DRAFT_CLEARANCE_CHANNEL)?.size,
    ).toBe(1);
    peer.close();
    expect(
      FakeBroadcastChannel.channels.get(COMPOSE_DRAFT_CLEARANCE_CHANNEL)
        ?.size ?? 0,
    ).toBe(0);
  });

  it("safely re-saves a divergent draft when BroadcastChannel is unavailable", async () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "无频道也不能丢" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const removed = window.localStorage.getItem(composeDraftKey(instance));
    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: removed,
        newValue: null,
      }),
    );

    await waitFor(
      () =>
        expect(
          window.localStorage.getItem(composeDraftKey(instance)),
        ).toContain("无频道也不能丢"),
      { timeout: 500 },
    );
    expect(inline).toHaveValue("无频道也不能丢");
  });

  it("clears inline only when another tab marks the same draft identity", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "会被另一标签页清理" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const storedDraft = loadComposeDraft(instance);
    expect(storedDraft?.fingerprint).toMatch(/^[0-9a-f]{64}$/);
    const clearedAt = new Date(
      Math.max(Date.now(), Date.parse(storedDraft?.savedAt ?? "")) + 1,
    ).toISOString();
    const clearance = JSON.stringify({
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instance,
      draft_fingerprint: storedDraft?.fingerprint,
      draft_saved_at: storedDraft?.savedAt,
      reason: "published",
      cleared_at: clearedAt,
    });
    const clearanceKey = composeDraftClearanceKey(instance);
    window.localStorage.setItem(clearanceKey, clearance);
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: clearanceKey,
        oldValue: null,
        newValue: clearance,
      }),
    );
    await waitFor(() => expect(inline).toHaveValue(""));

    fireEvent.change(inline, { target: { value: "跨标签页清理后重新写" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "跨标签页清理后重新写",
    );

    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(
      await screen.findByRole("dialog", { name: "退出发布？" }),
    ).toBeInTheDocument();
    expect(window.confirm).not.toHaveBeenCalled();
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("preserves a divergent draft identity when another tab publishes", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "A 标签页已经分叉的正文" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));

    const storedDraft = loadComposeDraft(instance);
    expect(storedDraft?.fingerprint).toMatch(/^[0-9a-f]{64}$/);
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftClearanceKey(instance),
        oldValue: null,
        newValue: '{"schema":"invalid"}',
      }),
    );
    expect(inline).toHaveValue("A 标签页已经分叉的正文");

    fireEvent(
      window,
      new CustomEvent(COMPOSE_DRAFT_CLEARED_EVENT, {
        detail: { instanceId: instance },
      }),
    );
    expect(inline).toHaveValue("A 标签页已经分叉的正文");

    fireEvent(
      window,
      new CustomEvent(COMPOSE_DRAFT_CLEARED_EVENT, {
        detail: {
          schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
          instance_id: "fedcba9876543210fedcba9876543210",
          draft_fingerprint: storedDraft?.fingerprint,
          draft_saved_at: storedDraft?.savedAt,
          reason: "published",
          cleared_at: new Date(
            Math.max(Date.now(), Date.parse(storedDraft?.savedAt ?? "")) + 1,
          ).toISOString(),
        },
      }),
    );
    expect(inline).toHaveValue("A 标签页已经分叉的正文");

    const otherFingerprint = "2".repeat(64);
    const otherClearance = JSON.stringify({
      schema: COMPOSE_DRAFT_CLEARANCE_SCHEMA,
      instance_id: instance,
      draft_fingerprint: otherFingerprint,
      draft_saved_at: storedDraft?.savedAt,
      reason: "published",
      cleared_at: new Date(
        Math.max(Date.now(), Date.parse(storedDraft?.savedAt ?? "")) + 1,
      ).toISOString(),
    });
    const otherClearanceKey = composeDraftClearanceKey(instance);
    window.localStorage.setItem(otherClearanceKey, otherClearance);
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: otherClearanceKey,
        oldValue: null,
        newValue: otherClearance,
      }),
    );

    expect(inline).toHaveValue("A 标签页已经分叉的正文");
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "A 标签页已经分叉的正文",
    );

    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(
      await screen.findByRole("dialog", { name: "退出发布？" }),
    ).toBeInTheDocument();
    expect(window.confirm).not.toHaveBeenCalled();
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("loads and saves an existing post without putting edits into the create draft", async () => {
    preauthenticate();
    const item = post("编辑前");
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${item.id}`) {
        return json({ data: item, meta: {} });
      }
      if (url === `/api/v1/posts/${item.id}` && init?.method === "PATCH") {
        return json({ data: post("编辑后"), meta: {} });
      }
    });
    render(<App />);
    const editor = await screen.findByLabelText("Moment 正文");
    expect(editor).toHaveValue("编辑前");
    fireEvent.change(editor, { target: { value: "编辑后" } });
    fireEvent.keyDown(editor, { key: "Enter", metaKey: true });

    await waitFor(() => expect(window.location.pathname).toBe(`/m/${item.id}`));
    const patchCall = fetcher.mock.calls.find(
      ([url, init]) =>
        String(url) === `/api/v1/posts/${item.id}` && init?.method === "PATCH",
    );
    expect(patchCall?.[1]?.body).toBe(
      JSON.stringify({ content_md: "编辑后", media: [] }),
    );
    expect(new Headers(patchCall?.[1]?.headers).get("X-CSRF-Token")).toBe(
      "csrf-from-cookie",
    );
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    const saved = screen.getByRole("dialog", { name: "Moment 已保存" });
    expect(
      within(saved).getByRole("button", { name: "查看 Moment" }),
    ).toBeInTheDocument();
  });

  it("keeps leave guards active after removing the last existing media usage", async () => {
    preauthenticate();
    const item = post("只编辑图片", "01K2ABCDEF0123456789ABCDF3", [
      {
        media_id: "01K2ABCDEF0123456789ABCDE7",
        position: 0,
        alt_text: "雨落下来",
        width: 1_200,
        height: 800,
      },
    ]);
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    installApi((url) => {
      if (url === `/api/v1/posts/${item.id}`) {
        return json({ data: item, meta: {} });
      }
    });
    render(<App />);

    await screen.findByLabelText("Moment 正文");
    fireEvent.click(screen.getByRole("button", { name: /移除/ }));
    expect(screen.queryByText("第 1 张")).toBeNull();

    fireEvent.click(screen.getByRole("link", { name: "返回时间流" }));
    expect(window.location.pathname).toBe(`/m/${item.id}/edit`);
    expect(
      await screen.findByRole("dialog", {
        name: "放弃未保存的更改？",
      }),
    ).toBeInTheDocument();
    expect(window.confirm).not.toHaveBeenCalled();
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("confirms only once when a direct edit falls back to the timeline", async () => {
    preauthenticate();
    const item = post("直接进入的编辑", "01K2ABCDEF0123456789ABCDF4");
    window.history.replaceState({}, "", `/m/${item.id}/edit`);
    installApi((url) => {
      if (url === `/api/v1/posts/${item.id}`) {
        return json({ data: item, meta: {} });
      }
    });
    render(<App />);
    const editor = await screen.findByLabelText("Moment 正文");
    fireEvent.change(editor, { target: { value: "尚未保存的编辑" } });

    fireEvent.click(screen.getByRole("link", { name: "返回时间流" }));

    const dialog = await screen.findByRole("dialog", {
      name: "放弃未保存的更改？",
    });
    fireEvent.click(
      within(dialog).getByRole("button", { name: "放弃更改并离开" }),
    );

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(window.confirm).not.toHaveBeenCalled();
  });

  it("uses a focus-restoring confirmation before owner deletion", async () => {
    preauthenticate();
    vi.spyOn(window, "scrollY", "get").mockReturnValue(900);
    const deletable = post("准备删除", "01K2ABCDEF0123456789ABCDE1");
    installApi((url, init) => {
      if (url.startsWith("/api/v1/posts?")) {
        return json({
          data: { items: [deletable] },
          meta: { next_cursor: null },
        });
      }
      if (url.includes("/api/v1/posts/") && init?.method === "DELETE") {
        return json({ data: deletable, meta: { deleted: true } });
      }
    });
    render(<App />);
    await screen.findByText("准备删除");
    const more = screen.getByRole("button", { name: "更多操作" });
    await openMomentActions(more);
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    expect(
      await screen.findByRole("heading", { name: "删除这段 Moment？" }),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    await waitFor(() =>
      expect(
        screen.getByRole("dialog", { name: "删除这段 Moment？" }),
      ).toHaveFocus(),
    );
    fireEvent.keyDown(document.body, { key: "n" });
    expect(window.location.pathname).toBe("/");
    fireEvent.click(screen.getByRole("button", { name: "保留" }));
    await waitFor(() => expect(more).toHaveFocus());
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");

    await openMomentActions(more);
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    fireEvent.click(await screen.findByRole("button", { name: "删除 Moment" }));
    await waitFor(() => expect(screen.queryByText("准备删除")).toBeNull());
    expect(screen.getByRole("dialog", { name: "Moment 已删除" })).toBeVisible();
  });

  it("keeps a failed delete open and retries without removing the Moment", async () => {
    preauthenticate();
    const deletable = post("删除失败后保留", "01K2ABCDEF0123456789ABCDE2");
    let deletes = 0;
    installApi((url, init) => {
      if (url.startsWith("/api/v1/posts?")) {
        return json({
          data: { items: [deletable] },
          meta: { next_cursor: null },
        });
      }
      if (url.includes("/api/v1/posts/") && init?.method === "DELETE") {
        deletes += 1;
        if (deletes === 1) {
          return json(
            {
              error: {
                code: "INTERNAL",
                message: "temporary",
                details: {},
                request_id: "req_delete_failed",
              },
            },
            500,
          );
        }
        return json({ data: deletable, meta: { deleted: true } });
      }
    });
    render(<App />);
    await screen.findByText("删除失败后保留");

    await openMomentActions();
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    const dialog = await screen.findByRole("dialog", {
      name: "删除这段 Moment？",
    });
    await waitFor(() => expect(dialog).toHaveFocus());
    fireEvent.click(
      within(dialog).getByRole("button", { name: "删除 Moment" }),
    );

    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "没有删除这个 Moment，可以安全重试。",
    );
    expect(screen.getByText("删除失败后保留")).toBeInTheDocument();
    expect(dialog).toBeVisible();

    fireEvent.click(within(dialog).getByRole("button", { name: "重试删除" }));
    await waitFor(() =>
      expect(screen.queryByText("删除失败后保留")).toBeNull(),
    );
    expect(deletes).toBe(2);
    expect(screen.getByRole("dialog", { name: "Moment 已删除" })).toBeVisible();
  });

  it("clears the instance draft and CSRF cache after confirmed logout", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/login");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "退出时清理",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi((url, init) => {
      if (url === "/api/v1/login/session" && init?.method === "DELETE") {
        return json({ data: { logged_out: true }, meta: {} });
      }
    });
    render(<App />);
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    fireEvent.click(
      await screen.findByRole("button", { name: "乔乔 的账户菜单" }),
    );
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "退出 @owner" }),
    );
    await waitFor(() =>
      expect(window.sessionStorage.getItem(sessionKey)).toBeNull(),
    );
    expect(window.location.pathname).toBe("/");
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
  });

  it("turns a provider callback into a session this browser can see", async () => {
    window.history.replaceState({}, "", "/compose?auth=ok");
    const fetcher = installApi((url) => {
      // The callback left a cookie behind and nothing else: no exchange ever
      // happened in this tab, so session storage is empty.
      if (url === "/api/v1/account/session") return accountSession();
    });
    render(<App />);

    expect(await screen.findByLabelText("此刻想到什么？")).toBeInTheDocument();
    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-from-cookie",
    );
    expect(window.location.search).toBe("");
    expect(
      FakeBroadcastChannel.posted.some(
        (message) => (message as { type?: string }).type === "offer-session",
      ),
    ).toBe(true);
    expect(
      fetcher.mock.calls.filter(
        ([url]) => String(url) === "/api/v1/account/session",
      ).length,
    ).toBeGreaterThan(0);
  });

  it("names the provider a completed binding just attached", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/?auth=linked&provider=github");
    installApi();
    render(<App />);

    // The callback says what it bound, so nothing has to be remembered across
    // the round trip for the confirmation to name it.
    expect(await screen.findByText("已绑定 GitHub")).toBeInTheDocument();
    expect(window.location.search).toBe("");
  });

  it("keeps a member signed in when the owner probe refuses them", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/compose");
    installApi((url) => {
      if (url === "/api/v1/status") return unauthorized();
      if (url === "/api/v1/account/session") {
        return accountSession({
          user_id: "1".repeat(32),
          username: "linlin",
          display_name: "林林",
          role: "member",
        });
      }
    });
    render(<App />);

    expect(await screen.findByLabelText("此刻想到什么？")).toBeInTheDocument();
    expect(screen.queryByText("此操作需要管理员权限")).toBeNull();
    expect(screen.queryByLabelText("密码")).toBeNull();
    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-from-cookie",
    );
  });

  it("asks a first provider sign-in for a handle and writes down a skip", async () => {
    preauthenticate();
    const patches: unknown[] = [];
    installApi((url, init) => {
      if (url === "/api/v1/account/session") {
        return accountSession({
          username: "cheems_9f2",
          username_chosen: false,
        });
      }
      if (url === "/api/v1/account/profile" && init?.method === "PATCH") {
        patches.push(JSON.parse(String(init.body)));
        return json({
          data: account({ username: "cheems_9f2" }),
          meta: { request_id: "req_profile" },
        });
      }
    });
    render(<App />);

    const dialog = await screen.findByRole("dialog", {
      name: "给自己起个用户名",
    });
    expect(within(dialog).getByLabelText("用户名")).toHaveValue("cheems_9f2");
    fireEvent.click(within(dialog).getByRole("button", { name: "跳过" }));

    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "给自己起个用户名" }),
      ).toBeNull(),
    );
    // Skipping is an answer, not a dismissal: the generated handle is saved so
    // the server stops treating it as unchosen and no other device asks again.
    expect(patches).toEqual([{ username: "cheems_9f2" }]);
  });
});
