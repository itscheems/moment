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

import { App } from "../../app/App";

const sessionKey = "moment.owner.session.v1";
const avatarID = "01K2ABCDEF0123456789ABCDE7";
const newAvatarID = "01K2ABCDEF0123456789ABCDE8";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function failure(code: string, status: number): Response {
  return json(
    {
      error: {
        code,
        message: code.toLowerCase(),
        details: {},
        request_id: "req_error",
      },
    },
    status,
  );
}

function siteEnvelope(avatarMediaId: string | null = null) {
  return {
    data: {
      title: "乔乔的 Moment",
      bio: "沿着日常留下坐标",
      timezone: "Asia/Shanghai",
      public_base_url: "https://moment.example.com",
      avatar_media_id: avatarMediaId,
      cover_media_id: null,
      pinned_post_id: null,
      comments_enabled: true,
    },
    meta: {
      instance_id: "0123456789abcdef0123456789abcdef",
      request_id: "req_site",
    },
  };
}

function statusEnvelope(epoch = 3) {
  return {
    data: {
      initialized: true,
      session_epoch: epoch,
      initialized_at: "2026-08-01T00:00:00Z",
      rotated_at: "2026-08-09T00:00:00Z",
    },
    meta: { request_id: "req_status" },
  };
}

function installSession() {
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({
      csrf_token: "csrf-settings",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
}

function installFetch(
  handler: (url: string, init?: RequestInit) => Promise<Response> = async (
    url,
  ) => {
    if (url === "/api/v1/site") return json(siteEnvelope());
    if (url === "/api/v1/owner/status") return json(statusEnvelope());
    return json({}, 404);
  },
) {
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/api/v1/owner/import/status") {
        return failure("IMPORT_STAGING_INVALID", 409);
      }
      return handler(url, init);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

async function waitForRestoreStatus() {
  await waitFor(() => expect(screen.getByLabelText(/站点标题/)).toBeEnabled());
}

class FakeUploadRequest extends EventTarget {
  static requests: FakeUploadRequest[] = [];

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
    FakeUploadRequest.requests.push(this);
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

  succeed() {
    this.status = 201;
    this.responseText = JSON.stringify({
      data: {
        media: {
          id: newAvatarID,
          sha256: "a".repeat(64),
          original_name: "avatar.png",
          mime_type: "image/png",
          byte_size: 4,
          width: 600,
          height: 600,
          created_at: "2026-08-11T00:00:00Z",
          unreferenced_since: "2026-08-11T00:00:00Z",
          animated_original: false,
        },
        preview: {
          recipe_version: "preview-v1",
          sha256: "b".repeat(64),
          mime_type: "image/png",
          width: 600,
          height: 600,
          animated_original: false,
        },
        original_url: `/api/v1/media/${newAvatarID}`,
        preview_url: `/api/v1/media/${newAvatarID}/preview`,
      },
      meta: { request_id: "req_upload" },
    });
    this.dispatchEvent(new Event("load"));
    this.dispatchEvent(new Event("loadend"));
  }
}

class ControlledFileReader extends EventTarget {
  static readonly EMPTY = 0;
  static readonly LOADING = 1;
  static readonly DONE = 2;
  static readers: ControlledFileReader[] = [];

  error: DOMException | null = null;
  readyState = ControlledFileReader.EMPTY;
  result: string | ArrayBuffer | null = null;

  constructor() {
    super();
    ControlledFileReader.readers.push(this);
  }

  readAsDataURL() {
    this.readyState = ControlledFileReader.LOADING;
  }

  abort() {
    this.readyState = ControlledFileReader.DONE;
    this.dispatchEvent(new ProgressEvent("abort"));
    this.dispatchEvent(new ProgressEvent("loadend"));
  }

  fail() {
    this.readyState = ControlledFileReader.DONE;
    this.error = new DOMException("preview failed", "NotReadableError");
    this.dispatchEvent(new ProgressEvent("error"));
    this.dispatchEvent(new ProgressEvent("loadend"));
  }

  succeed(value = "data:image/png;base64,cG5n") {
    this.readyState = ControlledFileReader.DONE;
    this.result = value;
    this.dispatchEvent(new ProgressEvent("load"));
    this.dispatchEvent(new ProgressEvent("loadend"));
  }
}

describe("owner site settings", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/owner/settings");
    window.sessionStorage.clear();
    installSession();
    FakeUploadRequest.requests = [];
    ControlledFileReader.readers = [];
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
      "requestAnimationFrame",
      vi.fn((callback: FrameRequestCallback) => {
        return window.setTimeout(() => callback(0), 0);
      }),
    );
    vi.stubGlobal("XMLHttpRequest", FakeUploadRequest);
    vi.stubGlobal("URL", URL);
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:avatar-preview");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  it("shows field errors, blocks dirty navigation, and warns before unload", async () => {
    installFetch();
    const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
    render(<App />);
    await screen.findByRole("heading", { name: "站点身份" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText(/站点时区/), {
      target: { value: "Mars/Olympus" },
    });
    fireEvent.change(screen.getByLabelText(/公开站点地址/), {
      target: { value: "https://owner@example.com/path?secret=1" },
    });
    expect(screen.getByText(/请输入有效的 IANA 时区/)).toBeInTheDocument();
    expect(screen.getByText(/不要包含账号、查询或片段/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存设置" })).toBeDisabled();

    fireEvent.click(screen.getAllByRole("link", { name: "搜索" })[0]);
    expect(confirm).toHaveBeenCalledWith("站点设置还没有保存。确定离开吗？");
    expect(window.location.pathname).toBe("/owner/settings");

    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);
  });

  it("preserves dirty fields across a failed save and patches only changed values on retry", async () => {
    let patches = 0;
    const bodies: Record<string, unknown>[] = [];
    installFetch(async (url, init) => {
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/owner/status") return json(statusEnvelope());
      if (url === "/api/v1/owner/site" && init?.method === "PATCH") {
        patches += 1;
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        bodies.push(body);
        if (patches === 1) return failure("INTERNAL", 500);
        return json({
          data: { ...siteEnvelope().data, ...body },
          meta: { request_id: "req_saved" },
        });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "站点身份" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText(/站点标题/), {
      target: { value: "雨天手记" },
    });
    fireEvent.click(screen.getByRole("switch", { name: /允许新评论/ }));
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    expect(
      await screen.findByText(/当前更改仍保留，可以安全重试/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/站点标题/)).toHaveValue("雨天手记");

    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    expect(
      await screen.findByText("设置已保存，公开站点身份已更新。"),
    ).toBeInTheDocument();
    expect(bodies).toEqual([
      { title: "雨天手记", comments_enabled: false },
      { title: "雨天手记", comments_enabled: false },
    ]);
    expect(screen.getAllByText("雨天手记").length).toBeGreaterThan(1);
  });

  it("rebases edits made during a pending save and accepts IPv6 loopback URLs", async () => {
    let resolveFirstPatch: ((response: Response) => void) | undefined;
    let patchCount = 0;
    const bodies: Record<string, unknown>[] = [];
    installFetch(async (url, init) => {
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/owner/status") return json(statusEnvelope());
      if (url === "/api/v1/owner/site" && init?.method === "PATCH") {
        patchCount += 1;
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        bodies.push(body);
        if (patchCount === 1) {
          return await new Promise<Response>((resolve) => {
            resolveFirstPatch = resolve;
          });
        }
        return json({
          data: { ...siteEnvelope().data, ...body },
          meta: { request_id: "req_saved_again" },
        });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "站点身份" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText(/站点标题/), {
      target: { value: "提交中的标题" },
    });
    fireEvent.change(screen.getByLabelText(/公开站点地址/), {
      target: { value: "http://[::1]:8080/memory" },
    });
    expect(screen.queryByText(/请输入 HTTPS 站点基址/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await screen.findByText("正在保存站点设置…");

    fireEvent.change(screen.getByLabelText(/站点标题/), {
      target: { value: "保存期间继续写的标题" },
    });
    await act(async () => {
      resolveFirstPatch?.(
        json({
          data: {
            ...siteEnvelope().data,
            title: "提交中的标题",
            public_base_url: "http://[::1]:8080/memory",
          },
          meta: { request_id: "req_saved_first" },
        }),
      );
    });

    expect(
      await screen.findByText("之前提交的设置已保存；新的更改仍待保存。"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/站点标题/)).toHaveValue(
      "保存期间继续写的标题",
    );
    expect(screen.getByRole("button", { name: "保存设置" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await screen.findByText("设置已保存，公开站点身份已更新。");
    expect(bodies).toEqual([
      {
        title: "提交中的标题",
        public_base_url: "http://[::1]:8080/memory",
      },
      { title: "保存期间继续写的标题" },
    ]);
  });

  it("keeps a failed avatar separate, then saves a managed avatar or explicit removal", async () => {
    const bodies: Record<string, unknown>[] = [];
    installFetch(async (url, init) => {
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/owner/status") return json(statusEnvelope());
      if (url === "/api/v1/owner/site" && init?.method === "PATCH") {
        const body = JSON.parse(String(init.body)) as Record<string, unknown>;
        bodies.push(body);
        return json({
          data: { ...siteEnvelope().data, ...body },
          meta: { request_id: "req_saved" },
        });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "站点身份" });
    await waitForRestoreStatus();

    const file = new File(["png"], "avatar.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("选择站点头像"), {
      target: { files: [file] },
    });
    expect(FakeUploadRequest.requests).toHaveLength(1);
    FakeUploadRequest.requests[0].fail();
    expect(await screen.findByText(/其他资料仍可单独保存/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/简介/), {
      target: { value: "头像稍后再试" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await screen.findByText("资料已保存；头像仍待重试或移除。");
    expect(bodies[0]).toEqual({ bio: "头像稍后再试" });

    fireEvent.click(screen.getByRole("button", { name: "重试头像" }));
    FakeUploadRequest.requests[1].succeed();
    await screen.findByText("头像已上传，保存设置后才会公开使用。");
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() =>
      expect(bodies.at(-1)).toEqual({ avatar_media_id: newAvatarID }),
    );

    fireEvent.click(screen.getByRole("button", { name: "移除头像" }));
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await waitFor(() =>
      expect(bodies.at(-1)).toEqual({ avatar_media_id: null }),
    );
  });

  it("uses CSP-safe data previews and cancels an upload without losing other edits", async () => {
    vi.stubGlobal("FileReader", ControlledFileReader);
    const createObjectURL = vi.spyOn(URL, "createObjectURL");
    installFetch();
    render(<App />);
    await screen.findByRole("heading", { name: "站点身份" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText(/简介/), {
      target: { value: "这段简介仍需保存" },
    });
    const file = new File(["png"], "avatar.png", { type: "image/png" });
    fireEvent.change(screen.getByLabelText("选择站点头像"), {
      target: { files: [file] },
    });
    expect(
      screen.getByText("正在读取本地预览；头像上传已同时开始。"),
    ).toBeInTheDocument();
    expect(createObjectURL).not.toHaveBeenCalled();
    expect(FakeUploadRequest.requests).toHaveLength(1);

    act(() => ControlledFileReader.readers[0].fail());
    expect(screen.getByText(/本地预览无法显示/)).toBeInTheDocument();
    const picker = screen.getByRole("button", { name: "正在上传…" });
    fireEvent.click(screen.getByRole("button", { name: "取消上传" }));
    expect(FakeUploadRequest.requests[0].aborted).toBe(true);
    await waitFor(() => expect(picker).toHaveFocus());
    expect(screen.getByLabelText(/简介/)).toHaveValue("这段简介仍需保存");
    expect(
      screen.getByText("头像上传已取消，其他更改仍保留。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存设置" })).toBeEnabled();
  });

  it("keeps unsaved values visible when the owner session expires", async () => {
    installFetch(async (url, init) => {
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/owner/status") return json(statusEnvelope());
      if (url === "/api/v1/owner/site" && init?.method === "PATCH") {
        return failure("UNAUTHORIZED", 401);
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "站点身份" });
    await waitForRestoreStatus();
    fireEvent.change(screen.getByLabelText(/站点标题/), {
      target: { value: "仍在输入中的标题" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));

    expect(
      await screen.findByText(/会话已过期。更改仍保留/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/站点标题/)).toHaveValue("仍在输入中的标题");
    expect(
      screen.getByRole("heading", { name: "重新登录" }),
    ).toBeInTheDocument();
  });

  it("restores dialog focus and reveals, copies, then forgets a rotated token", async () => {
    const clipboard = { writeText: vi.fn(async () => undefined) };
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: clipboard,
    });
    installFetch(async (url, init) => {
      if (url === "/api/v1/site") return json(siteEnvelope(avatarID));
      if (url === "/api/v1/owner/status") return json(statusEnvelope());
      if (url === "/api/v1/owner/token/rotate" && init?.method === "POST") {
        return json({
          data: {
            token: "one-time-owner-token",
            initialized: true,
            session_epoch: 4,
            initialized_at: "2026-08-01T00:00:00Z",
            rotated_at: "2026-08-11T00:00:00Z",
          },
          meta: { request_id: "req_rotate" },
        });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "访问凭据" });
    await waitForRestoreStatus();

    const trigger = screen.getByRole("button", { name: "轮换凭据" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("button", { name: "保留" }));
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("button", { name: "确认轮换" }));
    expect(await screen.findByText("one-time-owner-token")).toBeInTheDocument();
    expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
    expect(window.localStorage.getItem("one-time-owner-token")).toBeNull();
    expect(window.location.href).not.toContain("one-time-owner-token");

    fireEvent.click(screen.getByRole("button", { name: "复制新凭据" }));
    await waitFor(() =>
      expect(clipboard.writeText).toHaveBeenCalledWith("one-time-owner-token"),
    );
    expect(screen.getByText(/已复制。请确认/)).toBeInTheDocument();
  });

  it.each([
    ["网络中断", async () => Promise.reject(new TypeError("offline"))],
    [
      "成功响应被截断",
      async () =>
        new Response('{"data":', {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
    ],
  ])(
    "blocks blind rotation retries when %s leaves the outcome unknown",
    async (_name, rotateResponse) => {
      installFetch(async (url, init) => {
        if (url === "/api/v1/site") return json(siteEnvelope());
        if (url === "/api/v1/owner/status") return json(statusEnvelope());
        if (url === "/api/v1/owner/token/rotate" && init?.method === "POST") {
          return await rotateResponse();
        }
        return json({}, 404);
      });
      render(<App />);
      await screen.findByRole("heading", { name: "访问凭据" });
      await waitForRestoreStatus();

      fireEvent.click(screen.getByRole("button", { name: "轮换凭据" }));
      fireEvent.click(await screen.findByRole("button", { name: "确认轮换" }));

      expect(
        await screen.findByRole("heading", { name: "不要再次从浏览器轮换" }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "moment owner rotate-token --data-dir <数据目录> --json",
        ),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "轮换凭据" }),
      ).not.toBeInTheDocument();
      expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
      expect(
        screen.queryByRole("heading", { name: "重新登录" }),
      ).not.toBeInTheDocument();
    },
  );

  it.each([
    ["auth", failure("UNAUTHORIZED", 401), /服务器拒绝了当前会话/, false],
    ["server", failure("INTERNAL", 500), /服务器明确返回轮换失败/, true],
  ])(
    "keeps a %s rotation failure distinct from an unknown outcome",
    async (_name, response, message, sessionRemains) => {
      installFetch(async (url, init) => {
        if (url === "/api/v1/site") return json(siteEnvelope());
        if (url === "/api/v1/owner/status") return json(statusEnvelope());
        if (url === "/api/v1/owner/token/rotate" && init?.method === "POST") {
          return response.clone();
        }
        return json({}, 404);
      });
      render(<App />);
      await screen.findByRole("heading", { name: "访问凭据" });
      await waitForRestoreStatus();
      fireEvent.click(screen.getByRole("button", { name: "轮换凭据" }));
      fireEvent.click(await screen.findByRole("button", { name: "确认轮换" }));

      expect(await screen.findByText(message)).toBeInTheDocument();
      expect(
        screen.queryByRole("heading", { name: "不要再次从浏览器轮换" }),
      ).not.toBeInTheDocument();
      expect(Boolean(window.sessionStorage.getItem(sessionKey))).toBe(
        sessionRemains,
      );
    },
  );
});
