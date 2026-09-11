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
    if (url === "/api/v1/login/methods") {
      return json({
        data: { methods: ["password", "token"] },
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
    expect(screen.queryByText("或")).toBeNull();
    expect(screen.queryByRole("button", { name: /使用 .* 登录/ })).toBeNull();
    expect(document.querySelector('svg[data-icon="github"]')).toBeNull();
    expect(document.querySelector('svg[data-icon="google"]')).toBeNull();
  });

  it("degrades the same way when the instance does not serve the endpoint", async () => {
    installApi("unconfigured");
    renderLogin();

    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
    expect(screen.queryByText("或")).toBeNull();
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
    expect(screen.getByText("或")).toBeInTheDocument();
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

// The form collects a handle now, and a form that collects one and then ignores
// it is worse than one that never asked: every correct password would sign in
// as the owner no matter whose name was typed above it.
describe("owner login names who is signing in", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/owner/login");
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function installLoginApi(sessionResponse: () => Response) {
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        void init;
        const url = String(input);
        if (url === "/api/v1/owner/status") return ownerStatus();
        if (url === "/api/v1/account/session")
          return json(signedOutEnvelope(), 401);
        if (url === "/api/v1/login/methods") {
          return json({
            data: { methods: ["password", "token"] },
            meta: { request_id: "req_options" },
          });
        }
        if (url === "/api/v1/login/oauth/providers")
          return providersEnvelope([]);
        if (url === "/api/v1/login/session") return sessionResponse();
        return json({}, 404);
      },
    );
    vi.stubGlobal("fetch", fetcher);
    return fetcher;
  }

  async function signIn(username: string, password: string) {
    fireEvent.change(await screen.findByLabelText("用户名"), {
      target: { value: username },
    });
    fireEvent.change(await screen.findByLabelText("密码"), {
      target: { value: password },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
  }

  it("sends the typed handle alongside the password", async () => {
    const fetcher = installLoginApi(() =>
      json({
        data: {
          expires_at: "2026-09-01T00:00:00Z",
          csrf_token: "csrf-value",
        },
        meta: { request_id: "req_session" },
      }),
    );
    renderLogin();
    await signIn("  Owner  ", "hunter2");

    const call = await vi.waitFor(() => {
      const found = fetcher.mock.calls.find(
        ([input]) => String(input) === "/api/v1/login/session",
      );
      if (!found) throw new Error("no session exchange yet");
      return found;
    });
    expect(JSON.parse(String(call[1]?.body))).toEqual({
      password: "hunter2",
      username: "Owner",
    });
  });

  it("sends no handle when the field is left empty", async () => {
    const fetcher = installLoginApi(() =>
      json({
        data: {
          expires_at: "2026-09-01T00:00:00Z",
          csrf_token: "csrf-value",
        },
        meta: { request_id: "req_session" },
      }),
    );
    renderLogin();
    fireEvent.change(await screen.findByLabelText("密码"), {
      target: { value: "hunter2" },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));

    const call = await vi.waitFor(() => {
      const found = fetcher.mock.calls.find(
        ([input]) => String(input) === "/api/v1/login/session",
      );
      if (!found) throw new Error("no session exchange yet");
      return found;
    });
    expect(JSON.parse(String(call[1]?.body))).toEqual({
      password: "hunter2",
    });
  });

  // The server names the credential kinds that would have worked. Reading them
  // rather than assuming "a provider" is what keeps the message honest on the
  // day an account carries a token of its own.
  function credentialUnavailable(available: string[]) {
    return () =>
      json(
        {
          error: {
            code: "CREDENTIAL_UNAVAILABLE",
            message: "that account does not sign in that way here",
            details: { available },
            request_id: "req_session",
          },
        },
        401,
      );
  }

  it("sends an account carrying nothing to its provider instead of blaming the password", async () => {
    installLoginApi(credentialUnavailable([]));
    renderLogin();
    await signIn("someone", "hunter2");

    expect(
      await screen.findByText("这个账户只能用第三方账号登录，请在下方选择。"),
    ).toBeInTheDocument();
    expect(screen.queryByText("密码不正确，请重试。")).toBeNull();
  });

  it("names the credential that would have worked when the server reports one", async () => {
    installLoginApi(credentialUnavailable(["token"]));
    renderLogin();
    await signIn("someone", "hunter2");

    expect(
      await screen.findByText("这个账户不能这样登录，可以改用所有者令牌。"),
    ).toBeInTheDocument();
  });

  it("names the handle when it is the handle that is wrong", async () => {
    installLoginApi(() =>
      json(
        {
          error: {
            code: "ACCOUNT_NOT_FOUND",
            message: "no account here holds that username",
            details: {},
            request_id: "req_session",
          },
        },
        401,
      ),
    );
    renderLogin();
    await signIn("nobodyhere", "hunter2");

    expect(await screen.findByText("本站没有这个用户名。")).toBeInTheDocument();
  });

  it("still blames the password when the password is what was refused", async () => {
    installLoginApi(() =>
      json(
        {
          error: {
            code: "UNAUTHORIZED",
            message: "auth required",
            details: {},
            request_id: "req_session",
          },
        },
        401,
      ),
    );
    renderLogin();
    await signIn("owner", "wrong");

    expect(await screen.findByText("密码不正确，请重试。")).toBeInTheDocument();
  });
});

// The token is a recovery credential, not a way somebody signs in on an
// ordinary day, and there may be more ways later. So the form offers what the
// server says exists rather than a hard-coded pair, and keeps everything that
// is not the default one step back.
describe("owner login offers the methods the server reports", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/login");
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function installMethods(methods: string[] | "unreachable") {
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/owner/status") return ownerStatus();
      if (url === "/api/v1/account/session")
        return json(signedOutEnvelope(), 401);
      if (url === "/api/v1/login/methods") {
        return methods === "unreachable"
          ? json({}, 503)
          : json({
              data: { methods },
              meta: { request_id: "req_methods" },
            });
      }
      if (url === "/api/v1/login/providers") return providersEnvelope([]);
      if (url === "/api/v1/login/oauth/providers") return providersEnvelope([]);
      return json({}, 404);
    });
    vi.stubGlobal("fetch", fetcher);
    return fetcher;
  }

  it("asks for a password by default and keeps the token one step back", async () => {
    installMethods(["password", "token"]);
    renderLogin();

    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
    expect(screen.queryByLabelText("所有者令牌")).toBeNull();

    fireEvent.click(
      await screen.findByRole("button", { name: "选择其他登录方式" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "使用所有者令牌登录" }),
    );

    expect(await screen.findByLabelText("所有者令牌")).toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).toBeNull();
  });

  // An instance with no password set has nothing to fall back to, so the token
  // is the only way in and hiding it behind a disclosure would hide the door.
  it("makes the token the default when no password exists, and offers nothing else", async () => {
    installMethods(["token"]);
    renderLogin();

    expect(await screen.findByLabelText("所有者令牌")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "选择其他登录方式" }),
    ).toBeNull();
  });

  it("keeps the token path usable against an instance that will not say", async () => {
    installMethods("unreachable");
    renderLogin();

    expect(await screen.findByLabelText("所有者令牌")).toBeInTheDocument();
  });
});

// The server refuses backslashes, bare newlines and anything carrying a host;
// this side stopped at the leading slash. The gap opens no redirect -- the
// server performs those -- but it hands `pushState` a value some browsers
// normalise off-origin, and that throw lands in the catch that reports a
// successful sign-in as a failure.
describe("owner login refuses a return path the server would refuse", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it.each([
    // The raw characters, not their percent-encodings: URLSearchParams decodes
    // on the way out, so what safeReturnPath sees is what a browser would act
    // on. `%5C` reaching it undecoded is just a path with a percent sign in it.
    ["a backslash escape", "/\\evil.test"],
    ["a protocol-relative address", "//evil.test"],
    ["an absolute address", "https://evil.test/"],
    ["a smuggled newline", "/ok\nevil"],
    ["a smuggled carriage return", "/ok\revil"],
  ])("sends %s to the site root instead", async (_name, candidate) => {
    window.history.replaceState(
      {},
      "",
      `/login?return=${encodeURIComponent(candidate)}`,
    );
    installApi([{ name: "github", label: "GitHub" }]);
    const assign = captureRedirect();
    renderLogin();

    fireEvent.click(
      await screen.findByRole("button", { name: "使用 GitHub 登录" }),
    );

    expect(assign).toHaveBeenCalledWith(
      "/api/v1/login/oauth/github/start?return=%2F",
    );
  });

  it("keeps a path on this origin", async () => {
    window.history.replaceState({}, "", "/login?return=%2Fowner%2Fsettings");
    installApi([{ name: "github", label: "GitHub" }]);
    const assign = captureRedirect();
    renderLogin();

    fireEvent.click(
      await screen.findByRole("button", { name: "使用 GitHub 登录" }),
    );

    expect(assign).toHaveBeenCalledWith(
      "/api/v1/login/oauth/github/start?return=%2Fowner%2Fsettings",
    );
  });
});
