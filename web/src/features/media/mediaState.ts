import type { PostMediaInput, UploadedMedia } from "../../api/owner";
import type { PublicMediaUsage } from "../../api/public";

export type AltMode = "missing" | "decorative" | "provided";
export type MediaUploadState = "ready" | "queued" | "uploading" | "failed";

export interface ComposerMediaItem {
  key: string;
  file: File | null;
  objectUrl: string | null;
  localPreviewUrl: string | null;
  mediaId: string | null;
  previewUrl: string;
  originalUrl: string;
  width: number | null;
  height: number | null;
  byteSize: number | null;
  animatedOriginal: boolean;
  altMode: AltMode;
  altText: string;
  state: MediaUploadState;
  progress: number;
  error: string | null;
}

export const MAX_MEDIA_COUNT = 9;
export const MAX_FILE_BYTES = 10 * 1024 * 1024;
export const MAX_TOTAL_BYTES = 50 * 1024 * 1024;
export const MAX_ALT_CODE_POINTS = 1_000;
export const MEDIA_TYPES = new Set([
  "image/jpeg",
  "image/png",
  "image/webp",
  "image/gif",
]);

export function altMode(value: string | null): AltMode {
  if (value === null) return "missing";
  return value === "" ? "decorative" : "provided";
}

export function itemsFromPost(media: PublicMediaUsage[]): ComposerMediaItem[] {
  return media.map((usage) => ({
    key: `stored-${usage.position}-${usage.mediaId}`,
    file: null,
    objectUrl: null,
    localPreviewUrl: null,
    mediaId: usage.mediaId,
    previewUrl: `/api/v1/media/${encodeURIComponent(usage.mediaId)}/preview`,
    originalUrl: `/api/v1/media/${encodeURIComponent(usage.mediaId)}`,
    width: null,
    height: null,
    byteSize: null,
    animatedOriginal: false,
    altMode: altMode(usage.altText),
    altText: usage.altText && usage.altText.trim() ? usage.altText : "",
    state: "ready",
    progress: 100,
    error: null,
  }));
}

export function applyUpload(
  item: ComposerMediaItem,
  upload: UploadedMedia,
): ComposerMediaItem {
  return {
    ...item,
    localPreviewUrl: null,
    mediaId: upload.id,
    previewUrl: upload.previewUrl,
    originalUrl: upload.originalUrl,
    width: upload.width,
    height: upload.height,
    byteSize: upload.byteSize,
    animatedOriginal: upload.animatedOriginal,
    state: "ready",
    progress: 100,
    error: null,
  };
}

export function altValue(item: ComposerMediaItem): string | null {
  if (item.altMode === "missing") return null;
  if (item.altMode === "decorative") return "";
  return item.altText;
}

export function mediaInput(items: ComposerMediaItem[]): PostMediaInput[] {
  return items.map((item, position) => {
    if (!item.mediaId) throw new Error("media is not uploaded");
    return {
      mediaId: item.mediaId,
      position,
      altText: altValue(item),
    };
  });
}

export function mediaValidation(items: ComposerMediaItem[]): string | null {
  if (items.length > MAX_MEDIA_COUNT) return "每条 Moment 最多附加 9 张图片。";
  for (const item of items) {
    if (
      item.altMode === "provided" &&
      (!item.altText.trim() ||
        Array.from(item.altText).length > MAX_ALT_CODE_POINTS)
    ) {
      return "已选择填写描述的图片，需要 1–1,000 个字符；也可明确改为未描述或装饰图。";
    }
  }
  return null;
}

export function fileValidation(
  file: File,
  current: ComposerMediaItem[],
): string | null {
  if (!MEDIA_TYPES.has(file.type)) {
    return `${file.name || "所选文件"} 不是支持的 JPEG、PNG、WebP 或 GIF。`;
  }
  if (file.size <= 0 || file.size > MAX_FILE_BYTES) {
    return `${file.name || "所选文件"} 超过单图 10 MiB 限制或为空。`;
  }
  const currentBytes = current.reduce(
    (total, item) => total + (item.file?.size ?? 0),
    0,
  );
  if (currentBytes + file.size > MAX_TOTAL_BYTES) {
    return "本次选择的图片总量超过 50 MiB，请移除一些图片后重试。";
  }
  return null;
}
