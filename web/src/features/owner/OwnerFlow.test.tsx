import "@testing-library/jest-dom/vitest";
import {
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
import { makeAccountLimits } from "../../test/accountFixtures";

const instance = "0123456789abcdef0123456789abcdef";
const sessionKey = "moment.owner.session.v1";

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
    owner_id: "00000000000000000000000000000000",
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
  meta: { instance_id: instance, request_id: "req_site" },
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
    id,
    content_md: content,
    content_html: `<p>${content}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
    reposted_at: null,
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
    role: "owner",
    status: "active",
    username_chosen: true,
    ...overrides,
  };
  return { ...merged, profile_url: `/${String(merged.username)}` };
}

function accountSession(overrides: Record<string, unknown> = {}) {
  return json({
    data: {
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
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const response = await override?.(url, init);
      if (response) return response;
      if (url === "/api/v1/site") return json(siteEnvelope);
      if (url === "/api/v1/owner/status") return ownerStatus();
      if (url === "/api/v1/account/session") {
        // The server answers this from the session cookie; a stored session is
        // this suite's stand-in for a browser that holds one.
        return window.sessionStorage.getItem(sessionKey)
          ? accountSession()
          : unauthorized();
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
    window.history.replaceState({}, "", "/owner/compose");
    const bearer = "owner-bearer-never-store";
    const fetcher = installApi((url) => {
      if (url === "/api/v1/owner/status") {
        return window.sessionStorage.getItem(sessionKey)
          ? ownerStatus()
          : unauthorized();
      }
      if (url === "/api/v1/owner/session") {
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

    const token = await screen.findByLabelText("所有者令牌");
    fireEvent.change(token, { target: { value: bearer } });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    expect(await screen.findByLabelText("此刻想到什么？")).toBeInTheDocument();

    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-exchanged",
    );
    expect(window.sessionStorage.getItem(sessionKey)).not.toContain(bearer);
    expect(JSON.stringify(window.localStorage)).not.toContain(bearer);
    const exchange = fetcher.mock.calls.find(
      ([url]) => String(url) === "/api/v1/owner/session",
    );
    expect(exchange?.[1]?.body).toBe(JSON.stringify({ token: bearer }));
  });

  it("searches and inserts the owner identity from an active @mention", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    const fetcher = installApi((url) => {
      if (url.startsWith("/api/v1/users/mention-suggestions?")) {
        return json({
          data: [
            {
              owner_id: "00000000000000000000000000000000",
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

  it("offers explicit recovery and discarding for the real instance draft", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "刷新前留下的草稿",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi();
    const { unmount } = render(<App />);

    expect(await screen.findByText("发现一段未发布草稿")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "刷新前留下的草稿",
    );

    unmount();
    window.history.replaceState({}, "", "/owner/compose");
    render(<App />);
    expect(await screen.findByText("发现一段未发布草稿")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "丢弃" }));
    fireEvent.click(await screen.findByRole("button", { name: "丢弃草稿" }));
    await waitFor(() => expect(loadComposeDraft(instance)).toBeNull());
  });

  it("protects new text when typing directly over a recovery choice", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "较早的草稿",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi();
    render(<App />);

    expect(await screen.findByText("发现一段未发布草稿")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("此刻想到什么？"), {
      target: { value: "直接输入的新文字" },
    });
    expect(screen.queryByText("发现一段未发布草稿")).toBeNull();
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "直接输入的新文字",
    );
  });

  it("debounces the local draft, guards duplicate submit, and clears only after success", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    let resolveCreate!: (response: Response) => void;
    const createResponse = new Promise<Response>((resolve) => {
      resolveCreate = resolve;
    });
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
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
          String(url) === "/api/v1/owner/posts" && init?.method === "POST",
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
    window.history.replaceState({}, "", `/owner/compose?quote=${quotedId}`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${quotedId}` && !init?.method) {
        return json({
          data: post("原始 Moment", quotedId),
          meta: { request_id: "req_quote" },
        });
      }
      if (
        url === `/api/v1/owner/posts/${quotedId}/repost` &&
        init?.method === "PUT"
      ) {
        return json({
          data: {
            ...post("原始 Moment", quotedId),
            reposted_at: "2026-08-10T02:00:00.000Z",
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
          String(url) === `/api/v1/owner/posts/${quotedId}/repost` &&
          init?.method === "PUT",
      ),
    ).toHaveLength(1);
    expect(
      fetcher.mock.calls.some(
        ([url, init]) =>
          String(url) === "/api/v1/owner/posts" && init?.method === "POST",
      ),
    ).toBe(false);
    expect(screen.getByText("已转发到时间流顶部")).toBeInTheDocument();
  });

  it("keeps a quote with commentary as a new nested Moment", async () => {
    preauthenticate();
    const quotedId = "01K2ABCDEF0123456789ABCDE9";
    window.history.replaceState({}, "", `/owner/compose?quote=${quotedId}`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${quotedId}` && !init?.method) {
        return json({
          data: post("原始 Moment", quotedId),
          meta: { request_id: "req_quote" },
        });
      }
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
        return json(
          {
            data: {
              ...post("我的补充", "01K2ABCDEF0123456789ABCDE8"),
              quoted_post: {
                unavailable: false,
                id: quotedId,
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
        String(url) === "/api/v1/owner/posts" && init?.method === "POST",
    );
    expect(JSON.parse(String(create?.[1]?.body))).toMatchObject({
      content_md: "我的补充",
      quoted_post_id: quotedId,
    });
  });

  it("reuses an uploaded media ID when post submission fails and retries", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    let creates = 0;
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
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
        String(url) === "/api/v1/owner/posts" && init?.method === "POST",
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
    window.history.replaceState({}, "", "/owner/compose");
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
    expect(await screen.findByText("1 / 9")).toBeInTheDocument();

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
    expect(await screen.findByText("2 / 9")).toBeInTheDocument();

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
    expect(await screen.findByText("9 / 9")).toBeInTheDocument();
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
    expect(screen.getByText("9 / 9")).toBeInTheDocument();
  });

  it("publishes a Moment that contains only an image", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    vi.stubGlobal("XMLHttpRequest", ControlledUploadRequest);
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
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
        String(url) === "/api/v1/owner/posts" && init?.method === "POST",
    );
    expect(JSON.parse(String(create?.[1]?.body))).toEqual({
      content_md: "",
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
    window.history.replaceState({}, "", "/owner/compose");
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
    window.history.replaceState({}, "", "/owner/compose");
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
          String(url) === "/api/v1/owner/posts" && init?.method === "POST",
      ),
    ).toHaveLength(0);
  });

  it("preserves input before an auth-expiry response and requires a fresh exchange", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    installApi((url, init) => {
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
        return unauthorized();
      }
    });
    render(<App />);
    const textarea = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(textarea, { target: { value: "会话失效也不能丢" } });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    expect(await screen.findByLabelText("所有者令牌")).toBeInTheDocument();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "会话失效也不能丢",
    );
    expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
  });

  it("keeps an edited post mounted through auth expiry, re-login, and retry", async () => {
    preauthenticate();
    const item = post("编辑前", "01K2ABCDEF0123456789ABCDE8");
    let current = item;
    let patches = 0;
    window.history.replaceState({}, "", `/owner/m/${item.id}/edit`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${item.id}`)
        return json({ data: current, meta: {} });
      if (
        url === `/api/v1/owner/posts/${item.id}` &&
        init?.method === "PATCH"
      ) {
        patches += 1;
        if (patches === 1) return unauthorized();
        current = post("不会丢的编辑", item.id);
        return json({ data: current, meta: {} });
      }
      if (url === "/api/v1/owner/session" && init?.method === "POST") {
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

    expect(await screen.findByLabelText("所有者令牌")).toBeInTheDocument();
    expect(screen.getByLabelText("Moment 正文")).toHaveValue("不会丢的编辑");
    fireEvent.change(screen.getByLabelText("所有者令牌"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() =>
      expect(screen.queryByLabelText("所有者令牌")).toBeNull(),
    );
    expect(screen.getByLabelText("Moment 正文")).toHaveValue("不会丢的编辑");
    fireEvent.click(screen.getByRole("button", { name: "保存更改" }));

    await waitFor(() => expect(window.location.pathname).toBe(`/m/${item.id}`));
    const patchCalls = fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url) === `/api/v1/owner/posts/${item.id}` &&
        init?.method === "PATCH",
    );
    expect(patchCalls).toHaveLength(2);
    expect(new Headers(patchCalls[1]?.[1]?.headers).get("X-CSRF-Token")).toBe(
      "csrf-after-reauth",
    );
  });

  it("preserves a CSRF-invalid compose buffer for re-login instead of retrying forever", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    let creates = 0;
    const fetcher = installApi((url, init) => {
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
        creates += 1;
        return creates === 1
          ? csrfInvalid()
          : json({
              data: post("跨标签页冲突也不丢", "01K2ABCDEF0123456789ABCDF0"),
              meta: {},
            });
      }
      if (url === "/api/v1/owner/session" && init?.method === "POST") {
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

    expect(await screen.findByLabelText("所有者令牌")).toBeInTheDocument();
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "跨标签页冲突也不丢",
    );
    expect(creates).toBe(1);
    fireEvent.change(screen.getByLabelText("所有者令牌"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
    await waitFor(() =>
      expect(screen.queryByLabelText("所有者令牌")).toBeNull(),
    );
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "跨标签页冲突也不丢",
    );
    fireEvent.click(screen.getByRole("button", { name: "发布" }));
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(creates).toBe(2);
    const createCalls = fetcher.mock.calls.filter(
      ([url, init]) =>
        String(url) === "/api/v1/owner/posts" && init?.method === "POST",
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
    await waitFor(() =>
      expect(window.location.pathname).toBe("/owner/compose"),
    );
    expect(
      screen.getByRole("heading", { name: "发布 Moment" }),
    ).toBeInTheDocument();
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
    window.history.replaceState({}, "", "/owner/compose");
    installApi();
    render(<App />);
    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "还没有发布" },
    });
    fireEvent.click(
      screen.getAllByRole("link", { name: "乔乔的 Moment首页" })[0],
    );
    expect(window.location.pathname).toBe("/owner/compose");
    const dialog = await screen.findByRole("dialog", { name: "退出发布？" });
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    expect(window.confirm).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "继续编辑" }));
    expect(window.location.pathname).toBe("/owner/compose");
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");

    fireEvent.click(
      screen.getAllByRole("link", { name: "乔乔的 Moment首页" })[0],
    );
    await keepDraftAndResumeNavigation();
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "还没有发布",
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

    fireEvent.click(await screen.findByRole("link", { name: "发布" }));
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
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
        return json({
          data: post("跨 surface 的恢复草稿", "01K2ABCDEF0123456789ABCDE9"),
          meta: {},
        });
      }
    });
    render(<App />);
    expect(await screen.findByText("发现一段未发布草稿")).toBeInTheDocument();

    fireEvent.keyDown(document.body, { key: "n" });
    await waitFor(() =>
      expect(window.location.pathname).toBe("/owner/compose"),
    );
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.queryByText("发现一段未发布草稿")).toBeNull();
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue("");
    expect(loadComposeDraft(instance)).toBeNull();
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
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
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
    await waitFor(() =>
      expect(window.location.pathname).toBe("/owner/compose"),
    );
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
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
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
    await waitFor(() =>
      expect(window.location.pathname).toBe("/owner/compose"),
    );
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
      if (url === "/api/v1/owner/posts" && init?.method === "POST") {
        return createResponse;
      }
    });
    render(<App />);

    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "共同起点" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    fireEvent.keyDown(document.body, { key: "n" });
    await keepDraftAndResumeNavigation();
    await waitFor(() =>
      expect(window.location.pathname).toBe("/owner/compose"),
    );
    fireEvent.click(screen.getByRole("button", { name: "恢复" }));
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
    window.history.replaceState({}, "", `/owner/m/${item.id}/edit`);
    const fetcher = installApi((url, init) => {
      if (url === `/api/v1/posts/${item.id}`) {
        return json({ data: item, meta: {} });
      }
      if (
        url === `/api/v1/owner/posts/${item.id}` &&
        init?.method === "PATCH"
      ) {
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
        String(url) === `/api/v1/owner/posts/${item.id}` &&
        init?.method === "PATCH",
    );
    expect(patchCall?.[1]?.body).toBe(
      JSON.stringify({ content_md: "编辑后", media: [] }),
    );
    expect(new Headers(patchCall?.[1]?.headers).get("X-CSRF-Token")).toBe(
      "csrf-from-session",
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
    window.history.replaceState({}, "", `/owner/m/${item.id}/edit`);
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
    expect(window.location.pathname).toBe(`/owner/m/${item.id}/edit`);
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
    window.history.replaceState({}, "", `/owner/m/${item.id}/edit`);
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
      if (url.includes("/api/v1/owner/posts/") && init?.method === "DELETE") {
        return json({ data: deletable, meta: { deleted: true } });
      }
    });
    render(<App />);
    await screen.findByText("准备删除");
    const more = screen.getByRole("button", { name: "更多操作" });
    fireEvent.click(more);
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    expect(
      await screen.findByRole("heading", { name: "删除这段 Moment？" }),
    ).toBeInTheDocument();
    expect(document.documentElement.style.overflow).toBe("hidden");
    expect(document.body.style.position).toBe("");
    expect(document.body.style.top).toBe("");
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "保留" })).toHaveFocus(),
    );
    fireEvent.keyDown(document.body, { key: "n" });
    expect(window.location.pathname).toBe("/");
    fireEvent.click(screen.getByRole("button", { name: "保留" }));
    await waitFor(() => expect(more).toHaveFocus());
    expect(document.documentElement.style.overflow).toBe("");
    expect(document.body.style.position).toBe("");
    expect(document.documentElement.style.overflow).toBe("");

    fireEvent.click(more);
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
      if (url.includes("/api/v1/owner/posts/") && init?.method === "DELETE") {
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

    fireEvent.click(screen.getByRole("button", { name: "更多操作" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除" }));
    const dialog = await screen.findByRole("dialog", {
      name: "删除这段 Moment？",
    });
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "保留" }),
      ).toHaveFocus(),
    );
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
    window.history.replaceState({}, "", "/owner/login");
    window.localStorage.setItem(
      composeDraftKey(instance),
      JSON.stringify({
        content_md: "退出时清理",
        saved_at: new Date().toISOString(),
      }),
    );
    installApi((url, init) => {
      if (url === "/api/v1/owner/session" && init?.method === "DELETE") {
        return json({ data: { logged_out: true }, meta: {} });
      }
    });
    render(<App />);
    fireEvent.click(
      await screen.findByRole("button", { name: "退出所有者模式" }),
    );
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
  });

  it("turns a provider callback into a session this browser can see", async () => {
    window.history.replaceState({}, "", "/owner/compose?auth=ok");
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
    window.history.replaceState({}, "", "/owner/compose");
    installApi((url) => {
      // Only the site owner may read the owner probe. Reading its refusal as
      // "signed out" is what would sign every member out on every refresh.
      if (url === "/api/v1/owner/status") return unauthorized();
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

    // Signed in, and told the truth about where they are: not handed a login
    // form as though the session had gone, and not told they hold an owner
    // session they do not have.
    expect(await screen.findByText("这一页只有站长能打开")).toBeInTheDocument();
    expect(screen.queryByLabelText("此刻想到什么？")).toBeNull();
    expect(screen.queryByLabelText("密码")).toBeNull();
    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-from-session",
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
