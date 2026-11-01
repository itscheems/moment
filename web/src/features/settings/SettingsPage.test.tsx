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
import i18n from "../../i18n";
import { makeAccountLimits } from "../../test/accountFixtures";

const sessionKey = "moment.auth.session.v2";
const accountId = "1".repeat(32);
const otherId = "2".repeat(32);
const tokenId = "3".repeat(32);
const account = {
  user_id: accountId,
  username: "writer",
  display_name: "Writer",
  bio: "Personal biography",
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
  display_name: "Other writer",
  role: "member",
  profile_url: "/reader",
};
const site = {
  profile_user_id: accountId,
  username: "writer",
  display_name: "Writer",
  title: "Moment",
  bio: "Personal biography",
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
  label: "Work computer",
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
beforeEach(async () => {
  window.localStorage.setItem("moment.ui-locale", "en");
  await i18n.changeLanguage("en");
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
  await screen.findByRole("button", { name: "Create API token" });
  fireEvent.click(screen.getByRole("button", { name: "Go back" }));
  expect(window.location.pathname).toBe("/settings/security");
  await screen.findByRole("heading", { name: "Security and account access" });
  expect(
    await screen.findByRole("link", { name: /^Login methods/ }),
  ).toBeVisible();
});

it("searches permitted settings and opens a child using the keyboard", async () => {
  window.history.replaceState({}, "", "/settings");
  installApi("member");
  render(<App />);
  const input = await screen.findByRole("combobox", {
    name: "Search Settings",
  });
  act(() => input.focus());
  fireEvent.change(input, { target: { value: "Backups" } });
  expect(await screen.findByText("No matching settings found")).toBeVisible();
  expect(screen.queryByRole("option")).toBeNull();
  fireEvent.change(input, { target: { value: "token" } });
  await screen.findByRole("option", { name: /API token/ });
  fireEvent.keyDown(input, { key: "ArrowDown" });
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/security/tokens"),
  );
  expect(
    await screen.findByRole("button", { name: "Create API token" }),
  ).toBeEnabled();
  fireEvent.keyDown(input, { key: "Escape" });
  expect(
    screen.getByRole("navigation", { name: "Settings categories" }),
  ).toBeVisible();
  expect(
    screen.getByRole("tab", { name: "Security and account access" }),
  ).toHaveFocus();
  act(() => input.focus());
  fireEvent.change(input, { target: { value: "Language" } });
  const options = await screen.findAllByRole("option");
  expect(options[0]).toHaveTextContent(/^Language/);
  fireEvent.keyDown(input, { key: "Enter" });
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/display/language"),
  );
});

it("keeps deployment configuration out of site navigation and settings search", async () => {
  window.history.replaceState({}, "", "/settings/site");
  installApi("admin");
  render(<App />);
  expect(await screen.findByRole("link", { name: /^Comments/ })).toBeVisible();
  expect(screen.queryByRole("link", { name: /^Site information/ })).toBeNull();
  expect(
    screen.queryByRole("link", { name: /^Homepage display account/ }),
  ).toBeNull();
  expect(screen.queryByLabelText("Site title")).toBeNull();
  expect(screen.queryByLabelText("Timezone")).toBeNull();
  expect(screen.queryByLabelText("Public URL")).toBeNull();
  expect(screen.queryByLabelText("Display account")).toBeNull();
  const input = screen.getByRole("combobox", { name: "Search Settings" });
  act(() => input.focus());
  for (const query of [
    "Site information",
    "Homepage display account",
    "Public URL",
  ]) {
    fireEvent.change(input, { target: { value: query } });
    if (query === "Site information") {
      expect(
        await screen.findByRole("option", { name: /^Download a site backup/ }),
      ).toBeVisible();
      expect(
        screen.queryByRole("option", { name: /^Site information/ }),
      ).toBeNull();
    } else {
      expect(
        await screen.findByText("No matching settings found"),
      ).toBeVisible();
      expect(screen.queryByRole("option")).toBeNull();
    }
  }
});

it("reports a failed comments save without referring to removed fields", async () => {
  window.history.replaceState({}, "", "/settings/site/comments");
  installApi("admin", (url, init) =>
    url === "/api/v1/site" && init?.method === "PATCH"
      ? json({}, 503)
      : undefined,
  );
  window.localStorage.setItem("moment.ui-locale", "en");
  await act(() => i18n.changeLanguage("en"));
  render(<App />);
  fireEvent.click(
    await screen.findByRole("checkbox", { name: "Allow new comments" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Comment settings were not saved. Try again later.",
  );
  expect(screen.getByRole("button", { name: "Save" })).toBeEnabled();
  const checkbox = screen.getByRole("checkbox", { name: "Allow new comments" });
  const alert = screen.getByRole("alert");
  await act(() => i18n.changeLanguage("zh-CN"));
  expect(alert).not.toHaveTextContent(
    "Comment settings were not saved. Try again later.",
  );
  expect(screen.getByRole("checkbox")).toBe(checkbox);
  expect(checkbox).not.toBeChecked();
});

it("discards a modal language choice when canceled without changing the account", async () => {
  window.history.replaceState({}, "", "/settings/display/language");
  const calls = installApi("member");
  render(<App />);
  const english = await screen.findByRole("radio", {
    name: /Simplified Chinese/,
  });
  fireEvent.click(english);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/display"),
  );
  fireEvent.click(await screen.findByRole("link", { name: /^Language/ }));
  expect(screen.getByRole("radio", { name: /English/ })).toBeChecked();
  expect(
    calls.mock.calls.some(
      ([url, init]) =>
        String(url) === "/api/v1/account/preferences" &&
        init?.method === "PATCH",
    ),
  ).toBe(false);
});

it("opens display and profile rows directly as dialogs over their category", async () => {
  window.history.replaceState({}, "", "/settings/display");
  installApi("member");
  render(<App />);
  fireEvent.click(await screen.findByRole("link", { name: /^Display/ }));
  const appearance = await screen.findByRole("dialog", {
    name: "Customize your display",
  });
  await waitFor(() => expect(appearance).toBeVisible());
  fireEvent.click(within(appearance).getByRole("button", { name: "Done" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/display"),
  );
  fireEvent.click(
    within(
      screen.getByRole("navigation", { name: "Settings categories" }),
    ).getByRole("tab", { name: "Your account" }),
  );
  fireEvent.click(
    await screen.findByRole("link", { name: /^Profile.*Edit your name/ }),
  );
  await waitFor(() =>
    expect(screen.getByRole("dialog", { name: "Edit profile" })).toBeVisible(),
  );
  expect(window.location.pathname).toBe("/settings/profile");
  fireEvent.click(screen.getByRole("button", { name: "Close profile editor" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  await waitFor(() =>
    expect(window.location.pathname).toBe("/settings/account"),
  );
});

it("opens the personal categories for members without administrative settings", async () => {
  installApi("member");
  render(<App />);
  await screen.findByRole("heading", { name: "Your Account" });
  const nav = screen.getByRole("navigation", { name: "Settings categories" });
  expect(
    within(nav)
      .getAllByRole("tab")
      .map((link) => link.getAttribute("href")),
  ).toEqual(["/settings/account", "/settings/security", "/settings/display"]);
  expect(screen.queryByLabelText("Site title")).toBeNull();
  fireEvent.click(
    within(nav).getByRole("tab", { name: "Security and account access" }),
  );
  fireEvent.click(await screen.findByRole("link", { name: /^API token/ }));
  expect(
    await screen.findByRole("button", { name: "Create API token" }),
  ).toBeEnabled();
});

it("refuses an administrative settings address for a member", async () => {
  window.history.replaceState({}, "", "/settings/site");
  installApi("member");
  render(<App />);
  expect(
    await screen.findByText("This setting requires administrator permissions."),
  ).toBeVisible();
  expect(screen.queryByLabelText("Site title")).toBeNull();
});
it("navigates to a site child and saves only the fields on that page", async () => {
  const requests: unknown[] = [];
  installApi("admin", (url, init) => {
    if (url === "/api/v1/site" && init?.method === "PATCH") {
      requests.push(JSON.parse(String(init.body)));
      return json({ ...site, comments_enabled: false });
    }
  });
  window.localStorage.setItem("moment.ui-locale", "en");
  await act(() => i18n.changeLanguage("en"));
  render(<App />);
  await screen.findByRole("heading", { name: "Your Account" });
  const nav = screen.getByRole("navigation", { name: "Settings categories" });
  fireEvent.click(within(nav).getByRole("tab", { name: "Privacy and safety" }));
  expect(window.location.pathname).toBe("/settings/site");
  expect(
    await screen.findByRole("heading", { name: "Your Moment activity" }),
  ).toBeVisible();
  fireEvent.click(await screen.findByRole("link", { name: /^Comments/ }));
  expect(window.location.pathname).toBe("/settings/site/comments");
  fireEvent.click(
    await screen.findByRole("checkbox", { name: "Allow new comments" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "Save" })).toBeEnabled(),
  );
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  await screen.findByText("Saved");
  expect(requests).toEqual([{ comments_enabled: false }]);
  expect(
    within(nav).getByRole("tab", { name: "Privacy and safety" }),
  ).toHaveAttribute("aria-selected", "true");
});

it("preserves an unsaved token label across categories and child pages", async () => {
  window.history.replaceState({}, "", "/settings/security/tokens");
  installApi("admin");
  window.localStorage.setItem("moment.ui-locale", "en");
  await act(() => i18n.changeLanguage("en"));
  render(<App />);
  const field = await screen.findByLabelText("Token name");
  fireEvent.change(field, { target: { value: "Pending device token" } });
  const nav = screen.getByRole("navigation", { name: "Settings categories" });
  fireEvent.click(within(nav).getByRole("tab", { name: "Privacy and safety" }));
  expect(field).not.toBeVisible();
  expect(field).toHaveValue("Pending device token");
  fireEvent.click(
    within(nav).getByRole("tab", { name: "Security and account access" }),
  );
  fireEvent.click(await screen.findByRole("link", { name: /^API token/ }));
  expect(screen.getByLabelText("Token name")).toBe(field);
  expect(field).toBeVisible();
  expect(field).toHaveValue("Pending device token");
});

it("keeps password settings usable when the separate token list cannot be loaded", async () => {
  window.history.replaceState({}, "", "/settings/account/password");
  installApi("member", (url) =>
    url === "/api/v1/account/tokens" ? json({}, 503) : undefined,
  );
  render(<App />);
  expect(
    await screen.findByText(
      "Changing your password revokes all API tokens and browser sessions. You will need to log in and configure API clients again.",
    ),
  ).toBeVisible();
  expect(
    screen.queryByText("Could not load login credentials. You can retry."),
  ).toBeNull();
  const nav = screen.getByRole("navigation", { name: "Settings categories" });
  fireEvent.click(
    within(nav).getByRole("tab", { name: "Security and account access" }),
  );
  fireEvent.click(await screen.findByRole("link", { name: /^API token/ }));
  expect(
    await screen.findByText("Could not load login credentials. You can retry."),
  ).toBeVisible();
});

it("creates and revokes an account token without persisting the secret", async () => {
  window.history.replaceState({}, "", "/settings/security/tokens");
  const secret = "account-token-visible-once";
  const calls = installApi("member", (url, init) => {
    if (url === "/api/v1/account/tokens" && init?.method === "POST")
      return json(
        {
          credential: { ...token, id: "4".repeat(32), label: "New computer" },
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
  fireEvent.change(await screen.findByLabelText("Token name"), {
    target: { value: "New computer" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Create API token" }));
  expect(await screen.findByLabelText("New API token")).toHaveValue(secret);
  expect(window.sessionStorage.getItem(sessionKey)).not.toContain(secret);
  expect(
    Array.from({ length: window.localStorage.length }, (_, i) =>
      window.localStorage.getItem(window.localStorage.key(i)!),
    ).join(),
  ).not.toContain(secret);
  fireEvent.click(screen.getByRole("button", { name: "Saved; hide token" }));
  expect(screen.queryByLabelText("New API token")).toBeNull();
  const tokenRow = screen.getByText("Work computer").closest("li")!;
  fireEvent.click(within(tokenRow).getByRole("button", { name: "Revoke" }));
  await within(tokenRow).findByText("Revoked");
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
  fireEvent.change(await screen.findByLabelText("New password"), {
    target: { value: "a-new-account-password" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Save password and log in again" }),
  );
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
  expect(
    screen.queryByRole("navigation", { name: "Main navigation" }),
  ).toBeNull();
  // Public reading remains available after leaving the sign-in page.
  act(() => {
    window.history.pushState({}, "", "/");
    window.dispatchEvent(new PopStateEvent("popstate", { state: {} }));
  });
  await screen.findByRole("navigation", { name: "Main navigation" });
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
  const personal = await screen.findByRole("region", { name: "Personal data" });
  fireEvent.change(within(personal).getByLabelText("Personal export file"), {
    target: { files: [file] },
  });
  fireEvent.click(
    within(personal).getByRole("button", { name: "Inspect file" }),
  );
  expect(
    await screen.findByText(
      "Existing content on this site will not be deleted or overwritten.",
    ),
  ).toBeVisible();
  expect(restored).toBe(false);
  fireEvent.click(
    within(personal).getByRole("button", {
      name: "Confirm and add to this account",
    }),
  );
  expect(await screen.findByText(/Added 2 posts, 3 comments/)).toBeVisible();
  expect(restored).toBe(true);
  await waitFor(() =>
    expect(
      within(personal).getByRole("button", { name: "Inspect file" }),
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
  fireEvent.change(await screen.findByLabelText("Personal export file"), {
    target: { files: [new File(["invalid archive"], "archive.zip")] },
  });
  fireEvent.click(screen.getByRole("button", { name: "Inspect file" }));
  const failure =
    "The file is incomplete or verification failed. Export again and choose the original ZIP.";
  expect(await screen.findByText(failure)).toBeVisible();
  fireEvent.click(screen.getByRole("button", { name: "Go back" }));
  fireEvent.click(
    await screen.findByRole("link", { name: /^Download your data/ }),
  );
  expect(screen.queryByText(failure)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Go back" }));
  fireEvent.click(
    await screen.findByRole("link", { name: /^Import personal data/ }),
  );
  expect(screen.getByText(failure)).toBeVisible();
});

it("requires confirmation before revoking all credentials and restores focus on cancel", async () => {
  window.history.replaceState({}, "", "/settings/security/tokens");
  const calls = installApi("admin");
  window.localStorage.setItem("moment.ui-locale", "en");
  await act(() => i18n.changeLanguage("en"));
  render(<App />);
  const trigger = await screen.findByRole("button", {
    name: "Revoke all access credentials",
  });
  fireEvent.click(trigger);
  const dialog = await screen.findByRole("dialog", {
    name: "Revoke all access credentials?",
  });
  await waitFor(() =>
    expect(
      within(dialog).getByRole("button", {
        name: "Revoke all credentials and log out",
      }),
    ).toBeVisible(),
  );
  expect(calls.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(calls.mock.calls.some(([, init]) => init?.method === "POST")).toBe(
    false,
  );
  await waitFor(() => expect(trigger).toHaveFocus());
});
