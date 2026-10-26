import { describe, expect, it } from "vitest";
import { viewerMediaSize } from "./viewerMediaSize";

describe("media viewer fitting", () => {
  it.each([
    [600, 400, 600, 400],
    [160, 120, 600, 450],
    [500, 500, 600, 600],
    [1024, 1024, 672, 672],
    [900, 300, 930, 310],
    [400, 600, 400, 600],
    [360, 800, 302.4, 672],
  ])(
    "fits %s x %s in the measured 930 x 672 stage",
    (width, height, expectedWidth, expectedHeight) => {
      const size = viewerMediaSize(width, height, 930, 672);
      expect(size?.width).toBeCloseTo(expectedWidth);
      expect(size?.height).toBeCloseTo(expectedHeight);
    },
  );
  it("upscales into a large stage and bounds a phone image to its available width", () => {
    expect(viewerMediaSize(1024, 1024, 1650, 1952)).toEqual({
      width: 1650,
      height: 1650,
    });
    expect(viewerMediaSize(160, 120, 390, 796)).toEqual({
      width: 390,
      height: 292.5,
    });
  });
  it("waits for a real image and layout instead of producing invalid dimensions", () => {
    expect(viewerMediaSize(0, 120, 930, 672)).toBeNull();
    expect(viewerMediaSize(160, 120, 0, 0)).toBeNull();
  });
});
