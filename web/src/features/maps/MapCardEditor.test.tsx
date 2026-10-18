import { useState } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { locationsApi } from "../../api/locations";
import { MapCardEditor } from "./MapCardEditor";
import type { MapCardData } from "./types";

function Harness() {
  const [card, setCard] = useState<MapCardData | null>(null);
  return (
    <>
      <MapCardEditor value={card} onChange={setCard} triggerOnly />
      <MapCardEditor value={card} onChange={setCard} />
      <output>{JSON.stringify(card)}</output>
    </>
  );
}

beforeEach(() => {
  // This test covers editor state and provider I/O. Browser verification owns
  // the real canvas, layout and gestures; no synthetic map engine is used.
  vi.stubGlobal(
    "IntersectionObserver",
    class {
      observe() {}
      disconnect() {}
      unobserve() {}
    },
  );
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("map card editor", () => {
  it("cancels a search when the query is shortened and enables current location again", async () => {
    const search = vi
      .spyOn(locationsApi, "searchLocations")
      .mockImplementation(() => new Promise(() => {}));
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "添加地图" }));
    const input = screen.getByRole("textbox", { name: "搜索全球地点" });
    fireEvent.change(input, { target: { value: "东京" } });
    await screen.findByText("正在查找地点…");
    await waitFor(() => expect(search).toHaveBeenCalledTimes(1));
    fireEvent.change(input, { target: { value: "东" } });
    await waitFor(() => expect(screen.queryByText("正在查找地点…")).toBeNull());
    expect(
      screen.getByRole<HTMLButtonElement>("button", { name: "当前位置" })
        .disabled,
    ).toBe(false);
    expect(search.mock.calls[0][2]?.aborted).toBe(true);
  });

  it("adds global search results, keeps markers unconnected and permits removal", async () => {
    const search = vi.spyOn(locationsApi, "searchLocations").mockResolvedValue([
      {
        name: "银座",
        displayName: "日本东京都中央区银座",
        latitude: 35.67,
        longitude: 139.76,
        coordinateSystem: "wgs84",
        provider: "geoapify",
        providerPlaceId: "tokyo",
        countryCode: "JP",
        country: "日本",
        region: "东京都",
        city: "东京",
        district: "中央区",
        category: "",
      },
    ]);
    render(<Harness />);
    fireEvent.click(screen.getByRole("button", { name: "添加地图" }));
    fireEvent.change(screen.getByRole("textbox", { name: "搜索全球地点" }), {
      target: { value: "东京银座" },
    });
    fireEvent.click(
      await screen.findByRole("button", { name: /银座 日本东京都/ }),
    );
    expect(search).toHaveBeenCalledWith(
      "东京银座",
      undefined,
      expect.any(AbortSignal),
      "global",
    );
    expect(screen.getByRole("button", { name: "添加地点" })).toBeTruthy();
    expect(screen.getByText(/"connectPoints":false/)).toBeTruthy();
    expect(screen.queryByText("地点连线，非实际行驶路线")).toBeNull();
    expect(screen.queryByRole("button", { name: "移除地图" })).toBeNull();
    const row = await screen.findByRole("group", {
      name: "银座，按住拖动排序",
    });
    fireEvent.keyDown(
      screen.getByRole("link", { name: "在 Google Maps 打开银座" }),
      { key: "Delete" },
    );
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "展开地图" }));
    const expanded = await screen.findByRole("dialog", { name: "地图" });
    const expandedRow = await within(expanded).findByRole("group", {
      name: "银座，按住拖动排序",
    });
    // jsdom has no scrolling; this assertion only checks key propagation.
    Object.defineProperty(expandedRow.parentElement, "scrollIntoView", {
      value: vi.fn(),
    });
    fireEvent.keyDown(expandedRow, { key: " ", code: "Space" });
    await waitFor(() =>
      expect(expandedRow.getAttribute("aria-pressed")).toBe("true"),
    );
    const sortingKey = vi.fn();
    document.addEventListener("keydown", sortingKey, { once: true });
    fireEvent.keyDown(expandedRow, { key: "ArrowUp", code: "ArrowUp" });
    expect(sortingKey).toHaveBeenCalledOnce();
    // The first Escape cancels sorting without dismissing its containing map.
    fireEvent.keyDown(expandedRow, { key: "Escape", code: "Escape" });
    await waitFor(() =>
      expect(expandedRow.getAttribute("aria-pressed")).not.toBe("true"),
    );
    expect(screen.getByRole("dialog", { name: "地图" })).toBeTruthy();
    fireEvent.keyDown(
      within(expanded).getByRole("link", { name: "在 Google Maps 打开银座" }),
      { key: "Escape" },
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog", { name: "地图" })).toBeNull(),
    );
    fireEvent.contextMenu(row, { button: 2, clientX: 30, clientY: 30 });
    fireEvent.click(await screen.findByRole("menuitem", { name: "删除地点" }));
    fireEvent.click(await screen.findByRole("button", { name: "取消" }));
    expect(screen.getByText(/"connectPoints":false/)).toBeTruthy();
    fireEvent.keyDown(row, { key: "Delete" });
    fireEvent.click(await screen.findByRole("button", { name: "删除" }));
    await waitFor(() => expect(screen.getByText("null")).toBeTruthy());
  });
});
