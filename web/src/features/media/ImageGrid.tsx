import { useState, type CSSProperties } from "react";

import type { PublicMediaUsage } from "../../api/public";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import { ViewportVideo } from "./ViewportVideo";
import styles from "./Media.module.css";

function mediaAlt(
  usage: PublicMediaUsage,
  index: number,
  total: number,
): string {
  if (usage.altText === "") return "";
  if (usage.altText !== null) return usage.altText;
  return usage.mimeType === "video/mp4"
    ? `未提供视频描述，第 ${index + 1} 项，共 ${total} 项`
    : `未提供图片描述，第 ${index + 1} 张，共 ${total} 张`;
}

function tileName(
  usage: PublicMediaUsage,
  index: number,
  total: number,
): string {
  const state =
    usage.altText === null
      ? usage.mimeType === "video/mp4"
        ? "尚未提供视频描述"
        : "尚未提供图片描述"
      : usage.altText === ""
        ? "明确标记为纯装饰"
        : usage.altText;
  return usage.mimeType === "video/mp4"
    ? `打开第 ${index + 1} 项视频，共 ${total} 项：${state}`
    : `打开第 ${index + 1} 张图片，共 ${total} 张：${state}`;
}

function previewUrl(mediaId: string) {
  return `/api/v1/media/${encodeURIComponent(mediaId)}/preview`;
}

function originalUrl(mediaId: string) {
  return `/api/v1/media/${encodeURIComponent(mediaId)}`;
}

function usageKey(usage: PublicMediaUsage): string {
  return `${usage.position}:${usage.mediaId}`;
}

function MediaAsset({
  usage,
  index,
  total,
  original,
  eagerVideo,
  onError,
}: {
  usage: PublicMediaUsage;
  index: number;
  total: number;
  original: boolean;
  eagerVideo: boolean;
  onError: () => void;
}) {
  if (usage.mimeType === "video/mp4") {
    return (
      <ViewportVideo
        eager={eagerVideo}
        label={mediaAlt(usage, index, total)}
        onError={onError}
        poster={`/api/v1/media/${encodeURIComponent(usage.mediaId)}/poster`}
        src={original ? originalUrl(usage.mediaId) : previewUrl(usage.mediaId)}
      />
    );
  }
  return (
    <img
      alt={mediaAlt(usage, index, total)}
      decoding="async"
      loading="lazy"
      onError={onError}
      src={original ? originalUrl(usage.mediaId) : previewUrl(usage.mediaId)}
      width={usage.width}
      height={usage.height}
    />
  );
}

export function ImageGrid({
  media,
  eagerVideo = false,
}: {
  media: PublicMediaUsage[];
  eagerVideo?: boolean;
}) {
  const [selected, setSelected] = useState<number | null>(null);
  const [failed, setFailed] = useState(() => new Set<string>());
  if (media.length === 0) return null;
  const containsVideo = media.some((usage) => usage.mimeType === "video/mp4");
  const lightboxItems: MediaLightboxItem[] = media.map((usage, index) => ({
    key: usageKey(usage),
    previewSrc: previewUrl(usage.mediaId),
    originalSrc: originalUrl(usage.mediaId),
    alt: mediaAlt(usage, index, media.length),
    description:
      usage.altText === null
        ? usage.mimeType === "video/mp4"
          ? "这段视频尚未填写描述。"
          : "这张图片尚未填写描述。"
        : usage.altText === ""
          ? usage.mimeType === "video/mp4"
            ? "这是一段明确标记为装饰用途的视频。"
            : "这是一张明确标记为装饰用途的图片。"
          : usage.altText,
    width: usage.width,
    height: usage.height,
    kind: usage.mimeType === "video/mp4" ? "video" : "image",
    posterSrc:
      usage.mimeType === "video/mp4"
        ? `/api/v1/media/${encodeURIComponent(usage.mediaId)}/poster`
        : null,
  }));

  const markFailed = (usage: PublicMediaUsage) => {
    setFailed((current) => new Set(current).add(usageKey(usage)));
  };

  return (
    <>
      <div
        aria-label={
          containsVideo ? `${media.length} 项媒体` : `${media.length} 张图片`
        }
        className={styles.imageGrid}
        data-media-count={media.length}
        role="group"
      >
        {media.map((usage, index) => (
          <button
            aria-label={tileName(usage, index, media.length)}
            className={styles.imageTile}
            key={`${usage.position}-${usage.mediaId}`}
            onClick={() => setSelected(index)}
            style={
              media.length === 1
                ? ({
                    "--single-media-ratio": `${usage.width} / ${usage.height}`,
                    "--single-media-max-width": `${34 * (usage.width / usage.height)}rem`,
                  } as CSSProperties)
                : undefined
            }
            type="button"
          >
            {failed.has(usageKey(usage)) ? (
              <span
                className={styles.mediaFallback}
                role="img"
                aria-label={
                  mediaAlt(usage, index, media.length) ||
                  (containsVideo ? "装饰媒体不可用" : "装饰图片不可用")
                }
              >
                <span aria-hidden="true">×</span>
                {containsVideo ? "媒体暂不可用" : "图片暂不可用"}
              </span>
            ) : (
              <MediaAsset
                eagerVideo={eagerVideo}
                index={index}
                onError={() => markFailed(usage)}
                original={false}
                total={media.length}
                usage={usage}
              />
            )}
            {media.length >= 5 ? (
              <span
                aria-hidden="true"
                className={styles.orderMark}
                data-media-order={index + 1}
              >
                {String(index + 1).padStart(2, "0")}
              </span>
            ) : null}
          </button>
        ))}
      </div>
      <MediaLightbox
        items={lightboxItems}
        onSelectedChange={setSelected}
        selected={selected}
      />
    </>
  );
}
