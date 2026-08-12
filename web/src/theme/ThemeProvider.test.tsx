import "@testing-library/jest-dom/vitest";
import { act, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

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
    </>
  );
}

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
});
