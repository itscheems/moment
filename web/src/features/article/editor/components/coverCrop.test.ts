import { afterEach, describe, expect, it, vi } from "vitest";
import { coverCropGeometry, createCoverCrop } from "./coverCrop";

describe("article cover crop geometry", () => {
  it("keeps the selected center of a portrait and exports its real pixels at 5:2", () => {
    const result = coverCropGeometry(
      { x: 0, y: 328, width: 360, height: 144 },
      360,
      800,
    );
    expect(result).toEqual({
      source: { x: 0, y: 328, width: 360, height: 144 },
      width: 360,
      height: 144,
    });
  });

  it("limits a large crop to 1600px without changing the selected region", () => {
    const result = coverCropGeometry(
      { x: 400, y: 160, width: 2500, height: 1000 },
      3200,
      1600,
    );
    expect(result.width).toBe(1600);
    expect(result.height).toBe(640);
    expect(result.source).toEqual({
      x: 400,
      y: 160,
      width: 2500,
      height: 1000,
    });
  });

  it("corrects pixel-rounding differences without enlarging a fractional crop", () => {
    const result = coverCropGeometry(
      { x: 12.4, y: 22.2, width: 301, height: 120 },
      400,
      300,
    );
    expect(result.source.x).toBeCloseTo(12.9);
    expect(result.source.y).toBeCloseTo(22.2);
    expect(result.source.width).toBeCloseTo(300);
    expect(result.source.height).toBeCloseTo(120);
    expect(result.width).toBe(300);
    expect(result.height).toBe(120);
  });

  it("keeps sampling inside image bounds when a crop reaches an edge", () => {
    const result = coverCropGeometry(
      { x: -0.4, y: 45.8, width: 101, height: 41 },
      100,
      86,
    );
    expect(result.source.x).toBeGreaterThanOrEqual(0);
    expect(result.source.y).toBeGreaterThanOrEqual(0);
    expect(result.source.x + result.source.width).toBeLessThanOrEqual(100);
    expect(result.source.y + result.source.height).toBeLessThanOrEqual(86);
    expect(result.width / result.height).toBe(2.5);
    expect(result.width).toBeLessThanOrEqual(result.source.width);
    expect(result.height).toBeLessThanOrEqual(result.source.height);
  });

  it("refuses empty, invalid and too-small regions instead of exporting a blank image", () => {
    for (const area of [
      { x: 100, y: 0, width: 20, height: 8 },
      { x: 0, y: 0, width: 4, height: 1.6 },
      { x: NaN, y: 0, width: 20, height: 8 },
    ])
      expect(() => coverCropGeometry(area, 100, 100)).toThrow();
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it("does not allocate an image resource after cancellation", async () => {
  const controller = new AbortController();
  controller.abort();
  await expect(
    createCoverCrop(
      new File(["image"], "cover.jpg"),
      { x: 0, y: 0, width: 20, height: 8 },
      controller.signal,
    ),
  ).rejects.toMatchObject({ name: "AbortError" });
});

it("cancels a pending decode and releases its object URL", async () => {
  const revokeObjectURL = vi.fn();
  const removeAttribute = vi.fn();
  vi.stubGlobal("URL", {
    createObjectURL: vi.fn().mockReturnValue("blob:cover"),
    revokeObjectURL,
  });
  vi.stubGlobal(
    "Image",
    class {
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      removeAttribute = removeAttribute;
      set src(_: string) {
        /* Keep decoding pending until cancellation. */
      }
    },
  );
  const controller = new AbortController();
  const pending = createCoverCrop(
    new File(["image"], "cover.jpg"),
    { x: 0, y: 0, width: 20, height: 8 },
    controller.signal,
  );
  controller.abort();
  await expect(pending).rejects.toMatchObject({ name: "AbortError" });
  expect(removeAttribute).toHaveBeenCalledWith("src");
  expect(revokeObjectURL).toHaveBeenCalledWith("blob:cover");
});

it("releases the image URL when decoding fails", async () => {
  const revokeObjectURL = vi.fn();
  const createObjectURL = vi.fn().mockReturnValue("blob:cover");
  vi.stubGlobal("URL", { createObjectURL, revokeObjectURL });
  vi.stubGlobal(
    "Image",
    class {
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      set src(_: string) {
        queueMicrotask(() => this.onerror?.());
      }
    },
  );
  await expect(
    createCoverCrop(new File(["broken"], "broken.jpg"), {
      x: 0,
      y: 0,
      width: 20,
      height: 8,
    }),
  ).rejects.toThrow("无法读取");
  expect(revokeObjectURL).toHaveBeenCalledWith("blob:cover");
});

it("releases the image URL and reports a retryable error if canvas is unavailable", async () => {
  const revokeObjectURL = vi.fn();
  vi.stubGlobal("URL", {
    createObjectURL: vi.fn().mockReturnValue("blob:cover"),
    revokeObjectURL,
  });
  vi.stubGlobal(
    "Image",
    class {
      naturalWidth = 20;
      naturalHeight = 8;
      onload: (() => void) | null = null;
      onerror: (() => void) | null = null;
      set src(_: string) {
        queueMicrotask(() => this.onload?.());
      }
    },
  );
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
  await expect(
    createCoverCrop(new File(["image"], "cover.jpg", { type: "image/jpeg" }), {
      x: 0,
      y: 0,
      width: 20,
      height: 8,
    }),
  ).rejects.toThrow("请重试");
  expect(revokeObjectURL).toHaveBeenCalledWith("blob:cover");
});
