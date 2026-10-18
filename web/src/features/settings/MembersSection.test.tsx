import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
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
        username: "admin",
        display_name: "站长",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: "/admin",
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
        username: "admin",
        display_name: "站长",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        profile_url: "/admin",
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

async function openMemberMenu(name: string) {
  const trigger = await screen.findByRole("button", { name: `管理 ${name}` });
  trigger.focus();
  fireEvent.keyDown(trigger, { key: "ArrowDown", code: "ArrowDown" });
  await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "true"));
  return trigger;
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
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "管理 林林" }).closest("li"),
      ).toHaveTextContent("已启用"),
    );
    // And the controls that remain are usable rather than latched off.
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "管理 林林" })).toBeEnabled(),
    );
  });

  it("reads the registration policy and writes the administrator's choice", async () => {
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
            username: "admin",
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

    fireEvent.click(
      await screen.findByRole("button", { name: "更改加入方式" }),
    );
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

    fireEvent.click(
      await screen.findByRole("button", { name: "更改加入方式" }),
    );
    const open = await screen.findByRole("radio", { name: /开放注册/ });
    expect(open).toBeChecked();
    fireEvent.click(screen.getByRole("radio", { name: /关闭注册/ }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "注册方式没有保存",
    );
    expect(open).toBeChecked();
  });

  it("filters by status and searches names without requesting the roster again", async () => {
    const fetcher = install(async (url) => {
      if (url === "/api/v1/account/session")
        return json(accountSessionEnvelope());
      if (url === "/api/v1/accounts") return json(rosterEnvelope("approval"));
      return json({}, 404);
    });
    renderMembers();
    const search = await screen.findByRole("searchbox", { name: "搜索成员" });
    fireEvent.change(search, { target: { value: "LINLIN" } });
    const roster = screen.getByRole("list", { name: "站点成员" });
    expect(within(roster).getByText("林林")).toBeVisible();
    expect(within(roster).queryByText("站长")).toBeNull();
    fireEvent.click(
      within(screen.getByRole("group", { name: "筛选成员" })).getByRole(
        "button",
        { name: /已启用/ },
      ),
    );
    expect(screen.getByText("没有找到匹配的成员")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: "清除成员搜索" }));
    expect(screen.getByRole("list", { name: "站点成员" })).toHaveTextContent(
      "站长",
    );
    expect(screen.queryByRole("button", { name: "管理 林林" })).toBeNull();
    expect(
      fetcher.mock.calls.filter(([url]) => url === "/api/v1/accounts"),
    ).toHaveLength(1);
  });

  it("confirms suspension and restores focus when cancelled without writing", async () => {
    const fetcher = install(async (url) => {
      if (url === "/api/v1/account/session")
        return json(accountSessionEnvelope());
      if (url === "/api/v1/accounts")
        return json(rosterEnvelope("approval", "active"));
      return json({}, 404);
    });
    renderMembers();
    const trigger = await openMemberMenu("林林");
    fireEvent.click(await screen.findByRole("menuitem", { name: "停用账号" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog).toHaveTextContent("已发布的内容会保留");
    await waitFor(() =>
      expect(
        within(dialog).getByRole("button", { name: "取消" }),
      ).toHaveFocus(),
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "取消" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    await waitFor(() => expect(trigger).toHaveFocus());
    expect(
      fetcher.mock.calls.some(([, init]) => init?.method === "PATCH"),
    ).toBe(false);
  });

  it("keeps a failed role change open and lets the administrator retry", async () => {
    let writes = 0;
    install(async (url, init) => {
      if (url === "/api/v1/account/session")
        return json(accountSessionEnvelope());
      if (url === "/api/v1/accounts")
        return json(rosterEnvelope("approval", "active"));
      if (
        url === `/api/v1/accounts/${memberID}/role` &&
        init?.method === "PUT"
      ) {
        writes += 1;
        expect(JSON.parse(String(init.body))).toEqual({ role: "admin" });
        return writes === 1
          ? json({ error: { code: "INTERNAL", message: "try again" } }, 500)
          : json({
              data: {
                ...rosterEnvelope("approval", "active").data[1],
                role: "admin",
              },
              meta: { request_id: "role" },
            });
      }
      return json({}, 404);
    });
    renderMembers();
    await openMemberMenu("林林");
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "设为管理员" }),
    );
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "设为管理员" }));
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "没有保存",
    );
    fireEvent.click(within(dialog).getByRole("button", { name: "重试" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(
      screen.getByRole("button", { name: "管理 林林" }).closest("li"),
    ).toHaveTextContent("管理员");
    expect(writes).toBe(2);
  });

  it("makes the last active administrator's required role explicit", async () => {
    install(async (url) => {
      if (url === "/api/v1/account/session")
        return json(accountSessionEnvelope());
      if (url === "/api/v1/accounts") return json(rosterEnvelope("approval"));
      return json({}, 404);
    });
    renderMembers();
    await openMemberMenu("站长");
    expect(
      await screen.findByRole("menuitem", { name: "设为成员" }),
    ).toHaveAttribute("aria-disabled", "true");
    await waitFor(() =>
      expect(screen.getByText("至少保留一位已启用的管理员")).toBeVisible(),
    );
  });
});
