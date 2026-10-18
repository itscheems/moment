import "@testing-library/jest-dom/vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { NavigationProvider } from "../../app/navigation";
import { settingsSections, useSettingsSections } from "./useSettingsSections";

function SettingsHarness() {
  const { section, navRef, contentRef, goToSection } =
    useSettingsSections(true);
  return (
    <>
      <nav ref={navRef}>
        {settingsSections.map((id) => (
          <a
            key={id}
            href={`/settings/${id}`}
            onClick={(event) => goToSection(event, id)}
          >
            {id}
          </a>
        ))}
      </nav>
      <output>{section}</output>
      <div ref={contentRef}>
        {settingsSections.map((id) => (
          <div id={`settings-${id}`} key={id} />
        ))}
      </div>
    </>
  );
}
let frames: Map<number, FrameRequestCallback>;
let sequence: string[];
let nextFrame: number;
let scrollDescriptor: PropertyDescriptor | undefined;
function paint() {
  act(() => {
    const pending = [...frames.values()];
    frames.clear();
    pending.forEach((callback) => callback(0));
  });
}
beforeEach(() => {
  scrollDescriptor = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    "scrollIntoView",
  );
  frames = new Map();
  sequence = [];
  nextFrame = 0;
  window.history.replaceState({}, "", "/settings/account");
  vi.stubGlobal("requestAnimationFrame", (callback: FrameRequestCallback) => {
    frames.set(++nextFrame, callback);
    return nextFrame;
  });
  vi.stubGlobal("cancelAnimationFrame", (frame: number) =>
    frames.delete(frame),
  );
  vi.stubGlobal("scrollTo", () => sequence.push("restore history position"));
  vi.stubGlobal("matchMedia", () => ({ matches: false }));
  Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
    configurable: true,
    value: vi.fn(function (this: HTMLElement, options: ScrollIntoViewOptions) {
      sequence.push(`${this.id}: ${options.behavior}`);
    }),
  });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  if (scrollDescriptor)
    Object.defineProperty(
      HTMLElement.prototype,
      "scrollIntoView",
      scrollDescriptor,
    );
  else Reflect.deleteProperty(HTMLElement.prototype, "scrollIntoView");
});

it("keeps the form surface mounted and anchors an explicit category click", () => {
  render(
    <NavigationProvider>
      <SettingsHarness />
    </NavigationProvider>,
  );
  paint();
  const account = document.getElementById("settings-account");
  fireEvent.click(screen.getByRole("link", { name: "site" }));
  paint();
  expect(window.location.pathname).toBe("/settings/site");
  expect(document.getElementById("settings-account")).toBe(account);
  expect(sequence).toEqual([
    "settings-account: instant",
    "settings-site: smooth",
  ]);
});

it("applies a history section anchor after generic history scroll restoration", () => {
  render(
    <NavigationProvider>
      <SettingsHarness />
    </NavigationProvider>,
  );
  paint();
  sequence = [];
  act(() => {
    const state = { momentInternal: true, momentScrollY: 38 };
    window.history.replaceState(state, "", "/settings/members");
    window.dispatchEvent(new PopStateEvent("popstate", { state }));
  });
  paint();
  expect(sequence).toEqual([
    "restore history position",
    "settings-members: instant",
  ]);
});

it("respects reduced motion for both a new and an already selected category", () => {
  vi.stubGlobal("matchMedia", () => ({ matches: true }));
  render(
    <NavigationProvider>
      <SettingsHarness />
    </NavigationProvider>,
  );
  paint();
  sequence = [];
  fireEvent.click(screen.getByRole("link", { name: "site" }));
  paint();
  fireEvent.click(screen.getByRole("link", { name: "site" }));
  paint();
  expect(sequence).toEqual([
    "settings-site: instant",
    "settings-site: instant",
  ]);
});
