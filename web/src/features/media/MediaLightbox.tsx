import { Dialog } from "@base-ui/react/dialog";
import { useEffect, useState } from "react";

import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import styles from "./Media.module.css";

export interface MediaLightboxItem {
  key: string;
  previewSrc: string;
  originalSrc?: string | null;
  alt: string;
  description: string;
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

  useDocumentScrollLock(open);

  useEffect(() => {
    setFailed(false);
  }, [selected]);

  const select = (next: number) => {
    if (items.length === 0) return;
    onSelectedChange((next + items.length) % items.length);
  };

  const source = item?.previewSrc;
  const canViewOriginal = Boolean(item?.originalSrc);
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
              图片 {selected === null ? "" : selected + 1} / {items.length}
            </Dialog.Title>
            <Dialog.Close
              aria-label="关闭图片预览"
              className={styles.lightboxClose}
            >
              <span aria-hidden="true">×</span>
            </Dialog.Close>
            {items.length > 1 ? (
              <span aria-live="polite" className={styles.lightboxCounter}>
                {(selected ?? 0) + 1} / {items.length}
              </span>
            ) : null}
            <div className={styles.lightboxStage}>
              {item && source && !failed ? (
                <img
                  alt={item.alt}
                  decoding="async"
                  key={item.key}
                  onError={() => setFailed(true)}
                  src={source}
                  width={item.width ?? undefined}
                  height={item.height ?? undefined}
                />
              ) : (
                <div className={styles.lightboxFallback} role="alert">
                  图片暂时无法显示。
                </div>
              )}
              {items.length > 1 ? (
                <>
                  <button
                    aria-label="上一张图片"
                    className={`${styles.lightboxNav} ${styles.lightboxPrevious}`}
                    onClick={() => select((selected ?? 0) - 1)}
                    type="button"
                  >
                    <span aria-hidden="true">‹</span>
                  </button>
                  <button
                    aria-label="下一张图片"
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
              <Dialog.Description className={styles.lightboxDescription}>
                {item?.description}
              </Dialog.Description>
              {canViewOriginal && item?.originalSrc ? (
                <a
                  className={styles.lightboxOriginal}
                  href={item.originalSrc}
                  rel="noreferrer"
                  target="_blank"
                >
                  打开原图
                </a>
              ) : null}
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
