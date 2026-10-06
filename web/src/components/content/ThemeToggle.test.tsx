import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it } from "vitest";
import { ThemeProvider } from "../../theme/ThemeProvider";
import { ThemeToggle } from "./notion/components/templates/notion-like/notion-like-editor-theme-toggle";

beforeEach(() => window.localStorage.clear());
afterEach(cleanup);

it("persists the same theme from the template control across remounts", () => {
  const show = () =>
    render(
      <ThemeProvider>
        <ThemeToggle />
      </ThemeProvider>,
    );
  const first = show();
  fireEvent.click(screen.getByRole("button", { name: "切换到暗色模式" }));
  expect(window.localStorage.getItem("moment.theme.v1")).toBe("lights-out");
  expect(document.documentElement).toHaveAttribute("data-theme", "lights-out");
  first.unmount();
  show();
  fireEvent.click(screen.getByRole("button", { name: "切换到浅色模式" }));
  expect(window.localStorage.getItem("moment.theme.v1")).toBe("light");
  expect(document.documentElement).toHaveAttribute("data-theme", "light");
  expect(document.documentElement).toHaveAttribute("data-accent", "blue");
});

it("recognizes an existing dim preference as dark", () => {
  window.localStorage.setItem("moment.theme.v1", "dim");
  render(
    <ThemeProvider>
      <ThemeToggle />
    </ThemeProvider>,
  );
  expect(
    screen.getByRole("button", { name: "切换到浅色模式" }),
  ).toBeInTheDocument();
  expect(window.localStorage.getItem("moment.theme.v1")).toBe("dim");
});
