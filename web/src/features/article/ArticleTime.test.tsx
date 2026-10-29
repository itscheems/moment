import i18n from "../../i18n";
import { beforeEach } from "vitest";
beforeEach(() => {
  void i18n.changeLanguage("en");
});
import "@testing-library/jest-dom/vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { ArticleTime } from "./ArticleTime";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});
it("shares a clock, updates only labels, and cleans up after the last label", () => {
  vi.useFakeTimers();
  const date = new Date("2026-10-08T12:00:00Z");
  vi.setSystemTime(date);
  const Parent = vi.fn(() => (
    <>
      <ArticleTime value={date.toISOString()} timezone="Asia/Shanghai" draft />
      <ArticleTime value={date.toISOString()} timezone="Asia/Shanghai" />
      <div>editor remains mounted</div>
    </>
  ));
  const view = render(<Parent />);
  expect(screen.getByText("just now")).toBeInTheDocument();
  expect(vi.getTimerCount()).toBe(1);
  const renders = Parent.mock.calls.length;
  act(() => vi.advanceTimersByTime(6000));
  expect(screen.getByText("6 seconds ago")).toBeInTheDocument();
  expect(screen.getByText("6s")).toBeInTheDocument();
  expect(Parent).toHaveBeenCalledTimes(renders);
  view.unmount();
  expect(vi.getTimerCount()).toBe(0);
});
