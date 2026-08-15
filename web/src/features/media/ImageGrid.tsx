import { useState } from "react";

import type { PublicMediaUsage } from "../../api/public";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import styles from "./Media.module.css";

function mediaAlt(
  usage: PublicMediaUsage,
  index: number,
  total: number,
): string {
  if (usage.altText === "") return "";
  if (usage.altText !== null) return usage.altText;
  return `未提供图片描述，第 ${index + 1} 张，共 ${total} 张`;
}

function tileName(
  usage: PublicMediaUsage,
  index: number,
  total: number,
): string {
  const state =
    usage.altText === null
      ? "尚未提供图片描述"
      : usage.altText === ""
        ? "明确标记为纯装饰"
        : usage.altText;
  return `打开第 ${index + 1} 张图片，共 ${total} 张：${state}`;
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

function MediaImage({
  usage,
  index,
  total,
  original,
  onError,
}: {
  usage: PublicMediaUsage;
  index: number;
  total: number;
  original: boolean;
  onError: () => void;
}) {
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

export function ImageGrid({ media }: { media: PublicMediaUsage[] }) {
  const [selected, setSelected] = useState<number | null>(null);
  const [failed, setFailed] = useState(() => new Set<string>());
  if (media.length === 0) return null;
  const lightboxItems: MediaLightboxItem[] = media.map((usage, index) => ({
    key: usageKey(usage),
    previewSrc: previewUrl(usage.mediaId),
    originalSrc: originalUrl(usage.mediaId),
    alt: mediaAlt(usage, index, media.length),
    description:
      usage.altText === null
        ? "这张图片尚未填写描述。"
        : usage.altText === ""
          ? "这是一张明确标记为装饰用途的图片。"
          : usage.altText,
    width: usage.width,
    height: usage.height,
  }));

  const markFailed = (usage: PublicMediaUsage) => {
    setFailed((current) => new Set(current).add(usageKey(usage)));
  };

  return (
    <>
      <div
        aria-label={`${media.length} 张图片`}
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
            type="button"
          >
            {failed.has(usageKey(usage)) ? (
              <span
                className={styles.mediaFallback}
                role="img"
                aria-label={
                  mediaAlt(usage, index, media.length) || "装饰图片不可用"
                }
              >
                <span aria-hidden="true">×</span>
                图片暂不可用
              </span>
            ) : (
              <MediaImage
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
