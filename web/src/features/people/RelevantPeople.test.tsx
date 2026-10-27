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
import {
  PublicApi,
  type PublicAccount,
  type PublicPost,
} from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { MentionProvider } from "../../app/mentions";
import { RailSlotContext } from "../../components/railSlot";
import { RelevantPeople } from "./RelevantPeople";

const alice: PublicAccount = {
  userId: "alice",
  username: "alice",
  displayName: "Alice",
  bio: "A biography\nhttps://example.com/about",
  avatarMediaId: null,
  coverMediaId: null,
  profileUrl: "/alice",
};
const bob: PublicAccount = {
  ...alice,
  userId: "bob",
  username: "bobby",
  displayName: "Bob",
  profileUrl: "/bobby",
  bio: "",
};
const post: PublicPost = {
  id: "post",
  kind: "moment",
  author: alice,
  contentMd: "Post",
  contentHtml: "<p>Post</p>",
  article: null,
  createdAt: "2026-10-27T01:00:00Z",
  updatedAt: "2026-10-27T01:00:00Z",
  publishedAt: "2026-10-27T01:00:00Z",
  repostedAt: null,
  visibility: "everyone",
  timelineItemKind: "original",
  commentCount: 0,
  media: [],
  linkCards: [],
};
let slot: HTMLDivElement;
beforeEach(() => {
  slot = document.createElement("div");
  document.body.append(slot);
  const original = window.matchMedia;
  vi.spyOn(window, "matchMedia").mockImplementation((query) => ({
    ...original(query),
    matches: true,
  }));
  window.history.replaceState({}, "", "/m/post");
});
afterEach(() => {
  cleanup();
  slot.remove();
  vi.restoreAllMocks();
});

function setup() {
  const api = new PublicApi();
  const user = vi.spyOn(api, "user").mockResolvedValue(alice);
  const view = (selected = post, active = true) => (
    <NavigationProvider>
      <MentionProvider identities={[alice, bob]}>
        <RailSlotContext.Provider value={slot}>
          <p>Reading stays available</p>
          <RelevantPeople api={api} post={selected} active={active} />
        </RailSlotContext.Provider>
      </MentionProvider>
    </NavigationProvider>
  );
  return { api, user, view };
}

describe("RelevantPeople", () => {
  it("renders the real profile in the rail with links and no follow action", async () => {
    const { view } = setup();
    render(view());
    const card = await within(slot).findByRole("region", { name: "相关的人" });
    expect(within(card).getByRole("link", { name: "Alice" })).toHaveAttribute(
      "href",
      "/alice",
    );
    expect(within(card).getByText("@alice")).toBeInTheDocument();
    expect(
      within(card).getByRole("link", { name: "https://example.com/about" }),
    ).toHaveAttribute("rel", "noreferrer");
    expect(within(card).queryByRole("button")).not.toBeInTheDocument();
    fireEvent.keyDown(within(card).getByRole("listitem"), { key: "Enter" });
    expect(window.location.pathname).toBe("/alice");
  });

  it("does not load profiles or publish a portal for an inactive cached detail", async () => {
    const { view, user } = setup();
    const mounted = render(view(post, false));
    expect(user).not.toHaveBeenCalled();
    mounted.rerender(view());
    await within(slot).findByRole("region", { name: "相关的人" });
    mounted.rerender(view(post, false));
    expect(slot).toBeEmptyDOMElement();
  });

  it("opens the account preview from its handle and closes it before profile navigation", async () => {
    const { view } = setup();
    render(view());
    const handle = await within(slot).findByRole("link", { name: "@alice" });
    fireEvent.focus(handle);
    const heading = await screen.findByRole("heading", {
      name: "Alice",
    });
    const preview = heading.closest('[class*="identityCard"]');
    expect(preview?.textContent).toContain(alice.bio);
    fireEvent.click(within(heading).getByRole("link", { name: "Alice" }));
    expect(window.location.pathname).toBe("/alice");
    expect(
      screen.queryByRole("heading", { name: "Alice" }),
    ).not.toBeInTheDocument();
  });

  it("allows copying biography text before using the surrounding row to navigate", async () => {
    const { view } = setup();
    render(view());
    const row = await within(slot).findByRole("listitem");
    const bio = within(row).getByText(/A biography/);
    const range = document.createRange();
    range.selectNodeContents(bio);
    const selection = window.getSelection();
    selection?.removeAllRanges();
    selection?.addRange(range);
    fireEvent.click(bio);
    expect(window.location.pathname).toBe("/m/post");
    selection?.removeAllRanges();
    fireEvent.click(row);
    expect(window.location.pathname).toBe("/alice");
  });

  it("does not request or render the card below the existing rail breakpoint", () => {
    vi.mocked(window.matchMedia).mockImplementation((query) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
      dispatchEvent: () => false,
    }));
    const { view, user } = setup();
    render(view());
    expect(user).not.toHaveBeenCalled();
    expect(slot).toBeEmptyDOMElement();
  });

  it("ignores an old profile response after navigating to a different post", async () => {
    const { view, user } = setup();
    let resolveOld: (value: PublicAccount) => void = () => {
      throw new Error("No pending request");
    };
    user
      .mockImplementationOnce(
        () =>
          new Promise((resolve) => {
            resolveOld = resolve;
          }),
      )
      .mockResolvedValue(bob);
    const mounted = render(view());
    await waitFor(() => expect(user).toHaveBeenCalledOnce());
    mounted.rerender(view({ ...post, id: "second", author: bob }));
    await within(slot).findByRole("link", { name: "Bob" });
    await act(async () => resolveOld(alice));
    expect(
      within(slot).queryByRole("link", { name: "Alice" }),
    ).not.toBeInTheDocument();
  });

  it("retries a failed supplementary read without blocking the post", async () => {
    const { view, user } = setup();
    user.mockRejectedValueOnce(new Error("Offline")).mockResolvedValue(alice);
    render(view());
    const retry = await within(slot).findByRole("button", { name: "重试" });
    expect(screen.getByText("Reading stays available")).toBeInTheDocument();
    fireEvent.click(retry);
    await within(slot).findByRole("link", { name: "Alice" });
    expect(
      within(slot).queryByText("暂时无法读取相关的人。"),
    ).not.toBeInTheDocument();
  });
});
