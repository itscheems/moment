import "@testing-library/jest-dom/vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { OwnerApi } from "../../api/owner";
import { LocationPicker } from "./LocationPicker";

beforeEach(() => {
  vi.stubGlobal("scrollTo", vi.fn());
});

function json(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("location picker", () => {
  it("keeps browser permission behind an explicit action and selects a nearby place", async () => {
    vi.spyOn(window, "scrollY", "get").mockReturnValue(410);
    const getCurrentPosition = vi.fn((success: PositionCallback) =>
      success({
        coords: { latitude: 31.23, longitude: 121.47 },
      } as GeolocationPosition),
    );
    Object.defineProperty(navigator, "geolocation", {
      configurable: true,
      value: { getCurrentPosition },
    });
    const fetcherMock = vi.fn(async (...args: Parameters<typeof fetch>) => {
      void args;
      return json({
        data: {
          items: [
            {
              name: "静安寺",
              display_name: "上海市静安区南京西路1686号",
              country_code: "CN",
              country: "中国",
              region: "上海市",
              city: "上海市",
              district: "静安区",
              category: "风景名胜",
              provider: "amap",
              provider_place_id: "B0FFF",
              latitude: 31.223186,
              longitude: 121.44526,
              coordinate_system: "gcj02",
            },
          ],
        },
        meta: { request_id: "req_location" },
      });
    });
    const fetcher = fetcherMock as typeof fetch;
    const onChange = vi.fn();
    render(
      <LocationPicker
        api={new OwnerApi(fetcher)}
        disabled={false}
        onChange={onChange}
        value={null}
      />,
    );

    expect(getCurrentPosition).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "添加位置" }));
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.position).toBe("fixed");
    expect(document.body.style.top).toBe("-410px");
    expect(getCurrentPosition).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: /使用当前位置/ }));
    await waitFor(() =>
      expect(screen.getByRole("button", { name: /静安寺/ })).toBeEnabled(),
    );
    expect(getCurrentPosition).toHaveBeenCalledTimes(1);
    expect(String(fetcherMock.mock.calls[0]?.[0])).toContain("latitude=31.23");

    fireEvent.click(screen.getByRole("button", { name: /静安寺/ }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({ name: "静安寺", provider: "amap" }),
    );
    expect(document.documentElement.style.overflowY).toBe("");
    expect(document.body.style.position).toBe("");
    expect(window.scrollTo).toHaveBeenCalledWith(0, 410);
  });

  it("offers a deliberate manual fallback without coordinates", () => {
    const onChange = vi.fn();
    render(
      <LocationPicker
        api={new OwnerApi(vi.fn() as typeof fetch)}
        disabled={false}
        onChange={onChange}
        value={null}
      />,
    );
    const trigger = screen.getByRole("button", { name: "添加位置" });
    expect(
      trigger.querySelector('svg[data-icon="location-picker"]'),
    ).toHaveAttribute("fill", "currentColor");
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name: "手动添加地点" }));
    fireEvent.change(screen.getByLabelText("地点名称"), {
      target: { value: "书房" },
    });
    fireEvent.click(screen.getByRole("button", { name: "添加" }));
    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "书房",
        provider: "manual",
        latitude: null,
        longitude: null,
      }),
    );
  });

  it("adds a district only when visible search results contain distinct places with the same name", async () => {
    const fetcher = vi.fn(async () =>
      json({
        data: {
          items: [
            {
              name: "陆悦天地",
              display_name: "中科路1600号",
              country_code: "CN",
              country: "中国",
              region: "上海市",
              city: "上海市",
              district: "上海市浦东新区",
              category: "购物服务",
              provider: "amap",
              provider_place_id: "POI_A",
              latitude: 31.2,
              longitude: 121.6,
              coordinate_system: "gcj02",
            },
            {
              name: "陆悦天地",
              display_name: "吴中路附近",
              country_code: "CN",
              country: "中国",
              region: "上海市",
              city: "上海市",
              district: "闵行区",
              category: "购物服务",
              provider: "amap",
              provider_place_id: "POI_B",
              latitude: 31.1,
              longitude: 121.4,
              coordinate_system: "gcj02",
            },
          ],
        },
        meta: { request_id: "req_duplicate_locations" },
      }),
    ) as typeof fetch;
    const onChange = vi.fn();
    render(
      <LocationPicker
        api={new OwnerApi(fetcher)}
        disabled={false}
        onChange={onChange}
        value={null}
      />,
    );

    fireEvent.click(screen.getByRole("button", { name: "添加位置" }));
    fireEvent.change(screen.getByLabelText("搜索地点"), {
      target: { value: "陆悦" },
    });
    const choices = await screen.findAllByRole("button", { name: /陆悦天地/ });
    fireEvent.click(choices[0]);

    expect(onChange).toHaveBeenCalledWith(
      expect.objectContaining({
        name: "陆悦天地 · 浦东新区",
        displayName: "中科路1600号",
      }),
    );
  });
});
