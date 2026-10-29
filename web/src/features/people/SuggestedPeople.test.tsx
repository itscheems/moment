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
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { authApi, type AccountSession } from "../../api/auth";
import {
  PublicApi,
  PublicApiError,
  type PublicAccount,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { RailSlotContext } from "../../components/railSlot";
import { SessionProvider } from "../auth/SessionProvider";
import { SuggestedPeople } from "./SuggestedPeople";
import { FeedbackProvider } from "../../components/Feedback";

const amber: PublicAccount = {
  userId: "a".repeat(32),
  username: "amber",
  displayName: "Amber",
  bio: "A public biography",
  avatarMediaId: null,
  coverMediaId: null,
  profileUrl: "/amber",
};
const birch: PublicAccount = {
  ...amber,
  userId: "b".repeat(32),
  username: "birch",
  displayName: "Birch",
  profileUrl: "/birch",
};
const usernames = ["amber", "birch"];
const session: AccountSession = {
  preferences: { uiLocale: null },
  csrfToken: "test-csrf",
  expiresAt: "2099-01-01T00:00:00Z",
  account: { ...amber, role: "member", status: "active", usernameChosen: true },
  limits: {
    profilePictureBytes: 8000000,
    displayNameLength: 80,
    bioLength: 500,
  },
};
let slot: HTMLDivElement;
beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  window.history.replaceState({}, "", "/");
  slot = document.createElement("div");
  document.body.append(slot);
  const original = window.matchMedia;
  vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
    ...original(query),
    matches: true,
  }));
  vi.spyOn(authApi, "accountSession").mockRejectedValue(
    new PublicApiError("Signed out", { code: "UNAUTHORIZED", status: 401 }),
  );
});
afterEach(() => {
  cleanup();
  slot.remove();
  vi.restoreAllMocks();
});

function setup() {
  const api = new PublicApi();
  const user = vi
    .spyOn(api, "user")
    .mockImplementation(async (name) => (name === "amber" ? amber : birch));
  const view = (
    kind: "home" | "profile" = "home",
    profileUsername: string | null = null,
    active = true,
  ) => (
    <FeedbackProvider>
      <NavigationProvider>
        <SessionProvider>
          <RailSlotContext.Provider value={slot}>
            <SuggestedPeople
              api={api}
              kind={kind}
              profileUsername={profileUsername}
              active={active}
              usernames={usernames}
            />
          </RailSlotContext.Provider>
        </SessionProvider>
      </NavigationProvider>
    </FeedbackProvider>
  );
  return { view, user };
}

describe("SuggestedPeople", () => {
  it("shows a compact home list for guests without biographies or unsupported actions", async () => {
    const { view } = setup();
    render(view());
    const card = await within(slot).findByRole("region", {
      name: "Who to follow",
    });
    expect(within(card).getAllByRole("listitem")).toHaveLength(2);
    expect(
      within(card).queryByText("A public biography"),
    ).not.toBeInTheDocument();
    expect(within(card).queryByText("Show more")).not.toBeInTheDocument();
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
    fireEvent.keyDown(within(card).getAllByRole("listitem")[0], {
      key: "Enter",
    });
    expect(window.location.pathname).toBe("/amber");
  });

  it("waits for authentication and never briefly recommends the signed-in account", async () => {
    let resolveSession: (value: AccountSession) => void = () => {
      throw new Error("No pending session");
    };
    vi.mocked(authApi.accountSession).mockImplementation(
      () =>
        new Promise((resolve) => {
          resolveSession = resolve;
        }),
    );
    const { view, user } = setup();
    render(view());
    expect(user).not.toHaveBeenCalled();
    expect(slot).toBeEmptyDOMElement();
    await act(async () => resolveSession(session));
    const card = await within(slot).findByRole("region", {
      name: "Who to follow",
    });
    expect(
      within(card).queryByRole("link", { name: "Amber" }),
    ).not.toBeInTheDocument();
    expect(
      within(card).getByRole("link", { name: "Birch" }),
    ).toBeInTheDocument();
  });

  it("filters the viewed profile independently of the signed-in viewer", async () => {
    vi.mocked(authApi.accountSession).mockResolvedValue(session);
    const { view } = setup();
    const mounted = render(view("profile", "birch"));
    const card = await within(slot).findByRole("region", {
      name: "You might like",
    });
    expect(
      within(card).getByRole("link", { name: "Amber" }),
    ).toBeInTheDocument();
    expect(
      within(card).queryByRole("link", { name: "Birch" }),
    ).not.toBeInTheDocument();
    mounted.rerender(view("profile", "amber"));
    await within(slot).findByRole("link", { name: "Birch" });
    expect(
      within(slot).queryByRole("link", { name: "Amber" }),
    ).not.toBeInTheDocument();
  });

  it("removes cached cards when inactive and does not request profiles on narrow screens", async () => {
    const { view, user } = setup();
    const mounted = render(view("home", null, false));
    await waitFor(() => expect(authApi.accountSession).toHaveBeenCalled());
    expect(user).not.toHaveBeenCalled();
    mounted.rerender(view());
    await within(slot).findByRole("region", { name: "Who to follow" });
    mounted.rerender(view("home", null, false));
    expect(slot).toBeEmptyDOMElement();
    user.mockClear();
    const media = window.matchMedia("(min-width: 988px)");
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      ...media,
      media: query,
      matches: false,
    }));
    mounted.unmount();
    render(view());
    await waitFor(() =>
      expect(authApi.accountSession).toHaveBeenCalledTimes(2),
    );
    expect(user).not.toHaveBeenCalled();
    expect(slot).toBeEmptyDOMElement();
  });

  it("retries failed profile reads without keeping an outdated card", async () => {
    const { view, user } = setup();
    user.mockRejectedValueOnce(new Error("Offline"));
    const mounted = render(view("profile", "birch"));
    const retry = await screen.findByRole("button", { name: "重试" });
    expect(slot).not.toContainElement(retry);
    fireEvent.click(retry);
    await within(slot).findByRole("link", { name: "Amber" });
    mounted.rerender(view("home", null, false));
    expect(slot).toBeEmptyDOMElement();
  });
});
