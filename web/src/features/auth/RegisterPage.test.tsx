import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import { makeAccountLimits } from "../../test/accountFixtures";
import { RegisterPage } from "./RegisterPage";
import { SessionProvider } from "./SessionProvider";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function registrationOptions(mode: "open" | "approval" | "closed") {
  return json({
    data: {
      registration_mode: mode,
      username_min_length: 3,
      username_max_length: 30,
      password_min_bytes: 8,
      password_max_bytes: 72,
    },
    meta: {},
  });
}

function accountSession(status: "active" | "pending") {
  return json({
    data: {
      csrf_token: "csrf-registered",
      expires_at: "2999-08-11T01:00:00.000Z",
      account: {
        user_id: "1".repeat(32),
        username: "newmember",
        display_name: "newmember",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "member",
        status,
        username_chosen: true,
        profile_url: "/newmember",
      },
      limits: makeAccountLimits(),
    },
    meta: {},
  });
}

function installApi(
  mode: "open" | "approval" | "closed",
  failure?:
    | "session-restore"
    | "server-session"
    | "closed-after-load"
    | "username-taken",
) {
  let registered = false;
  let restoreFails = false;
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input);
      if (path === "/api/v1/register/options") return registrationOptions(mode);
      if (path === "/api/v1/register") {
        if (init?.method !== "POST") return json({}, 405);
        if (failure === "closed-after-load") {
          return json(
            {
              error: {
                code: "REGISTRATION_CLOSED",
                message: "registration closed",
              },
            },
            403,
          );
        }
        if (failure === "username-taken") {
          return json(
            { error: { code: "USERNAME_TAKEN", message: "username taken" } },
            409,
          );
        }
        registered = true;
        if (failure === "server-session") {
          return json(
            {
              error: {
                code: "REGISTRATION_SESSION_UNAVAILABLE",
                message: "account created, but sign-in is unavailable",
                details: {
                  account_created: true,
                  username: "newmember",
                  recovery: "login",
                },
              },
            },
            503,
          );
        }
        if (failure === "session-restore") restoreFails = true;
        return json(
          {
            data: {
              account_created: true,
              username: "newmember",
              account_status: mode === "open" ? "active" : "pending",
              expires_at: "2999-08-11T01:00:00.000Z",
              csrf_token: "csrf-registered",
            },
            meta: {},
          },
          201,
        );
      }
      if (path === "/api/v1/account/session") {
        if (restoreFails)
          return json({ error: { code: "AUTH_UNAVAILABLE" } }, 503);
        return registered
          ? accountSession(mode === "open" ? "active" : "pending")
          : json({ error: { code: "UNAUTHORIZED" } }, 401);
      }
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return { fetcher };
}

function renderRegister() {
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <SessionProvider>
          <RegisterPage />
        </SessionProvider>
      </NavigationProvider>
    </FeedbackProvider>,
  );
}

async function fillRegistration(password = "password123", confirm = password) {
  fireEvent.change(await screen.findByLabelText("用户名"), {
    target: { value: "NewMember" },
  });
  fireEvent.change(screen.getByLabelText("密码", { exact: true }), {
    target: { value: password },
  });
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: confirm },
  });
}

describe("account registration page", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/register?return=%2Farchive");
    window.sessionStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("keeps login navigation and explains approval before submission", async () => {
    installApi("approval");
    renderRegister();
    expect(await screen.findByLabelText("用户名")).toBeInTheDocument();
    expect(
      screen.getByRole("heading", { name: "Join Moment" }),
    ).toBeInTheDocument();
    expect(screen.getByText(/注册后需要管理员批准/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "立即登录" })).toHaveAttribute(
      "href",
      "/login?return=%2Farchive",
    );
  });

  it("uses UTF-8 byte limits and confirmation feedback before sending", async () => {
    const { fetcher } = installApi("approval");
    renderRegister();
    await fillRegistration("ééé", "ééé");
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    await waitFor(() =>
      expect(document.querySelector('[role="alertdialog"]')).toHaveTextContent(
        "8–72 字节",
      ),
    );
    expect(screen.getByLabelText("密码", { exact: true })).toHaveAttribute(
      "aria-invalid",
      "true",
    );
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/v1/register"),
    ).toHaveLength(0);

    fireEvent.change(screen.getByLabelText("密码", { exact: true }), {
      target: { value: "password123" },
    });
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    await waitFor(() =>
      expect(document.querySelector('[role="alertdialog"]')).toHaveTextContent(
        "两次输入的密码不一致",
      ),
    );
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/v1/register"),
    ).toHaveLength(0);
  });

  it("creates a pending account, adopts its session, and shows approval status", async () => {
    const { fetcher } = installApi("approval");
    renderRegister();
    await fillRegistration();
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "账号已创建，正在等待管理员批准",
    );
    expect(screen.queryByRole("link", { name: "立即登录" })).toBeNull();
    expect(window.location.pathname).toBe("/register");
    expect(
      JSON.parse(
        window.sessionStorage.getItem("moment.auth.session.v2") ?? "{}",
      ),
    ).toMatchObject({ csrf_token: "csrf-registered" });
    const call = fetcher.mock.calls.find(
      ([path]) => path === "/api/v1/register",
    );
    expect(JSON.parse(String(call?.[1]?.body))).toEqual({
      username: "newmember",
      password: "password123",
    });
  });

  it("redirects an active account to the safe return path", async () => {
    installApi("open");
    renderRegister();
    await fillRegistration();
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    await waitFor(() => expect(window.location.pathname).toBe("/archive"));
  });

  it("does not offer registration when the site is closed", async () => {
    const { fetcher } = installApi("closed");
    renderRegister();
    expect(await screen.findByRole("status")).toHaveTextContent(
      "本站当前不接受新注册。",
    );
    expect(screen.queryByRole("button", { name: "注册" })).toBeNull();
    expect(
      fetcher.mock.calls.filter(([path]) => path === "/api/v1/register"),
    ).toHaveLength(0);
  });

  it("handles a policy change after the form opened", async () => {
    installApi("approval", "closed-after-load");
    renderRegister();
    await fillRegistration();
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    expect(await screen.findByRole("status")).toHaveTextContent(
      "本站当前不接受新注册。",
    );
    expect(screen.queryByRole("button", { name: "注册" })).toBeNull();
  });

  it("keeps the entered values and focuses a username rejected by the server", async () => {
    installApi("approval", "username-taken");
    renderRegister();
    await fillRegistration();
    fireEvent.click(screen.getByRole("button", { name: "注册" }));
    await waitFor(() =>
      expect(screen.getByLabelText("用户名")).toHaveAttribute(
        "aria-invalid",
        "true",
      ),
    );
    expect(screen.getByLabelText("用户名")).toHaveFocus();
    expect(screen.getByLabelText("密码", { exact: true })).toHaveValue(
      "password123",
    );
  });

  it("drops an external return address from the login link", async () => {
    window.history.replaceState(
      {},
      "",
      "/register?return=https%3A%2F%2Fexample.org",
    );
    installApi("approval");
    renderRegister();
    expect(
      await screen.findByRole("link", { name: "立即登录" }),
    ).toHaveAttribute("href", "/login?return=%2F");
  });

  it.each(["session-restore", "server-session"] as const)(
    "keeps account-created recovery visible after %s fails",
    async (failure) => {
      const { fetcher } = installApi("approval", failure);
      renderRegister();
      await fillRegistration();
      fireEvent.click(screen.getByRole("button", { name: "注册" }));
      expect(await screen.findByRole("status")).toHaveTextContent(
        "账号 newmember 已创建，暂时无法确认登录状态",
      );
      expect(screen.getByRole("link", { name: "立即登录" })).toHaveAttribute(
        "href",
        "/login?return=%2Farchive",
      );
      expect(
        fetcher.mock.calls.filter(([path]) => path === "/api/v1/register"),
      ).toHaveLength(1);
      expect(screen.queryByRole("button", { name: "注册" })).toBeNull();
    },
  );
});
