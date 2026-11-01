import "@testing-library/jest-dom/vitest";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { Menu } from "@base-ui/react/menu";
import { useRef } from "react";
import { PublicApiError } from "../../api/public";
import i18n from "../../i18n";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import {
  makeAccountSession,
  makeSiteIdentity,
} from "../../test/accountFixtures";
import { SessionProvider, useSession } from "./SessionProvider";
import {
  BrowserAccountsProvider,
  useBrowserAccounts,
} from "./BrowserAccountsProvider";
import {
  AccountMenuAccounts,
  AccountMenuActions,
  BrowserAccountDialogs,
} from "./AccountSwitcher";

const site = makeSiteIdentity();
const owner = makeAccountSession();
const reader = {
  ...makeAccountSession(),
  csrf_token: "reader-csrf",
  account: {
    ...makeAccountSession().account,
    user_id: "2".repeat(32),
    username: "reader",
    display_name: "Reader",
    profile_url: "/reader",
  },
};
function publicIdentity(wire: typeof owner) {
  const {
    role: _role,
    status: _status,
    username_chosen: _chosen,
    ...account
  } = wire.account;
  void _role;
  void _status;
  void _chosen;
  return account;
}
function json(data: unknown, status = 200) {
  return new Response(JSON.stringify({ data, meta: {} }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
function installAccounts(extra = 0, includeReader = true) {
  let active: typeof owner | null = owner;
  let members = [
    owner,
    ...(includeReader ? [reader] : []),
    ...Array.from({ length: extra }, (_, i) => ({
      ...reader,
      account: {
        ...reader.account,
        user_id: (i + 3).toString(16).repeat(32),
        username: `reader${i + 3}`,
        display_name: `Reader ${i + 3}`,
        profile_url: `/reader${i + 3}`,
      },
    })),
  ];
  let failRemove = false;
  let loseRemoveResponse = false;
  let loseLogoutResponse = false;
  const operations: string[] = [];
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      if (url === "/api/v1/account/session")
        return active
          ? json(active)
          : new Response(
              JSON.stringify({
                error: {
                  code: "UNAUTHORIZED",
                  message: "signed out",
                  details: {},
                  request_id: "test",
                },
              }),
              { status: 401 },
            );
      if (url === "/api/v1/login/methods")
        return json({ methods: ["password", "token"] });
      if (url === "/api/v1/login/oauth/providers")
        return json({ providers: [] });
      if (url === "/api/v1/browser/accounts" && method === "GET")
        return json({
          accounts: members.map((m) => ({
            account: publicIdentity(m),
            available: true,
          })),
          csrf_token: "browser-csrf",
        });
      if (url.endsWith("/notification-counts"))
        return json({ counts: { [reader.account.user_id]: 21 } });
      if (url.endsWith("/activate")) {
        operations.push("activate");
        active = reader;
        return json({ switched: true });
      }
      if (url === "/api/v1/browser/accounts" && method === "POST") {
        operations.push("add");
        const body = JSON.parse(String(init?.body));
        if (body.password === "wrong")
          return new Response(
            JSON.stringify({
              error: {
                code: "UNAUTHORIZED",
                message: "invalid credential",
                details: {},
                request_id: "test",
              },
            }),
            { status: 401 },
          );
        active = reader;
        return json({
          csrf_token: reader.csrf_token,
          expires_at: reader.expires_at,
        });
      }
      if (url.startsWith("/api/v1/browser/accounts/") && method === "DELETE") {
        operations.push("remove");
        if (failRemove)
          return new Response(
            JSON.stringify({
              error: {
                code: "UNAVAILABLE",
                message: "try again",
                details: {},
                request_id: "test",
              },
            }),
            { status: 503 },
          );
        members = members.filter((m) => !url.endsWith(m.account.user_id));
        if (loseRemoveResponse)
          throw new TypeError("response lost after commit");
        return json({ logged_out: true, current_changed: false, empty: false });
      }
      if (url === "/api/v1/login/session" && method === "DELETE") {
        operations.push("logout");
        members = members.filter(
          (m) => m.account.user_id !== active?.account.user_id,
        );
        active = reader;
        if (loseLogoutResponse) throw new TypeError("logout response lost");
        return json({ logged_out: true, active_account: true });
      }
      throw new Error(`Unexpected request ${method} ${url}`);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return {
    operations,
    failRemoval: (fail: boolean) => {
      failRemove = fail;
    },
    loseRemoval: () => {
      loseRemoveResponse = true;
    },
    loseLogout: () => {
      loseLogoutResponse = true;
    },
    fetcher,
  };
}
function Harness() {
  const browser = useBrowserAccounts()!;
  const auth = useSession();
  const earlierFailure = useRef<typeof auth.expireIfInvalidSession | null>(
    null,
  );
  if (!earlierFailure.current && auth.account?.username === "writer")
    earlierFailure.current = auth.expireIfInvalidSession;
  return (
    <>
      <output aria-label="Session state">{auth.state}</output>
      <button
        onClick={() =>
          earlierFailure.current?.(
            new PublicApiError("old request ended", {
              status: 401,
              code: "UNAUTHORIZED",
            }),
          )
        }
      >
        Finish earlier request
      </button>
      <output aria-label="Current identity">
        {auth.account?.username ?? "guest"}
      </output>
      <button onClick={browser.openManage}>Manage</button>
      <button onClick={() => browser.openAdd()}>Add</button>
      <Menu.Root onOpenChange={browser.setMenuOpen}>
        <Menu.Trigger data-account-menu-trigger>Accounts menu</Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner>
            <Menu.Popup>
              <AccountMenuAccounts />
              <AccountMenuActions className="action" />
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>
      <BrowserAccountDialogs site={site} />
    </>
  );
}
async function mount() {
  render(
    <FeedbackProvider>
      <NavigationProvider>
        <SessionProvider>
          <BrowserAccountsProvider>
            <Harness />
          </BrowserAccountsProvider>
        </SessionProvider>
      </NavigationProvider>
    </FeedbackProvider>,
  );
  await waitFor(() =>
    expect(screen.getByLabelText("Current identity")).toHaveTextContent(
      "writer",
    ),
  );
}
describe("browser account interactions", () => {
  beforeEach(async () => {
    await i18n.changeLanguage("en");
    window.history.replaceState({}, "", "/settings");
    sessionStorage.clear();
    vi.stubGlobal("scrollTo", vi.fn());
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
    sessionStorage.clear();
  });

  it("opens an independent exit confirmation, preserves failures and retries without switching", async () => {
    const api = installAccounts(1);
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Manage" }));
    const manager = await screen.findByRole("dialog", {
      name: "Accounts",
    });
    const exit = await within(manager).findByRole("button", {
      name: "Log out @reader",
    });
    fireEvent.click(exit);
    let confirmation = await screen.findByRole("dialog", {
      name: "Log out @reader?",
    });
    expect(api.operations).toEqual([]);
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Cancel" }),
    );
    await waitFor(() => expect(confirmation).not.toBeInTheDocument());
    await waitFor(() => expect(exit).toHaveFocus());
    fireEvent.click(exit);
    confirmation = await screen.findByRole("dialog", {
      name: "Log out @reader?",
    });
    api.failRemoval(true);
    fireEvent.click(
      within(confirmation).getByRole("button", {
        name: "Log out",
      }),
    );
    expect(await within(confirmation).findByRole("alert")).toHaveTextContent(
      "Try again",
    );
    expect(confirmation).toBeInTheDocument();
    expect(screen.getByLabelText("Current identity")).toHaveTextContent(
      "writer",
    );
    api.failRemoval(false);
    fireEvent.click(
      within(confirmation).getByRole("button", {
        name: "Log out",
      }),
    );
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Log out @reader?" }),
      ).toBeNull(),
    );
    expect(
      within(manager).queryByRole("button", { name: "Log out @reader" }),
    ).toBeNull();
    expect(api.operations).toEqual(["remove", "remove"]);
    await waitFor(() =>
      expect(
        within(manager).getByRole("button", { name: "Switch to @reader3" }),
      ).toHaveFocus(),
    );
    expect(window.location.pathname).toBe("/settings");
  });

  it("shows management only when another retained account exists", async () => {
    const api = installAccounts(0, false);
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Accounts menu" }));
    await waitFor(() =>
      expect(
        api.fetcher.mock.calls.some(([url]) =>
          String(url).endsWith("/notification-counts"),
        ),
      ).toBe(true),
    );
    expect(
      screen.getByRole("menuitem", { name: "Add an existing account" }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("menuitem", { name: "Manage accounts" }),
    ).toBeNull();
  });

  it("shows the other account's capped badge and switches to the home page", async () => {
    const api = installAccounts();
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Accounts menu" }));
    const other = await screen.findByRole("menuitem", {
      name: "Switch to @reader",
    });
    await waitFor(() =>
      expect(within(other).getByText("20+")).toBeInTheDocument(),
    );
    expect(
      screen.getByRole("menuitem", { name: "Current account @writer" }),
    ).toHaveAttribute("aria-disabled", "true");
    fireEvent.click(other);
    await waitFor(() =>
      expect(screen.getByLabelText("Current identity")).toHaveTextContent(
        "reader",
      ),
    );
    expect(window.location.pathname).toBe("/");
    expect(api.operations).toEqual(["activate"]);
    fireEvent.click(
      screen.getByRole("button", { name: "Finish earlier request" }),
    );
    expect(screen.getByLabelText("Session state")).toHaveTextContent(
      "signed-in",
    );
  });

  it("retains every account when the browser has more than five sign-ins", async () => {
    installAccounts(5);
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Manage" }));
    const dialog = await screen.findByRole("dialog", { name: "Accounts" });
    await waitFor(() =>
      expect(
        within(dialog).getAllByRole("button", { name: /^Switch to @/ }),
      ).toHaveLength(6),
    );
    expect(
      within(dialog).getAllByRole("button", { name: /^Log out @/ }),
    ).toHaveLength(6);
  });

  it("recognizes a completed noncurrent exit when its response was lost", async () => {
    const api = installAccounts();
    api.loseRemoval();
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Manage" }));
    fireEvent.click(
      await screen.findByRole("button", { name: "Log out @reader" }),
    );
    const confirmation = await screen.findByRole("dialog", {
      name: "Log out @reader?",
    });
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Log out" }),
    );
    await waitFor(() => expect(confirmation).not.toBeInTheDocument());
    expect(
      screen.queryByRole("button", { name: "Log out @reader" }),
    ).toBeNull();
    expect(api.operations).toEqual(["remove"]);
    expect(screen.getByLabelText("Current identity")).toHaveTextContent(
      "writer",
    );
  });

  it("restores the remaining identity after a current-account logout response is lost", async () => {
    const api = installAccounts();
    api.loseLogout();
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Accounts menu" }));
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Log out @writer" }),
    );
    const confirmation = await screen.findByRole("dialog", {
      name: "Log out @writer?",
    });
    fireEvent.click(
      within(confirmation).getByRole("button", { name: "Log out" }),
    );
    await waitFor(() => expect(confirmation).not.toBeInTheDocument());
    expect(screen.getByLabelText("Current identity")).toHaveTextContent(
      "reader",
    );
    expect(window.location.pathname).toBe("/");
    expect(api.operations).toEqual(["logout"]);
  });

  it("keeps an existing account signed in while adding and only changes identity after success", async () => {
    const api = installAccounts();
    await mount();
    fireEvent.click(screen.getByRole("button", { name: "Add" }));
    const dialog = await screen.findByRole("dialog", {
      name: "Add an existing account",
    });
    const username = await within(dialog).findByLabelText("Email or username");
    fireEvent.change(username, { target: { value: "reader" } });
    const password = within(dialog).getByLabelText("Password", { exact: true });
    fireEvent.change(password, { target: { value: "wrong" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Log in" }));
    await waitFor(() => expect(api.operations).toEqual(["add"]));
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "Log in" }),
      ).toBeEnabled(),
    );
    expect(screen.getByLabelText("Current identity")).toHaveTextContent(
      "writer",
    );
    expect(
      api.fetcher.mock.calls.filter(
        ([url]) => String(url) === "/api/v1/account/session",
      ),
    ).toHaveLength(1);
    fireEvent.change(password, { target: { value: "valid-password" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Log in" }));
    await waitFor(() =>
      expect(
        screen.queryByRole("dialog", { name: "Add an existing account" }),
      ).toBeNull(),
    );
    expect(screen.getByLabelText("Current identity")).toHaveTextContent(
      "reader",
    );
    expect(window.location.pathname).toBe("/");
  });
});
