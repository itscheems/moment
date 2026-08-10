import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OwnerProvider, useOwner } from "./OwnerProvider";

const channelName = "moment.owner.session.sync.v1";
const sessionKey = "moment.owner.session.v1";

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

function Probe({ name }: { name: string }) {
  const owner = useOwner();
  return (
    <section aria-label={name}>
      <output data-testid={`${name}-state`}>{owner.state}</output>
      <output data-testid={`${name}-csrf`}>
        {owner.session?.csrfToken ?? "none"}
      </output>
      <button onClick={() => void owner.login("bearer-secret")} type="button">
        login {name}
      </button>
    </section>
  );
}

describe("owner session cross-tab repair", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    FakeBroadcastChannel.reset();
    vi.stubGlobal("BroadcastChannel", FakeBroadcastChannel);
    vi.stubGlobal(
      "fetch",
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/api/v1/owner/status") {
          return json({
            data: { initialized: true, session_epoch: 1 },
            meta: {},
          });
        }
        if (url === "/api/v1/owner/session" && init?.method === "POST") {
          return json({
            data: {
              csrf_token: "csrf-newest",
              expires_at: "2999-08-11T01:00:00.000Z",
            },
            meta: {},
          });
        }
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

  it("answers a new tab handshake without storing or broadcasting the bearer", async () => {
    const view = render(
      <OwnerProvider>
        <Probe name="first" />
      </OwnerProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "login first" }));
    await waitFor(() =>
      expect(screen.getByTestId("first-csrf")).toHaveTextContent("csrf-newest"),
    );

    window.sessionStorage.removeItem(sessionKey);
    view.rerender(
      <>
        <OwnerProvider>
          <Probe name="first" />
        </OwnerProvider>
        <OwnerProvider>
          <Probe name="new-tab" />
        </OwnerProvider>
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
    render(
      <OwnerProvider>
        <Probe name="requester" />
      </OwnerProvider>,
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
      <OwnerProvider>
        <Probe name="closing" />
      </OwnerProvider>,
    );
    await waitFor(() =>
      expect(FakeBroadcastChannel.channels.get(channelName)?.size).toBe(1),
    );
    view.unmount();
    expect(FakeBroadcastChannel.channels.get(channelName)?.size ?? 0).toBe(0);
  });
});
