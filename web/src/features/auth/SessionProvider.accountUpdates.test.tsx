import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Account } from "../../api/public";
import { makeAccountLimits } from "../../test/accountFixtures";
import { SessionProvider, useSession } from "./SessionProvider";

const first: Account = {
  userId: "1".repeat(32),
  username: "first",
  displayName: "First",
  bio: "",
  avatarMediaId: null,
  coverMediaId: null,
  profileUrl: "/first",
  role: "admin",
  status: "active",
  usernameChosen: true,
};
const second: Account = {
  ...first,
  userId: "2".repeat(32),
  username: "second",
  profileUrl: "/second",
  role: "member",
};
let current = first;
let auth: ReturnType<typeof useSession>;

function Probe() {
  auth = useSession();
  return (
    <output data-testid="identity">
      {auth.account?.username}|{auth.account?.displayName}|{auth.account?.role}|
      {auth.session?.csrfToken}
    </output>
  );
}

beforeEach(() => {
  current = first;
  sessionStorage.clear();
  vi.stubGlobal("BroadcastChannel", undefined);
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            data: {
              account: {
                user_id: current.userId,
                username: current.username,
                display_name: current.displayName,
                bio: current.bio,
                avatar_media_id: null,
                cover_media_id: null,
                profile_url: current.profileUrl,
                role: current.role,
                status: current.status,
                username_chosen: true,
              },
              csrf_token: `csrf-${current.username}`,
              expires_at: "2999-01-01T00:00:00Z",
              preferences: { ui_locale: null },
              limits: makeAccountLimits(),
            },
            meta: {},
          }),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
    ),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

async function start() {
  render(
    <SessionProvider>
      <Probe />
    </SessionProvider>,
  );
  await waitFor(() => expect(auth.state).toBe("signed-in"));
}

async function switchTo(next: Account) {
  current = next;
  await act(async () => {
    await auth.refreshSession();
  });
}

it("rejects account updates captured before switching to a different identity", async () => {
  await start();
  const update = auth.applyAccount;
  await switchTo(second);
  act(() => {
    expect(update({ ...first, displayName: "Obsolete" })).toBe(false);
  });
  expect(screen.getByTestId("identity")).toHaveTextContent(
    "second|First|member|csrf-second",
  );
});

it("rejects an earlier generation after returning to the same account", async () => {
  await start();
  const update = auth.applyAccount;
  await switchTo(second);
  await switchTo(first);
  act(() => {
    expect(update({ ...first, displayName: "Obsolete" })).toBe(false);
  });
  expect(auth.account?.displayName).toBe("First");
});

it("invalidates previous updates as soon as session replacement starts", async () => {
  await start();
  const update = auth.applyAccount;
  current = second;
  await act(async () => {
    const replacement = auth.refreshSession();
    expect(update({ ...first, displayName: "Obsolete" })).toBe(false);
    await replacement;
  });
  expect(auth.account?.userId).toBe(second.userId);
});

it("accepts current profile changes while refusing a different account payload", async () => {
  await start();
  act(() => {
    expect(auth.applyAccount(second)).toBe(false);
  });
  act(() => {
    expect(auth.applyAccount({ ...first, displayName: "Updated" })).toBe(true);
  });
  expect(screen.getByTestId("identity")).toHaveTextContent(
    "first|Updated|admin|csrf-first",
  );
});
