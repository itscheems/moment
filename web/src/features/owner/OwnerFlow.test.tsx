import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "../../app/App";
import {
  COMPOSE_DRAFT_CLEARED_EVENT,
  COMPOSE_DRAFT_CLEARED_SCHEMA,
  composeDraftKey,
} from "./draft";

const instance = "0123456789abcdef0123456789abcdef";
const sessionKey = "moment.owner.session.v1";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const siteEnvelope = {
  data: {
    title: "乔乔的 Moment",
    bio: "沿着日常留下坐标",
    timezone: "Asia/Shanghai",
    public_base_url: null,
    avatar_media_id: null,
    comments_enabled: true,
  },
  meta: { instance_id: instance, request_id: "req_site" },
};

function post(content = "刚写下的 Moment", id = "01K2ABCDEF0123456789ABCDE0") {
  return {
    id,
    content_md: content,
    content_html: `<p>${content}</p>`,
    created_at: "2026-08-10T01:00:00.000Z",
    updated_at: "2026-08-10T01:00:00.000Z",
    published_at: "2026-08-10T01:00:00.000Z",
  };
}

function ownerStatus() {
  return json({
    data: { initialized: true, session_epoch: 1 },
    meta: { request_id: "req_owner" },
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

    const token = await screen.findByLabelText("所有者凭据");
    fireEvent.change(token, { target: { value: bearer } });
    fireEvent.click(screen.getByRole("button", { name: "进入所有者模式" }));
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
    await waitFor(() =>
      expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull(),
    );
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
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    await waitFor(
      () => expect(screen.getByText("十秒内写下这一刻")).toBeInTheDocument(),
      { timeout: 3_000 },
    );
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

    expect(await screen.findByLabelText("所有者凭据")).toBeInTheDocument();
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

    expect(await screen.findByLabelText("所有者凭据")).toBeInTheDocument();
    expect(screen.getByLabelText("Moment 正文")).toHaveValue("不会丢的编辑");
    fireEvent.change(screen.getByLabelText("所有者凭据"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "进入所有者模式" }));
    await waitFor(() =>
      expect(screen.queryByLabelText("所有者凭据")).toBeNull(),
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

    expect(await screen.findByLabelText("所有者凭据")).toBeInTheDocument();
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue(
      "跨标签页冲突也不丢",
    );
    expect(creates).toBe(1);
    fireEvent.change(screen.getByLabelText("所有者凭据"), {
      target: { value: "fresh-owner-token" },
    });
    fireEvent.click(screen.getByRole("button", { name: "进入所有者模式" }));
    await waitFor(() =>
      expect(screen.queryByLabelText("所有者凭据")).toBeNull(),
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

  it("keeps an unsaved page composer in place until leave is confirmed", async () => {
    preauthenticate();
    window.history.replaceState({}, "", "/owner/compose");
    installApi();
    const confirm = vi.mocked(window.confirm);
    confirm.mockReturnValue(false);
    render(<App />);
    fireEvent.change(await screen.findByLabelText("此刻想到什么？"), {
      target: { value: "还没有发布" },
    });
    fireEvent.click(
      screen.getAllByRole("link", { name: "乔乔的 Moment首页" })[0],
    );
    expect(window.location.pathname).toBe("/owner/compose");
    expect(confirm).toHaveBeenCalledWith(
      "这段内容还没有发布。离开后仍会保留本地草稿。",
    );

    confirm.mockReturnValue(true);
    fireEvent.click(
      screen.getAllByRole("link", { name: "乔乔的 Moment首页" })[0],
    );
    expect(window.location.pathname).toBe("/");
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "还没有发布",
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
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
    expect(screen.getAllByText("跨 surface 的恢复草稿")).toHaveLength(1);

    const inline = screen.getByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "发布后继续写的新草稿" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "发布后继续写的新草稿",
    );

    const confirm = vi.mocked(window.confirm);
    confirm.mockReturnValue(false);
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(confirm).toHaveBeenCalledWith(
      "这段内容还没有发布。离开后仍会保留本地草稿。",
    );
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("clears the matching hidden inline surface after publishing when storage is denied", async () => {
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
    await waitFor(() =>
      expect(window.location.pathname).toBe("/owner/compose"),
    );
    fireEvent.change(screen.getAllByLabelText("此刻想到什么？")[1], {
      target: { value: "无法落盘但已经发布" },
    });
    fireEvent.click(screen.getByRole("button", { name: "发布" }));

    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(screen.getByLabelText("此刻想到什么？")).toHaveValue("");
    const publishAgain = screen.getByRole("button", { name: "发布" });
    expect(publishAgain).toBeDisabled();
    fireEvent.click(publishAgain);
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

  it("clears inline only when another tab removes the same draft", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "会被另一标签页清理" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    const clearedDraft = window.localStorage.getItem(composeDraftKey(instance));
    expect(clearedDraft).not.toBeNull();
    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: clearedDraft,
        newValue: null,
      }),
    );
    await waitFor(() => expect(inline).toHaveValue(""));

    fireEvent.change(inline, { target: { value: "跨标签页清理后重新写" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "跨标签页清理后重新写",
    );

    const confirm = vi.mocked(window.confirm);
    confirm.mockReturnValue(false);
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(confirm).toHaveBeenCalledWith(
      "这段内容还没有发布。离开后仍会保留本地草稿。",
    );
    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("preserves and re-saves divergent dirty text when another tab clears a draft", async () => {
    preauthenticate();
    installApi();
    render(<App />);
    const inline = await screen.findByLabelText("此刻想到什么？");
    fireEvent.change(inline, { target: { value: "A 标签页已经分叉的正文" } });
    await new Promise((resolve) => window.setTimeout(resolve, 550));

    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: '{"content_md":"缺少时间"}',
        newValue: null,
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
          schema: COMPOSE_DRAFT_CLEARED_SCHEMA,
          instance_id: "fedcba9876543210fedcba9876543210",
          content_md: "A 标签页已经分叉的正文",
          saved_at: "2026-08-10T12:00:00.000Z",
        },
      }),
    );
    expect(inline).toHaveValue("A 标签页已经分叉的正文");

    const otherDraft = JSON.stringify({
      content_md: "B 标签页刚刚发布的正文",
      saved_at: "2026-08-10T12:01:00.000Z",
    });
    window.localStorage.setItem(composeDraftKey(instance), otherDraft);
    window.localStorage.removeItem(composeDraftKey(instance));
    fireEvent(
      window,
      new StorageEvent("storage", {
        key: composeDraftKey(instance),
        oldValue: otherDraft,
        newValue: null,
      }),
    );

    expect(inline).toHaveValue("A 标签页已经分叉的正文");
    await new Promise((resolve) => window.setTimeout(resolve, 550));
    expect(window.localStorage.getItem(composeDraftKey(instance))).toContain(
      "A 标签页已经分叉的正文",
    );

    const confirm = vi.mocked(window.confirm);
    confirm.mockReturnValue(false);
    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(window.location.pathname).toBe("/");
    expect(confirm).toHaveBeenCalledWith(
      "这段内容还没有发布。离开后仍会保留本地草稿。",
    );
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
    expect(patchCall?.[1]?.body).toBe(JSON.stringify({ content_md: "编辑后" }));
    expect(new Headers(patchCall?.[1]?.headers).get("X-CSRF-Token")).toBe(
      "csrf-from-session",
    );
    expect(window.localStorage.getItem(composeDraftKey(instance))).toBeNull();
  });

  it("uses a focus-restoring confirmation before owner deletion", async () => {
    preauthenticate();
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
    const remove = screen.getByRole("button", { name: "删除" });
    fireEvent.click(remove);
    expect(
      await screen.findByRole("heading", { name: "删除这段 Moment？" }),
    ).toBeInTheDocument();
    fireEvent.keyDown(document.body, { key: "n" });
    expect(window.location.pathname).toBe("/");
    fireEvent.click(screen.getByRole("button", { name: "保留" }));
    await waitFor(() => expect(remove).toHaveFocus());

    fireEvent.click(remove);
    fireEvent.click(await screen.findByRole("button", { name: "删除 Moment" }));
    await waitFor(() => expect(screen.queryByText("准备删除")).toBeNull());
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
});
