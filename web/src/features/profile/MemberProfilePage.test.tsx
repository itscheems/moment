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

import i18n from "../../i18n";

import { PublicApi } from "../../api/public";
import defaultAvatarUrl from "../../assets/default-avatar.png";
import { NavigationProvider } from "../../app/navigation";
import { FeedbackProvider } from "../../components/Feedback";
import { SessionProvider } from "../auth/SessionProvider";
import { MemberProfilePage } from "./MemberProfilePage";
import {
  makeSiteIdentity,
  makeAccountLimits,
} from "../../test/accountFixtures";

const sessionKey = "moment.auth.session.v2";

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function memberEnvelope() {
  return {
    data: {
      user_id: "11111111111111111111111111111111",
      username: "linlin",
      display_name: "林林",
      bio: "在这里记下路过的天气",
      avatar_media_id: null,
      cover_media_id: null,
      profile_url: "/linlin",
      joined_at: "2026-08-01T09:08:00Z",
    },
    meta: { request_id: "req_user" },
  };
}

/**
 * A signed-in browser, or none.
 *
 * The page asks who is reading before it decides whether to offer the editor,
 * so every case has to answer that question one way or the other -- a silent
 * network failure would leave the answer pending and quietly hide the control
 * the case is about.
 */
function signedIn(username: string | null) {
  return vi.fn(async (input: RequestInfo | URL) => {
    if (String(input) !== "/api/v1/account/session") return json({}, 404);
    if (username === null) {
      return json(
        {
          error: {
            code: "UNAUTHORIZED",
            message: "sign in",
            details: {},
            request_id: "req_guest",
          },
        },
        401,
      );
    }
    return json({
      data: {
        preferences: { ui_locale: null },
        expires_at: "2999-08-11T01:00:00.000Z",
        csrf_token: "csrf-from-cookie",
        account: {
          user_id:
            username === "linlin"
              ? "11111111111111111111111111111111"
              : "22222222222222222222222222222222",
          username,
          display_name: username,
          bio: "",
          avatar_media_id: null,
          cover_media_id: null,
          role: "member",
          profile_url: `/${username}`,
          status: "active",
          username_chosen: true,
        },
        limits: makeAccountLimits(),
      },
      meta: { request_id: "req_session" },
    });
  });
}

function mount(api: PublicApi, username: string) {
  return render(
    <FeedbackProvider>
      <NavigationProvider>
        <SessionProvider>
          <MemberProfilePage
            site={makeSiteIdentity()}
            api={api}
            username={username}
          />
        </SessionProvider>
      </NavigationProvider>
    </FeedbackProvider>,
  );
}

function notFound() {
  return json(
    {
      error: {
        code: "ACCOUNT_NOT_FOUND",
        message: "account not found",
        details: {},
        request_id: "req_missing",
      },
    },
    404,
  );
}

describe("member profile", () => {
  beforeEach(() => {
    window.sessionStorage.setItem(
      sessionKey,
      JSON.stringify({
        csrf_token: "csrf-member",
        expires_at: "2999-08-11T01:00:00.000Z",
      }),
    );
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    window.sessionStorage.clear();
  });

  it("renders another account's identity over the plain cover band", async () => {
    const requested: string[] = [];
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      requested.push(String(input));
      return json(memberEnvelope());
    });
    vi.stubGlobal("fetch", signedIn(null));
    const { container } = mount(
      new PublicApi(fetcher as unknown as typeof fetch),
      "linlin",
    );

    expect(await screen.findByRole("heading", { name: "林林" })).toBeVisible();
    expect(screen.getByText("@linlin")).toBeInTheDocument();
    expect(screen.getByText("在这里记下路过的天气")).toBeInTheDocument();
    expect(screen.getByText("2026年8月加入")).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "全部" })).toBeInTheDocument();
    expect(screen.getByRole("tab", { name: "文章" })).toBeInTheDocument();
    // No cover of their own falls back to the same colour band the site page
    // uses, rather than to an empty strip that reads as a failed image.
    expect(container.querySelector('[class*="coverFallback"]')).not.toBeNull();
    expect(container.querySelector("img[alt='']")).toHaveAttribute(
      "src",
      defaultAvatarUrl,
    );
    expect(requested[0]).toBe("/api/v1/users/linlin");
    await waitFor(() =>
      expect(
        requested.some(
          (url) =>
            url.startsWith("/api/v1/posts?") && url.includes("author_user_id="),
        ),
      ).toBe(true),
    );
    // A reader who is not this account gets an identity and nothing to press.
    expect(screen.queryByRole("button", { name: "编辑资料" })).toBeNull();
  });

  it.each([
    "/",
    "/m/01K2ABCDEF0123456789ABCDE1",
    "/m/01K2ABCDEF0123456789ABCDE1/comments/01K2ABCDEF0123456789ABCDE3?view=thread",
  ])(
    "switches member feeds without adding history before returning to %s",
    async (source) => {
      await i18n.changeLanguage("en");
      window.history.replaceState({}, "", source);
      window.history.pushState({ momentInternal: true }, "", "/linlin");
      const historyLength = window.history.length;
      const requested: string[] = [];
      const fetcher = vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input);
        requested.push(url);
        if (url === "/api/v1/users/linlin")
          return json({
            ...memberEnvelope(),
            data: {
              ...memberEnvelope().data,
              display_name: "River",
              bio: "Field notes",
            },
          });
        return json({
          data: { items: [] },
          meta: {
            next_cursor: null,
            prev_cursor: null,
            request_id: "req_posts",
          },
        });
      });
      vi.stubGlobal("fetch", signedIn(null));
      mount(new PublicApi(fetcher as unknown as typeof fetch), "linlin");

      fireEvent.click(await screen.findByRole("tab", { name: "Articles" }));
      await waitFor(() =>
        expect(window.location.pathname).toBe("/linlin/articles"),
      );
      await waitFor(() =>
        expect(requested.some((url) => url.includes("kind=article"))).toBe(
          true,
        ),
      );
      const articleTab = screen.getByRole("tab", { name: "Articles" });
      expect(articleTab).toHaveTextContent("Articles");
      expect(articleTab.closest("[data-feed-motion]")).toHaveAttribute(
        "data-feed-motion",
        "slide",
      );
      expect(articleTab.closest("[data-feed-direction]")).toHaveAttribute(
        "data-feed-direction",
        "right",
      );
      fireEvent.click(screen.getByRole("tab", { name: "All" }));
      await waitFor(() => expect(window.location.pathname).toBe("/linlin/all"));
      expect(articleTab.closest("[data-feed-direction]")).toHaveAttribute(
        "data-feed-direction",
        "left",
      );
      fireEvent.click(screen.getByRole("button", { name: "Choose post type" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Posts" }));
      await waitFor(() =>
        expect(window.location.pathname).toBe("/linlin/posts"),
      );
      await waitFor(() =>
        expect(requested.some((url) => url.includes("kind=moment"))).toBe(true),
      );
      expect(articleTab.closest("[data-feed-motion]")).toHaveAttribute(
        "data-feed-motion",
        "none",
      );
      expect(window.history.length).toBe(historyLength);
      act(() => window.history.back());
      await waitFor(() =>
        expect(window.location.pathname + window.location.search).toBe(source),
      );
      act(() => window.history.forward());
      await waitFor(() =>
        expect(window.location.pathname).toBe("/linlin/posts"),
      );
    },
  );

  it("automatically restores a member identity after a temporary read failure", async () => {
    let reads = 0;
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/v1/users/linlin") {
        reads += 1;
        if (reads === 1) return json({}, 503);
      }
      return json(memberEnvelope());
    });
    vi.stubGlobal("fetch", signedIn(null));
    mount(new PublicApi(fetcher as unknown as typeof fetch), "linlin");
    await screen.findByText("正在重新获取内容…");
    expect(await screen.findByRole("heading", { name: "林林" })).toBeVisible();
    expect(reads).toBe(2);
    expect(screen.queryByRole("button", { name: "重试" })).toBeNull();
  });

  it("reads an account that is not there as a page that is not there", async () => {
    vi.stubGlobal("fetch", signedIn(null));
    mount(
      new PublicApi(vi.fn(async () => notFound()) as unknown as typeof fetch),
      "ghost",
    );

    expect(
      await screen.findByRole("heading", { name: "页面不存在" }),
    ).toBeVisible();
  });

  it("gives the account behind the page somewhere to edit it", async () => {
    vi.stubGlobal("fetch", signedIn("linlin"));
    mount(
      new PublicApi(
        vi.fn(async () => json(memberEnvelope())) as unknown as typeof fetch,
      ),
      "linlin",
    );

    // Settings is about the account; this page is about the face on it, and
    // until now a member had no way to change what it said.
    const edit = await screen.findByRole("button", { name: "编辑资料" });
    fireEvent.click(edit);
    expect(
      await screen.findByRole("dialog", { name: "编辑资料" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("显示名称")).toHaveValue("林林");
  });

  it("offers a member no editor on somebody else's page", async () => {
    vi.stubGlobal("fetch", signedIn("other"));
    mount(
      new PublicApi(
        vi.fn(async () => json(memberEnvelope())) as unknown as typeof fetch,
      ),
      "linlin",
    );

    expect(await screen.findByRole("heading", { name: "林林" })).toBeVisible();
    await waitFor(() =>
      expect(screen.queryByRole("button", { name: "编辑资料" })).toBeNull(),
    );
  });
});
