// Keep this value byte-for-byte aligned with internal/media.RecipeVersion.
// The query is part of the browser cache key: changing the media recipe must
// never let a native video element reuse bytes or color metadata from an older
// rendition at the same media ID.
export const MEDIA_RECIPE_VERSION =
  "v10-max1600-display10m-icc-heic-mp4-h264-sdr-bt709-jpeg444";

export function mediaOriginalUrl(mediaId: string): string {
  return `/api/v1/media/${encodeURIComponent(mediaId)}`;
}

/** Resolve editor upload URLs while retaining a stable media identity. */
export function mediaIdFromUrl(value: unknown): string | null {
  return typeof value === "string"
    ? (value.match(
        /^\/api\/v1\/media\/([0-9A-HJKMNP-TV-Z]{26})(?:\/preview)?(?:\?|$)/,
      )?.[1] ?? null)
    : null;
}

export function mediaPreviewUrl(mediaId: string): string {
  return `${mediaOriginalUrl(mediaId)}/preview?v=${encodeURIComponent(MEDIA_RECIPE_VERSION)}`;
}

export function mediaPosterUrl(mediaId: string): string {
  return `${mediaOriginalUrl(mediaId)}/poster?v=${encodeURIComponent(MEDIA_RECIPE_VERSION)}`;
}
