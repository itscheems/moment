import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { makeAccountLimits } from "../../test/accountFixtures";
import { MembersSection } from "./MembersSection";
import { SessionProvider } from "../auth/SessionProvider";

const profileUserID = "00000000000000000000000000000000";
const memberID = "11111111111111111111111111111111";

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
      csrf_token: "csrf-members",
      account: {
        user_id: profileUserID,
        username: "owner",
        display_name: "站长",
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
    meta: { request_id: "req_account" },
  };
}

function rosterEnvelope(registrationMode: string, memberStatus = "pending") {
  return {
    data: [
      {
        user_id: profileUserID,
        username: "owner",
        display_name: "站长",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: "/owner",
        status: "active",
        username_chosen: true,
      },
      {
        user_id: memberID,
        username: "linlin",
        display_name: "林林",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "member",
        profile_url: "/linlin",
        status: memberStatus,
        username_chosen: true,
      },
    ],
    meta: {
      count: 2,
      registration_mode: registrationMode,
      request_id: "req_roster",
    },
  };
}

function install(
  handler: (url: string, init?: RequestInit) => Promise<Response>,
) {
  const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) =>
    handler(String(input), init),
  );
  vi.stubGlobal("fetch", fetcher);
  return fetcher;
}

function renderMembers(strict = false) {
  const tree = (
    <SessionProvider>
      <MembersSection />
    </SessionProvider>
  );
  return render(strict ? <StrictMode>{tree}</StrictMode> : tree);
}

describe("members section", () => {
  beforeEach(() => {
    window.sessionStorage.setItem(
      "moment.auth.session.v2",
      JSON.stringify({
        csrf_token: "csrf-members",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  /**
   * A ref claimed only on unmount stays false after StrictMode's development
   * remount, and every guarded update after it is silently dropped.
   *
   * Here that means the `finally` never clears `busy`, and `busy` disables
   * every button in the roster -- so one approval in development locks the
   * whole list for the rest of the session, with nothing said about why.
   */
  it("keeps the roster usable after a StrictMode remount", async () => {
    install(async (url, init) => {
      if (url === "/api/v1/account/session") {
        return json(accountSessionEnvelope());
      }
      if (url === "/api/v1/accounts") {
        return json(rosterEnvelope("approval"));
      }
      if (url === `/api/v1/accounts/${memberID}` && init?.method === "PATCH") {
        return json({
          data: { ...rosterEnvelope("approval").data[1], status: "active" },
          meta: { request_id: "req_approve" },
        });
      }
      return json({}, 404);
    });
    renderMembers(true);

    const approve = await screen.findByRole("button", { name: "批准" });
    await waitFor(() => expect(approve).toBeEnabled());
    fireEvent.click(approve);

    // The row moves on, which only happens if the update after the request
    // was allowed through.
    expect(await screen.findByText("已启用")).toBeInTheDocument();
    // And the controls that remain are usable rather than latched off.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "停用" })).toBeEnabled(),
    );
  });

  it("reads the registration policy and writes the owner's choice", async () => {
    const bodies: unknown[] = [];
    const fetcher = install(async (url, init) => {
      if (url === "/api/v1/account/session") {
        return json(accountSessionEnvelope());
      }
      if (url === "/api/v1/accounts") {
        return json(rosterEnvelope("approval"));
      }
      if (url === "/api/v1/site" && init?.method === "PATCH") {
        bodies.push(JSON.parse(String(init.body)));
        return json({
          data: {
            profile_user_id: profileUserID,
            username: "owner",
            display_name: "站长",
            title: "Moment",
            bio: "",
            timezone: "UTC",
            public_base_url: null,
            avatar_media_id: null,
            cover_media_id: null,
            pinned_post_id: null,
            comments_enabled: true,
          },
          meta: { request_id: "req_site" },
        });
      }
      return json({}, 404);
    });
    renderMembers();

    const approval = await screen.findByRole("radio", { name: /需要审核/ });
    expect(approval).toBeChecked();
    const closed = screen.getByRole("radio", { name: /关闭注册/ });
    expect(closed).not.toBeChecked();

    fireEvent.click(closed);

    await waitFor(() => expect(closed).toBeChecked());
    expect(bodies).toEqual([{ registration_mode: "closed" }]);
    expect(approval).not.toBeChecked();
    // Choosing what is already chosen asks the server nothing.
    fireEvent.click(closed);
    expect(
      fetcher.mock.calls.filter(([input]) => String(input) === "/api/v1/site"),
    ).toHaveLength(1);
  });

  it("says what went wrong and keeps the old policy when the write fails", async () => {
    install(async (url, init) => {
      if (url === "/api/v1/account/session") {
        return json(accountSessionEnvelope());
      }
      if (url === "/api/v1/accounts") {
        return json(rosterEnvelope("open"));
      }
      if (url === "/api/v1/site" && init?.method === "PATCH") {
        return json(
          {
            error: {
              code: "INVALID_ARGUMENT",
              message: "registration mode is not recognised",
              details: { field: "registration_mode" },
            },
          },
          400,
        );
      }
      return json({}, 404);
    });
    renderMembers();

    const open = await screen.findByRole("radio", { name: /开放注册/ });
    expect(open).toBeChecked();
    fireEvent.click(screen.getByRole("radio", { name: /关闭注册/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "注册方式没有保存",
    );
    expect(open).toBeChecked();
  });
});
