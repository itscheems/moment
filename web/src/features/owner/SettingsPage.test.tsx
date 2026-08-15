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
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "../../app/App";
import settingsCss from "./Settings.module.css?inline";

const sessionKey = "moment.owner.session.v1";
const avatarID = "01K2ABCDEF0123456789ABCDE7";
const sectionIDs = ["identity", "conversation", "access", "data"] as const;
let intersectionCallback: IntersectionObserverCallback | undefined;
let intersectionObserver: IntersectionObserver;

function captureIntersectionObserver(observer: IntersectionObserver) {
  intersectionObserver = observer;
}

function sectionRect(top: number): DOMRect {
  return {
    bottom: top + 450,
    height: 450,
    left: 0,
    right: 640,
    top,
    width: 640,
    x: 0,
    y: top,
    toJSON: () => ({}),
  };
}

function boundedSectionRect(top: number, bottom: number): DOMRect {
  return {
    ...sectionRect(top),
    bottom,
    height: bottom - top,
  };
}

function intersectSection(activeID: (typeof sectionIDs)[number]) {
  const activeIndex = sectionIDs.indexOf(activeID);
  sectionIDs.forEach((id, index) => {
    const section = document.getElementById(id);
    if (!section) throw new Error(`missing settings section ${id}`);
    vi.spyOn(section, "getBoundingClientRect").mockReturnValue(
      sectionRect(96 + (index - activeIndex) * 500),
    );
  });
  act(() => intersectionCallback?.([], intersectionObserver));
}

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
      owner_id: "00000000000000000000000000000000",
      username: "owner",
      display_name: "乔乔",
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
      password_enabled: false,
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
  await waitFor(() => expect(screen.getByLabelText(/^时区/)).toBeEnabled());
}

describe("owner site settings", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/owner/settings");
    window.sessionStorage.clear();
    installSession();
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
      configurable: true,
      value: vi.fn(),
    });
    class IntersectionObserverMock implements IntersectionObserver {
      readonly root: Element | Document | null = null;
      readonly rootMargin = "-96px 0px -65% 0px";
      readonly scrollMargin = "0px";
      readonly thresholds = [0, 1];

      constructor(callback: IntersectionObserverCallback) {
        intersectionCallback = callback;
        captureIntersectionObserver(this);
      }

      disconnect = vi.fn();
      observe = vi.fn();
      takeRecords = vi.fn(() => []);
      unobserve = vi.fn();
    }
    vi.stubGlobal("IntersectionObserver", IntersectionObserverMock);
    vi.stubGlobal(
      "requestAnimationFrame",
      vi.fn((callback: FrameRequestCallback) => {
        return window.setTimeout(() => callback(0), 0);
      }),
    );
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    delete (
      HTMLElement.prototype as Partial<Pick<HTMLElement, "scrollIntoView">>
    ).scrollIntoView;
    window.sessionStorage.clear();
  });

  it("tracks passive scrolling without adding history or stealing focus", async () => {
    installFetch();
    render(<App />);
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();

    const navigation = screen.getByRole("navigation", { name: "设置分区" });
    const dataLink = within(navigation).getByRole("link", { name: "备份" });
    const centerActiveLink = vi.fn();
    Object.defineProperty(dataLink, "scrollIntoView", {
      configurable: true,
      value: centerActiveLink,
    });
    const timezone = screen.getByLabelText(/^时区/);
    timezone.focus();
    const historyLength = window.history.length;

    intersectSection("data");

    await waitFor(() =>
      expect(dataLink).toHaveAttribute("aria-current", "location"),
    );
    expect(
      within(navigation)
        .getAllByRole("link")
        .filter((link) => link.getAttribute("aria-current") === "location"),
    ).toHaveLength(1);
    expect(centerActiveLink).toHaveBeenCalledWith({
      block: "nearest",
      inline: "center",
    });
    expect(window.location.hash).toBe("#data");
    expect(window.history.length).toBe(historyLength);
    expect(timezone).toHaveFocus();
  });

  it("pushes and focuses only for explicit section activation", async () => {
    installFetch();
    render(<App />);
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();

    const navigation = screen.getByRole("navigation", { name: "设置分区" });
    const accessLink = within(navigation).getByRole("link", {
      name: "登录与安全",
    });
    const accessSection = document.getElementById("access");
    if (!accessSection) throw new Error("missing access settings section");
    const scrollSection = vi.fn();
    Object.defineProperty(accessSection, "scrollIntoView", {
      configurable: true,
      value: scrollSection,
    });
    const pushState = vi.spyOn(window.history, "pushState");

    fireEvent.click(accessLink);

    const heading = screen.getByRole("heading", { name: "登录与安全" });
    await waitFor(() => expect(heading).toHaveFocus());
    expect(accessLink).toHaveAttribute("aria-current", "location");
    expect(pushState.mock.calls.at(-1)?.[2]).toBe("#access");
    expect(scrollSection).toHaveBeenCalledWith({ block: "start" });
    expect(heading).toHaveAttribute("tabindex", "-1");
  });

  it("keeps a clicked final section current when the viewport cannot align its heading", async () => {
    installFetch();
    render(<App />);
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();

    const navigation = screen.getByRole("navigation", { name: "设置分区" });
    const dataLink = within(navigation).getByRole("link", { name: "备份" });
    const centerDataLink = vi.fn();
    Object.defineProperty(dataLink, "scrollIntoView", {
      configurable: true,
      value: centerDataLink,
    });
    fireEvent.click(dataLink);

    const dataHeading = screen.getByRole("heading", { name: "备份与恢复" });
    await waitFor(() => expect(dataHeading).toHaveFocus());

    const rects: Record<(typeof sectionIDs)[number], DOMRect> = {
      identity: boundedSectionRect(-1_900, -1_250),
      conversation: boundedSectionRect(-1_250, -730),
      access: boundedSectionRect(-730, 215),
      data: boundedSectionRect(215, 1_100),
    };
    sectionIDs.forEach((id) => {
      const section = document.getElementById(id);
      if (!section) throw new Error(`missing settings section ${id}`);
      vi.spyOn(section, "getBoundingClientRect").mockReturnValue(rects[id]);
    });

    act(() => intersectionCallback?.([], intersectionObserver));

    expect(window.location.hash).toBe("#data");
    expect(dataLink).toHaveAttribute("aria-current", "location");
    expect(dataHeading).toHaveFocus();
    expect(centerDataLink).toHaveBeenCalledWith({
      block: "nearest",
      inline: "center",
    });
  });

  it("restores direct hashes and browser history with matching heading focus", async () => {
    window.history.replaceState({}, "", "/owner/settings#data");
    installFetch();
    render(<App />);
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();

    const navigation = screen.getByRole("navigation", { name: "设置分区" });
    const dataLink = within(navigation).getByRole("link", { name: "备份" });
    const accessLink = within(navigation).getByRole("link", {
      name: "登录与安全",
    });
    const dataHeading = screen.getByRole("heading", { name: "备份与恢复" });
    expect(dataLink).toHaveAttribute("aria-current", "location");
    await waitFor(() => expect(dataHeading).toHaveFocus());

    fireEvent.click(accessLink);
    const accessHeading = screen.getByRole("heading", { name: "登录与安全" });
    await waitFor(() => expect(accessHeading).toHaveFocus());

    window.history.back();
    await waitFor(() => expect(window.location.hash).toBe("#data"));
    expect(dataLink).toHaveAttribute("aria-current", "location");
    await waitFor(() => expect(dataHeading).toHaveFocus());

    window.history.forward();
    await waitFor(() => expect(window.location.hash).toBe("#access"));
    expect(accessLink).toHaveAttribute("aria-current", "location");
    await waitFor(() => expect(accessHeading).toHaveFocus());
  });

  it("gives the current section a non-color visual indicator", () => {
    expect(settingsCss).toContain('a[aria-current="location"]');
    expect(settingsCss).toContain("border-block-end-color: var(--accent)");
    expect(settingsCss).toContain("font-weight: 700");
  });

  it("shows field errors, blocks dirty navigation, and warns before unload", async () => {
    installFetch();
    render(<App />);
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();
    expect(screen.queryByLabelText(/站点标题/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/选择站点头像/)).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/^时区/), {
      target: { value: "Mars/Olympus" },
    });
    fireEvent.change(screen.getByLabelText(/公开站点地址/), {
      target: { value: "https://owner@example.com/path?secret=1" },
    });
    expect(screen.getByText(/请输入有效的时区/)).toBeInTheDocument();
    expect(screen.getByText(/不要包含账号、查询或片段/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存设置" })).toBeDisabled();

    const searchLink = screen.getAllByRole("link", { name: "搜索" })[0];
    searchLink.focus();
    fireEvent.click(searchLink);
    const dialog = await screen.findByRole("dialog", {
      name: "要离开设置吗？",
    });
    expect(
      within(dialog).getByText(
        "当前还有未保存的站点设置。离开后这些更改会丢失。",
      ),
    ).toBeInTheDocument();
    expect(window.location.pathname).toBe("/owner/settings");
    fireEvent.click(within(dialog).getByRole("button", { name: "留在这里" }));
    await waitFor(() => expect(searchLink).toHaveFocus());

    const beforeUnload = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(beforeUnload);
    expect(beforeUnload.defaultPrevented).toBe(true);

    fireEvent.click(searchLink);
    const leaveDialog = await screen.findByRole("dialog", {
      name: "要离开设置吗？",
    });
    fireEvent.click(
      within(leaveDialog).getByRole("button", { name: "仍然离开" }),
    );
    await waitFor(() => expect(window.location.pathname).toBe("/search"));
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
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText(/公开站点地址/), {
      target: { value: "https://diary.example.com" },
    });
    fireEvent.click(screen.getByRole("switch", { name: /允许新评论/ }));
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    expect(
      await screen.findByText(/当前更改仍保留，可以安全重试/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/公开站点地址/)).toHaveValue(
      "https://diary.example.com",
    );

    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    expect(await screen.findByText("设置已保存。")).toBeInTheDocument();
    expect(bodies).toEqual([
      {
        public_base_url: "https://diary.example.com",
        comments_enabled: false,
      },
      {
        public_base_url: "https://diary.example.com",
        comments_enabled: false,
      },
    ]);
    expect(screen.getByLabelText(/公开站点地址/)).toHaveValue(
      "https://diary.example.com",
    );
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
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText(/公开站点地址/), {
      target: { value: "http://[::1]:8080/memory" },
    });
    expect(screen.queryByText(/请输入 HTTPS 站点基址/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await screen.findByText("正在保存站点设置…");

    fireEvent.change(screen.getByLabelText(/^时区/), {
      target: { value: "Asia/Tokyo" },
    });
    await act(async () => {
      resolveFirstPatch?.(
        json({
          data: {
            ...siteEnvelope().data,
            public_base_url: "http://[::1]:8080/memory",
          },
          meta: { request_id: "req_saved_first" },
        }),
      );
    });

    expect(
      await screen.findByText("之前提交的设置已保存；新的更改仍待保存。"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^时区/)).toHaveValue("Asia/Tokyo");
    expect(screen.getByRole("button", { name: "保存设置" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));
    await screen.findByText("设置已保存。");
    expect(bodies).toEqual([
      {
        public_base_url: "http://[::1]:8080/memory",
      },
      { timezone: "Asia/Tokyo" },
    ]);
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
    await screen.findByRole("heading", { name: "网站" });
    await waitForRestoreStatus();
    fireEvent.change(screen.getByLabelText(/^时区/), {
      target: { value: "Asia/Tokyo" },
    });
    fireEvent.click(screen.getByRole("button", { name: "保存设置" }));

    expect(
      await screen.findByText(/会话已过期。更改仍保留/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText(/^时区/)).toHaveValue("Asia/Tokyo");
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
    await screen.findByRole("heading", { name: "登录与安全" });
    await waitForRestoreStatus();

    const trigger = screen.getByRole("button", { name: "更换密钥" });
    trigger.focus();
    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("button", { name: "保留" }));
    await waitFor(() => expect(trigger).toHaveFocus());

    fireEvent.click(trigger);
    fireEvent.click(await screen.findByRole("button", { name: "确认更换" }));
    expect(await screen.findByText("one-time-owner-token")).toBeInTheDocument();
    expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
    expect(window.localStorage.getItem("one-time-owner-token")).toBeNull();
    expect(window.location.href).not.toContain("one-time-owner-token");

    fireEvent.click(screen.getByRole("button", { name: "复制登录密钥" }));
    await waitFor(() =>
      expect(clipboard.writeText).toHaveBeenCalledWith("one-time-owner-token"),
    );
    expect(screen.getByText(/已复制。请确认/)).toBeInTheDocument();
  });

  it("enables password login without exposing the password in the response", async () => {
    const requests: unknown[] = [];
    installFetch(async (url, init) => {
      if (url === "/api/v1/site") return json(siteEnvelope());
      if (url === "/api/v1/owner/status") return json(statusEnvelope());
      if (url === "/api/v1/owner/password" && init?.method === "PUT") {
        requests.push(JSON.parse(String(init.body)));
        return json({
          data: {
            ...statusEnvelope().data,
            password_enabled: true,
          },
          meta: { request_id: "req_password" },
        });
      }
      return json({}, 404);
    });
    render(<App />);
    await screen.findByRole("heading", { name: "登录与安全" });
    await waitForRestoreStatus();

    fireEvent.change(screen.getByLabelText("新密码"), {
      target: { value: "correct horse battery staple" },
    });
    fireEvent.change(screen.getByLabelText("再次输入"), {
      target: { value: "correct horse battery staple" },
    });
    fireEvent.click(screen.getByRole("button", { name: "启用密码登录" }));

    expect(
      await screen.findByText("密码已保存，下次可直接使用密码登录。"),
    ).toBeInTheDocument();
    expect(requests).toEqual([{ password: "correct horse battery staple" }]);
    expect(screen.getByText("已启用")).toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("correct horse battery staple");
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
      await screen.findByRole("heading", { name: "登录与安全" });
      await waitForRestoreStatus();

      fireEvent.click(screen.getByRole("button", { name: "更换密钥" }));
      fireEvent.click(await screen.findByRole("button", { name: "确认更换" }));

      expect(
        await screen.findByRole("heading", { name: "不要再次从浏览器轮换" }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(
          "moment owner rotate-token --data-dir <数据目录> --json",
        ),
      ).toBeInTheDocument();
      expect(
        screen.queryByRole("button", { name: "更换密钥" }),
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
      await screen.findByRole("heading", { name: "登录与安全" });
      await waitForRestoreStatus();
      fireEvent.click(screen.getByRole("button", { name: "更换密钥" }));
      fireEvent.click(await screen.findByRole("button", { name: "确认更换" }));

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
