import type { PostMediaInput, UploadedMedia } from "../../api/owner";
import { mediaOriginalUrl, mediaPreviewUrl } from "../../api/mediaUrls";
import type { PublicMediaUsage } from "../../api/public";
import { PublicApiError } from "../../api/public";

export type AltMode = "missing" | "decorative" | "provided";
export type MediaUploadState = "ready" | "queued" | "uploading" | "failed";

export interface ComposerMediaItem {
  key: string;
  file: File | null;
  localPreviewUrl: string | null;
  processingPosterUrl: string | null;
  mediaId: string | null;
  previewUrl: string;
  originalUrl: string;
  mimeType: string;
  previewMimeType: string;
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
export const MAX_IMAGE_FILE_BYTES = 30 * 1024 * 1024;
export const MAX_VIDEO_FILE_BYTES = 300 * 1024 * 1024;
export const MAX_FILE_BYTES = MAX_IMAGE_FILE_BYTES;
export const MAX_TOTAL_BYTES = 300 * 1024 * 1024;
export const MAX_ALT_CODE_POINTS = 1_000;
export const MEDIA_TYPES = new Set([
  "image/jpeg",
  "image/png",
  "image/webp",
  "image/gif",
  "image/heic",
  "image/heif",
  "video/mp4",
  "video/quicktime",
]);

export function isVideoMimeType(mimeType: string): boolean {
  return mimeType === "video/mp4" || mimeType === "video/quicktime";
}

function inferredMediaType(file: File): string {
  if (MEDIA_TYPES.has(file.type)) return file.type;
  const name = file.name.toLowerCase();
  if (name.endsWith(".heic") || name.endsWith(".heif")) return "image/heic";
  if (name.endsWith(".mp4") || name.endsWith(".m4v")) return "video/mp4";
  if (name.endsWith(".mov") || name.endsWith(".qt")) return "video/quicktime";
  return file.type;
}

function createMediaKey(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(12)), (value) =>
    value.toString(16).padStart(2, "0"),
  ).join("");
}

export function newMediaItem(file: File): ComposerMediaItem {
  return {
    key: `local-${createMediaKey()}`,
    file,
    localPreviewUrl: null,
    processingPosterUrl: null,
    mediaId: null,
    previewUrl: "",
    originalUrl: "",
    mimeType: inferredMediaType(file),
    previewMimeType: "",
    width: null,
    height: null,
    byteSize: null,
    animatedOriginal: file.type === "image/gif",
    altMode: "missing",
    altText: "",
    state: "queued",
    progress: 0,
    error: null,
  };
}

export function readLocalPreview(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    try {
      resolve(URL.createObjectURL(file));
    } catch (error) {
      reject(
        error instanceof Error
          ? error
          : new Error("local media preview failed"),
      );
    }
  });
}

export function releaseLocalPreview(url: string | null): void {
  if (url?.startsWith("blob:")) URL.revokeObjectURL(url);
}

export function releaseDiscardedLocalPreviews(
  current: ComposerMediaItem[],
  next: ComposerMediaItem[],
): void {
  const retained = new Set(
    next.flatMap((item) =>
      [item.localPreviewUrl, item.processingPosterUrl].filter(
        (url): url is string => Boolean(url),
      ),
    ),
  );
  for (const item of current) {
    for (const url of [item.localPreviewUrl, item.processingPosterUrl]) {
      if (url && !retained.has(url)) releaseLocalPreview(url);
    }
  }
}

export function uploadMessage(error: unknown): string {
  if (!(error instanceof PublicApiError))
    return "媒体没有上传完成。文字和已完成的媒体都已保留，可以重试。";
  switch (error.code) {
    case "MEDIA_TYPE_UNSUPPORTED":
      return "媒体格式不受支持，请选择 JPEG、PNG、WebP、GIF、HEIC、MP4 或 QuickTime MOV。";
    case "MEDIA_TOO_LARGE":
      return "媒体超过大小限制，请移除或压缩后重试。";
    case "MEDIA_DIMENSIONS_EXCEEDED":
      return "图片尺寸或 GIF 帧数超过安全限制。";
    case "MEDIA_CORRUPT":
      return "媒体内容无效或与文件类型不一致，请重新选择。";
    case "MEDIA_PROCESSOR_UNAVAILABLE":
      return "服务器暂时无法处理 HEIC 或视频，请稍后重试。";
    case "RATE_LIMITED":
      return "媒体上传得有点快，请稍后重试。";
    default:
      return "媒体没有上传完成。文字和已完成的媒体都已保留，可以重试。";
  }
}

export function altMode(value: string | null): AltMode {
  if (value === null) return "missing";
  return value === "" ? "decorative" : "provided";
}

export function itemsFromPost(media: PublicMediaUsage[]): ComposerMediaItem[] {
  return media.map((usage) => ({
    key: `stored-${usage.position}-${usage.mediaId}`,
    file: null,
    localPreviewUrl: null,
    processingPosterUrl: null,
    mediaId: usage.mediaId,
    previewUrl: mediaPreviewUrl(usage.mediaId),
    originalUrl: mediaOriginalUrl(usage.mediaId),
    mimeType: usage.mimeType,
    previewMimeType: isVideoMimeType(usage.mimeType)
      ? "video/mp4"
      : "image/jpeg",
    width: usage.width,
    height: usage.height,
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
    file: null,
    localPreviewUrl: null,
    processingPosterUrl: null,
    mediaId: upload.id,
    previewUrl: upload.previewUrl,
    originalUrl: upload.originalUrl,
    width: upload.width,
    height: upload.height,
    byteSize: upload.byteSize,
    mimeType: upload.mimeType,
    previewMimeType: upload.previewMimeType,
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

export function mediaValidation(
  items: ComposerMediaItem[],
  maximum = MAX_MEDIA_COUNT,
): string | null {
  if (items.length > maximum) return `最多附加 ${maximum} 张图片。`;
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
  current: Array<Pick<ComposerMediaItem, "file">>,
  maximumTotalBytes = MAX_TOTAL_BYTES,
): string | null {
  if (!MEDIA_TYPES.has(inferredMediaType(file))) {
    return `${file.name || "所选文件"} 不是支持的 JPEG、PNG、WebP、GIF、HEIC、MP4 或 QuickTime MOV。`;
  }
  const mimeType = inferredMediaType(file);
  const maximumFileBytes = isVideoMimeType(mimeType)
    ? MAX_VIDEO_FILE_BYTES
    : MAX_IMAGE_FILE_BYTES;
  if (file.size <= 0 || file.size > maximumFileBytes) {
    return `${file.name || "所选文件"} 超过单个${isVideoMimeType(mimeType) ? "视频 300" : "图片 30"} MiB 限制或为空。`;
  }
  const currentBytes = current.reduce(
    (total, item) => total + (item.file?.size ?? 0),
    0,
  );
  if (currentBytes + file.size > maximumTotalBytes) {
    return `本次选择的媒体总量超过 ${Math.round(maximumTotalBytes / 1024 / 1024)} MiB，请移除一些媒体后重试。`;
  }
  return null;
}

export function selectMediaFiles(
  files: Iterable<File>,
  current: ComposerMediaItem[],
  {
    maximum = MAX_MEDIA_COUNT,
    maximumTotalBytes = MAX_TOTAL_BYTES,
    subject = "Moment",
  }: {
    maximum?: number;
    maximumTotalBytes?: number;
    subject?: string;
  } = {},
): { accepted: File[]; error: string | null } {
  const accepted: File[] = [];
  let error: string | null = null;
  for (const file of files) {
    if (current.length + accepted.length >= maximum) {
      error = `每条${subject}最多附加 ${maximum} 张图片；多出的文件没有加入。`;
      break;
    }
    const invalid = fileValidation(
      file,
      [...current, ...accepted.map((selected) => ({ file: selected }))],
      maximumTotalBytes,
    );
    if (invalid) {
      error = invalid;
      continue;
    }
    accepted.push(file);
  }
  return { accepted, error };
}
