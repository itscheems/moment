import { useState, type CSSProperties } from "react";

import type { PublicMediaUsage } from "../../api/public";
import {
  mediaOriginalUrl,
  mediaPosterUrl,
  mediaPreviewUrl,
} from "../../api/mediaUrls";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import { ViewportVideo } from "./ViewportVideo";
import { isVideoMimeType } from "./mediaState";
import styles from "./Media.module.css";
import { useMediaViewer, type MediaSource } from "./mediaViewerContext";

function mediaAlt(
  usage: PublicMediaUsage,
  index: number,
  total: number,
  mixed = false,
): string {
  if (usage.altText === "") return "";
  if (usage.altText !== null) return usage.altText;
  return isVideoMimeType(usage.mimeType)
    ? `未提供视频描述，第 ${index + 1} 项，共 ${total} 项`
    : mixed
      ? `未提供图片描述，第 ${index + 1} 项媒体，共 ${total} 项`
      : `未提供图片描述，第 ${index + 1} 张，共 ${total} 张`;
}

function imageTileName(
  usage: PublicMediaUsage,
  index: number,
  total: number,
  mixed = false,
): string {
  const state =
    usage.altText === null
      ? "尚未提供图片描述"
      : usage.altText === ""
        ? "明确标记为纯装饰"
        : usage.altText;
  return mixed
    ? `打开第 ${index + 1} 项媒体，共 ${total} 项：${state}`
    : `打开第 ${index + 1} 张图片，共 ${total} 张：${state}`;
}

function usageKey(usage: PublicMediaUsage): string {
  return `${usage.position}:${usage.mediaId}`;
}

function mediaShape(usage: PublicMediaUsage): "tall" | undefined {
  return usage.width * 3 < usage.height * 2 ? "tall" : undefined;
}

function MediaAsset({
  usage,
  index,
  total,
  original,
  eagerVideo,
  onError,
  onOpen,
  openOnSurface,
  mixed,
}: {
  usage: PublicMediaUsage;
  index: number;
  total: number;
  original: boolean;
  eagerVideo: boolean;
  onError: () => void;
  onOpen?: (trigger: HTMLElement) => void;
  openOnSurface?: boolean;
  mixed?: boolean;
}) {
  if (isVideoMimeType(usage.mimeType)) {
    return (
      <ViewportVideo
        eager={eagerVideo}
        originalSrc={mediaOriginalUrl(usage.mediaId)}
        label={mediaAlt(usage, index, total, mixed)}
        onError={onError}
        onOpen={onOpen}
        openOnSurface={openOnSurface}
        poster={mediaPosterUrl(usage.mediaId)}
        src={
          original
            ? mediaOriginalUrl(usage.mediaId)
            : mediaPreviewUrl(usage.mediaId)
        }
      />
    );
  }
  return (
    <img
      alt={mediaAlt(usage, index, total, mixed)}
      decoding="async"
      loading="lazy"
      onError={onError}
      src={
        original
          ? mediaOriginalUrl(usage.mediaId)
          : mediaPreviewUrl(usage.mediaId)
      }
      width={usage.width}
      height={usage.height}
    />
  );
}

export function buildLightboxItems(
  media: PublicMediaUsage[],
): MediaLightboxItem[] {
  const mixed = media.some((usage) => isVideoMimeType(usage.mimeType));
  return media.map((usage, index) => ({
    key: usageKey(usage),
    previewSrc: mediaPreviewUrl(usage.mediaId),
    originalSrc: mediaOriginalUrl(usage.mediaId),
    alt: mediaAlt(usage, index, media.length, mixed),
    // Only a description the author wrote. "尚未填写描述" and "纯装饰" are
    // facts about the metadata, not about the picture, and printing them
    // under it told the reader nothing they wanted to know.
    description: usage.altText ?? "",
    width: usage.width,
    height: usage.height,
    kind: isVideoMimeType(usage.mimeType) ? "video" : "image",
    posterSrc: isVideoMimeType(usage.mimeType)
      ? mediaPosterUrl(usage.mediaId)
      : null,
  }));
}

export function ImageGrid({
  media,
  eagerVideo = false,
  compactTallVideo = false,
  source,
}: {
  media: PublicMediaUsage[];
  eagerVideo?: boolean;
  compactTallVideo?: boolean;
  source?: MediaSource;
}) {
  const viewer = useMediaViewer();
  const [selected, setSelected] = useState<number | null>(null);
  const [failed, setFailed] = useState(() => new Set<string>());
  if (media.length === 0) return null;
  const containsVideo = media.some((usage) => isVideoMimeType(usage.mimeType));
  const singleUsage = media.length === 1 ? media[0] : null;
  const singleMediaStyle = singleUsage
    ? ({
        "--single-media-ratio": `${singleUsage.width} / ${singleUsage.height}`,
        "--single-media-max-width": `${34 * (singleUsage.width / singleUsage.height)}rem`,
      } as CSSProperties)
    : undefined;
  const singleMediaShape =
    singleUsage && isVideoMimeType(singleUsage.mimeType)
      ? mediaShape(singleUsage)
      : undefined;
  const lightboxItems = buildLightboxItems(media);

  const openMedia = (index: number, trigger: HTMLElement) => {
    if (viewer && source) viewer.open(source, lightboxItems, index, trigger);
    else setSelected(index);
  };

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
        data-compact-tall-video={
          compactTallVideo && singleMediaShape === "tall" ? true : undefined
        }
        data-media-count={media.length}
        data-media-shape={singleMediaShape}
        role="group"
        style={singleMediaStyle}
      >
        {media.map((usage, index) => {
          const videoUsage = isVideoMimeType(usage.mimeType);
          const contents = failed.has(usageKey(usage)) ? (
            <span
              className={styles.mediaFallback}
              role="img"
              aria-label={
                mediaAlt(usage, index, media.length, containsVideo) ||
                (containsVideo ? "装饰媒体不可用" : "装饰图片不可用")
              }
            >
              <span aria-hidden="true">×</span>
              {containsVideo ? "媒体暂不可用" : "图片暂不可用"}
            </span>
          ) : (
            <MediaAsset
              eagerVideo={eagerVideo}
              mixed={containsVideo}
              onOpen={(trigger) => openMedia(index, trigger)}
              openOnSurface={media.length > 1}
              index={index}
              onError={() => markFailed(usage)}
              original={false}
              total={media.length}
              usage={usage}
            />
          );
          if (videoUsage) {
            return (
              <div
                className={`${styles.imageTile} ${styles.videoTile}`}
                data-media-shape={mediaShape(usage)}
                key={`${usage.position}-${usage.mediaId}`}
                style={singleMediaStyle}
              >
                {contents}
              </div>
            );
          }
          return (
            <button
              aria-label={imageTileName(
                usage,
                index,
                media.length,
                containsVideo,
              )}
              className={`${styles.imageTile} ${styles.imageButton}`}
              key={`${usage.position}-${usage.mediaId}`}
              onClick={(event) => {
                event.stopPropagation();
                openMedia(index, event.currentTarget);
              }}
              style={singleMediaStyle}
              type="button"
            >
              {contents}
            </button>
          );
        })}
      </div>
      <MediaLightbox
        items={lightboxItems}
        onSelectedChange={setSelected}
        selected={selected}
      />
    </>
  );
}
