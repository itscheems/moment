import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
  waitFor,
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

it("keeps drag announcements outside editable documents during modal isolation", async () => {
  const { container } = render(
    <div contentEditable suppressContentEditableWarning>
      <p>Document paragraph</p>
      <MapCard card={card} onChange={vi.fn()} editorBlock />
    </div>,
  );
  await waitFor(() => {
    expect(document.body.querySelector('[aria-live="assertive"]')).toBeTruthy();
  });
  expect(container.querySelector('[aria-live="assertive"]')).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "展开地图" }));
  expect(screen.getByRole("dialog", { name: "地图" })).toBeTruthy();
  expect(container.querySelector("p")?.hasAttribute("aria-hidden")).toBe(false);
});

const manyPlaces: MapCardData = {
  connectPoints: true,
  places: Array.from({ length: 13 }, (_, index) => ({
    ...card.places[0],
    name: `地点${index + 1}`,
    longitude: 139 + index / 10,
  })),
};

it("shows only the first place in previews while fullscreen keeps the complete list", () => {
  render(<MapCard card={manyPlaces} preview />);
  expect(screen.getAllByRole("listitem")).toHaveLength(1);
  expect(screen.getByRole("button", { name: "前往地点1" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "展开地图" }));
  const dialog = screen.getByRole("dialog", { name: "地图" });
  expect(within(dialog).getAllByRole("listitem")).toHaveLength(13);
});

it("opens a detail list in pages of ten and collapses it from the same trigger", async () => {
  render(<MapCard card={manyPlaces} />);
  expect(screen.queryByRole("list", { name: "地图地点" })).toBeNull();
  const toggle = screen.getByRole("button", { name: "展开地点列表" });
  fireEvent.click(toggle);
  expect(screen.getByRole("button", { name: "收起地点列表" })).toBe(toggle);
  expect(screen.getByRole("list", { name: "地图地点" }).children).toHaveLength(
    10,
  );
  fireEvent.click(screen.getByRole("button", { name: "下一页地点" }));
  await waitFor(() => {
    expect(
      screen.getByRole("list", { name: "地图地点" }).children,
    ).toHaveLength(3);
    expect(screen.getByRole("button", { name: "前往地点11" })).toBeTruthy();
  });
  fireEvent.click(toggle);
  await waitFor(() =>
    expect(screen.queryByRole("list", { name: "地图地点" })).toBeNull(),
  );
});

it("keeps every editable place available without the reader's collapse or pagination", () => {
  render(<MapCard card={manyPlaces} onChange={vi.fn()} />);
  expect(screen.queryByRole("button", { name: "展开地点列表" })).toBeNull();
  expect(screen.getByRole("list", { name: "地图地点" }).children).toHaveLength(
    13,
  );
});
