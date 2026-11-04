import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { ThemeProvider } from "./ThemeProvider";
import { ThemeToggle } from "../components/content/notion/components/templates/notion-like/notion-like-editor-theme-toggle";

const root = document.documentElement;
const originalStart = Object.getOwnPropertyDescriptor(
  document,
  "startViewTransition",
);
const originalAnimate = Object.getOwnPropertyDescriptor(root, "animate");

function deferred() {
  let resolve!: () => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<void>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

function browserTransition(reduce = false) {
  const ready = deferred();
  const finished = deferred();
  const skip = vi.fn();
  const cancel = vi.fn();
  const animate = vi.fn<
    (
      frames: PropertyIndexedKeyframes,
      options: KeyframeAnimationOptions,
    ) => { cancel: typeof cancel }
  >(() => ({ cancel }));
  const capturedThemes: (string | undefined)[] = [];
  const start = vi.fn((update: () => void) => {
    update();
    capturedThemes.push(root.dataset.theme);
    return {
      ready: ready.promise,
      finished: finished.promise,
      skipTransition: skip,
    };
  });
  Object.defineProperty(document, "startViewTransition", {
    configurable: true,
    value: start,
  });
  Object.defineProperty(root, "animate", {
    configurable: true,
    value: animate,
  });
  vi.spyOn(window, "matchMedia").mockReturnValue({
    matches: reduce,
    media: "(prefers-reduced-motion: reduce)",
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(() => true),
  });
  return { ready, finished, skip, cancel, start, animate, capturedThemes };
}

function show() {
  return render(
    <ThemeProvider>
      <ThemeToggle />
    </ThemeProvider>,
  );
}

beforeEach(() => window.localStorage.clear());
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  if (originalStart)
    Object.defineProperty(document, "startViewTransition", originalStart);
  else Reflect.deleteProperty(document, "startViewTransition");
  if (originalAnimate) Object.defineProperty(root, "animate", originalAnimate);
  else Reflect.deleteProperty(root, "animate");
});

it("captures the persisted new palette and reveals it from the toggle, ignoring overlapping clicks", async () => {
  const browser = browserTransition();
  show();
  const button = screen.getByRole("button", { name: "切换到暗色模式" });
  vi.spyOn(button, "getBoundingClientRect").mockReturnValue(
    new DOMRect(100, 50, 32, 32),
  );
  fireEvent.click(button);
  expect(browser.capturedThemes).toEqual(["lights-out"]);
  expect(window.localStorage.getItem("moment.theme.v1")).toBe("lights-out");
  expect(root).toHaveAttribute("data-theme-transition", "active");
  expect(root.style.getPropertyValue("--theme-transition-from")).toContain(
    "circle(0% at",
  );
  fireEvent.click(screen.getByRole("button", { name: "切换到浅色模式" }));
  expect(browser.start).toHaveBeenCalledTimes(1);

  await act(async () => browser.ready.resolve());
  expect(browser.animate).toHaveBeenCalledWith(
    {
      clipPath: [
        expect.stringContaining("circle(0% at"),
        expect.stringContaining("circle("),
      ],
    },
    expect.objectContaining({
      duration: 400,
      pseudoElement: "::view-transition-new(root)",
      fill: "forwards",
    }),
  );
  await act(async () => browser.finished.resolve());
  expect(root).not.toHaveAttribute("data-theme-transition");
  expect(root.style.getPropertyValue("--theme-transition-from")).toBe("");
  expect(browser.cancel).toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "切换到浅色模式" }));
  expect(browser.start).toHaveBeenCalledTimes(2);
  expect(root).toHaveAttribute("data-theme", "light");
});

it("switches directly when reduced motion is requested", () => {
  const browser = browserTransition(true);
  show();
  fireEvent.click(screen.getByRole("button", { name: "切换到暗色模式" }));
  expect(root).toHaveAttribute("data-theme", "lights-out");
  expect(window.localStorage.getItem("moment.theme.v1")).toBe("lights-out");
  expect(browser.start).not.toHaveBeenCalled();
  expect(root).not.toHaveAttribute("data-theme-transition");
});

it("keeps the selected theme usable if the browser skips the snapshot", async () => {
  const browser = browserTransition();
  const errors = vi.spyOn(console, "error").mockImplementation(() => {});
  show();
  fireEvent.click(screen.getByRole("button", { name: "切换到暗色模式" }));
  await act(async () =>
    browser.ready.reject(new DOMException("Skipped", "AbortError")),
  );
  expect(browser.skip).toHaveBeenCalled();
  expect(root).not.toHaveAttribute("data-theme-transition");
  expect(root).toHaveAttribute("data-theme", "lights-out");
  expect(errors).not.toHaveBeenCalled();
});

it("releases the transition when leaving the page before its snapshots are ready", async () => {
  const browser = browserTransition();
  const mounted = show();
  fireEvent.click(screen.getByRole("button", { name: "切换到暗色模式" }));
  mounted.unmount();
  expect(browser.skip).toHaveBeenCalled();
  expect(root).not.toHaveAttribute("data-theme-transition");
  await act(async () => browser.ready.resolve());
  expect(browser.animate).not.toHaveBeenCalled();
});
