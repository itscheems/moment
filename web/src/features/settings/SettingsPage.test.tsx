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
        preferences: { ui_locale: null },
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

it("returns from a directly opened child to its category without leaving settings", async () => {
  window.history.replaceState({}, "", "/settings/security/tokens");
  installApi("member");
  render(<App />);
  await screen.findByRole("button", { name: "创建 API 令牌" });
  fireEvent.click(screen.getByRole("button", { name: "返回上一级" }));
  expect(window.location.pathname).toBe("/settings/security");
  await screen.findByRole("heading", { name: "安全和账号访问" });
  expect(await screen.findByRole("link", { name: /^登录方式/ })).toBeVisible();
});

it("searches permitted settings and opens a child using the keyboard", async () => {
  window.history.replaceState({}, "", "/settings");
  installApi("member");
  render(<App />);
  const input = await screen.findByRole("combobox", { name: "搜索设置" });
  act(() => input.focus());
  fireEvent.change(input, { target: { value: "备份" } });
  expect(await screen.findByText("没有找到匹配的设置")).toBeVisible();
  expect(screen.queryByRole("option")).toBeNull();
  fireEvent.change(input, { target: { value: "令牌" } });
  await screen.findByRole("option", { name: /API 令牌/ });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/security/tokens"),
  );
  expect(
    await screen.findByRole("button", { name: "创建 API 令牌" }),
  ).toBeEnabled();
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.getByRole("navigation", { name: "设置分类" })).toBeVisible();
  expect(screen.getByRole("tab", { name: "安全和账号访问" })).toHaveFocus();
  act(() => input.focus());
  fireEvent.change(input, { target: { value: "语言" } });
  const options = await screen.findAllByRole("option");
  expect(options[0]).toHaveTextContent(/^语言/);
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/display/language"),
  );
});

it("keeps the unsaved site title when saving comments on a separate child page", async () => {
  window.history.replaceState({}, "", "/settings/site/information");
  const requests: unknown[] = [];
  installApi("admin", (url, init) => {
    if (url === "/api/v1/site" && init?.method === "PATCH") {
      requests.push(JSON.parse(String(init.body)));
      return json({ ...site, comments_enabled: false });
    }
  });
  render(<App />);
  const title = await screen.findByLabelText("站点标题");
  fireEvent.change(title, { target: { value: "尚未保存的标题" } });
  fireEvent.click(screen.getByRole("button", { name: "返回上一级" }));
  fireEvent.click(await screen.findByRole("link", { name: /^评论/ }));
  fireEvent.click(screen.getByRole("checkbox", { name: "允许新评论" }));
  fireEvent.click(screen.getByRole("button", { name: "保存站点设置" }));
  await waitFor(() => expect(requests).toEqual([{ comments_enabled: false }]));
  expect(title).toHaveValue("尚未保存的标题");
});

it("does not let an invalid hidden site draft block the comments form", async () => {
  window.history.replaceState({}, "", "/settings/site/information");
  const requests: unknown[] = [];
  installApi("admin", (url, init) => {
    if (url === "/api/v1/site" && init?.method === "PATCH") {
      requests.push(JSON.parse(String(init.body)));
      return json({ ...site, comments_enabled: false });
    }
  });
  render(<App />);
  const title = await screen.findByLabelText("站点标题");
  fireEvent.change(title, { target: { value: "" } });
  expect(title).toBeInvalid();
  fireEvent.click(screen.getByRole("button", { name: "返回上一级" }));
  fireEvent.click(await screen.findByRole("link", { name: /^评论/ }));
  const checkbox = screen.getByRole("checkbox", { name: "允许新评论" });
  expect(checkbox.closest("form")?.checkValidity()).toBe(true);
  fireEvent.click(checkbox);
  fireEvent.click(screen.getByRole("button", { name: "保存站点设置" }));
  await waitFor(() => expect(requests).toEqual([{ comments_enabled: false }]));
  expect(title).toHaveValue("");
});

it("discards an inline language choice when canceled without changing the account", async () => {
  window.history.replaceState({}, "", "/settings/display/language");
  const calls = installApi("member");
  render(<App />);
  const english = await screen.findByRole("radio", { name: /English/ });
  fireEvent.click(english);
  fireEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(window.location.pathname).toBe("/settings/display");
  fireEvent.click(await screen.findByRole("link", { name: /^语言/ }));
  expect(screen.getByRole("radio", { name: /简体中文/ })).toBeChecked();
  expect(
    calls.mock.calls.some(
      ([url, init]) =>
        String(url) === "/api/v1/account/preferences" &&
        init?.method === "PATCH",
    ),
  ).toBe(false);
});

it("opens the personal categories for members without administrative settings", async () => {
  installApi("member");
  render(<App />);
  await screen.findByRole("heading", { name: "你的账号" });
  const nav = screen.getByRole("navigation", { name: "设置分类" });
  expect(
    within(nav)
      .getAllByRole("tab")
      .map((link) => link.getAttribute("href")),
  ).toEqual(["/settings/account", "/settings/security", "/settings/display"]);
  expect(screen.queryByLabelText("站点标题")).toBeNull();
  fireEvent.click(within(nav).getByRole("tab", { name: "安全和账号访问" }));
  fireEvent.click(await screen.findByRole("link", { name: /^API 令牌/ }));
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
it("navigates to a site child and saves only the fields on that page", async () => {
  const requests: unknown[] = [];
  installApi("admin", (url, init) => {
    if (url === "/api/v1/site" && init?.method === "PATCH") {
      requests.push(JSON.parse(String(init.body)));
      return json({ ...site, title: "新站点" });
    }
  });
  render(<App />);
  await screen.findByRole("heading", { name: "你的账号" });
  const nav = screen.getByRole("navigation", { name: "设置分类" });
  fireEvent.click(within(nav).getByRole("tab", { name: "站点设置" }));
  expect(window.location.pathname).toBe("/settings/site");
  fireEvent.click(await screen.findByRole("link", { name: /^基本信息/ }));
  expect(window.location.pathname).toBe("/settings/site/information");
  fireEvent.change(await screen.findByLabelText("站点标题"), {
    target: { value: "新站点" },
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存站点设置" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "保存站点设置" }));
  await screen.findByText("站点设置已保存。");
  expect(requests).toEqual([
    { title: "新站点", timezone: "UTC", public_base_url: null },
  ]);
  expect(within(nav).getByRole("tab", { name: "站点设置" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
});

it("preserves an unsaved token label across categories and child pages", async () => {
  window.history.replaceState({}, "", "/settings/security/tokens");
  installApi("admin");
  render(<App />);
  const field = await screen.findByLabelText("令牌名称");
  fireEvent.change(field, { target: { value: "尚未创建的令牌" } });
  const nav = screen.getByRole("navigation", { name: "设置分类" });
  fireEvent.click(within(nav).getByRole("tab", { name: "站点设置" }));
  expect(field).not.toBeVisible();
  expect(field).toHaveValue("尚未创建的令牌");
  fireEvent.click(within(nav).getByRole("tab", { name: "安全和账号访问" }));
  fireEvent.click(await screen.findByRole("link", { name: /^API 令牌/ }));
  expect(screen.getByLabelText("令牌名称")).toBe(field);
  expect(field).toBeVisible();
  expect(field).toHaveValue("尚未创建的令牌");
});

it("selects a display account independently of its member role", async () => {
  window.history.replaceState({}, "", "/settings/site/profile");
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
it("keeps password settings usable when the separate token list cannot be loaded", async () => {
  window.history.replaceState({}, "", "/settings/account/password");
  installApi("member", (url) =>
    url === "/api/v1/account/tokens" ? json({}, 503) : undefined,
  );
  render(<App />);
  expect(
    await screen.findByText(
      "修改密码会撤销全部 API 令牌和浏览器会话，需要重新登录并重新配置 API 客户端。",
    ),
  ).toBeVisible();
  expect(screen.queryByText("登录凭据暂时无法读取，可以重试。")).toBeNull();
  const nav = screen.getByRole("navigation", { name: "设置分类" });
  fireEvent.click(within(nav).getByRole("tab", { name: "安全和账号访问" }));
  fireEvent.click(await screen.findByRole("link", { name: /^API 令牌/ }));
  expect(
    await screen.findByText("登录凭据暂时无法读取，可以重试。"),
  ).toBeVisible();
});

it("creates and revokes an account token without persisting the secret", async () => {
  window.history.replaceState({}, "", "/settings/security/tokens");
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
  const tokenRow = screen.getByText("工作电脑").closest("li")!;
  fireEvent.click(within(tokenRow).getByRole("button", { name: "撤销" }));
  await within(tokenRow).findByText("已撤销");
  expect(
    calls.mock.calls.some(
      ([url, init]) =>
        String(url) === `/api/v1/account/tokens/${tokenId}` &&
        init?.method === "DELETE",
    ),
  ).toBe(true);
});
it("updates only this account password and requests a fresh sign-in", async () => {
  window.history.replaceState({}, "", "/settings/account/password");
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

it("sends expired account settings to the standalone login page with its full return URL", async () => {
  window.history.replaceState(
    {},
    "",
    "/settings/account?section=security#credentials",
  );
  installApi("admin", (url) => {
    if (url === "/api/v1/account/security")
      return new Response(
        JSON.stringify({
          error: {
            code: "UNAUTHORIZED",
            message: "expired",
            details: {},
            request_id: "expired",
          },
        }),
        { status: 401, headers: { "Content-Type": "application/json" } },
      );
    if (url === "/api/v1/login/methods")
      return json({ methods: ["password", "token"] });
  });
  render(<App />);
  await waitFor(() => expect(window.location.pathname).toBe("/login"));
  expect(new URLSearchParams(window.location.search).get("return")).toBe(
    "/settings/account?section=security#credentials",
  );
  expect(await screen.findByText("Happening now")).toBeInTheDocument();
  expect(screen.queryByRole("navigation", { name: "主要导航" })).toBeNull();
  // Public reading remains available after leaving the sign-in page.
  act(() => {
    window.history.pushState({}, "", "/");
    window.dispatchEvent(new PopStateEvent("popstate", { state: {} }));
  });
  await screen.findByRole("navigation", { name: "主要导航" });
  expect(window.location.pathname).toBe("/");
  // A prior redirect does not exempt later private-route entries.
  act(() => {
    window.history.pushState({}, "", "/compose/articles");
    window.dispatchEvent(new PopStateEvent("popstate", { state: {} }));
  });
  await waitFor(() => expect(window.location.pathname).toBe("/login"));
  expect(new URLSearchParams(window.location.search).get("return")).toBe(
    "/compose/articles",
  );
});

it("lets a member preview and append personal data through account endpoints", async () => {
  window.history.replaceState({}, "", "/settings/account/import");
  const empty = {
    entries: 0,
    comments: 0,
    media: 0,
    users: 0,
    identities: 0,
    articles: 0,
    article_drafts: 0,
  };
  const hash = "b".repeat(64),
    file = new File(["personal archive"], "personal.zip");
  let restored = false;
  const calls = installApi("member", (url, init) => {
    if (!url.startsWith("/api/v1/account/import/")) return;
    expect(init?.body).toBe(file);
    const commit = url.endsWith("/restore");
    if (commit) {
      expect(new Headers(init?.headers).get("X-Moment-Archive-SHA256")).toBe(
        hash,
      );
      restored = true;
    }
    return json({
      dry_run: !commit,
      archive_sha256: hash,
      account_id: accountId,
      counts: { ...empty, entries: 2, comments: 3 },
      added: commit ? { ...empty, entries: 2, comments: 3 } : empty,
      skipped: empty,
    });
  });
  render(<App />);
  const personal = await screen.findByRole("region", { name: "个人数据" });
  fireEvent.change(within(personal).getByLabelText("个人导出文件"), {
    target: { files: [file] },
  });
  fireEvent.click(within(personal).getByRole("button", { name: "检查文件" }));
  expect(
    await screen.findByText("当前站点已有的内容不会被删除或覆盖。"),
  ).toBeVisible();
  expect(restored).toBe(false);
  fireEvent.click(
    within(personal).getByRole("button", { name: "确认添加到当前账号" }),
  );
  expect(await screen.findByText(/已添加 2 条内容、3 条评论/)).toBeVisible();
  expect(restored).toBe(true);
  await waitFor(() =>
    expect(
      within(personal).getByRole("button", { name: "检查文件" }),
    ).toHaveFocus(),
  );
  expect(
    calls.mock.calls.some(([url]) =>
      String(url).startsWith("/api/v1/backups/import"),
    ),
  ).toBe(false);
});

it("keeps personal import feedback on the import child page", async () => {
  window.history.replaceState({}, "", "/settings/account/import");
  installApi("member", (url) =>
    url.startsWith("/api/v1/account/import/")
      ? new Response(
          JSON.stringify({
            error: {
              code: "IMPORT_INVALID",
              message: "Invalid archive",
              details: {},
              request_id: "test",
            },
          }),
          { status: 400, headers: { "Content-Type": "application/json" } },
        )
      : undefined,
  );
  render(<App />);
  fireEvent.change(await screen.findByLabelText("个人导出文件"), {
    target: { files: [new File(["invalid archive"], "archive.zip")] },
  });
  fireEvent.click(screen.getByRole("button", { name: "检查文件" }));
  const failure = "文件不完整或校验失败。请重新导出并选择原始 ZIP 文件。";
  expect(await screen.findByText(failure)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "返回上一级" }));
  fireEvent.click(await screen.findByRole("link", { name: /^导出个人数据/ }));
  expect(screen.queryByText(failure)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "返回上一级" }));
  fireEvent.click(await screen.findByRole("link", { name: /^导入个人数据/ }));
  expect(screen.getByText(failure)).toBeVisible();
});
