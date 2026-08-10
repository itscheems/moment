import { Dialog } from "@base-ui/react/dialog";
import { useState } from "react";

import type { PublicMediaUsage } from "../../api/public";
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
    />
  );
}

export function ImageGrid({ media }: { media: PublicMediaUsage[] }) {
  const [selected, setSelected] = useState<number | null>(null);
  const [failed, setFailed] = useState(() => new Set<number>());
  const [original, setOriginal] = useState(false);
  const [lightboxFailed, setLightboxFailed] = useState(false);
  if (media.length === 0) return null;
  const selectedUsage = selected === null ? null : media[selected];

  const markFailed = (index: number) => {
    setFailed((current) => new Set(current).add(index));
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
            onClick={() => {
              setSelected(index);
              setOriginal(false);
              setLightboxFailed(false);
            }}
            type="button"
          >
            {failed.has(index) ? (
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
                onError={() => markFailed(index)}
                original={false}
                total={media.length}
                usage={usage}
              />
            )}
            <span aria-hidden="true" className={styles.orderMark}>
              {String(index + 1).padStart(2, "0")}
            </span>
          </button>
        ))}
      </div>
      <Dialog.Root
        onOpenChange={(open) => {
          if (!open) {
            setSelected(null);
            setOriginal(false);
            setLightboxFailed(false);
          }
        }}
        open={selected !== null}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.lightboxBackdrop} />
          <Dialog.Viewport className={styles.lightboxViewport}>
            <Dialog.Popup className={styles.lightboxPopup}>
              <Dialog.Title className={styles.srOnly}>
                图片 {selected === null ? "" : selected + 1} / {media.length}
              </Dialog.Title>
              <Dialog.Description className={styles.lightboxDescription}>
                {selectedUsage?.altText === null
                  ? "这张图片尚未填写描述。"
                  : selectedUsage?.altText === ""
                    ? "这是一张明确标记为装饰用途的图片。"
                    : selectedUsage?.altText}
              </Dialog.Description>
              <Dialog.Close className={styles.lightboxClose}>关闭</Dialog.Close>
              <div className={styles.lightboxStage}>
                {selectedUsage && !lightboxFailed ? (
                  <MediaImage
                    index={selected ?? 0}
                    onError={() => setLightboxFailed(true)}
                    original={original}
                    total={media.length}
                    usage={selectedUsage}
                  />
                ) : (
                  <div className={styles.lightboxFallback} role="alert">
                    {original
                      ? "原图没有加载完成；仍可返回预览。"
                      : "预览暂不可用；Moment 正文仍保留在页面中。"}
                  </div>
                )}
              </div>
              <div className={styles.lightboxActions}>
                <button
                  disabled={(selected ?? 0) <= 0}
                  onClick={() => {
                    setSelected((value) =>
                      value === null ? null : Math.max(0, value - 1),
                    );
                    setOriginal(false);
                    setLightboxFailed(false);
                  }}
                  type="button"
                >
                  上一张
                </button>
                {original ? (
                  <button
                    onClick={() => {
                      setOriginal(false);
                      setLightboxFailed(false);
                    }}
                    type="button"
                  >
                    返回预览
                  </button>
                ) : (
                  <button
                    onClick={() => {
                      setOriginal(true);
                      setLightboxFailed(false);
                    }}
                    type="button"
                  >
                    查看原图 / 播放 GIF
                  </button>
                )}
                <button
                  disabled={(selected ?? 0) >= media.length - 1}
                  onClick={() => {
                    setSelected((value) =>
                      value === null
                        ? null
                        : Math.min(media.length - 1, value + 1),
                    );
                    setOriginal(false);
                    setLightboxFailed(false);
                  }}
                  type="button"
                >
                  下一张
                </button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
    </>
  );
}
