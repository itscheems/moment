import { afterEach, describe, expect, it, vi } from "vitest";

import type { ComposerMediaItem } from "./mediaState";
import {
  altValue,
  fileValidation,
  MAX_ALT_CODE_POINTS,
  MAX_FILE_BYTES,
  MAX_TOTAL_BYTES,
  mediaInput,
  mediaValidation,
  readLocalPreview,
  releaseDiscardedLocalPreviews,
} from "./mediaState";

afterEach(() => vi.restoreAllMocks());

function item(overrides: Partial<ComposerMediaItem> = {}): ComposerMediaItem {
  return {
    key: "media-1",
    file: null,
    localPreviewUrl: null,
    mediaId: "01K2ABCDEF0123456789ABCDE1",
    previewUrl: "/api/v1/media/01K2ABCDEF0123456789ABCDE1/preview",
    originalUrl: "/api/v1/media/01K2ABCDEF0123456789ABCDE1",
    mimeType: "image/jpeg",
    previewMimeType: "image/jpeg",
    width: 800,
    height: 600,
    byteSize: 100,
    animatedOriginal: false,
    altMode: "missing",
    altText: "",
    state: "ready",
    progress: 100,
    error: null,
    ...overrides,
  };
}

function sizedFile(name: string, type: string, size: number): File {
  const file = new File(["x"], name, { type });
  Object.defineProperty(file, "size", { value: size });
  return file;
}

describe("media composer contract", () => {
  it("uses a browser object URL for a local original and releases it when discarded", async () => {
    const createObjectURL = vi
      .spyOn(URL, "createObjectURL")
      .mockReturnValue("blob:http://localhost/local-original");
    const revokeObjectURL = vi
      .spyOn(URL, "revokeObjectURL")
      .mockImplementation(() => undefined);
    const file = new File(["image"], "local.png", { type: "image/png" });

    const localPreviewUrl = await readLocalPreview(file);
    expect(createObjectURL).toHaveBeenCalledWith(file);
    expect(localPreviewUrl).toBe("blob:http://localhost/local-original");

    releaseDiscardedLocalPreviews([item({ file, localPreviewUrl })], []);
    expect(revokeObjectURL).toHaveBeenCalledWith(localPreviewUrl);
  });

  it("preserves missing, decorative, and provided alt values without trimming", () => {
    const missing = item({ altMode: "missing" });
    const decorative = item({ key: "media-2", altMode: "decorative" });
    const provided = item({
      key: "media-3",
      altMode: "provided",
      altText: "  雨后的窗  ",
    });

    expect([
      altValue(missing),
      altValue(decorative),
      altValue(provided),
    ]).toEqual([null, "", "  雨后的窗  "]);
    expect(mediaInput([missing, decorative, provided])).toEqual([
      { mediaId: missing.mediaId, position: 0, altText: null },
      { mediaId: decorative.mediaId, position: 1, altText: "" },
      {
        mediaId: provided.mediaId,
        position: 2,
        altText: "  雨后的窗  ",
      },
    ]);
  });

  it("counts Unicode code points rather than UTF-16 units for alt limits", () => {
    const atLimit = item({
      altMode: "provided",
      altText: "🙂".repeat(MAX_ALT_CODE_POINTS),
    });
    const overLimit = item({
      altMode: "provided",
      altText: "🙂".repeat(MAX_ALT_CODE_POINTS + 1),
    });

    expect(mediaValidation([atLimit])).toBeNull();
    expect(mediaValidation([overLimit])).toContain("1–1,000");
    expect(
      mediaValidation([item({ altMode: "provided", altText: "   " })]),
    ).toContain("1–1,000");
  });

  it("validates logical duplicate usage bytes and browser-declared types", () => {
    const duplicate = sizedFile("same.png", "image/png", MAX_TOTAL_BYTES / 5);
    const current = Array.from({ length: 5 }, (_, index) =>
      item({ key: `copy-${index}`, file: duplicate }),
    );
    expect(
      current.reduce((sum, value) => sum + (value.file?.size ?? 0), 0),
    ).toBe(MAX_TOTAL_BYTES);
    expect(fileValidation(duplicate, current)).toContain("150 MiB");
    expect(
      fileValidation(sizedFile("vector.svg", "image/svg+xml", 100), []),
    ).toContain("不是支持的");
    expect(
      fileValidation(
        sizedFile("camera-original.jpg", "image/jpeg", 30 * 1024 * 1024),
        [],
      ),
    ).toBeNull();
    expect(
      fileValidation(
        sizedFile("large.jpg", "image/jpeg", MAX_FILE_BYTES + 1),
        [],
      ),
    ).toContain("30 MiB");
    expect(
      fileValidation(
        sizedFile("IMG_1337.MOV", "video/quicktime", 44 * 1024 * 1024),
        [],
      ),
    ).toBeNull();
    expect(
      fileValidation(sizedFile("camera.mov", "", 44 * 1024 * 1024), []),
    ).toBeNull();
  });
});
