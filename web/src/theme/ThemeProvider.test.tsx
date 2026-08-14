import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ThemeProvider, type ThemePreference, useTheme } from "./ThemeProvider";

function Probe() {
  const { preference, resolvedTheme, setPreference } = useTheme();
  return (
    <>
      <output>{`${preference}:${resolvedTheme}`}</output>
      <button
        type="button"
        onClick={() => setPreference("lights-out" satisfies ThemePreference)}
      >
        Dark
      </button>
      <button
        type="button"
        onClick={() => setPreference("light" satisfies ThemePreference)}
      >
        Light
      </button>
    </>
  );
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("ThemeProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    );
  });

  it("follows the system until a local override is chosen", () => {
    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByText("system:light")).toBeInTheDocument();
    act(() => screen.getByRole("button", { name: "Dark" }).click());
    expect(screen.getByText("lights-out:lights-out")).toBeInTheDocument();
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("lights-out");
    expect(document.documentElement).toHaveAttribute(
      "data-theme",
      "lights-out",
    );
  });

  it("keeps an explicit saved light preference independent from a dark system", () => {
    const addEventListener = vi.fn();
    window.localStorage.setItem("moment.theme.v1", "light");
    vi.stubGlobal(
      "matchMedia",
      vi.fn().mockReturnValue({
        matches: true,
        addEventListener,
        removeEventListener: vi.fn(),
      }),
    );

    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByText("light:light")).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    expect(addEventListener).not.toHaveBeenCalled();
  });

  it("tracks system changes while the system preference is active", () => {
    let listener: (() => void) | undefined;
    const media = {
      matches: false,
      addEventListener: vi.fn(
        (_event: string, nextListener: () => void) => (listener = nextListener),
      ),
      removeEventListener: vi.fn(),
    };
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue(media));

    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByText("system:light")).toBeInTheDocument();
    expect(media.addEventListener).toHaveBeenCalledWith(
      "change",
      expect.any(Function),
    );

    act(() => {
      media.matches = true;
      listener?.();
    });
    expect(screen.getByText("system:lights-out")).toBeInTheDocument();

    act(() => screen.getByRole("button", { name: "Light" }).click());
    expect(screen.getByText("light:light")).toBeInTheDocument();
    expect(media.removeEventListener).toHaveBeenCalledWith(
      "change",
      expect.any(Function),
    );
  });
});
