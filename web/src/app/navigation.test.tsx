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

import { NavigationProvider, useNavigation } from "./navigation";

function NavigationHarness({
  blockers,
}: {
  blockers: ReadonlyArray<() => boolean>;
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
      <button onClick={() => navigation.navigate("/current")} type="button">
        Navigate
      </button>
      <button onClick={() => navigation.goBack("/fallback")} type="button">
        Go back
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
