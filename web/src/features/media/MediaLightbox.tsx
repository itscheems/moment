import { Dialog } from "../../components/Dialog";
import {
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type RefObject,
} from "react";
import { Menu } from "@base-ui/react/menu";
import { useElementSize, useMergedRef } from "@mantine/hooks";
import { viewerMediaSize } from "./viewerMediaSize";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { useMediaQuery } from "../../components/useMediaQuery";
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

// Filled artwork on the reference viewer's 24-unit grid.
const viewerPaths = {
  more: "M3 12c0-1.1.9-2 2-2s2 .9 2 2-.9 2-2 2-2-.9-2-2zm9 2c1.1 0 2-.9 2-2s-.9-2-2-2-2 .9-2 2 .9 2 2 2zm7 0c1.1 0 2-.9 2-2s-.9-2-2-2-2 .9-2 2 .9 2 2 2z",
  close:
    "M10.59 12L4.54 5.96l1.42-1.42L12 10.59l6.04-6.05 1.42 1.42L13.41 12l6.05 6.04-1.42 1.42L12 13.41l-6.04 6.05-1.42-1.42L10.59 12z",
  next: "M12.957 4.54L20.414 12l-7.457 7.46-1.414-1.42L16.586 13H3v-2h13.586l-5.043-5.04 1.414-1.42z",
  hide: "M11.59 12L3.54 3.96l1.42-1.42L14.41 12l-9.45 9.46-1.42-1.42L11.59 12zm7 0l-8.05-8.04 1.42-1.42L21.41 12l-9.45 9.46-1.42-1.42L18.59 12z",
};
function ViewerIcon({
  name,
  reverse = false,
}: {
  name: keyof typeof viewerPaths;
  reverse?: boolean;
}) {
  return (
    <svg
      aria-hidden="true"
      viewBox="0 0 24 24"
      className={reverse ? styles.viewerReverse : undefined}
    >
      <path d={viewerPaths[name]} />
    </svg>
  );
}

export function MediaLightbox({
  items,
  selected,
  onSelectedChange,
  discussion,
  panelShown = false,
  onPanelShownChange,
  toolbar,
  onViewPost,
  returnFocus,
  unavailable = false,
}: {
  items: MediaLightboxItem[];
  selected: number | null;
  onSelectedChange: (selected: number | null) => void;
  discussion?: ReactNode;
  panelShown?: boolean;
  onPanelShownChange?: (shown: boolean) => void;
  toolbar?: ReactNode;
  onViewPost?: () => void;
  returnFocus?: RefObject<HTMLElement | null>;
  unavailable?: boolean;
}) {
  const [failed, setFailed] = useState(false);
  const [descriptionOpen, setDescriptionOpen] = useState(false);
  const item = selected === null ? null : (items[selected] ?? null);
  const open = selected !== null && item !== null;
  const mobile = useMediaQuery("(max-width: 687px)");
  const containsVideo = items.some((candidate) => candidate.kind === "video");
  const lastOpened = useRef<{ item: MediaLightboxItem; index: number } | null>(
    null,
  );
  const {
    ref: resizeStageRef,
    width: stageWidth,
    height: stageHeight,
  } = useElementSize<HTMLDivElement>();
  const stageRef = useRef<HTMLDivElement>(null);
  const combinedStageRef = useMergedRef(stageRef, resizeStageRef);
  const [natural, setNatural] = useState<{
    key: string;
    width: number;
    height: number;
  } | null>(null);
  const lastPointer = useRef<{ x: number; y: number } | null>(null);
  useDocumentScrollLock(open);
  useEffect(() => {
    setFailed(false);
    setDescriptionOpen(false);
  }, [selected, item?.key]);
  useEffect(() => {
    if (item && selected !== null)
      lastOpened.current = { item, index: selected };
  }, [item, selected]);
  const shown = item ?? lastOpened.current?.item ?? null;
  const shownIndex = selected ?? lastOpened.current?.index ?? 0;
  const intrinsic =
    shown?.width && shown.height
      ? { width: shown.width, height: shown.height }
      : natural?.key === shown?.key
        ? natural
        : null;
  const fitted = intrinsic
    ? viewerMediaSize(
        intrinsic.width,
        intrinsic.height,
        stageWidth,
        stageHeight,
      )
    : null;
  const mediaStyle = fitted
    ? { width: fitted.width, height: fitted.height }
    : undefined;
  const select = (next: number) => {
    if (next >= 0 && next < items.length) {
      stageRef.current?.focus({ preventScroll: true });
      onSelectedChange(next);
    }
  };
  const editable = (target: EventTarget | null) =>
    target instanceof Element &&
    Boolean(
      target.closest(
        'input, textarea, select, [contenteditable="true"], [role="textbox"], video, [role="menu"], [role="slider"]',
      ),
    );
  const original =
    shown?.originalSrc && !unavailable ? (
      <a
        className={styles.lightboxOriginal}
        href={shown.originalSrc}
        rel="noreferrer"
        target="_blank"
      >
        {shown.kind === "video" ? "打开原画质视频" : "打开原图"}
      </a>
    ) : null;

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) onSelectedChange(null);
      }}
    >
      <Dialog.Portal
        className={styles.lightboxPortal}
        container={typeof document === "undefined" ? null : document.body}
        keepMounted={Boolean(discussion)}
      >
        <Dialog.Backdrop className={styles.lightboxBackdrop} />
        <Dialog.Viewport className={styles.lightboxViewport}>
          <Dialog.Popup
            className={styles.lightboxPopup}
            data-media-viewer-open={discussion && open ? "true" : undefined}
            data-discussion={discussion ? "true" : undefined}
            data-panel-shown={
              discussion && panelShown && !mobile ? "true" : undefined
            }
            finalFocus={returnFocus}
            onKeyDown={(event) => {
              if (
                event.defaultPrevented ||
                event.nativeEvent.isComposing ||
                event.shiftKey ||
                event.altKey ||
                event.ctrlKey ||
                event.metaKey ||
                editable(event.target)
              )
                return;
              // Child portals retain React ancestry. Their keys belong to them.
              if (
                (event.target as Element).closest('[role="dialog"]') !==
                event.currentTarget
              )
                return;
              if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
                event.preventDefault();
                select((selected ?? 0) + (event.key === "ArrowLeft" ? -1 : 1));
              }
            }}
          >
            <Dialog.Title className={styles.srOnly}>
              {containsVideo ? "媒体" : "图片"} {shownIndex + 1} /{" "}
              {items.length}
            </Dialog.Title>
            <div className={styles.lightboxMedia}>
              <div
                className={styles.lightboxStage}
                ref={combinedStageRef}
                tabIndex={-1}
                onPointerDown={(event) => {
                  if (
                    event.target instanceof Element &&
                    !event.target.closest("button, a, video")
                  )
                    event.currentTarget.focus({ preventScroll: true });
                  lastPointer.current = { x: event.clientX, y: event.clientY };
                }}
                onClick={(event) => {
                  const start = lastPointer.current;
                  if (
                    event.target !== event.currentTarget ||
                    window.getSelection()?.toString()
                  )
                    return;
                  if (
                    start &&
                    Math.hypot(
                      event.clientX - start.x,
                      event.clientY - start.y,
                    ) > 5
                  )
                    return;
                  onSelectedChange(null);
                }}
              >
                {shown && shown.previewSrc && !failed && !unavailable ? (
                  shown.kind === "video" ? (
                    open ? (
                      <video
                        style={mediaStyle}
                        onLoadedMetadata={(event) =>
                          setNatural({
                            key: shown.key,
                            width: event.currentTarget.videoWidth,
                            height: event.currentTarget.videoHeight,
                          })
                        }
                        aria-label={shown.alt}
                        autoPlay
                        controls
                        key={shown.key}
                        onError={() => setFailed(true)}
                        playsInline
                        poster={shown.posterSrc ?? undefined}
                        preload="metadata"
                        src={shown.previewSrc}
                      />
                    ) : null
                  ) : (
                    <img
                      style={mediaStyle}
                      onLoad={(event) =>
                        setNatural({
                          key: shown.key,
                          width: event.currentTarget.naturalWidth,
                          height: event.currentTarget.naturalHeight,
                        })
                      }
                      alt={shown.alt}
                      decoding="async"
                      key={shown.key}
                      onError={() => setFailed(true)}
                      src={shown.previewSrc}
                      width={shown.width ?? undefined}
                      height={shown.height ?? undefined}
                    />
                  )
                ) : shown ? (
                  <div className={styles.lightboxFallback} role="alert">
                    {unavailable
                      ? "此内容已不可用。"
                      : containsVideo
                        ? "媒体暂时无法显示。"
                        : "图片暂时无法显示。"}
                  </div>
                ) : null}
                {shownIndex > 0 ? (
                  <button
                    aria-label={containsVideo ? "上一项媒体" : "上一张图片"}
                    className={
                      styles.lightboxNav + " " + styles.lightboxPrevious
                    }
                    onClick={() => select(shownIndex - 1)}
                    type="button"
                  >
                    <ViewerIcon name="next" reverse />
                  </button>
                ) : null}
                {shownIndex < items.length - 1 ? (
                  <button
                    aria-label={containsVideo ? "下一项媒体" : "下一张图片"}
                    className={styles.lightboxNav + " " + styles.lightboxNext}
                    onClick={() => select(shownIndex + 1)}
                    type="button"
                  >
                    <ViewerIcon name="next" />
                  </button>
                ) : null}
              </div>
              <Dialog.Close
                aria-label={containsVideo ? "关闭媒体预览" : "关闭图片预览"}
                className={styles.lightboxClose}
              >
                <Icon name="media-remove" />
              </Dialog.Close>
              {discussion ? (
                mobile ? (
                  <Menu.Root>
                    <Menu.Trigger
                      aria-label="图片选项"
                      className={styles.lightboxToggle}
                    >
                      <ViewerIcon name="more" />
                    </Menu.Trigger>
                    <Menu.Portal>
                      <Menu.Positioner
                        className={styles.viewerMenuPositioner}
                        side="bottom"
                        align="end"
                        sideOffset={-35}
                      >
                        <Menu.Popup className={styles.viewerMenu}>
                          <Menu.Item onClick={onViewPost}>
                            查看所属内容与回复
                          </Menu.Item>
                          {original ? (
                            <Menu.Item render={original}>打开原图</Menu.Item>
                          ) : null}
                        </Menu.Popup>
                      </Menu.Positioner>
                    </Menu.Portal>
                  </Menu.Root>
                ) : (
                  <button
                    aria-label={panelShown ? "隐藏帖子" : "显示帖子"}
                    aria-expanded={panelShown}
                    className={styles.lightboxToggle}
                    onClick={() => onPanelShownChange?.(!panelShown)}
                    type="button"
                  >
                    <ViewerIcon name="hide" reverse={!panelShown} />
                  </button>
                )
              ) : null}
              <div className={styles.lightboxFooter}>
                {toolbar}
                {shown?.description && !unavailable ? (
                  <button
                    aria-label="查看图片说明"
                    className={styles.lightboxAlt}
                    onClick={() => setDescriptionOpen((v) => !v)}
                    aria-expanded={descriptionOpen}
                    type="button"
                  >
                    ALT
                  </button>
                ) : null}
                {!mobile || !discussion ? original : null}
                <span className={styles.srOnly} aria-live="polite">
                  {shownIndex + 1} / {items.length}
                </span>
              </div>
              {shown?.description && descriptionOpen && !unavailable ? (
                <div className={styles.lightboxCaption}>
                  <p className={styles.lightboxDescription}>
                    {shown.description}
                  </p>
                </div>
              ) : null}
            </div>
            {discussion ? (
              <aside
                className={styles.lightboxDiscussion}
                aria-label="图片所属内容与回复"
                hidden={!open || !panelShown || mobile}
                inert={!open || !panelShown || mobile}
              >
                {discussion}
              </aside>
            ) : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
