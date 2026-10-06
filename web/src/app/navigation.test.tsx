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
import { useEffect, useRef } from "react";

import {
  NavigationProvider,
  type NavigationBlocker,
  useNavigation,
} from "./navigation";

function NavigationHarness({
  blockers,
}: {
  blockers: ReadonlyArray<NavigationBlocker>;
}) {
  const navigation = useNavigation();
  const blockerIds = useRef(blockers.map(() => Symbol("navigation-test")));

  useEffect(() => {
    blockers.forEach((blocker, index) => {
      navigation.setNavigationBlocker(blockerIds.current[index]!, blocker);
    });
    return () => {
      blockerIds.current.forEach((id) => {
        navigation.setNavigationBlocker(id, null);
      });
    };
  }, [blockers, navigation.setNavigationBlocker]);

  return (
    <div>
      <output data-testid="location">{navigation.pathname}</output>
      <output data-testid="overlay">
        {navigation.overlayFrom?.pathname ?? "none"}
      </output>
      <button onClick={() => navigation.navigate("/current")} type="button">
        Navigate
      </button>
      <button onClick={() => navigation.goBack("/fallback")} type="button">
        Go back
      </button>
      <button onClick={() => navigation.replace("/replaced")} type="button">
        Replace
      </button>
      <button
        onClick={() =>
          navigation.navigate("/owner/articles", { preserveScroll: true })
        }
        type="button"
      >
        Filter
      </button>
      <button
        onClick={() => navigation.navigateOverlay("/overlay")}
        type="button"
      >
        Overlay
      </button>
    </div>
  );
}

describe("guarded navigation history", () => {
  beforeEach(() => {
    window.history.replaceState({ momentScrollY: 0 }, "", "/previous");
    vi.stubGlobal("scrollTo", vi.fn());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("keeps every blocker and requires all of them to approve", () => {
    const first = vi.fn(() => true);
    const newest = vi.fn(() => false);
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[first, newest]} />
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/previous");
    expect(newest).toHaveBeenCalledOnce();
    expect(first).not.toHaveBeenCalled();

    newest.mockReturnValue(true);
    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/current");
    expect(newest).toHaveBeenCalledTimes(2);
    expect(first).toHaveBeenCalledOnce();
  });

  it("resumes the original destination after asynchronous approval", () => {
    let allow = false;
    let resume: (() => void) | null = null;
    const blocker = vi.fn((next: () => void) => {
      resume = next;
      return allow;
    });
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[blocker]} />
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/previous");
    expect(resume).not.toBeNull();

    allow = true;
    act(() => resume?.());
    expect(screen.getByTestId("location")).toHaveTextContent("/current");
    expect(blocker).toHaveBeenCalledTimes(2);
  });

  it("preserves scroll for an in-page filter, including deferred navigation", () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(640);
    let allowed = false;
    let resume: (() => void) | undefined;
    render(
      <NavigationProvider>
        <NavigationHarness
          blockers={[
            (next) => {
              resume = next;
              return allowed;
            },
          ]}
        />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Filter" }));
    expect(window.location.pathname).toBe("/previous");
    allowed = true;
    act(() => resume?.());
    expect(window.location.pathname).toBe("/owner/articles");
    expect(window.history.state).toMatchObject({
      momentScrollY: 640,
      momentFresh: false,
    });
    expect(window.scrollTo).not.toHaveBeenCalled();
    // Ordinary navigation still opens at the top.
    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    expect(window.scrollTo).toHaveBeenCalledWith({ top: 0, behavior: "auto" });
  });

  it("tracks an internal overlay without treating a refreshed URL as layered", () => {
    const { unmount } = render(
      <NavigationProvider>
        <NavigationHarness blockers={[]} />
      </NavigationProvider>,
    );

    fireEvent.click(screen.getByRole("button", { name: "Overlay" }));
    expect(screen.getByTestId("location")).toHaveTextContent("/overlay");
    expect(screen.getByTestId("overlay")).toHaveTextContent("/previous");

    unmount();
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[]} />
      </NavigationProvider>,
    );
    expect(screen.getByTestId("location")).toHaveTextContent("/overlay");
    expect(screen.getByTestId("overlay")).toHaveTextContent("none");
  });

  it("rejects browser back once and restores the current entry", async () => {
    let allow = true;
    const blocker = vi.fn(() => allow);
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[blocker]} />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    expect(window.location.pathname).toBe("/current");
    window.history.replaceState(
      { ...window.history.state, momentScrollY: 480 },
      "",
      window.location.href,
    );
    blocker.mockClear();
    vi.mocked(window.scrollTo).mockClear();
    allow = false;

    act(() => window.history.back());

    await waitFor(() =>
      expect(window.scrollTo).toHaveBeenCalledWith({
        top: 480,
        behavior: "auto",
      }),
    );
    expect(blocker).toHaveBeenCalledOnce();
    expect(window.location.pathname).toBe("/current");
    expect(screen.getByTestId("location")).toHaveTextContent("/current");
  });

  it("accepts browser back with one guard pass", async () => {
    const blocker = vi.fn(() => true);
    vi.spyOn(window, "scrollY", "get").mockReturnValue(96);
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[blocker]} />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    blocker.mockClear();
    vi.mocked(window.scrollTo).mockClear();

    act(() => window.history.back());

    await waitFor(() => expect(window.location.pathname).toBe("/previous"));
    expect(screen.getByTestId("location")).toHaveTextContent("/previous");
    expect(blocker).toHaveBeenCalledOnce();
    await waitFor(() =>
      expect(window.scrollTo).toHaveBeenCalledWith({
        top: 96,
        behavior: "auto",
      }),
    );
  });

  // A redirect overwrites the entry that asked for it and keeps that entry's
  // way back. Pushed instead, a page that redirects to sign-in sat one step
  // behind the sign-in page forever: back re-entered it, it redirected again.
  it("replaces the current entry in place and keeps its way back", async () => {
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[]} />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    const entries = window.history.length;

    fireEvent.click(screen.getByRole("button", { name: "Replace" }));
    expect(window.location.pathname).toBe("/replaced");
    expect(window.history.length).toBe(entries);
    expect(screen.getByTestId("location")).toHaveTextContent("/replaced");

    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    await waitFor(() => expect(window.location.pathname).toBe("/previous"));
  });

  it("falls back from a replaced first entry instead of leaving the app", async () => {
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[]} />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Replace" }));
    expect(window.location.pathname).toBe("/replaced");

    fireEvent.click(screen.getByRole("button", { name: "Go back" }));
    await waitFor(() => expect(window.location.pathname).toBe("/fallback"));
  });

  it("does not guard an approved goBack transition twice", async () => {
    const blocker = vi.fn(() => true);
    render(
      <NavigationProvider>
        <NavigationHarness blockers={[blocker]} />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Navigate" }));
    blocker.mockClear();

    fireEvent.click(screen.getByRole("button", { name: "Go back" }));

    await waitFor(() => expect(window.location.pathname).toBe("/previous"));
    expect(screen.getByTestId("location")).toHaveTextContent("/previous");
    expect(blocker).toHaveBeenCalledOnce();
  });
});
