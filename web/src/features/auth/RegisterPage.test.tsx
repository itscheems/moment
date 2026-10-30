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

vi.mock("motion/react", async () => {
  const actual =
    await vi.importActual<typeof import("motion/react")>("motion/react");
  return { ...actual, useReducedMotion: () => true };
});
function json(data: unknown, status = 200) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function installApi(
  mode: "open" | "approval" | "closed" = "open",
  failure?: string,
) {
  let created = false;
  const calls: { path: string; body: Record<string, string> }[] = [];
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input),
        body = JSON.parse(String(init?.body || "{}")) as Record<string, string>;
      calls.push({ path, body });
      if (path === "/api/v1/register/options")
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
      if (path === "/api/v1/register/check")
        return failure === "username"
          ? json({ error: { code: "USERNAME_TAKEN" } }, 409)
          : json({ data: { username: body.username }, meta: {} });
      if (path === "/api/v1/register/email")
        return failure === "send"
          ? json({ error: { code: "EMAIL_UNAVAILABLE" } }, 503)
          : json({
              data: {
                challenge_id: "challenge",
                expires_at: "2999-01-01T00:00:00Z",
                resend_at: "2999-01-01T00:00:00Z",
              },
              meta: {},
            });
      if (path === "/api/v1/register/email/verify")
        return body.code === "000000"
          ? json({ error: { code: "VERIFICATION_INVALID" } }, 400)
          : json({
              data: {
                verification_token: "private-proof",
                email: "reader@example.test",
                expires_at: "2999-01-01T00:00:00Z",
              },
              meta: {},
            });
      if (path === "/api/v1/register") {
        if (failure === "finish")
          return json({ error: { code: "EMAIL_UNAVAILABLE" } }, 503);
        created = true;
        if (failure === "server-session")
          return json(
            {
              error: {
                code: "REGISTRATION_SESSION_UNAVAILABLE",
                details: { account_created: true },
              },
            },
            503,
          );
        return json(
          {
            data: {
              account_created: true,
              username: "newmember",
              account_status: mode === "approval" ? "pending" : "active",
              expires_at: "2999-01-01T00:00:00Z",
              csrf_token: "registered-csrf",
            },
            meta: {},
          },
          201,
        );
      }
      if (path === "/api/v1/account/session") {
        if (!created) return json({ error: { code: "UNAUTHORIZED" } }, 401);
        if (failure === "session-restore")
          return json({ error: { code: "AUTH_UNAVAILABLE" } }, 503);
        return json({
          data: {
            csrf_token: "registered-csrf",
            expires_at: "2999-01-01T00:00:00Z",
            account: {
              user_id: "1".repeat(32),
              username: "newmember",
              display_name: "newmember",
              bio: "",
              avatar_media_id: null,
              cover_media_id: null,
              role: "member",
              status: mode === "approval" ? "pending" : "active",
              username_chosen: true,
              profile_url: "/newmember",
            },
            limits: makeAccountLimits(),
            preferences: { ui_locale: null },
          },
          meta: {},
        });
      }
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return calls;
}
function renderRegister() {
  render(
    <FeedbackProvider>
      <NavigationProvider>
        <SessionProvider>
          <RegisterPage />
        </SessionProvider>
      </NavigationProvider>
    </FeedbackProvider>,
  );
}
async function fillAccount(password = "password123", confirmation = password) {
  fireEvent.change(await screen.findByLabelText("用户名"), {
    target: { value: "NewMember" },
  });
  fireEvent.change(screen.getByLabelText("密码", { exact: true }), {
    target: { value: password },
  });
  fireEvent.change(screen.getByLabelText("确认密码"), {
    target: { value: confirmation },
  });
}
async function nextToEmail() {
  await fillAccount();
  fireEvent.click(screen.getByRole("button", { name: "下一步" }));
  await screen.findByLabelText("邮箱地址");
}
async function sendCode() {
  fireEvent.change(screen.getByLabelText("邮箱地址"), {
    target: { value: "reader@example.test" },
  });
  fireEvent.click(screen.getByRole("button", { name: "获取验证码" }));
  await screen.findByText(/验证码已发送/);
}
async function verifyCode(code = "123456") {
  fireEvent.change(screen.getByLabelText("验证码"), {
    target: { value: code },
  });
  fireEvent.click(screen.getByRole("button", { name: "验证邮箱" }));
}
async function nextToName() {
  await nextToEmail();
  await sendCode();
  await verifyCode();
  await screen.findByPlaceholderText("输入名字（选填）");
}

describe("three-step email registration", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/register?return=%2Farchive");
    window.sessionStorage.clear();
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it("shows ordered progress and preserves the return destination", async () => {
    installApi();
    renderRegister();
    await screen.findByLabelText("用户名");
    expect(
      screen.getByRole("progressbar", { name: "注册进度" }),
    ).toBeInTheDocument();
    expect(screen.getByText("第 1 步，共 3 步")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "立即登录" })).toHaveAttribute(
      "href",
      "/login?return=%2Farchive",
    );
  });
  it("checks UTF-8 password length and confirmation before making requests", async () => {
    const calls = installApi();
    renderRegister();
    await fillAccount("ééé");
    fireEvent.click(screen.getByRole("button", { name: "下一步" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("8–72 字节");
    fireEvent.change(screen.getByLabelText("密码", { exact: true }), {
      target: { value: "password123" },
    });
    fireEvent.click(screen.getByRole("button", { name: "下一步" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "两次输入的密码不一致",
    );
    expect(calls.filter((call) => call.path.endsWith("/check"))).toHaveLength(
      0,
    );
  });
  it("keeps account fields and email draft when going back and forward", async () => {
    installApi();
    renderRegister();
    await nextToEmail();
    fireEvent.change(screen.getByLabelText("邮箱地址"), {
      target: { value: "reader@example.test" },
    });
    fireEvent.click(screen.getByRole("button", { name: "返回上一步" }));
    await screen.findByLabelText("用户名");
    expect(screen.getByLabelText("密码", { exact: true })).toHaveValue(
      "password123",
    );
    expect(screen.getByLabelText("确认密码")).toHaveValue("password123");
    fireEvent.click(screen.getByRole("button", { name: "下一步" }));
    expect(await screen.findByLabelText("邮箱地址")).toHaveValue(
      "reader@example.test",
    );
  });
  it("keeps the email and code after rejection and prevents resend during cooldown", async () => {
    const calls = installApi();
    renderRegister();
    await nextToEmail();
    await sendCode();
    await verifyCode("000000");
    expect(await screen.findByRole("alert")).toHaveTextContent("验证码不正确");
    expect(screen.getByLabelText("验证码")).toHaveValue("000000");
    expect(screen.getByLabelText("邮箱地址")).toHaveValue(
      "reader@example.test",
    );
    expect(calls.filter((call) => call.path.endsWith("/email"))).toHaveLength(
      1,
    );
  });
  it("completes only after verification and accepts an optional name", async () => {
    const calls = installApi();
    renderRegister();
    await nextToName();
    fireEvent.change(screen.getByPlaceholderText("输入名字（选填）"), {
      target: { value: "Forest" },
    });
    const button = screen.getByRole("button", { name: "完成注册" });
    fireEvent.click(button);
    fireEvent.click(button);
    await screen.findByRole("heading", { name: "注册完成" });
    expect(calls.filter((call) => call.path === "/api/v1/register")).toEqual([
      {
        path: "/api/v1/register",
        body: {
          username: "newmember",
          password: "password123",
          verification_token: "private-proof",
          display_name: "Forest",
        },
      },
    ]);
    expect(window.location.pathname).toBe("/register");
    fireEvent.click(screen.getByRole("button", { name: "继续浏览" }));
    await waitFor(() => expect(window.location.pathname).toBe("/archive"));
  });
  it("retains verification and name across a backward visit", async () => {
    installApi();
    renderRegister();
    await nextToName();
    fireEvent.change(screen.getByPlaceholderText("输入名字（选填）"), {
      target: { value: "Forest" },
    });
    fireEvent.click(screen.getByRole("button", { name: "返回上一步" }));
    await screen.findByText("邮箱验证成功");
    fireEvent.click(screen.getByRole("button", { name: "下一步" }));
    await screen.findByPlaceholderText("输入名字（选填）");
    expect(screen.getByPlaceholderText("输入名字（选填）")).toHaveValue(
      "Forest",
    );
  });
  it("keeps draft inputs after a delivery failure", async () => {
    installApi("open", "send");
    renderRegister();
    await nextToEmail();
    fireEvent.change(screen.getByLabelText("邮箱地址"), {
      target: { value: "reader@example.test" },
    });
    fireEvent.click(screen.getByRole("button", { name: "获取验证码" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("暂时无法发送");
    expect(screen.getByLabelText("邮箱地址")).toHaveValue(
      "reader@example.test",
    );
    expect(screen.getByRole("button", { name: "获取验证码" })).toBeEnabled();
  });
  it("explains approval and permits leaving the name blank", async () => {
    const calls = installApi("approval");
    renderRegister();
    await nextToName();
    expect(screen.getByText(/注册后需要管理员批准/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "完成注册" }));
    await screen.findByRole("heading", { name: "注册完成" });
    expect(screen.getByText(/正在等待管理员批准/)).toBeInTheDocument();
    expect(
      calls.find((call) => call.path === "/api/v1/register")?.body.display_name,
    ).toBe("");
  });
  it.each(["server-session", "session-restore"])(
    "offers sign-in recovery after account creation: %s",
    async (failure) => {
      installApi("open", failure);
      renderRegister();
      await nextToName();
      fireEvent.click(screen.getByRole("button", { name: "完成注册" }));
      expect(
        await screen.findByRole("button", { name: "前往登录" }),
      ).toBeInTheDocument();
      expect(
        screen.getByText(/账号已创建，暂时无法确认登录状态/),
      ).toBeInTheDocument();
    },
  );
  it("does not offer a form when registration is closed", async () => {
    installApi("closed");
    renderRegister();
    await screen.findByText("本站当前不接受新注册。");
    expect(screen.queryByLabelText("用户名")).not.toBeInTheDocument();
  });
});
