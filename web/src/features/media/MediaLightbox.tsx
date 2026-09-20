import { Dialog } from "@base-ui/react/dialog";
import { useEffect, useRef, useState } from "react";

import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import styles from "./Media.module.css";

export interface MediaLightboxItem {
  key: string;
  previewSrc: string;
  originalSrc?: string | null;
  alt: string;
  description: string;
  kind?: "image" | "video";
  posterSrc?: string | null;
  width?: number | null;
  height?: number | null;
}

export function MediaLightbox({
  items,
  selected,
  onSelectedChange,
}: {
  items: MediaLightboxItem[];
  selected: number | null;
  onSelectedChange: (selected: number | null) => void;
}) {
  const [failed, setFailed] = useState(false);
  const item = selected === null ? null : (items[selected] ?? null);
  const open = selected !== null && item !== null;
  const containsVideo = items.some((candidate) => candidate.kind === "video");

  useDocumentScrollLock(open);

  useEffect(() => {
    setFailed(false);
  }, [selected]);

  // What the dialog keeps showing while it animates out.
  //
  // Closing sets `selected` to null, so `item` is already gone on the first
  // frame of the 180ms exit -- and the render's last branch is the "media
  // unavailable" panel, which meant every close played that light grey error
  // box over the black backdrop instead of the photo. Measured: at t=7ms the
  // stage read 图片暂时无法显示。 on rgb(239 243 244) and stayed there for the
  // whole fade. Holding the last opened item lets the picture be the thing
  // that fades.
  const lastOpened = useRef<{ item: MediaLightboxItem; index: number } | null>(
    null,
  );
  useEffect(() => {
    if (item && selected !== null)
      lastOpened.current = { item, index: selected };
  }, [item, selected]);
  const shown = item ?? lastOpened.current?.item ?? null;
  const shownIndex = selected ?? lastOpened.current?.index ?? 0;

  const select = (next: number) => {
    if (items.length === 0) return;
    onSelectedChange((next + items.length) % items.length);
  };

  const source = shown?.previewSrc;
  const canViewOriginal = Boolean(shown?.originalSrc);
  const portalContainer =
    typeof document === "undefined" ? null : document.body;

  return (
    <Dialog.Root
      onOpenChange={(open) => {
        if (!open) onSelectedChange(null);
      }}
      open={open}
    >
      <Dialog.Portal
        className={styles.lightboxPortal}
        container={portalContainer}
      >
        <Dialog.Backdrop className={styles.lightboxBackdrop} />
        <Dialog.Viewport className={styles.lightboxViewport}>
          <Dialog.Popup
            className={styles.lightboxPopup}
            // A press on the dark surround closes, the way every photo
            // viewer does it. The popup fills the screen so the backdrop
            // underneath it can never be pressed -- measured, a point far
            // from the picture hit `.lightboxStage`, inside the dialog, so
            // there was no "outside" left to click.
            onClick={(event) => {
              if (
                (event.target as HTMLElement).closest("img, video, button, a")
              )
                return;
              onSelectedChange(null);
            }}
            onKeyDown={(event) => {
              if (items.length < 2) return;
              if (event.key === "ArrowLeft") {
                event.preventDefault();
                select((selected ?? 0) - 1);
              }
              if (event.key === "ArrowRight") {
                event.preventDefault();
                select((selected ?? 0) + 1);
              }
            }}
          >
            <Dialog.Title className={styles.srOnly}>
              {containsVideo ? "媒体" : "图片"} {shownIndex + 1} /{" "}
              {items.length}
            </Dialog.Title>
            <Dialog.Close
              aria-label={containsVideo ? "关闭媒体预览" : "关闭图片预览"}
              className={`${styles.lightboxClose} ${styles.mediaRemoveControl}`}
            >
              <Icon name="media-remove" />
            </Dialog.Close>
            {items.length > 1 ? (
              <span aria-live="polite" className={styles.lightboxCounter}>
                {shownIndex + 1} / {items.length}
              </span>
            ) : null}
            <div className={styles.lightboxStage}>
              {shown && source && !failed && shown.kind === "video" ? (
                <video
                  aria-label={shown.alt}
                  autoPlay
                  controls
                  key={shown.key}
                  onError={() => setFailed(true)}
                  playsInline
                  poster={shown.posterSrc ?? undefined}
                  preload="metadata"
                  src={source}
                />
              ) : shown && source && !failed ? (
                <img
                  alt={shown.alt}
                  decoding="async"
                  key={shown.key}
                  onError={() => setFailed(true)}
                  src={source}
                  width={shown.width ?? undefined}
                  height={shown.height ?? undefined}
                />
              ) : shown ? (
                // Only ever a real failure. This branch used to double as
                // "nothing is selected", which is what put an error panel
                // on screen for the length of every close.
                <div className={styles.lightboxFallback} role="alert">
                  {containsVideo ? "媒体暂时无法显示。" : "图片暂时无法显示。"}
                </div>
              ) : null}
              {items.length > 1 ? (
                <>
                  <button
                    aria-label={containsVideo ? "上一项媒体" : "上一张图片"}
                    className={`${styles.lightboxNav} ${styles.lightboxPrevious}`}
                    onClick={() => select((selected ?? 0) - 1)}
                    type="button"
                  >
                    <span aria-hidden="true">‹</span>
                  </button>
                  <button
                    aria-label={containsVideo ? "下一项媒体" : "下一张图片"}
                    className={`${styles.lightboxNav} ${styles.lightboxNext}`}
                    onClick={() => select((selected ?? 0) + 1)}
                    type="button"
                  >
                    <span aria-hidden="true">›</span>
                  </button>
                </>
              ) : null}
            </div>
            <div className={styles.lightboxFooter}>
              {/* Only a description the author wrote. "尚未填写描述" and
                  "纯装饰" are facts about the metadata, not about the
                  picture, and printing them under it told the reader
                  nothing they wanted to know. */}
              {shown?.description ? (
                <Dialog.Description className={styles.lightboxDescription}>
                  {shown.description}
                </Dialog.Description>
              ) : (
                <span />
              )}
              {canViewOriginal && shown?.originalSrc ? (
                <a
                  className={styles.lightboxOriginal}
                  href={shown.originalSrc}
                  rel="noreferrer"
                  target="_blank"
                >
                  {shown.kind === "video" ? "打开原画质视频" : "打开原图"}
                </a>
              ) : null}
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
