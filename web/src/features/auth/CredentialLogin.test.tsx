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

import type { SiteIdentity } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { makeAccountLimits } from "../../test/accountFixtures";
import { CredentialLogin } from "./CredentialLogin";
import { SessionProvider } from "./SessionProvider";
import { FeedbackProvider } from "../../components/Feedback";

afterEach(() => vi.useRealTimers());

const site: SiteIdentity = {
  profileUserId: "00000000000000000000000000000000",
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

// The descriptor as the server sends it: flows for every client, and the
// native client id only where the assertion flow is.
interface ProviderDescriptor {
  name: string;
  label: string;
  flows?: string[] | null;
  native_client_id?: string;
}

function providersEnvelope(providers: ProviderDescriptor[]) {
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

function installApi(providers: ProviderDescriptor[] | "unconfigured") {
  const fetcher = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    if (url === "/api/v1/status") return ownerStatus();
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
    <FeedbackProvider>
      <NavigationProvider>
        <SessionProvider>
          <CredentialLogin site={site} />
        </SessionProvider>
      </NavigationProvider>
    </FeedbackProvider>,
  );
}

async function findErrorToast(message?: string) {
  return waitFor(() => {
    const toast = [
      ...document.querySelectorAll<HTMLElement>('[role="alertdialog"]'),
    ].find(
      (element) =>
        !element.hasAttribute("data-ending-style") &&
        (!message || element.textContent?.includes(message)),
    );
    if (!toast) throw new Error("No matching error toast");
    return toast;
  });
}

describe("owner login third-party entry points", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/login");
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // The form used to arrive in pieces -- the credential fields as soon as
  // the methods answered, the provider buttons a request later -- and the
  // page moved under the reader as each piece landed. It waits for both.
  it("draws the form once, whole, rather than fields first and providers later", async () => {
    let releaseProviders: (() => void) | undefined;
    const providersGate = new Promise<void>((resolve) => {
      releaseProviders = resolve;
    });
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/status") return ownerStatus();
      if (url === "/api/v1/account/session")
        return json(signedOutEnvelope(), 401);
      if (url === "/api/v1/login/methods") {
        return json({
          data: { methods: ["password", "token"] },
          meta: { request_id: "req_options" },
        });
      }
      if (url === "/api/v1/login/oauth/providers") {
        await providersGate;
        return providersEnvelope([{ name: "github", label: "GitHub" }]);
      }
      return json({}, 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderLogin();

    // Methods have answered; providers have not. No field yet, only a busy
    // placeholder with nothing in it to move.
    await vi.waitFor(() =>
      expect(
        fetcher.mock.calls.some(
          ([input]) => String(input) === "/api/v1/login/oauth/providers",
        ),
      ).toBe(true),
    );
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByLabelText("用户名")).toBeNull();
    expect(screen.queryByText("检查当前会话…")).toBeNull();
    expect(screen.getByLabelText("正在准备登录")).toHaveAttribute(
      "aria-busy",
      "true",
    );

    releaseProviders!();
    expect(
      await screen.findByLabelText(/^(邮箱或)?用户名$/),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "使用 GitHub 登录" }),
    ).toBeInTheDocument();
    expect(screen.queryByLabelText("正在准备登录")).toBeNull();
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

  it("reloads an initially empty provider list when the tab regains focus", async () => {
    let configured = false;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/account/session")
        return json(signedOutEnvelope(), 401);
      if (url === "/api/v1/login/methods") {
        return json({ data: { methods: ["password", "token"] }, meta: {} });
      }
      if (url === "/api/v1/login/oauth/providers") {
        return providersEnvelope(
          configured ? [{ name: "github", label: "GitHub" }] : [],
        );
      }
      return json({}, 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderLogin();

    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "使用 GitHub 登录" }),
    ).toBeNull();
    configured = true;
    act(() => window.dispatchEvent(new Event("focus")));
    expect(
      await screen.findByRole("button", { name: "使用 GitHub 登录" }),
    ).toBeInTheDocument();
  });

  it("puts GitHub above Google whatever order the server sends", async () => {
    // What the server actually publishes: the flows each client can finish
    // and, for Google, the id a native app runs its own authorization as.
    // The page has to read past both to reach the buttons.
    installApi([
      {
        name: "google",
        label: "Google",
        flows: ["browser", "assertion"],
        native_client_id: "1234567890-native.apps.googleusercontent.com",
      },
      { name: "github", label: "GitHub", flows: ["browser", "device"] },
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

  it("offers only the providers a browser can finish with", async () => {
    // A provider that names flows and leaves out the browser one is a way in
    // for a native client, not for this page; one from an older instance
    // names no flows and was only ever the browser flow.
    installApi([
      { name: "github", label: "GitHub", flows: ["device"] },
      { name: "google", label: "Google" },
      { name: "gitea", label: "Gitea", flows: null },
    ]);
    renderLogin();

    const buttons = await screen.findAllByRole("button", {
      name: /使用 .* 登录/,
    });
    expect(buttons.map((button) => button.textContent)).toEqual([
      "使用 Google 登录",
      "使用 Gitea 登录",
    ]);
  });

  it("redirects the whole page to the start endpoint and locks the buttons", async () => {
    window.history.replaceState({}, "", "/login?return=%2Fcompose");
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
      "/api/v1/login/oauth/github/start?return=%2Fcompose",
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
    window.history.replaceState({}, "", `/login?auth_error=${code}`);
    installApi([{ name: "github", label: "GitHub" }]);
    renderLogin();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent(text);
  });

  it("consumes the failure code so a refresh cannot replay it", async () => {
    window.history.replaceState(
      {},
      "",
      "/login?return=%2Farchive&auth_error=ACCOUNT_SUSPENDED",
    );
    installApi([]);
    renderLogin();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "这个账号已被停用。",
    );
    expect(window.location.search).toBe("?return=%2Farchive");
  });

  it("still names an unrecognised failure rather than staying silent", async () => {
    window.history.replaceState({}, "", "/login?auth_error=SOMETHING_NEW");
    installApi([]);
    renderLogin();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "第三方登录没有完成，请重试。",
    );
    expect(window.location.search).toBe("");
  });
});

describe("owner login names who is signing in", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/login");
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  function installLoginApi(
    sessionResponse: () => Response | Promise<Response>,
  ) {
    let signedIn = false;
    const fetcher = vi.fn(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        void init;
        const url = String(input);
        if (url === "/api/v1/status") return ownerStatus();
        if (url === "/api/v1/account/session")
          return signedIn
            ? json({
                data: {
                  preferences: { ui_locale: null },
                  csrf_token: "csrf-value",
                  expires_at: "2999-09-01T00:00:00Z",
                  account: {
                    user_id: "0".repeat(32),
                    username: "owner",
                    display_name: "Writer",
                    bio: "",
                    avatar_media_id: null,
                    cover_media_id: null,
                    profile_url: "/owner",
                    role: "member",
                    status: "active",
                    username_chosen: true,
                  },
                  limits: makeAccountLimits(),
                },
                meta: { request_id: "account" },
              })
            : json(signedOutEnvelope(), 401);
        if (url === "/api/v1/login/methods") {
          return json({
            data: { methods: ["password", "token"] },
            meta: { request_id: "req_options" },
          });
        }
        if (url === "/api/v1/login/oauth/providers")
          return providersEnvelope([]);
        if (url === "/api/v1/login/session") {
          const response = await sessionResponse();
          if (response.ok) signedIn = true;
          return response;
        }
        return json({}, 404);
      },
    );
    vi.stubGlobal("fetch", fetcher);
    return fetcher;
  }

  async function signIn(username: string, password: string) {
    fireEvent.change(await screen.findByLabelText(/^(邮箱或)?用户名$/), {
      target: { value: username },
    });
    fireEvent.change(await screen.findByLabelText("密码"), {
      target: { value: password },
    });
    fireEvent.click(screen.getByRole("button", { name: "登录" }));
  }

  it("clears an outdated credential error when the input changes", async () => {
    installLoginApi(() => json(signedOutEnvelope(), 401));
    renderLogin();
    await signIn("owner", "wrong-password");
    const toast = await findErrorToast("密码不正确，请重试。");
    const password = screen.getByLabelText("密码");
    expect(password).toHaveAttribute("aria-invalid", "true");
    const description = document.getElementById(
      password.getAttribute("aria-describedby")!,
    );
    expect(description).toHaveTextContent("密码不正确，请重试。");
    fireEvent.change(password, { target: { value: "updated-password" } });
    expect(toast).toHaveAttribute("data-ending-style");
    expect(password).not.toHaveAttribute("aria-describedby");
    expect(password).not.toHaveAttribute("aria-invalid");
    expect(screen.getByLabelText(/^(邮箱或)?用户名$/)).toHaveValue("owner");
    expect(screen.getByRole("button", { name: "登录" })).toBeEnabled();
  });

  it("merges repeated credential failures without stealing password focus", async () => {
    const fetcher = installLoginApi(() => json(signedOutEnvelope(), 401));
    renderLogin();
    await signIn("owner", "wrong-password");
    const toast = await findErrorToast("密码不正确，请重试。");
    const password = screen.getByLabelText("密码");
    password.focus();
    fireEvent.submit(password.closest("form")!);
    await waitFor(() =>
      expect(
        fetcher.mock.calls.filter(
          ([url]) => String(url) === "/api/v1/login/session",
        ),
      ).toHaveLength(2),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "登录" })).toBeEnabled(),
    );
    expect(screen.getAllByRole("alertdialog", { hidden: true })).toHaveLength(
      1,
    );
    expect(
      document
        .querySelector<HTMLElement>('[role="alertdialog"]')!
        .getAttribute("aria-labelledby"),
    ).toBe(toast.getAttribute("aria-labelledby"));
    expect(password).toHaveFocus();
  });

  it("shows transport failures without blaming either credential field", async () => {
    installLoginApi(() => Promise.reject(new TypeError("offline")));
    renderLogin();
    await signIn("owner", "password");
    expect(await findErrorToast()).toHaveTextContent("登录没有完成");
    expect(screen.getByLabelText(/^(邮箱或)?用户名$/)).not.toHaveAttribute(
      "aria-invalid",
    );
    expect(screen.getByLabelText("密码")).not.toHaveAttribute("aria-invalid");
  });

  it("retains the field error after the toast is dismissed", async () => {
    installLoginApi(() => json(signedOutEnvelope(), 401));
    renderLogin();
    await signIn("owner", "wrong-password");
    const toast = await findErrorToast("密码不正确，请重试。");
    const password = screen.getByLabelText("密码");
    const description = password.getAttribute("aria-describedby");
    fireEvent.click(toast.querySelector('button[aria-label="关闭通知"]')!);
    expect(toast).toHaveAttribute("data-ending-style");
    expect(password).toHaveAttribute("aria-invalid", "true");
    expect(document.getElementById(description!)).toHaveTextContent(
      "密码不正确，请重试。",
    );
    fireEvent.change(password, { target: { value: "changed-password" } });
    expect(password).not.toHaveAttribute("aria-invalid");
  });

  it("does not steal navigation when sign-in finishes after leaving the form", async () => {
    let finish!: (response: Response) => void;
    const exchange = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    installLoginApi(() => exchange);
    window.history.replaceState({}, "", "/login?return=%2Farchive");
    const view = renderLogin();
    await signIn("owner", "password");
    expect(screen.getByLabelText("密码")).toHaveAttribute("readonly");
    expect(screen.getByLabelText(/^(邮箱或)?用户名$/)).toHaveAttribute(
      "readonly",
    );
    expect(screen.getByRole("button", { name: "正在登录…" })).toBeDisabled();
    view.unmount();
    window.history.replaceState({}, "", "/search");
    await act(async () => {
      finish(
        json({
          data: {
            csrf_token: "csrf-late-login",
            expires_at: "2999-08-11T01:00:00.000Z",
          },
          meta: {},
        }),
      );
    });
    expect(window.location.pathname).toBe("/search");
  });

  it("sends the typed handle alongside the password", async () => {
    const entries = window.history.length;
    const fetcher = installLoginApi(() =>
      json({
        data: {
          expires_at: "2999-09-01T00:00:00Z",
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
    // The sign-in page leaves the history with the signing in: the return
    // path takes its entry rather than stacking on it.
    await waitFor(() => expect(window.location.pathname).toBe("/"));
    expect(window.history.length).toBe(entries);
  });

  it("sends no handle when the field is left empty", async () => {
    const fetcher = installLoginApi(() =>
      json({
        data: {
          expires_at: "2999-09-01T00:00:00Z",
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
      await findErrorToast("这个账户只能用第三方账号登录，请在下方选择。"),
    ).toBeInTheDocument();
    expect(screen.queryByText("密码不正确，请重试。")).toBeNull();
  });

  it("names the credential that would have worked when the server reports one", async () => {
    installLoginApi(credentialUnavailable(["token"]));
    renderLogin();
    await signIn("someone", "hunter2");

    expect(
      await findErrorToast("这个账户不能这样登录，可以改用API 令牌。"),
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

    expect(await findErrorToast("本站没有这个用户名。")).toBeInTheDocument();
    expect(screen.getByLabelText(/^(邮箱或)?用户名$/)).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(screen.getByLabelText("密码")).not.toHaveAttribute("aria-invalid");
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

    expect(await findErrorToast("密码不正确，请重试。")).toBeInTheDocument();
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
      if (url === "/api/v1/status") return ownerStatus();
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
    expect(screen.queryByLabelText("API 令牌")).toBeNull();

    fireEvent.click(
      await screen.findByRole("button", { name: "选择其他登录方式" }),
    );
    fireEvent.click(
      await screen.findByRole("button", { name: "使用API 令牌登录" }),
    );

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    expect(screen.queryByLabelText("密码")).toBeNull();
  });

  // An instance with no password set has nothing to fall back to, so the token
  // is the only way in and hiding it behind a disclosure would hide the door.
  it("makes the token the default when no password exists, and offers nothing else", async () => {
    installMethods(["token"]);
    renderLogin();

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "选择其他登录方式" }),
    ).toBeNull();
  });

  it("keeps the token path usable against an instance that will not say", async () => {
    installMethods("unreachable");
    renderLogin();

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
  });

  it("returns to password login when the server recovers", async () => {
    let available = false;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/account/session")
        return json(signedOutEnvelope(), 401);
      if (url === "/api/v1/login/methods") {
        return available
          ? json({ data: { methods: ["password", "token"] }, meta: {} })
          : json({}, 503);
      }
      if (url === "/api/v1/login/oauth/providers") return providersEnvelope([]);
      return json({}, 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderLogin();

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    available = true;
    act(() => window.dispatchEvent(new Event("focus")));
    expect(await screen.findByLabelText("密码")).toBeInTheDocument();
    expect(screen.queryByLabelText("API 令牌")).toBeNull();
  });

  it("automatically retries both sign-in methods and providers after an outage", async () => {
    let methodCalls = 0;
    let providerCalls = 0;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const url = String(input);
      if (url === "/api/v1/account/session")
        return json(signedOutEnvelope(), 401);
      if (url === "/api/v1/login/methods") {
        methodCalls += 1;
        return methodCalls === 1
          ? json({}, 503)
          : json({ data: { methods: ["password", "token"] }, meta: {} });
      }
      if (url === "/api/v1/login/oauth/providers") {
        providerCalls += 1;
        return providerCalls === 1
          ? json({}, 503)
          : providersEnvelope([{ name: "github", label: "GitHub" }]);
      }
      return json({}, 404);
    });
    vi.stubGlobal("fetch", fetcher);
    renderLogin();

    expect(await screen.findByLabelText("API 令牌")).toBeInTheDocument();
    expect(
      await screen.findByLabelText("密码", {}, { timeout: 4500 }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("button", { name: "使用 GitHub 登录" }),
    ).toBeInTheDocument();
    expect(methodCalls).toBeGreaterThanOrEqual(2);
    expect(providerCalls).toBeGreaterThanOrEqual(2);
  }, 5500);
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
