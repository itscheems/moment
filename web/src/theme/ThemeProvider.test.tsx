import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { ThemeProvider, type ThemePreference, useTheme } from "./ThemeProvider";

function Probe() {
  const { accent, preference, resolvedTheme, setAccent, setPreference } =
    useTheme();
  return (
    <>
      <output>{`${preference}:${resolvedTheme}`}</output>
      <output aria-label="accent">{accent}</output>
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
      <button
        type="button"
        onClick={() => setPreference("dim" satisfies ThemePreference)}
      >
        Dim
      </button>
      <button type="button" onClick={() => setAccent("black")}>
        Black accent
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
  });

  it("defaults to light until an explicit background is chosen", () => {
    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByText("light:light")).toBeInTheDocument();
    act(() => screen.getByRole("button", { name: "Dim" }).click());
    expect(screen.getByText("dim:dim")).toBeInTheDocument();
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("dim");
    act(() => screen.getByRole("button", { name: "Dark" }).click());
    expect(screen.getByText("lights-out:lights-out")).toBeInTheDocument();
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("lights-out");
    expect(document.documentElement).toHaveAttribute(
      "data-theme",
      "lights-out",
    );
  });

  it("migrates a legacy system preference to light", () => {
    window.localStorage.setItem("moment.theme.v1", "system");

    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByText("light:light")).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("light");
  });

  it("keeps an explicit saved light preference", () => {
    window.localStorage.setItem("moment.theme.v1", "light");

    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByText("light:light")).toBeInTheDocument();
    expect(document.documentElement).toHaveAttribute("data-theme", "light");
  });

  it("restores and persists the black accent", () => {
    window.localStorage.setItem("moment.accent.v1", "black");

    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );

    expect(screen.getByLabelText("accent")).toHaveTextContent("black");
    expect(document.documentElement).toHaveAttribute("data-accent", "black");
    act(() => screen.getByRole("button", { name: "Black accent" }).click());
    expect(window.localStorage.getItem("moment.accent.v1")).toBe("black");
  });
});
