import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";

import { LoadMoreSentinel } from "./LoadMoreSentinel";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("keeps a disabled frontier in the document without observing or loading it", () => {
  const observe = vi.fn();
  const disconnect = vi.fn();
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe = observe;
      disconnect = disconnect;
    },
  );
  const onLoad = vi.fn();
  const props = { cursor: "next-page", label: "More", loading: false, onLoad };
  const view = render(<LoadMoreSentinel {...props} enabled={false} />);
  const button = screen.getByRole("button", { name: "More" });
  expect(button).toBeDisabled();
  expect(observe).not.toHaveBeenCalled();
  fireEvent.click(button);
  expect(onLoad).not.toHaveBeenCalled();

  view.rerender(<LoadMoreSentinel {...props} enabled />);
  expect(screen.getByRole("button", { name: "More" })).toBe(button);
  expect(observe).toHaveBeenCalledWith(button.parentElement);
  expect(button).toBeEnabled();
  fireEvent.click(button);
  expect(onLoad).toHaveBeenCalledWith("manual");

  view.rerender(<LoadMoreSentinel {...props} enabled={false} />);
  expect(disconnect).toHaveBeenCalledOnce();
});
