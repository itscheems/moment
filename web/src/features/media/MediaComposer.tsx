import {
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
import type { ComposerMediaItem } from "./mediaState";
import {
  MAX_MEDIA_COUNT,
  MAX_ALT_CODE_POINTS,
  selectMediaFiles,
} from "./mediaState";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import styles from "./Media.module.css";

const MEDIA_SPRING = {
  type: "spring" as const,
  duration: 0.5,
  bounce: 0.3,
};

const MediaPresenceItem = forwardRef<
  HTMLLIElement,
  {
    children: (present: boolean) => ReactNode;
    itemKey: string;
    reduceMotion: boolean;
  }
>(function MediaPresenceItem(
  { children, itemKey, reduceMotion },
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
      style={present ? undefined : { pointerEvents: "none" }}
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
  const focusAfterChange = useRef<string | null | undefined>(undefined);
  const containsVideo = items.some((item) => item.mimeType === "video/mp4");

  const selectedItemIndex = items.findIndex((item) => item.key === selectedKey);
  const selectedIndex =
    selectedKey === null || selectedItemIndex < 0 ? null : selectedItemIndex;
  const lightboxItems: MediaLightboxItem[] = items.map((item, index) => {
    const previewSrc = item.localPreviewUrl || item.previewUrl || "";
    return {
      key: item.key,
      previewSrc,
      originalSrc:
        item.localPreviewUrl || item.originalUrl || item.previewUrl || null,
      alt:
        item.altMode === "provided"
          ? item.altText
          : item.altMode === "decorative"
            ? ""
            : item.mimeType === "video/mp4"
              ? `未提供视频描述，第 ${index + 1} 项，共 ${items.length} 项`
              : `未提供图片描述，第 ${index + 1} 张，共 ${items.length} 张`,
      description:
        item.altMode === "provided"
          ? item.altText
          : item.altMode === "decorative"
            ? item.mimeType === "video/mp4"
              ? "这是一段明确标记为装饰用途的视频。"
              : "这是一张明确标记为装饰用途的图片。"
            : item.mimeType === "video/mp4"
              ? "这段视频尚未填写描述。"
              : "这张图片尚未填写描述。",
      kind: item.mimeType === "video/mp4" ? "video" : "image",
      posterSrc:
        item.mimeType === "video/mp4" && item.mediaId
          ? `/api/v1/media/${encodeURIComponent(item.mediaId)}/poster`
          : null,
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
    const key = focusAfterChange.current;
    focusAfterChange.current = undefined;
    window.requestAnimationFrame(() => {
      if (key) actionRefs.current.get(`${key}:remove`)?.focus();
      else pickerRef.current?.focus();
    });
  }, [items]);

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
    focusAfterChange.current = item.key;
    onChange(next, `已将第 ${from + 1} 张移到第 ${to + 1} 张。`);
  };

  const remove = (index: number) => {
    const item = items[index];
    if (!item) return;
    focusAfterChange.current =
      items[index + 1]?.key ?? items[index - 1]?.key ?? null;
    if (selectedKey === item.key) setSelectedKey(null);
    onRemove(item.key);
  };

  return (
    <section
      aria-label={containsVideo ? "媒体" : "图片"}
      className={`${styles.composerMedia} ${compact ? styles.compactComposer : ""} ${className ?? ""}`}
    >
      <input
        accept="image/jpeg,image/png,image/webp,image/gif,image/heic,image/heif,video/mp4,.heic,.heif,.mp4,.m4v"
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
            const isVideo = item.mimeType === "video/mp4";
            const editorPreviewUrl =
              item.mimeType === "image/heic" && !item.previewUrl
                ? null
                : item.localPreviewUrl || item.previewUrl || null;
            return (
              <MediaPresenceItem
                itemKey={item.key}
                key={item.key}
                reduceMotion={Boolean(reduceMotion)}
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
                      disabled={!editorPreviewUrl || !present}
                      onClick={() => setSelectedKey(item.key)}
                      type="button"
                    >
                      {editorPreviewUrl && isVideo ? (
                        <video
                          aria-hidden="true"
                          muted
                          playsInline
                          poster={
                            item.mediaId
                              ? `/api/v1/media/${encodeURIComponent(item.mediaId)}/poster`
                              : undefined
                          }
                          preload="metadata"
                          src={editorPreviewUrl}
                        />
                      ) : editorPreviewUrl ? (
                        <img alt="" decoding="async" src={editorPreviewUrl} />
                      ) : (
                        <span className={styles.editorPreviewPending}>
                          正在准备预览
                        </span>
                      )}
                    </button>
                    <button
                      aria-label={
                        isVideo
                          ? `移除第 ${index + 1} 项视频`
                          : `移除第 ${index + 1} 张图片`
                      }
                      className={styles.editorRemove}
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
                      <span aria-hidden="true">×</span>
                    </button>
                    {item.state === "uploading" ? (
                      <progress
                        aria-label={
                          isVideo
                            ? `第 ${index + 1} 项上传进度`
                            : `第 ${index + 1} 张上传进度`
                        }
                        max="100"
                        value={item.progress}
                      />
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
        <details className={styles.mediaOptions}>
          <summary>
            {containsVideo ? "媒体选项" : "图片选项"}
            <span>
              {items.filter((item) => item.altMode === "missing").length > 0
                ? `${items.filter((item) => item.altMode === "missing").length} 项未描述`
                : "描述已处理"}
            </span>
          </summary>
          <ol className={styles.mediaOptionList}>
            {items.map((item, index) => {
              const isVideo = item.mimeType === "video/mp4";
              const itemLabel = isVideo
                ? `第 ${index + 1} 项视频`
                : `第 ${index + 1} 张图片`;
              const providedInvalid =
                item.altMode === "provided" &&
                (!item.altText.trim() ||
                  Array.from(item.altText).length > MAX_ALT_CODE_POINTS);
              return (
                <li className={styles.mediaOptionItem} key={item.key}>
                  <header>
                    <div>
                      <strong>{itemLabel}</strong>
                      <span>
                        {item.state === "queued" ? "等待上传" : null}
                        {item.state === "uploading"
                          ? `处理中 ${item.progress}%`
                          : null}
                        {item.state === "ready" ? "已就绪" : null}
                        {item.state === "failed" ? "上传失败" : null}
                      </span>
                    </div>
                    <div className={styles.editorActions}>
                      {items.length > 1 ? (
                        <>
                          <button
                            disabled={disabled || index === 0}
                            onClick={() => move(index, index - 1)}
                            type="button"
                          >
                            上移
                            <span className={styles.srOnly}>{itemLabel}</span>
                          </button>
                          <button
                            disabled={disabled || index === items.length - 1}
                            onClick={() => move(index, index + 1)}
                            type="button"
                          >
                            下移
                            <span className={styles.srOnly}>{itemLabel}</span>
                          </button>
                        </>
                      ) : null}
                    </div>
                  </header>
                  <fieldset className={styles.altFieldset} disabled={disabled}>
                    <legend>{itemLabel}描述（可选）</legend>
                    <div className={styles.altModes}>
                      {(
                        [
                          ["missing", "暂未描述"],
                          ["decorative", "纯装饰"],
                          ["provided", "填写描述"],
                        ] as const
                      ).map(([mode, label]) => (
                        <label key={mode}>
                          <input
                            checked={item.altMode === mode}
                            name={`${item.key}-alt-mode`}
                            onChange={() => {
                              const next = items.map((candidate) =>
                                candidate.key === item.key
                                  ? { ...candidate, altMode: mode, error: null }
                                  : candidate,
                              );
                              onChange(next, `${itemLabel}已设为${label}。`);
                            }}
                            type="radio"
                          />
                          {label}
                        </label>
                      ))}
                    </div>
                    {item.altMode === "provided" ? (
                      <label className={styles.altInput}>
                        <span>{isVideo ? "描述视频内容" : "描述图片内容"}</span>
                        <textarea
                          aria-invalid={providedInvalid}
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
                            isVideo ? "简要描述视频内容" : "简要描述图片内容"
                          }
                          rows={2}
                          value={item.altText}
                        />
                        <small>
                          {Array.from(item.altText).length} /{" "}
                          {MAX_ALT_CODE_POINTS}
                        </small>
                      </label>
                    ) : null}
                  </fieldset>
                </li>
              );
            })}
          </ol>
        </details>
      ) : null}
      <div className={styles.mediaControls}>
        <div className={styles.mediaToolbar}>
          <button
            aria-label="选择媒体"
            disabled={disabled || items.length >= maximum}
            onClick={() => fileInputRef.current?.click()}
            ref={pickerRef}
            title="添加媒体"
            type="button"
          >
            <Icon name="media-picker" />
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
        支持 JPEG、PNG、WebP、GIF、HEIC 或 MP4，单个原文件最大 30 MiB，最多
        {maximum} 项。媒体不会写入本地文字草稿。
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
