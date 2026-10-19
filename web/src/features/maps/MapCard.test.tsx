import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { MapCard } from "./MapCard";
import type { MapCardData } from "./types";

const card: MapCardData = {
  places: [
    {
      name: "东京",
      displayName: "日本",
      latitude: 35.67,
      longitude: 139.76,
      coordinateSystem: "wgs84",
    },
  ],
  connectPoints: false,
};

beforeEach(() =>
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe() {}
      disconnect() {}
    },
  ),
);
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

it("keeps removal exclusive to editing and puts places in a collapsible fullscreen panel", async () => {
  render(<MapCard card={card} />);
  expect(screen.queryByRole("button", { name: "移除地图" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "展开地图" }));
  const dialog = screen.getByRole("dialog", { name: "地图" });
  expect(dialog.hasAttribute("data-map-fullscreen")).toBe(true);
  const toggle = within(dialog).getByRole("button", { name: "收起地点列表" });
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  expect(within(dialog).getByRole("button", { name: "展开地点列表" })).toBe(
    toggle,
  );
  fireEvent.click(toggle);
  expect(within(dialog).getByRole("button", { name: "前往东京" })).toBeTruthy();
  expect(
    within(dialog).getByRole("link", { name: "在 Google Maps 打开东京" }),
  ).toBeTruthy();
});

it("removes the entire card from its edit-only close control", () => {
  const onChange = vi.fn();
  render(<MapCard card={card} onChange={onChange} />);
  fireEvent.click(screen.getByRole("button", { name: "移除地图" }));
  expect(onChange).toHaveBeenCalledWith(null);
});
