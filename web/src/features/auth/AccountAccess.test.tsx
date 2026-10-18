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
import { makeAccountLimits } from "../../test/accountFixtures";
import { AccountAccess } from "./AccountAccess";
import { SessionProvider } from "./SessionProvider";
import { keepDraft, listKeptDrafts } from "../compose/drafts";

const instance = "0123456789abcdef0123456789abcdef";
const sessionKey = "moment.auth.session.v2";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function accountSessionEnvelope() {
  return {
    data: {
      expires_at: "2999-08-11T01:00:00.000Z",
      csrf_token: "csrf-from-cookie",
      account: {
        user_id: "00000000000000000000000000000000",
        username: "owner",
        display_name: "乔乔",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: "/owner",
        status: "active",
        username_chosen: true,
      },
      limits: makeAccountLimits(),
    },
    meta: { request_id: "req_account_session" },
  };
}

function installApi(onDelete: (url: string) => Response) {
  const calls: string[] = [];
  const fetcher = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      const method = init?.method ?? "GET";
      calls.push(`${method} ${url}`);
      if (url === "/api/v1/account/session") {
        return json(accountSessionEnvelope());
      }
      if (url === "/api/v1/status") {
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
      if (url === "/api/v1/account/identities") {
        return json({ data: [], meta: { request_id: "req_identities" } });
      }
      if (url === "/api/v1/account/sessions") {
        if (method === "DELETE") return onDelete(url);
        return json({
          data: [
            {
              id: "11111111111111111111111111111111",
              issued_at: "2026-08-20T00:00:00Z",
              last_seen_at: "2026-08-23T00:00:00Z",
              expires_at: "2999-08-11T01:00:00Z",
              surface: "web",
              current: true,
            },
          ],
          meta: { request_id: "req_sessions", count: 1 },
        });
      }
      if (url === "/api/v1/login/session" && method === "DELETE") {
        return onDelete(url);
      }
      if (url === "/api/v1/login/oauth/providers") {
        return json({
          data: { providers: [] },
          meta: { request_id: "req_providers" },
        });
      }
      return json({}, 404);
    },
  );
  vi.stubGlobal("fetch", fetcher);
  return { fetcher, calls };
}

function renderAccess(onSignedOut = vi.fn()) {
  window.sessionStorage.setItem(
    sessionKey,
    JSON.stringify({
      csrf_token: "csrf-from-session",
      expires_at: "2999-08-11T01:00:00.000Z",
    }),
  );
  render(
    <NavigationProvider>
      <SessionProvider>
        <AccountAccess
          instanceId={instance}
          onSignedOut={onSignedOut}
          timezone="Asia/Shanghai"
        />
      </SessionProvider>
    </NavigationProvider>,
  );
  return onSignedOut;
}

// Ending this session was reachable only from the sign-in page, which meant
// leaving the settings you were already in to do the most ordinary thing there
// is. The one control that did live here ended every session everywhere, so
// signing out at a desk also signed out the phone in a pocket.
describe("signing out from settings", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/settings/account");
    window.sessionStorage.clear();
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("ends this session without touching the others", async () => {
    const { calls } = installApi(() =>
      json({ data: { logged_out: true }, meta: { request_id: "req_out" } }),
    );
    keepDraft(instance, "签退前写的一段");
    const onSignedOut = renderAccess();

    fireEvent.click(await screen.findByRole("button", { name: "退出登录" }));

    await waitFor(() => expect(onSignedOut).toHaveBeenCalled());
    expect(listKeptDrafts(instance)).toEqual([]);
    expect(calls).toContain("DELETE /api/v1/login/session");
    // The bulk control posts to the account's session collection. Reaching it
    // from here would mean the ordinary button had the drastic effect.
    expect(calls).not.toContain("DELETE /api/v1/account/sessions");
    expect(window.sessionStorage.getItem(sessionKey)).toBeNull();
  });

  it("keeps the session and says so when signing out does not complete", async () => {
    installApi(() => json({}, 500));
    const onSignedOut = renderAccess();

    fireEvent.click(await screen.findByRole("button", { name: "退出登录" }));

    expect(
      await screen.findByText("没有退出成功。当前会话仍然有效，可以重试。"),
    ).toBeInTheDocument();
    expect(onSignedOut).not.toHaveBeenCalled();
  });

  // A session the server has already forgotten is signed out. Reporting a
  // failure would describe the transport rather than the outcome asked for.
  it("treats an already-expired session as signed out", async () => {
    installApi(() =>
      json(
        {
          error: {
            code: "UNAUTHORIZED",
            message: "auth required",
            details: {},
            request_id: "req_out",
          },
        },
        401,
      ),
    );
    const onSignedOut = renderAccess();

    fireEvent.click(await screen.findByRole("button", { name: "退出登录" }));

    await waitFor(() => expect(onSignedOut).toHaveBeenCalled());
    expect(screen.queryByText(/没有退出成功/)).toBeNull();
  });
});
