import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { App } from "./App";

describe("App shell", () => {
  beforeEach(() => {
    window.history.replaceState({}, "", "/");
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

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("keeps the timeline central with landmarked desktop and mobile navigation", () => {
    const { container } = render(<App />);

    expect(screen.getByRole("main")).toHaveAttribute("id", "main-content");
    expect(
      screen.getByRole("navigation", { name: "主要导航" }),
    ).toBeInTheDocument();
    const mobileNavigation = container.querySelector<HTMLElement>(
      'nav[aria-label="移动导航"]',
    );
    expect(mobileNavigation).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "时间流" })).toBeInTheDocument();
    expect(
      mobileNavigation?.querySelector('a[aria-label="发布"]'),
    ).toHaveAttribute("href", "/owner/compose");
    expect(screen.getByRole("link", { name: "发布 Moment" })).toHaveAttribute(
      "href",
      "/owner/compose",
    );
  });

  it("renders loading and error examples without replacing the route shell", () => {
    window.history.replaceState({}, "", "/search?state=loading");
    const { unmount } = render(<App />);
    expect(screen.getByRole("heading", { name: "搜索" })).toBeInTheDocument();
    expect(screen.getByLabelText("正在加载时间流")).toHaveAttribute(
      "aria-busy",
      "true",
    );

    unmount();
    window.history.replaceState({}, "", "/archive?state=error");
    render(<App />);
    expect(screen.getByRole("alert")).toHaveTextContent("这次没有取回时间流");
  });

  it("opens the accessible Base UI theme menu and persists an override", async () => {
    render(<App />);
    const triggers = screen.getAllByRole("button", { name: "选择主题" });
    fireEvent.click(triggers[0]);

    const dark = await screen.findByRole("menuitemradio", { name: "深色" });
    fireEvent.click(dark);
    await waitFor(() =>
      expect(document.documentElement).toHaveAttribute("data-theme", "dark"),
    );
    expect(window.localStorage.getItem("moment.theme.v1")).toBe("dark");
    await waitFor(() => expect(triggers[0]).toHaveFocus());
  });

  it("gives active navigation a semantic current-page state", () => {
    window.history.replaceState({}, "", "/archive");
    render(<App />);
    expect(screen.getAllByRole("link", { name: "归档" })[0]).toHaveAttribute(
      "aria-current",
      "page",
    );
  });
});
