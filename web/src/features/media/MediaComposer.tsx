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

import { Icon } from "../../components/icons";
import { mediaPosterUrl } from "../../api/mediaUrls";
import type { ComposerMediaItem } from "./mediaState";
import {
  MAX_MEDIA_COUNT,
  isVideoMimeType,
  selectMediaFiles,
} from "./mediaState";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import styles from "./Media.module.css";
import { canCropMedia } from "./mediaCrop";
import { MediaDescriptionEditor } from "./MediaDescriptionEditor";

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
  beforeToolbar,
  toolbarAction,
  compact = false,
  dialogToolbar = false,
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
  beforeToolbar?: ReactNode;
  toolbarAction?: ReactNode;
  compact?: boolean;
  dialogToolbar?: boolean;
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
  const [editRequest, setEditRequest] = useState<{
    key: string;
    version: number;
  } | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pickerRef = useRef<HTMLButtonElement>(null);
  const actionRefs = useRef(new Map<string, HTMLButtonElement>());
  const focusAfterChange = useRef<{ key: string | null } | undefined>(
    undefined,
  );
  const previousUploadStates = useRef(
    new Map(items.map((item) => [item.key, item.state])),
  );
  const uploadSuccessTimers = useRef(new Map<string, number>());
  const [recentlyUploaded, setRecentlyUploaded] = useState<Set<string>>(
    () => new Set(),
  );
  const containsVideo = items.some((item) => isVideoMimeType(item.mimeType));
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
      // Only a description the author wrote; see ImageGrid.
      description: item.altMode === "provided" ? item.altText : "",
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
    if (focusAfterChange.current === undefined) return;
    const target = focusAfterChange.current;
    focusAfterChange.current = undefined;
    window.requestAnimationFrame(() => {
      if (!target.key) pickerRef.current?.focus();
      else actionRefs.current.get(`${target.key}:remove`)?.focus();
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

  const remove = (index: number) => {
    const item = items[index];
    if (!item) return;
    focusAfterChange.current = {
      key: items[index + 1]?.key ?? items[index - 1]?.key ?? null,
    };
    if (selectedKey === item.key) setSelectedKey(null);
    onRemove(item.key);
  };

  return (
    <section
      aria-label={containsVideo ? "媒体" : "图片"}
      className={`${styles.composerMedia} ${compact ? styles.compactComposer : ""} ${dialogToolbar ? styles.dialogComposer : ""} ${divider ? styles.dividedComposer : ""} ${className ?? ""}`}
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
                    {canCropMedia(item) ? (
                      <button
                        type="button"
                        aria-label={`Edit image ${index + 1}`}
                        className={styles.editorEdit}
                        disabled={
                          disabled || !present || !editorPreviewAvailable
                        }
                        onClick={() =>
                          setEditRequest((current) => ({
                            key: item.key,
                            version: (current?.version ?? 0) + 1,
                          }))
                        }
                      >
                        Edit
                      </button>
                    ) : null}
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
        <MediaDescriptionEditor
          editRequest={editRequest}
          items={items}
          disabled={disabled}
          onChange={onChange}
        />
      ) : null}
      {beforeToolbar}
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
            <span aria-live="polite" className={styles.mediaCount}>
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
