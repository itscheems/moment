import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { App } from "../../app/App";
import { makeAccountLimits } from "../../test/accountFixtures";

const sessionKey = "moment.auth.session.v2";
const accountId = "1".repeat(32);
const otherId = "2".repeat(32);
const tokenId = "3".repeat(32);
const account = {
  user_id: accountId,
  username: "writer",
  display_name: "写作者",
  bio: "自己的简介",
  avatar_media_id: null,
  cover_media_id: null,
  role: "admin",
  status: "active",
  username_chosen: true,
  profile_url: "/writer",
};
const other = {
  ...account,
  user_id: otherId,
  username: "reader",
  display_name: "另一位作者",
  role: "member",
  profile_url: "/reader",
};
const site = {
  profile_user_id: accountId,
  username: "writer",
  display_name: "写作者",
  title: "Moment",
  bio: "自己的简介",
  timezone: "UTC",
  public_base_url: null,
  avatar_media_id: null,
  cover_media_id: null,
  pinned_post_id: null,
  comments_enabled: true,
};
const token = {
  id: tokenId,
  user_id: accountId,
  label: "工作电脑",
  created_at_ms: 1785801600000,
  revoked_at_ms: null,
};
function json(data: unknown, status = 200) {
  return new Response(JSON.stringify({ data, meta: { request_id: "test" } }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function installApi(
  role = "admin",
  override?: (url: string, init?: RequestInit) => Response | undefined,
) {
  const calls = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input),
      custom = override?.(url, init);
    if (custom) return custom;
    if (url === "/api/v1/site")
      return new Response(
        JSON.stringify({
          data: site,
          meta: { request_id: "site", instance_id: "a".repeat(32) },
        }),
        { headers: { "Content-Type": "application/json" } },
      );
    if (url === "/api/v1/account/session")
      return json({
        account: { ...account, role },
        csrf_token: "csrf",
        expires_at: "2999-08-01T00:00:00Z",
        limits: makeAccountLimits(),
      });
    if (url === "/api/v1/account/security")
      return json({ methods: ["password"], password_enabled: true });
    if (url === "/api/v1/account/tokens") return json([token]);
    if (url === "/api/v1/account/identities") return json([]);
    if (url === "/api/v1/account/sessions") return json([]);
    if (url === "/api/v1/login/oauth/providers") return json({ providers: [] });
    if (url === "/api/v1/accounts")
      return new Response(
        JSON.stringify({
          data: [account, other],
          meta: {
            registration_mode: "approval",
            count: 2,
            request_id: "accounts",
          },
        }),
        { headers: { "Content-Type": "application/json" } },
      );
    if (url.startsWith("/api/v1/posts?")) return json({ items: [] });
    return json({}, 404);
  });
  vi.stubGlobal("fetch", calls);
  return calls;
}
beforeEach(() => {
  window.history.replaceState({}, "", "/settings/account");
  window.sessionStorage.clear();
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({ csrf_token: "csrf", expires_at: "2999-08-01T00:00:00Z" }),
  );
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

it("opens account settings for a member without exposing administrative sections", async () => {
  installApi("member");
  render(<App />);
  expect(
    await screen.findByRole("heading", { name: "个人资料" }),
  ).toBeVisible();
  const nav = screen.getByRole("navigation", { name: "设置分类" });
  expect(
    within(nav)
      .getAllByRole("link")
      .map((link) => link.getAttribute("href")),
  ).toEqual(["/settings/account"]);
  expect(
    await screen.findByRole("button", { name: "创建 API 令牌" }),
  ).toBeEnabled();
});
it("refuses an administrative settings address for a member", async () => {
  window.history.replaceState({}, "", "/settings/site");
  installApi("member");
  render(<App />);
  expect(await screen.findByText("此设置需要管理员权限。")).toBeVisible();
  expect(screen.queryByLabelText("站点标题")).toBeNull();
});
it("uses distinct settings addresses and sends only site fields when saving", async () => {
  const requests: unknown[] = [];
  installApi("admin", (url, init) => {
    if (url === "/api/v1/site" && init?.method === "PATCH") {
      requests.push(JSON.parse(String(init.body)));
      return json({ ...site, title: "新站点" });
    }
  });
  render(<App />);
  await screen.findByRole("heading", { name: "个人资料" });
  fireEvent.click(
    within(screen.getByRole("navigation", { name: "设置分类" })).getByRole(
      "link",
      { name: "站点" },
    ),
  );
  expect(window.location.pathname).toBe("/settings/site");
  fireEvent.change(await screen.findByLabelText("站点标题"), {
    target: { value: "新站点" },
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存站点设置" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "保存站点设置" }));
  await screen.findByText("站点设置已保存。");
  expect(requests).toEqual([
    {
      title: "新站点",
      timezone: "UTC",
      public_base_url: null,
      comments_enabled: true,
    },
  ]);
  expect(screen.getByRole("heading", { name: "个人资料" })).toBeInTheDocument();
});
it("keeps account fields mounted when moving between settings sections", async () => {
  installApi("admin");
  render(<App />);
  const field = await screen.findByLabelText("令牌名称");
  fireEvent.change(field, { target: { value: "尚未创建的令牌" } });
  const nav = screen.getByRole("navigation", { name: "设置分类" });
  fireEvent.click(within(nav).getByRole("link", { name: "站点" }));
  expect(window.location.pathname).toBe("/settings/site");
  expect(screen.getByLabelText("令牌名称")).toBe(field);
  expect(field).toHaveValue("尚未创建的令牌");
  expect(
    screen.getByRole("heading", { name: "备份与恢复" }),
  ).toBeInTheDocument();
  fireEvent.click(within(nav).getByRole("link", { name: "账号" }));
  expect(window.location.pathname).toBe("/settings/account");
  expect(field).toHaveValue("尚未创建的令牌");
});
it("selects a display account independently of its member role", async () => {
  window.history.replaceState({}, "", "/settings/site");
  const requests: unknown[] = [];
  installApi("admin", (url, init) => {
    if (url === "/api/v1/site/profile") {
      requests.push(JSON.parse(String(init?.body)));
      return json({
        ...site,
        profile_user_id: otherId,
        username: other.username,
        display_name: other.display_name,
      });
    }
  });
  render(<App />);
  await screen.findByRole("option", { name: "另一位作者 @reader" });
  fireEvent.change(screen.getByLabelText("展示账号"), {
    target: { value: otherId },
  });
  fireEvent.click(screen.getByRole("button", { name: "更新展示账号" }));
  await waitFor(() => expect(requests).toEqual([{ user_id: otherId }]));
  expect(
    await screen.findByText("首页展示账号已更新，账号权限保持不变。"),
  ).toBeVisible();
});
it("creates and revokes an account token without persisting the secret", async () => {
  const secret = "account-token-visible-once";
  const calls = installApi("member", (url, init) => {
    if (url === "/api/v1/account/tokens" && init?.method === "POST")
      return json(
        {
          credential: { ...token, id: "4".repeat(32), label: "新电脑" },
          token: secret,
        },
        201,
      );
    if (
      url === `/api/v1/account/tokens/${tokenId}` &&
      init?.method === "DELETE"
    )
      return json({ revoked: true, reauthentication_required: false });
  });
  render(<App />);
  fireEvent.change(await screen.findByLabelText("令牌名称"), {
    target: { value: "新电脑" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建 API 令牌" }));
  expect(await screen.findByLabelText("新 API 令牌")).toHaveValue(secret);
  expect(window.sessionStorage.getItem(sessionKey)).not.toContain(secret);
  expect(
    Array.from({ length: window.localStorage.length }, (_, i) =>
      window.localStorage.getItem(window.localStorage.key(i)!),
    ).join(),
  ).not.toContain(secret);
  fireEvent.click(screen.getByRole("button", { name: "已保存，隐藏令牌" }));
  expect(screen.queryByLabelText("新 API 令牌")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "撤销 工作电脑" }));
  await screen.findByText("· 已撤销");
  expect(
    calls.mock.calls.some(
      ([url, init]) =>
        String(url) === `/api/v1/account/tokens/${tokenId}` &&
        init?.method === "DELETE",
    ),
  ).toBe(true);
});
it("updates only this account password and requests a fresh sign-in", async () => {
  const requests: unknown[] = [];
  installApi("member", (url, init) => {
    if (url === "/api/v1/account/password") {
      requests.push(JSON.parse(String(init?.body)));
      return json({ password_enabled: true, reauthentication_required: true });
    }
    if (url === "/api/v1/login/methods")
      return json({ methods: ["password", "token"] });
  });
  render(<App />);
  fireEvent.change(await screen.findByLabelText("新密码"), {
    target: { value: "a-new-account-password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存密码并重新登录" }));
  await waitFor(() => expect(window.location.pathname).toBe("/login"));
  expect(requests).toEqual([{ password: "a-new-account-password" }]);
  expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
});
