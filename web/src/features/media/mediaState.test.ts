import { describe, expect, it } from "vitest";

import type { ComposerMediaItem } from "./mediaState";
import {
  altValue,
  fileValidation,
  MAX_ALT_CODE_POINTS,
  MAX_FILE_BYTES,
  MAX_TOTAL_BYTES,
  mediaInput,
  mediaValidation,
} from "./mediaState";

function item(overrides: Partial<ComposerMediaItem> = {}): ComposerMediaItem {
  return {
    key: "media-1",
    file: null,
    localPreviewUrl: null,
    mediaId: "01K2ABCDEF0123456789ABCDE1",
    previewUrl: "/api/v1/media/01K2ABCDEF0123456789ABCDE1/preview",
    originalUrl: "/api/v1/media/01K2ABCDEF0123456789ABCDE1",
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
    const duplicate = sizedFile("same.png", "image/png", MAX_FILE_BYTES);
    const current = Array.from({ length: 5 }, (_, index) =>
      item({ key: `copy-${index}`, file: duplicate }),
    );
    expect(
      current.reduce((sum, value) => sum + (value.file?.size ?? 0), 0),
    ).toBe(MAX_TOTAL_BYTES);
    expect(fileValidation(duplicate, current)).toContain("50 MiB");
    expect(
      fileValidation(sizedFile("vector.svg", "image/svg+xml", 100), []),
    ).toContain("不是支持的");
    expect(
      fileValidation(
        sizedFile("large.jpg", "image/jpeg", MAX_FILE_BYTES + 1),
        [],
      ),
    ).toContain("10 MiB");
  });
});
