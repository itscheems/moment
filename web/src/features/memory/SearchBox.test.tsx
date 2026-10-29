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
import { PublicApi } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { SearchBox } from "./RailSearch";
import {
  recentSearchScope,
  rememberSearch,
  rememberUserSearch,
} from "./recentSearches";

const user = {
  userId: "11111111111111111111111111111111",
  username: "cedar",
  displayName: "杉木",
  avatarMediaId: null,
  profileUrl: "/cedar",
};
const wire = {
  user_id: user.userId,
  username: user.username,
  display_name: user.displayName,
  avatar_media_id: null,
  profile_url: user.profileUrl,
};
const scope = recentSearchScope("test-site", "test-account");
const json = (items: unknown[]) =>
  new Response(
    JSON.stringify({
      data: { items, next_offset: null },
      meta: { request_id: "req_user_search" },
    }),
    { headers: { "Content-Type": "application/json" } },
  );
function mount(fetcher: typeof fetch = async () => json([wire])) {
  return render(
    <NavigationProvider>
      <SearchBox api={new PublicApi(fetcher)} scope={scope} main />
    </NavigationProvider>,
  );
}
describe("shared content and people search", () => {
  beforeEach(() => {
    localStorage.clear();
    window.dispatchEvent(new StorageEvent("storage", { key: null }));
    window.history.replaceState({}, "", "/search");
    vi.stubGlobal("scrollTo", vi.fn());
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it("shows mixed history, filters it while typing, and removes one entry without navigating", async () => {
    rememberSearch(scope, "杉木林");
    rememberUserSearch(scope, user);
    mount();
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    expect(
      await screen.findByRole("option", { name: "杉木 @cedar" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "杉木林" })).toBeInTheDocument();
    fireEvent.change(input, { target: { value: "杉" } });
    expect(
      await screen.findByRole("option", { name: "杉木林" }),
    ).toBeInTheDocument();
    expect(
      await screen.findByRole("option", { name: "杉木 @cedar" }),
    ).toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "删除搜索记录：@cedar" }),
    );
    expect(window.location.pathname).toBe("/search");
    expect(localStorage.getItem(scope!)).not.toContain('"kind":"user"');
    fireEvent.change(input, { target: { value: "" } });
    expect(screen.queryByRole("option", { name: "杉木 @cedar" })).toBeNull();
  });
  it("loads users by display name and remembers a selected identity", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => json([wire]));
    mount(fetcher);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "杉木" } });
    const option = await screen.findByRole("option", { name: "杉木 @cedar" });
    expect(String(fetcher.mock.calls[0][0])).toContain("q=%E6%9D%89%E6%9C%A8");
    fireEvent.click(option);
    expect(window.location.pathname).toBe("/cedar");
    expect(localStorage.getItem(scope!)).toContain(
      '"userId":"' + user.userId + '"',
    );
  });
  it("discards responses from a superseded query", async () => {
    let resolveFirst: ((response: Response) => void) | undefined;
    const fetcher = vi.fn((input: RequestInfo | URL) =>
      String(input).includes("q=old")
        ? new Promise<Response>((resolve) => {
            resolveFirst = resolve;
          })
        : Promise.resolve(json([])),
    );
    mount(fetcher);
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "old" } });
    await waitFor(() => expect(resolveFirst).toBeDefined());
    fireEvent.change(input, { target: { value: "new" } });
    await waitFor(() => expect(fetcher).toHaveBeenCalledTimes(2));
    await act(async () => {
      resolveFirst!(json([wire]));
    });
    expect(screen.queryByRole("option", { name: "杉木 @cedar" })).toBeNull();
  });
  it("submits the typed query with Enter and preserves a single history item", async () => {
    mount();
    const input = screen.getByRole("combobox");
    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "pine" } });
    fireEvent.submit(input.closest("form")!);
    expect(window.location.search).toBe("?q=pine");
    expect(JSON.parse(localStorage.getItem(scope!)!)).toEqual(["pine"]);
  });
});
