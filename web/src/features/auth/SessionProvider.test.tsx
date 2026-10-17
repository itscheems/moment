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

import { SessionProvider, useSession } from "./SessionProvider";
import { makeAccountLimits } from "../../test/accountFixtures";

const channelName = "moment.auth.session.sync.v2";
const sessionKey = "moment.auth.session.v2";

class FakeBroadcastChannel {
  static channels = new Map<string, Set<FakeBroadcastChannel>>();
  static posted: unknown[] = [];

  readonly name: string;
  private readonly listeners = new Set<
    (event: MessageEvent<unknown>) => void
  >();

  constructor(name: string) {
    this.name = name;
    const peers = FakeBroadcastChannel.channels.get(name) ?? new Set();
    peers.add(this);
    FakeBroadcastChannel.channels.set(name, peers);
  }

  addEventListener(
    type: string,
    listener: (event: MessageEvent<unknown>) => void,
  ) {
    if (type === "message") this.listeners.add(listener);
  }

  removeEventListener(
    type: string,
    listener: (event: MessageEvent<unknown>) => void,
  ) {
    if (type === "message") this.listeners.delete(listener);
  }

  postMessage(data: unknown) {
    FakeBroadcastChannel.posted.push(data);
    for (const peer of FakeBroadcastChannel.channels.get(this.name) ?? []) {
      if (peer === this) continue;
      queueMicrotask(() => peer.receive(data));
    }
  }

  close() {
    FakeBroadcastChannel.channels.get(this.name)?.delete(this);
    this.listeners.clear();
  }

  private receive(data: unknown) {
    const event = new MessageEvent("message", { data });
    for (const listener of this.listeners) listener(event);
  }

  static reset() {
    for (const peers of FakeBroadcastChannel.channels.values()) {
      for (const peer of peers) peer.close();
    }
    FakeBroadcastChannel.channels.clear();
    FakeBroadcastChannel.posted = [];
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function accountSession(
  csrfToken: string,
  overrides: Record<string, unknown> = {},
) {
  return json({
    data: {
      csrf_token: csrfToken,
      expires_at: "2999-08-11T01:00:00.000Z",
      account: {
        user_id: "00000000000000000000000000000000",
        username: "owner",
        display_name: "Owner",
        bio: "",
        avatar_media_id: null,
        cover_media_id: null,
        role: "admin",
        status: "active",
        username_chosen: true,
        profile_url: "/owner",
        ...overrides,
      },
      limits: makeAccountLimits(),
    },
    meta: {},
  });
}

function Probe({ name }: { name: string }) {
  const auth = useSession();
  return (
    <section aria-label={name}>
      <output data-testid={`${name}-state`}>{auth.state}</output>
      <output data-testid={`${name}-csrf`}>
        {auth.session?.csrfToken ?? "none"}
      </output>
      <output data-testid={`${name}-account`}>
        {auth.account?.username ?? "none"}
      </output>
      <button onClick={() => void auth.login("bearer-secret")} type="button">
        login {name}
      </button>
      <button onClick={() => auth.forgetSession(null)} type="button">
        forget {name}
      </button>
    </section>
  );
}

describe("account session cross-tab repair", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    FakeBroadcastChannel.reset();
    vi.stubGlobal("BroadcastChannel", FakeBroadcastChannel);
    let cookieAuthenticated = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/status") {
          return json({
            data: {
              initialized: true,
              session_epoch: 1,
              initialized_at: "2026-08-01T00:00:00Z",
            },
            meta: {},
          });
        }
        if (url === "/api/v1/login/session" && init?.method === "POST") {
          cookieAuthenticated = true;
          return json({
            data: {
              csrf_token: "csrf-newest",
              expires_at: "2999-08-11T01:00:00.000Z",
            },
            meta: {},
          });
        }
        if (url === "/api/v1/account/session")
          return cookieAuthenticated
            ? accountSession("csrf-newest")
            : json(
                {
                  error: {
                    code: "UNAUTHORIZED",
                    message: "sign in",
                    details: {},
                    request_id: "guest",
                  },
                },
                401,
              );
        return json({}, 404);
      }),
    );
  });

  afterEach(() => {
    cleanup();
    FakeBroadcastChannel.reset();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("replaces stored CSRF with the session actually carried by the cookie", async () => {
    window.sessionStorage.setItem(
      sessionKey,
      JSON.stringify({
        csrf_token: "csrf-before-another-tab-signed-in",
        expires_at: "2999-08-11T01:00:00.000Z",
        issued_at: Date.now() - 1000,
        version: "stored-session-0001",
      }),
    );
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => accountSession("csrf-current-cookie")),
    );
    render(
      <SessionProvider>
        <Probe name="restored" />
      </SessionProvider>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("restored-state")).toHaveTextContent(
        "signed-in",
      ),
    );
    expect(screen.getByTestId("restored-csrf")).toHaveTextContent(
      "csrf-current-cookie",
    );
    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-current-cookie",
    );
  });

  it("keeps the marker when stored credentials already match the cookie", async () => {
    const stored = {
      csrf_token: "csrf-current-cookie",
      expires_at: "2999-08-11T01:00:00.000Z",
      issued_at: Date.now() - 1000,
      version: "stored-session-0001",
    };
    window.sessionStorage.setItem(sessionKey, JSON.stringify(stored));
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => accountSession(stored.csrf_token)),
    );
    render(
      <SessionProvider>
        <Probe name="unchanged" />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("unchanged-state")).toHaveTextContent(
        "signed-in",
      ),
    );
    expect(JSON.parse(window.sessionStorage.getItem(sessionKey)!)).toEqual(
      stored,
    );
    expect(FakeBroadcastChannel.posted).not.toEqual(
      expect.arrayContaining([
        expect.objectContaining({ type: "offer-session" }),
      ]),
    );
  });

  it("does not restore a session from a response started before signing out", async () => {
    let resolveStatus!: (response: Response) => void;
    const status = new Promise<Response>((resolve) => {
      resolveStatus = resolve;
    });
    vi.stubGlobal(
      "fetch",
      vi.fn(() => status),
    );
    render(
      <SessionProvider>
        <Probe name="signed-out" />
      </SessionProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "forget signed-out" }));
    await act(async () => {
      resolveStatus(accountSession("csrf-ended"));
    });

    expect(screen.getByTestId("signed-out-state")).toHaveTextContent("guest");
    expect(screen.getByTestId("signed-out-csrf")).toHaveTextContent("none");
    expect(screen.getByTestId("signed-out-account")).toHaveTextContent("none");
  });

  it("answers a new tab handshake without storing or broadcasting the bearer", async () => {
    const view = render(
      <SessionProvider>
        <Probe name="first" />
      </SessionProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "login first" }));
    await waitFor(() =>
      expect(screen.getByTestId("first-csrf")).toHaveTextContent("csrf-newest"),
    );

    window.sessionStorage.removeItem(sessionKey);
    view.rerender(
      <>
        <SessionProvider>
          <Probe name="first" />
        </SessionProvider>
        <SessionProvider>
          <Probe name="new-tab" />
        </SessionProvider>
      </>,
    );

    await waitFor(() =>
      expect(screen.getByTestId("new-tab-csrf")).toHaveTextContent(
        "csrf-newest",
      ),
    );
    expect(JSON.stringify(FakeBroadcastChannel.posted)).not.toContain(
      "bearer-secret",
    );
    expect(window.sessionStorage.getItem(sessionKey)).not.toContain(
      "bearer-secret",
    );
  });

  it("keeps the newest CSRF when two responder tabs answer out of order", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => accountSession("csrf-from-new-tab")),
    );
    render(
      <SessionProvider>
        <Probe name="requester" />
      </SessionProvider>,
    );
    const newerTab = new FakeBroadcastChannel(channelName);
    const staleTab = new FakeBroadcastChannel(channelName);
    const now = Date.now();

    newerTab.postMessage({
      type: "offer-session",
      request_id: "requester-0001",
      session: {
        csrf_token: "csrf-from-new-tab",
        expires_at: "2999-08-11T01:00:00.000Z",
        issued_at: now,
        version: "newest-tab-0001",
      },
    });
    staleTab.postMessage({
      type: "offer-session",
      request_id: "requester-0001",
      session: {
        csrf_token: "csrf-from-stale-tab",
        expires_at: "2999-08-11T01:00:00.000Z",
        issued_at: now - 1_000,
        version: "stale-tab-0001",
      },
    });
    staleTab.postMessage({
      type: "offer-session",
      request_id: "requester-0001",
      session: {
        csrf_token: "invalid-schema",
        expires_at: "2999-08-11T01:00:00.000Z",
        issued_at: now + 1,
        version: "invalid-tab-0001",
        bearer: "must-be-rejected",
      },
    });

    await waitFor(() =>
      expect(screen.getByTestId("requester-csrf")).toHaveTextContent(
        "csrf-from-new-tab",
      ),
    );
    expect(window.sessionStorage.getItem(sessionKey)).toContain(
      "csrf-from-new-tab",
    );
    expect(window.sessionStorage.getItem(sessionKey)).not.toContain(
      "csrf-from-stale-tab",
    );
    newerTab.close();
    staleTab.close();
  });

  it("closes its origin-scoped channel when the provider unmounts", async () => {
    const view = render(
      <SessionProvider>
        <Probe name="closing" />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(FakeBroadcastChannel.channels.get(channelName)?.size).toBe(1),
    );
    view.unmount();
    expect(FakeBroadcastChannel.channels.get(channelName)?.size ?? 0).toBe(0);
  });
  it("resolves another account after a sibling tab replaces the cookie session", async () => {
    let second = false;
    vi.stubGlobal(
      "fetch",
      vi.fn(async () =>
        second
          ? accountSession("csrf-second", {
              user_id: "1".repeat(32),
              username: "second",
              display_name: "Second",
              profile_url: "/second",
              role: "member",
            })
          : accountSession("csrf-first"),
      ),
    );
    render(
      <SessionProvider>
        <Probe name="switch" />
      </SessionProvider>,
    );
    await waitFor(() =>
      expect(screen.getByTestId("switch-account")).toHaveTextContent("owner"),
    );
    second = true;
    const peer = new FakeBroadcastChannel(channelName);
    peer.postMessage({
      type: "offer-session",
      request_id: null,
      session: {
        csrf_token: "csrf-second",
        expires_at: "2999-08-11T01:00:00.000Z",
        issued_at: Date.now() + 1000,
        version: "another-account-0001",
      },
    });
    await waitFor(() =>
      expect(screen.getByTestId("switch-account")).toHaveTextContent("second"),
    );
    expect(screen.getByTestId("switch-csrf")).toHaveTextContent("csrf-second");
    peer.close();
  });
});
