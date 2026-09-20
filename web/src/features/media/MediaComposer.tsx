import {
  type CSSProperties,
  type ChangeEvent,
  forwardRef,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import {
  AnimatePresence,
  motion,
  useIsPresent,
  useReducedMotion,
} from "motion/react";

import { Dialog } from "@base-ui/react/dialog";

import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { mediaPosterUrl } from "../../api/mediaUrls";
import type { ComposerMediaItem } from "./mediaState";
import {
  MAX_MEDIA_COUNT,
  MAX_ALT_CODE_POINTS,
  isVideoMimeType,
  selectMediaFiles,
} from "./mediaState";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import styles from "./Media.module.css";

const MEDIA_SPRING = {
  type: "spring" as const,
  duration: 0.5,
  bounce: 0.3,
};

const UPLOAD_SUCCESS_DURATION_MS = 1_600;

function VideoEditorPreview({
  fileName,
  height,
  pendingLabel,
  poster,
  width,
}: {
  fileName: string | null;
  height?: number;
  pendingLabel: string;
  poster?: string;
  width?: number;
}) {
  return (
    <span
      className={styles.editorVideoPreview}
      data-video-frame={poster ? "ready" : "loading"}
    >
      <span className={styles.videoCoverFallback} aria-hidden="true">
        <Icon name="media" />
        <span>{pendingLabel}</span>
        {fileName ? <small>{fileName}</small> : null}
      </span>
      {poster ? (
        <img
          alt=""
          decoding="async"
          height={height}
          src={poster}
          width={width}
        />
      ) : null}
    </span>
  );
}

const MediaPresenceItem = forwardRef<
  HTMLLIElement,
  {
    children: (present: boolean) => ReactNode;
    itemKey: string;
    reduceMotion: boolean;
    style?: CSSProperties;
  }
>(function MediaPresenceItem(
  { children, itemKey, reduceMotion, style },
  forwardedRef,
) {
  const present = useIsPresent();
  return (
    <motion.li
      animate={{ opacity: 1, scale: 1 }}
      aria-hidden={present ? undefined : true}
      className={styles.mediaEditorItem}
      data-media-slot={itemKey}
      data-media-state={present ? undefined : "departing"}
      exit={reduceMotion ? undefined : { opacity: 0, scale: 0.5 }}
      initial={reduceMotion ? false : { opacity: 0, scale: 0.5 }}
      layout={!reduceMotion}
      ref={forwardedRef}
      style={present ? style : { ...style, pointerEvents: "none" }}
      transition={MEDIA_SPRING}
    >
      {children(present)}
    </motion.li>
  );
});

export function MediaComposer({
  className,
  items,
  disabled,
  onAdd,
  onChange,
  onRemove,
  onRetry,
  footerAction,
  toolbarAction,
  compact = false,
  divider = false,
  pickerTooltip,
  selectionError: controlledSelectionError,
  onSelectionError,
  maximum = MAX_MEDIA_COUNT,
  maximumTotalBytes,
  subject = "Moment",
}: {
  className?: string;
  items: ComposerMediaItem[];
  disabled: boolean;
  onAdd: (files: File[]) => void;
  onChange: (items: ComposerMediaItem[], message: string) => void;
  onRemove: (key: string) => void;
  onRetry: (key: string) => void;
  footerAction?: ReactNode;
  toolbarAction?: ReactNode;
  compact?: boolean;
  /** Draw the rule above the toolbar even in the compact layout. */
  divider?: boolean;
  pickerTooltip?: string;
  selectionError?: string | null;
  onSelectionError?: (error: string | null) => void;
  maximum?: number;
  maximumTotalBytes?: number;
  subject?: string;
}) {
  const inputId = useId();
  const reduceMotion = useReducedMotion();
  const [localSelectionError, setLocalSelectionError] = useState<string | null>(
    null,
  );
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pickerRef = useRef<HTMLButtonElement>(null);
  const actionRefs = useRef(new Map<string, HTMLButtonElement>());
  // Where focus goes after the list changes under it. `remove` hands focus
  // to a neighbouring tile; a reorder keeps it on the button that was
  // pressed, because reordering happens inside the description sheet and
  // its focus trap would immediately pull focus back out of a tile behind
  // it.
  const focusAfterChange = useRef<
    { key: string | null; action: "remove" | "up" | "down" } | undefined
  >(undefined);
  const previousUploadStates = useRef(
    new Map(items.map((item) => [item.key, item.state])),
  );
  const uploadSuccessTimers = useRef(new Map<string, number>());
  const [recentlyUploaded, setRecentlyUploaded] = useState<Set<string>>(
    () => new Set(),
  );
  const containsVideo = items.some((item) => isVideoMimeType(item.mimeType));
  const undescribed = items.filter((item) => item.altMode === "missing").length;
  // The description sheet shows one picture at a time. It holds the item's key rather
  // than its index so that reordering carries the page with the picture the
  // writer is looking at, and so that a removal cannot silently swap in the
  // neighbour underneath the open sheet.
  const [describeKey, setDescribeKey] = useState<string | null>(null);
  const describePopupRef = useRef<HTMLDivElement>(null);
  const describeTextRef = useRef<HTMLTextAreaElement>(null);
  const describeIndex =
    describeKey === null
      ? -1
      : items.findIndex((item) => item.key === describeKey);
  const describing = describeIndex >= 0;
  const described = describing ? items[describeIndex] : null;
  useDocumentScrollLock(describing);

  const selectedItemIndex = items.findIndex((item) => item.key === selectedKey);
  const selectedIndex =
    selectedKey === null || selectedItemIndex < 0 ? null : selectedItemIndex;
  const lightboxItems: MediaLightboxItem[] = items.map((item, index) => {
    const video = isVideoMimeType(item.mimeType);
    const videoReady =
      video && item.state === "ready" && Boolean(item.previewUrl);
    const previewSrc = video
      ? videoReady
        ? item.previewUrl
        : item.processingPosterUrl || ""
      : item.localPreviewUrl || item.previewUrl || "";
    return {
      key: item.key,
      previewSrc,
      originalSrc: video
        ? videoReady
          ? item.originalUrl || null
          : null
        : item.localPreviewUrl || item.originalUrl || item.previewUrl || null,
      alt:
        item.altMode === "provided"
          ? item.altText
          : item.altMode === "decorative"
            ? ""
            : isVideoMimeType(item.mimeType)
              ? `未提供视频描述，第 ${index + 1} 项，共 ${items.length} 项`
              : `未提供图片描述，第 ${index + 1} 张，共 ${items.length} 张`,
      description:
        item.altMode === "provided"
          ? item.altText
          : item.altMode === "decorative"
            ? isVideoMimeType(item.mimeType)
              ? "这是一段明确标记为装饰用途的视频。"
              : "这是一张明确标记为装饰用途的图片。"
            : isVideoMimeType(item.mimeType)
              ? "这段视频尚未填写描述。"
              : "这张图片尚未填写描述。",
      kind: videoReady ? "video" : "image",
      posterSrc:
        videoReady && item.mediaId ? mediaPosterUrl(item.mediaId) : null,
    };
  });

  useEffect(() => {
    if (
      selectedKey !== null &&
      !items.some((item) => item.key === selectedKey)
    ) {
      setSelectedKey(null);
    }
  }, [items, selectedKey]);

  useEffect(() => {
    if (describeKey !== null && describeIndex < 0) setDescribeKey(null);
  }, [describeIndex, describeKey]);

  useEffect(() => {
    if (focusAfterChange.current === undefined) return;
    const target = focusAfterChange.current;
    focusAfterChange.current = undefined;
    window.requestAnimationFrame(() => {
      if (!target.key) {
        pickerRef.current?.focus();
        return;
      }
      if (target.action === "remove") {
        actionRefs.current.get(`${target.key}:remove`)?.focus();
        return;
      }
      // The pressed button, unless the move just reached an end and
      // disabled it -- then its opposite, which is where the next press
      // would go anyway.
      const pressed = actionRefs.current.get(`${target.key}:${target.action}`);
      const opposite = actionRefs.current.get(
        `${target.key}:${target.action === "up" ? "down" : "up"}`,
      );
      (pressed && !pressed.disabled ? pressed : opposite)?.focus();
    });
  }, [items]);

  useEffect(() => {
    const completed = items.filter(
      (item) =>
        previousUploadStates.current.get(item.key) === "uploading" &&
        item.state === "ready",
    );
    previousUploadStates.current = new Map(
      items.map((item) => [item.key, item.state]),
    );
    if (completed.length === 0) return;

    setRecentlyUploaded((current) => {
      const next = new Set(current);
      for (const item of completed) next.add(item.key);
      return next;
    });
    for (const item of completed) {
      const existing = uploadSuccessTimers.current.get(item.key);
      if (existing !== undefined) window.clearTimeout(existing);
      uploadSuccessTimers.current.set(
        item.key,
        window.setTimeout(() => {
          uploadSuccessTimers.current.delete(item.key);
          setRecentlyUploaded((current) => {
            if (!current.has(item.key)) return current;
            const next = new Set(current);
            next.delete(item.key);
            return next;
          });
        }, UPLOAD_SUCCESS_DURATION_MS),
      );
    }
  }, [items]);

  useEffect(
    () => () => {
      for (const timer of uploadSuccessTimers.current.values()) {
        window.clearTimeout(timer);
      }
      uploadSuccessTimers.current.clear();
    },
    [],
  );

  const selectionError =
    controlledSelectionError === undefined
      ? localSelectionError
      : controlledSelectionError;
  const reportSelectionError = (error: string | null) => {
    if (onSelectionError) onSelectionError(error);
    else setLocalSelectionError(error);
  };

  const selectFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const { accepted, error } = selectMediaFiles(
      Array.from(event.currentTarget.files ?? []),
      items,
      { maximum, maximumTotalBytes, subject },
    );
    reportSelectionError(error);
    if (accepted.length > 0) onAdd(accepted);
    event.currentTarget.value = "";
  };

  const move = (from: number, to: number) => {
    const next = [...items];
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item);
    focusAfterChange.current = {
      key: item.key,
      action: to < from ? "up" : "down",
    };
    onChange(next, `已将第 ${from + 1} 张移到第 ${to + 1} 张。`);
  };

  const remove = (index: number) => {
    const item = items[index];
    if (!item) return;
    focusAfterChange.current = {
      key: items[index + 1]?.key ?? items[index - 1]?.key ?? null,
      action: "remove",
    };
    if (selectedKey === item.key) setSelectedKey(null);
    onRemove(item.key);
  };

  return (
    <section
      aria-label={containsVideo ? "媒体" : "图片"}
      className={`${styles.composerMedia} ${compact ? styles.compactComposer : ""} ${divider ? styles.dividedComposer : ""} ${className ?? ""}`}
    >
      <input
        accept="image/jpeg,image/png,image/webp,image/gif,image/heic,image/heif,video/mp4,video/quicktime,.heic,.heif,.mp4,.m4v,.mov,.qt"
        aria-describedby={`${inputId}-guidance`}
        aria-label="媒体文件"
        disabled={disabled || items.length >= maximum}
        id={inputId}
        multiple
        onChange={selectFiles}
        ref={fileInputRef}
        tabIndex={-1}
        type="file"
      />
      <ol
        aria-label={
          containsVideo ? "待发布媒体，按发布顺序" : "待发布图片，按发布顺序"
        }
        className={styles.mediaEditorList}
        data-media-count={items.length}
      >
        <AnimatePresence initial={false} mode="popLayout">
          {items.map((item, index) => {
            const isVideo = isVideoMimeType(item.mimeType);
            const intrinsicWidth = item.width ?? 0;
            const intrinsicHeight = item.height ?? 0;
            const hasIntrinsicSize =
              items.length === 1 && intrinsicWidth > 0 && intrinsicHeight > 0;
            const editorItemStyle = hasIntrinsicSize
              ? ({
                  "--editor-media-ratio": `${intrinsicWidth} / ${intrinsicHeight}`,
                  "--editor-media-max-width": `${32 * (intrinsicWidth / intrinsicHeight)}rem`,
                } as CSSProperties)
              : undefined;
            const editorPreviewUrl =
              item.mimeType === "image/heic" && !item.previewUrl
                ? null
                : item.localPreviewUrl || item.previewUrl || null;
            const videoPosterUrl = isVideo
              ? (item.processingPosterUrl ??
                (item.mediaId ? mediaPosterUrl(item.mediaId) : null))
              : null;
            const editorPreviewAvailable = isVideo
              ? Boolean(videoPosterUrl)
              : Boolean(editorPreviewUrl);
            const processing =
              item.state === "uploading" && item.progress >= 99;
            const uploadStatus = processing
              ? isVideo
                ? item.processingPosterUrl
                  ? "封面已就绪 · 正在处理视频"
                  : "正在生成封面 · 视频处理中"
                : "正在处理媒体"
              : `上传 ${item.progress}%`;
            return (
              <MediaPresenceItem
                itemKey={item.key}
                key={item.key}
                reduceMotion={Boolean(reduceMotion)}
                style={editorItemStyle}
              >
                {(present) => (
                  <div className={styles.editorPreview}>
                    <button
                      aria-label={
                        isVideo
                          ? `预览第 ${index + 1} 项视频，共 ${items.length} 项`
                          : `预览第 ${index + 1} 张图片，共 ${items.length} 张`
                      }
                      className={styles.editorPreviewButton}
                      disabled={!editorPreviewAvailable || !present}
                      onClick={() => setSelectedKey(item.key)}
                      type="button"
                      data-preview-pending={
                        editorPreviewAvailable ? undefined : ""
                      }
                    >
                      {isVideo ? (
                        <VideoEditorPreview
                          fileName={item.file?.name ?? null}
                          height={item.height ?? undefined}
                          pendingLabel={
                            item.progress >= 99
                              ? "正在生成视频封面"
                              : "视频封面将在上传后生成"
                          }
                          poster={videoPosterUrl ?? undefined}
                          width={item.width ?? undefined}
                        />
                      ) : editorPreviewUrl ? (
                        <img
                          alt=""
                          decoding="async"
                          height={item.height ?? undefined}
                          src={editorPreviewUrl}
                          width={item.width ?? undefined}
                        />
                      ) : (
                        <span className={styles.editorPreviewPending}>
                          <span aria-hidden="true" />
                          {item.state === "uploading"
                            ? `正在处理 ${item.progress}%`
                            : item.state === "failed"
                              ? "处理未完成"
                              : "等待处理"}
                        </span>
                      )}
                    </button>
                    <button
                      aria-label={
                        isVideo
                          ? `移除第 ${index + 1} 项视频`
                          : `移除第 ${index + 1} 张图片`
                      }
                      className={`${styles.editorRemove} ${styles.mediaRemoveControl}`}
                      data-media-key={item.key}
                      disabled={disabled || !present}
                      onClick={() => remove(index)}
                      ref={(node) => {
                        const refKey = `${item.key}:remove`;
                        if (node) actionRefs.current.set(refKey, node);
                        else actionRefs.current.delete(refKey);
                      }}
                      type="button"
                    >
                      <Icon name="media-remove" />
                    </button>
                    {item.state === "uploading" ? (
                      <>
                        <div
                          aria-live="polite"
                          className={styles.mediaUploadStatus}
                          data-processing={processing ? "true" : undefined}
                          role="status"
                        >
                          <span
                            aria-hidden="true"
                            className={styles.uploadProgressRing}
                            style={
                              {
                                "--upload-progress": item.progress,
                              } as CSSProperties
                            }
                          >
                            {processing ? (
                              <span className={styles.processingSpinner} />
                            ) : (
                              <>
                                <svg viewBox="0 0 44 44">
                                  <circle cx="22" cy="22" r="18" />
                                  <circle
                                    cx="22"
                                    cy="22"
                                    pathLength="100"
                                    r="18"
                                  />
                                </svg>
                                <strong>{item.progress}%</strong>
                              </>
                            )}
                          </span>
                          <span>{uploadStatus}</span>
                        </div>
                        <progress
                          aria-label={
                            isVideo
                              ? `第 ${index + 1} 项上传进度`
                              : `第 ${index + 1} 张上传进度`
                          }
                          className={styles.srOnly}
                          max="100"
                          value={item.progress}
                        />
                      </>
                    ) : recentlyUploaded.has(item.key) ? (
                      <div
                        aria-live="polite"
                        className={styles.mediaUploadSuccess}
                        role="status"
                      >
                        <Icon name="check" />
                        <span>{isVideo ? "视频已上传" : "媒体已上传"}</span>
                      </div>
                    ) : null}
                  </div>
                )}
              </MediaPresenceItem>
            );
          })}
        </AnimatePresence>
      </ol>
      {items.some((item) => item.state === "failed") ? (
        <div className={styles.mediaFailures}>
          {items.map((item, index) =>
            item.state === "failed" ? (
              <div key={item.key}>
                <p role="alert">
                  第 {index + 1} 项上传失败
                  {item.error ? `：${item.error}` : ""}
                </p>
                <button
                  disabled={disabled}
                  onClick={() => onRetry(item.key)}
                  type="button"
                >
                  重试上传
                  <span className={styles.srOnly}>第 {index + 1} 项媒体</span>
                </button>
              </div>
            ) : null,
          )}
        </div>
      ) : null}
      {items.length > 0 ? (
        <Dialog.Root
          onOpenChange={(open) => {
            if (!open) {
              setDescribeKey(null);
              return;
            }
            // Open on the first picture that still needs describing: the
            // action says how many are missing, so pressing it should land
            // on one of them rather than on page one every time.
            const first =
              items.find((item) => item.altMode === "missing") ?? items[0];
            setDescribeKey(first?.key ?? null);
          }}
          open={describing}
        >
          {/* One quiet description action under the picture: no rules,
              no box, no disclosure triangle.
              This used to be a <details> whose `border-block` drew a hairline
              above *and* below itself, with the toolbar's own rule 8px under
              that -- three lines stacked in the gap between the picture and
              the toolbar. The count rides on the same line, and only while
              something still needs describing. */}
          <Dialog.Trigger className={styles.mediaDescribe} disabled={disabled}>
            <Icon name="description" />
            <span>添加描述</span>
            {undescribed > 0 ? <small>{undescribed} 项未描述</small> : null}
          </Dialog.Trigger>
          <Dialog.Portal>
            <Dialog.Backdrop className={styles.describeBackdrop} />
            <Dialog.Viewport className={styles.describeViewport}>
              {/* One picture per page, paged from the header. The sheet used
                  to stack all nine items as a list of
                  radio groups and textareas, which asked the writer to hold
                  the running order in their head and buried the picture each
                  description was about. */}
              <Dialog.Popup
                className={styles.describePopup}
                // The sheet itself, not the first control in it: Base UI
                // focuses the leading tabbable by default, which put a ring
                // on the back arrow every time the sheet opened under the
                // mouse. A description already being written keeps the
                // caret instead.
                initialFocus={() =>
                  describeTextRef.current ?? describePopupRef.current
                }
                ref={describePopupRef}
              >
                <header className={styles.describeHeader}>
                  <Dialog.Close
                    aria-label="返回"
                    className={styles.describeBack}
                  >
                    <Icon name="back" />
                  </Dialog.Close>
                  <Dialog.Title>
                    编辑{containsVideo ? "媒体" : "图片"}描述
                  </Dialog.Title>
                  {items.length > 1 ? (
                    <div className={styles.describePager}>
                      <button
                        aria-label={containsVideo ? "上一项媒体" : "上一张图片"}
                        disabled={describeIndex <= 0}
                        onClick={() =>
                          setDescribeKey(items[describeIndex - 1]?.key ?? null)
                        }
                        type="button"
                      >
                        <Icon name="chevron-left" />
                      </button>
                      <span aria-live="polite">
                        {describeIndex + 1} / {items.length}
                      </span>
                      <button
                        aria-label={containsVideo ? "下一项媒体" : "下一张图片"}
                        disabled={describeIndex >= items.length - 1}
                        onClick={() =>
                          setDescribeKey(items[describeIndex + 1]?.key ?? null)
                        }
                        type="button"
                      >
                        <Icon name="chevron-right" />
                      </button>
                    </div>
                  ) : null}
                  <Dialog.Close className={styles.describeDone}>
                    完成
                  </Dialog.Close>
                </header>
                {described
                  ? (() => {
                      const item = described;
                      const index = describeIndex;
                      const isVideo = isVideoMimeType(item.mimeType);
                      const itemLabel = isVideo
                        ? `第 ${index + 1} 项视频`
                        : `第 ${index + 1} 张图片`;
                      const providedInvalid =
                        item.altMode === "provided" &&
                        (!item.altText.trim() ||
                          Array.from(item.altText).length >
                            MAX_ALT_CODE_POINTS);
                      const stageUrl = isVideo
                        ? (item.processingPosterUrl ??
                          (item.mediaId ? mediaPosterUrl(item.mediaId) : null))
                        : item.localPreviewUrl || item.previewUrl || null;
                      return (
                        <>
                          <div className={styles.describeBody}>
                            <div className={styles.describeStage}>
                              {stageUrl ? (
                                <img alt="" decoding="async" src={stageUrl} />
                              ) : (
                                <span className={styles.describeStagePending}>
                                  {isVideo ? "视频封面处理中" : "预览处理中"}
                                </span>
                              )}
                            </div>
                            <p className={styles.describeCaption}>
                              <strong>{itemLabel}</strong>
                              <span>
                                {item.state === "queued" ? "等待上传" : null}
                                {item.state === "uploading"
                                  ? `处理中 ${item.progress}%`
                                  : null}
                                {item.state === "ready" ? "已就绪" : null}
                                {item.state === "failed" ? "上传失败" : null}
                              </span>
                            </p>
                            <fieldset
                              className={styles.altFieldset}
                              disabled={disabled}
                            >
                              <legend>{itemLabel}描述（可选）</legend>
                              <div className={styles.altModes}>
                                {(
                                  [
                                    ["missing", "暂未描述"],
                                    ["decorative", "纯装饰"],
                                    ["provided", "填写描述"],
                                  ] as const
                                ).map(([mode, label]) => (
                                  <label
                                    data-checked={
                                      item.altMode === mode ? "" : undefined
                                    }
                                    key={mode}
                                  >
                                    <input
                                      checked={item.altMode === mode}
                                      name={`${item.key}-alt-mode`}
                                      onChange={() => {
                                        const next = items.map((candidate) =>
                                          candidate.key === item.key
                                            ? {
                                                ...candidate,
                                                altMode: mode,
                                                error: null,
                                              }
                                            : candidate,
                                        );
                                        onChange(
                                          next,
                                          `${itemLabel}已设为${label}。`,
                                        );
                                      }}
                                      type="radio"
                                    />
                                    {label}
                                  </label>
                                ))}
                              </div>
                              {item.altMode === "provided" ? (
                                <label className={styles.altInput}>
                                  <span>
                                    {isVideo ? "描述视频内容" : "描述图片内容"}
                                  </span>
                                  <textarea
                                    aria-invalid={providedInvalid}
                                    // Choosing 填写描述 is a request to write
                                    // one, so the caret goes where the
                                    // writing happens. Mounting is the only
                                    // moment this fires, so paging between
                                    // pictures never steals the focus.
                                    autoFocus
                                    onChange={(event) =>
                                      onChange(
                                        items.map((candidate) =>
                                          candidate.key === item.key
                                            ? {
                                                ...candidate,
                                                altText: event.target.value,
                                                error: null,
                                              }
                                            : candidate,
                                        ),
                                        `已更新${itemLabel}描述。`,
                                      )
                                    }
                                    placeholder={
                                      isVideo
                                        ? "简要描述视频内容"
                                        : "简要描述图片内容"
                                    }
                                    ref={describeTextRef}
                                    rows={3}
                                    value={item.altText}
                                  />
                                  <small>
                                    {Array.from(item.altText).length} /{" "}
                                    {MAX_ALT_CODE_POINTS}
                                  </small>
                                </label>
                              ) : (
                                <p className={styles.altHint}>
                                  {item.altMode === "decorative"
                                    ? "读屏软件会跳过这一项。"
                                    : "描述会读给使用读屏软件的人听，也会在加载失败时顶替这张图。"}
                                </p>
                              )}
                            </fieldset>
                          </div>
                          {items.length > 1 ? (
                            <footer className={styles.describeFooter}>
                              {/* The running order, and a way to jump straight
                                to a picture instead of paging past the ones
                                in between. A dot marks the ones still
                                without a description. */}
                              <ol className={styles.describeRail}>
                                {items.map((candidate, candidateIndex) => {
                                  const thumbnail = isVideoMimeType(
                                    candidate.mimeType,
                                  )
                                    ? (candidate.processingPosterUrl ??
                                      (candidate.mediaId
                                        ? mediaPosterUrl(candidate.mediaId)
                                        : null))
                                    : candidate.localPreviewUrl ||
                                      candidate.previewUrl ||
                                      null;
                                  const label = isVideoMimeType(
                                    candidate.mimeType,
                                  )
                                    ? `第 ${candidateIndex + 1} 项视频`
                                    : `第 ${candidateIndex + 1} 张图片`;
                                  return (
                                    <li key={candidate.key}>
                                      <button
                                        aria-current={
                                          candidate.key === item.key
                                            ? "true"
                                            : undefined
                                        }
                                        aria-label={label}
                                        data-undescribed={
                                          candidate.altMode === "missing"
                                            ? ""
                                            : undefined
                                        }
                                        onClick={() =>
                                          setDescribeKey(candidate.key)
                                        }
                                        type="button"
                                      >
                                        {thumbnail ? (
                                          <img
                                            alt=""
                                            decoding="async"
                                            loading="lazy"
                                            src={thumbnail}
                                          />
                                        ) : (
                                          <span aria-hidden="true">
                                            {candidateIndex + 1}
                                          </span>
                                        )}
                                      </button>
                                    </li>
                                  );
                                })}
                              </ol>
                              <div className={styles.editorActions}>
                                <button
                                  data-media-action="up"
                                  data-media-key={item.key}
                                  disabled={disabled || index === 0}
                                  onClick={() => move(index, index - 1)}
                                  ref={(node) => {
                                    const refKey = `${item.key}:up`;
                                    if (node)
                                      actionRefs.current.set(refKey, node);
                                    else actionRefs.current.delete(refKey);
                                  }}
                                  type="button"
                                >
                                  上移
                                  <span className={styles.srOnly}>
                                    {itemLabel}
                                  </span>
                                </button>
                                <button
                                  data-media-action="down"
                                  data-media-key={item.key}
                                  disabled={
                                    disabled || index === items.length - 1
                                  }
                                  onClick={() => move(index, index + 1)}
                                  ref={(node) => {
                                    const refKey = `${item.key}:down`;
                                    if (node)
                                      actionRefs.current.set(refKey, node);
                                    else actionRefs.current.delete(refKey);
                                  }}
                                  type="button"
                                >
                                  下移
                                  <span className={styles.srOnly}>
                                    {itemLabel}
                                  </span>
                                </button>
                              </div>
                            </footer>
                          ) : null}
                        </>
                      );
                    })()
                  : null}
              </Dialog.Popup>
            </Dialog.Viewport>
          </Dialog.Portal>
        </Dialog.Root>
      ) : null}
      <div className={styles.mediaControls}>
        <div className={styles.mediaToolbar}>
          <button
            aria-label="选择媒体"
            data-picker-tooltip={pickerTooltip}
            disabled={disabled || items.length >= maximum}
            onClick={() => fileInputRef.current?.click()}
            ref={pickerRef}
            title={pickerTooltip ? undefined : "添加媒体"}
            type="button"
          >
            <Icon name="media-picker" />
            {pickerTooltip ? (
              <span aria-hidden="true">{pickerTooltip}</span>
            ) : null}
          </button>
          {toolbarAction}
          {items.length > 0 ? (
            <span aria-live="polite">
              {items.length} / {maximum}
            </span>
          ) : null}
        </div>
        {footerAction}
      </div>
      <p className={styles.srOnly} id={`${inputId}-guidance`}>
        支持 JPEG、PNG、WebP、GIF、HEIC、MP4 或 QuickTime MOV；图片原件最大 30
        MiB，视频原件最大 300 MiB，最多 {maximum} 项。媒体不会写入本地文字草稿。
      </p>
      {selectionError ? (
        <p className={styles.mediaError} role="alert">
          {selectionError}
        </p>
      ) : null}
      <MediaLightbox
        items={lightboxItems}
        onSelectedChange={(index) =>
          setSelectedKey(index === null ? null : (items[index]?.key ?? null))
        }
        selected={selectedIndex}
      />
    </section>
  );
}
