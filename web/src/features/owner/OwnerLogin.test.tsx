import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { OwnerLogin } from "./OwnerLogin";
import { OwnerProvider } from "./OwnerProvider";

const site: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "乔乔",
  title: "乔乔的 Moment",
  bio: "沿着日常留下坐标",
  timezone: "Asia/Shanghai",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: "0123456789abcdef0123456789abcdef",
  instanceInitializedAt: "2026-08-01T00:00:00Z",
};

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function ownerStatus() {
  return json({
    data: {
      initialized: true,
      password_enabled: true,
      session_epoch: 1,
      initialized_at: "2026-08-01T00:00:00Z",
    },
    meta: { request_id: "req_owner" },
  });
}

function providersEnvelope(providers: Array<{ name: string; label: string }>) {
  return json({
    data: { providers },
    meta: { request_id: "req_providers" },
  });
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
  providers: Array<{ name: string; label: string }> | "unconfigured",
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === "/api/v1/owner/status") return ownerStatus();
    // Every page in this suite is a signed-out one, and the account session is
    // what now says so.
    if (url === "/api/v1/account/session") {
      return json(signedOutEnvelope(), 401);
    }
    if (url === "/api/v1/owner/login-options") {
      return json({
        data: { password_enabled: true },
        meta: { request_id: "req_options" },
      });
    }
    if (url === "/api/v1/login/oauth/providers") {
      // Instances built before third-party login do not serve this route at all.
      return providers === "unconfigured"
        ? json({}, 404)
        : providersEnvelope(providers);
    }
    return json({}, 404);
  });
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

// jsdom's Location has unforgeable members, so the redirect is observed by
// swapping the global for a delegate that keeps the real URL readable.
function captureRedirect() {
  const real = window.location;
  const assign = vi.fn();
  vi.stubGlobal("location", {
    get href() {
      return real.href;
    },
    get pathname() {
      return real.pathname;
    },
    get search() {
      return real.search;
    },
    get hash() {
      return real.hash;
    },
    assign,
  });
  return assign;
}

function renderLogin() {
  return render(
    <NavigationProvider>
      <OwnerProvider>
        <OwnerLogin site={site} />
      </OwnerProvider>
    </NavigationProvider>,
  );
}

describe("owner login third-party entry points", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/owner/login");
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("degrades to the credential form when no provider is configured", async () => {
    installApi([]);
    renderLogin();

    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
    expect(screen.queryByText("or")).toBeNull();
    expect(screen.queryByRole("button", { name: /使用 .* 登录/ })).toBeNull();
    expect(document.querySelector('svg[data-icon="github"]')).toBeNull();
    expect(document.querySelector('svg[data-icon="google"]')).toBeNull();
  });

  it("degrades the same way when the instance does not serve the endpoint", async () => {
    installApi("unconfigured");
    renderLogin();

    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
    expect(screen.queryByText("or")).toBeNull();
    expect(screen.queryByRole("button", { name: /使用 .* 登录/ })).toBeNull();
  });

  it("puts GitHub above Google whatever order the server sends", async () => {
    installApi([
      { name: "google", label: "Google" },
      { name: "github", label: "GitHub" },
    ]);
    renderLogin();

    const buttons = await screen.findAllByRole("button", {
      name: /使用 .* 登录/,
    });
    expect(buttons.map((button) => button.textContent)).toEqual([
      "使用 GitHub 登录",
      "使用 Google 登录",
    ]);
    expect(buttons[0]?.querySelector('svg[data-icon="github"]')).not.toBeNull();
    expect(buttons[1]?.querySelector('svg[data-icon="google"]')).not.toBeNull();
    expect(screen.getByText("or")).toBeInTheDocument();
  });

  it("redirects the whole page to the start endpoint and locks the buttons", async () => {
    window.history.replaceState(
      {},
      "",
      "/owner/login?return=%2Fowner%2Fcompose",
    );
    installApi([
      { name: "github", label: "GitHub" },
      { name: "google", label: "Google" },
    ]);
    const assign = captureRedirect();
    renderLogin();

    const github = await screen.findByRole("button", {
      name: "使用 GitHub 登录",
    });
    fireEvent.click(github);

    expect(assign).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledWith(
      "/api/v1/login/oauth/github/start?return=%2Fowner%2Fcompose",
    );

    // The browser has not left yet, so nothing may accept a second click.
    const starting = screen.getByRole("button", {
      name: "正在前往 GitHub…",
    });
    expect(starting).toBeDisabled();
    const google = screen.getByRole("button", { name: "使用 Google 登录" });
    expect(google).toBeDisabled();
    fireEvent.click(google);
    expect(assign).toHaveBeenCalledTimes(1);
  });

  it("falls back to the site root when no return path was supplied", async () => {
    installApi([{ name: "google", label: "Google" }]);
    const assign = captureRedirect();
    renderLogin();

    fireEvent.click(
      await screen.findByRole("button", { name: "使用 Google 登录" }),
    );
    expect(assign).toHaveBeenCalledWith(
      "/api/v1/login/oauth/google/start?return=%2F",
    );
  });

  it.each([
    [
      "OAUTH_PROVIDER_UNAVAILABLE",
      "这个登录方式暂时不可用，请稍后再试或改用密码登录。",
    ],
    ["OAUTH_STATE_INVALID", "登录请求已过期，请重新点击登录。"],
    ["OAUTH_IDENTITY_UNUSABLE", "没能从该账号读到可用的身份信息。"],
    ["ACCOUNT_SUSPENDED", "这个账号已被停用。"],
    ["REGISTRATION_CLOSED", "本站当前不接受新注册。"],
    ["OAUTH_EXCHANGE_FAILED", "与登录服务通信失败，请重试。"],
  ])("explains the %s failure in the reader's language", async (code, text) => {
    window.history.replaceState({}, "", `/owner/login?auth_error=${code}`);
    installApi([{ name: "github", label: "GitHub" }]);
    renderLogin();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(text);
  });

  it("consumes the failure code so a refresh cannot replay it", async () => {
    window.history.replaceState(
      {},
      "",
      "/owner/login?return=%2Farchive&auth_error=ACCOUNT_SUSPENDED",
    );
    installApi([]);
    renderLogin();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这个账号已被停用。",
    );
    expect(window.location.search).toBe("?return=%2Farchive");
  });

  it("still names an unrecognised failure rather than staying silent", async () => {
    window.history.replaceState(
      {},
      "",
      "/owner/login?auth_error=SOMETHING_NEW",
    );
    installApi([]);
    renderLogin();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "第三方登录没有完成，请重试。",
    );
    expect(window.location.search).toBe("");
  });
});
