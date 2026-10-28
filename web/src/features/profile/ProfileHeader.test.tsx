import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { PublicApi } from "../../api/public";
import { NavigationProvider } from "../../app/navigation";
import { HeaderActionsContext } from "../../components/headerActions";
import { ProfileHeader, ProfileHeaderContext } from "./ProfileHeader";

vi.mock("../auth/SessionProvider", () => ({
  useSession: () => ({ account: null }),
}));

afterEach(cleanup);

it("uses the complete author total and cancels a count when the viewed author changes", async () => {
  const timeline = vi
    .fn()
    .mockResolvedValue({ items: [], authorPostCount: 137 });
  const api = { timeline } as unknown as PublicApi;
  const setHeader = vi.fn();
  const page = (userId: string, title: string) => (
    <NavigationProvider>
      <ProfileHeaderContext.Provider value={setHeader}>
        <HeaderActionsContext.Provider value={document.body}>
          <ProfileHeader api={api} userId={userId} title={title} />
        </HeaderActionsContext.Provider>
      </ProfileHeaderContext.Provider>
    </NavigationProvider>
  );
  const view = render(page("a".repeat(32), "小满"));
  await waitFor(() =>
    expect(setHeader).toHaveBeenLastCalledWith({
      userId: "a".repeat(32),
      title: "小满",
      postCount: 137,
    }),
  );
  expect(screen.getByRole("link", { name: "搜索 Moment" })).toHaveAttribute(
    "href",
    "/search",
  );
  const firstSignal = timeline.mock.calls[0][0].signal as AbortSignal;
  timeline.mockResolvedValueOnce({ items: [], authorPostCount: 0 });
  view.rerender(page("b".repeat(32), "阿明"));
  await waitFor(() =>
    expect(setHeader).toHaveBeenLastCalledWith({
      userId: "b".repeat(32),
      title: "阿明",
      postCount: 0,
    }),
  );
  expect(firstSignal.aborted).toBe(true);
  expect(timeline).toHaveBeenLastCalledWith(
    expect.objectContaining({ authorUserId: "b".repeat(32), limit: 1 }),
  );
  view.unmount();
  expect(setHeader).toHaveBeenLastCalledWith(null);
});
