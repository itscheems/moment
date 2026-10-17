import { describe, it, expect, vi, afterEach } from "vitest";
import { canCropMedia, cropGeometry, createMediaCrop } from "./mediaCrop";
import { releaseDiscardedLocalPreviews, newMediaItem } from "./mediaState";
afterEach(() => vi.restoreAllMocks());
describe("media crop export", () => {
  it("clips fractional source coordinates to actual image bounds without upscaling", () => {
    expect(
      cropGeometry({ x: -3, y: 2.5, width: 104, height: 30 }, 100, 30),
    ).toEqual({
      x: 0,
      y: 2.5,
      width: 100,
      height: 27.5,
      outputWidth: 100,
      outputHeight: 28,
    });
  });
  it("rejects invalid and empty crop regions", () => {
    for (const area of [
      { x: NaN, y: 0, width: 4, height: 4 },
      { x: 20, y: 0, width: 4, height: 4 },
      { x: 0, y: 0, width: 0, height: 4 },
    ])
      expect(() => cropGeometry(area, 10, 10)).toThrow();
  });
  it("does not flatten animations or videos into still images", () => {
    expect(
      canCropMedia({ mimeType: "image/png", animatedOriginal: false }),
    ).toBe(true);
    expect(
      canCropMedia({ mimeType: "image/gif", animatedOriginal: true }),
    ).toBe(false);
    expect(
      canCropMedia({ mimeType: "video/mp4", animatedOriginal: false }),
    ).toBe(false);
  });
  it("cancels before decoding or exporting when the editor closes", async () => {
    const controller = new AbortController();
    controller.abort();
    await expect(
      createMediaCrop(
        { url: "blob:source", name: "photo.png", mimeType: "image/png" },
        { x: 0, y: 0, width: 10, height: 10 },
        controller.signal,
      ),
    ).rejects.toMatchObject({ name: "AbortError" });
  });
  it("retains the uncropped source until its usage is removed", () => {
    const revoke = vi
      .spyOn(URL, "revokeObjectURL")
      .mockImplementation(() => {});
    const first = {
      ...newMediaItem(new File(["a"], "a.png", { type: "image/png" })),
      localPreviewUrl: "blob:original",
    };
    const cropped = {
      ...first,
      localPreviewUrl: "blob:cropped",
      cropSource: {
        url: "blob:original",
        name: "a.png",
        mimeType: "image/png",
      },
    };
    releaseDiscardedLocalPreviews([first], [cropped]);
    expect(revoke).not.toHaveBeenCalled();
    releaseDiscardedLocalPreviews([cropped], []);
    expect(revoke).toHaveBeenCalledWith("blob:original");
    expect(revoke).toHaveBeenCalledWith("blob:cropped");
  });
});
