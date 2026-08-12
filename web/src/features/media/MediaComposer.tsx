import {
  type ChangeEvent,
  type ReactNode,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";

import { Icon } from "../../components/icons";
import type { ComposerMediaItem } from "./mediaState";
import {
  fileValidation,
  MAX_MEDIA_COUNT,
  MAX_ALT_CODE_POINTS,
} from "./mediaState";
import { MediaLightbox, type MediaLightboxItem } from "./MediaLightbox";
import styles from "./Media.module.css";

const MEDIA_EXIT_MS = 170;

function reducedMotionPreferred(): boolean {
  return Boolean(
    window.matchMedia?.("(prefers-reduced-motion: reduce)").matches,
  );
}

export function MediaComposer({
  items,
  disabled,
  onAdd,
  onChange,
  onRemove,
  onRetry,
  footerAction,
  maximum = MAX_MEDIA_COUNT,
  maximumTotalBytes,
  subject = "Moment",
}: {
  items: ComposerMediaItem[];
  disabled: boolean;
  onAdd: (files: File[]) => void;
  onChange: (items: ComposerMediaItem[], message: string) => void;
  onRemove: (key: string) => void;
  onRetry: (key: string) => void;
  footerAction?: ReactNode;
  maximum?: number;
  maximumTotalBytes?: number;
  subject?: string;
}) {
  const inputId = useId();
  const [selectionError, setSelectionError] = useState<string | null>(null);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [departingItems, setDepartingItems] = useState<
    { key: string; previewSrc: string | null }[]
  >([]);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pickerRef = useRef<HTMLButtonElement>(null);
  const actionRefs = useRef(new Map<string, HTMLButtonElement>());
  const focusAfterChange = useRef<string | null | undefined>(undefined);
  const removalTimers = useRef(new Map<string, number>());

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
            : `未提供图片描述，第 ${index + 1} 张，共 ${items.length} 张`,
      description:
        item.altMode === "provided"
          ? item.altText
          : item.altMode === "decorative"
            ? "这是一张明确标记为装饰用途的图片。"
            : "这张图片尚未填写描述。",
    };
  });

  useEffect(
    () => () => {
      for (const timer of removalTimers.current.values()) {
        window.clearTimeout(timer);
      }
      removalTimers.current.clear();
    },
    [],
  );

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

  const selectFiles = (event: ChangeEvent<HTMLInputElement>) => {
    const accepted: File[] = [];
    const nextItems = items;
    let error: string | null = null;
    for (const file of Array.from(event.currentTarget.files ?? [])) {
      if (nextItems.length + accepted.length >= maximum) {
        error = `每条${subject}最多附加 ${maximum} 张图片；多出的文件没有加入。`;
        break;
      }
      const candidateItems = [
        ...nextItems,
        ...accepted.map(
          (selected) => ({ file: selected }) as ComposerMediaItem,
        ),
      ];
      const invalid = fileValidation(file, candidateItems, maximumTotalBytes);
      if (invalid) {
        error = invalid;
        continue;
      }
      accepted.push(file);
    }
    setSelectionError(error);
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
    if (!reducedMotionPreferred()) {
      setDepartingItems((current) => [
        ...current,
        {
          key: item.key,
          previewSrc: item.localPreviewUrl || item.previewUrl || null,
        },
      ]);
      removalTimers.current.set(
        item.key,
        window.setTimeout(() => {
          removalTimers.current.delete(item.key);
          setDepartingItems((current) =>
            current.filter((candidate) => candidate.key !== item.key),
          );
        }, MEDIA_EXIT_MS),
      );
    }
    onRemove(item.key);
  };

  return (
    <section aria-label="图片" className={styles.composerMedia}>
      <input
        accept="image/jpeg,image/png,image/webp,image/gif"
        aria-describedby={`${inputId}-guidance`}
        aria-label="图片文件"
        disabled={disabled || items.length >= maximum}
        id={inputId}
        multiple
        onChange={selectFiles}
        ref={fileInputRef}
        tabIndex={-1}
        type="file"
      />
      {items.length + departingItems.length > 0 ? (
        <ol
          aria-label="待发布图片，按发布顺序"
          className={styles.mediaEditorList}
          data-media-count={items.length + departingItems.length}
        >
          {items.map((item, index) => {
            const editorPreviewUrl =
              item.localPreviewUrl || item.previewUrl || null;
            const providedInvalid =
              item.altMode === "provided" &&
              (!item.altText.trim() ||
                Array.from(item.altText).length > MAX_ALT_CODE_POINTS);
            return (
              <li className={styles.mediaEditorItem} key={item.key}>
                <div className={styles.editorPreview}>
                  <button
                    aria-label={`预览第 ${index + 1} 张图片，共 ${items.length} 张`}
                    className={styles.editorPreviewButton}
                    disabled={!editorPreviewUrl}
                    onClick={() => setSelectedKey(item.key)}
                    type="button"
                  >
                    {editorPreviewUrl ? (
                      <img alt="" decoding="async" src={editorPreviewUrl} />
                    ) : (
                      <span className={styles.editorPreviewPending}>
                        正在准备预览
                      </span>
                    )}
                  </button>
                  <button
                    aria-label={`移除第 ${index + 1} 张图片`}
                    className={styles.editorRemove}
                    data-media-key={item.key}
                    disabled={disabled}
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
                </div>
                <div className={styles.editorMain}>
                  <div className={styles.srOnly}>
                    <strong>第 {index + 1} 张</strong>
                    <span>
                      {item.state === "queued" ? "等待上传" : null}
                      {item.state === "uploading"
                        ? `上传与生成预览 ${item.progress}%`
                        : null}
                      {item.state === "ready" ? "已就绪" : null}
                      {item.state === "failed" ? "上传失败" : null}
                    </span>
                  </div>
                  {item.state === "uploading" ? (
                    <progress
                      aria-label={`第 ${index + 1} 张上传进度`}
                      max="100"
                      value={item.progress}
                    />
                  ) : null}
                  {item.error ? (
                    <p className={styles.mediaError} role="alert">
                      {item.error}
                    </p>
                  ) : null}
                  <div className={styles.editorActions}>
                    {items.length > 1 ? (
                      <>
                        <button
                          disabled={disabled || index === 0}
                          onClick={() => move(index, index - 1)}
                          type="button"
                        >
                          上移
                          <span className={styles.srOnly}>
                            第 {index + 1} 张图片
                          </span>
                        </button>
                        <button
                          disabled={disabled || index === items.length - 1}
                          onClick={() => move(index, index + 1)}
                          type="button"
                        >
                          下移
                          <span className={styles.srOnly}>
                            第 {index + 1} 张图片
                          </span>
                        </button>
                      </>
                    ) : null}
                    {item.state === "failed" ? (
                      <button
                        disabled={disabled}
                        onClick={() => onRetry(item.key)}
                        type="button"
                      >
                        重试上传
                        <span className={styles.srOnly}>
                          第 {index + 1} 张图片
                        </span>
                      </button>
                    ) : null}
                  </div>
                  <details className={styles.altDisclosure}>
                    <summary>
                      {item.altMode === "provided"
                        ? "编辑描述"
                        : item.altMode === "decorative"
                          ? "纯装饰"
                          : "添加描述"}
                    </summary>
                    <fieldset
                      className={styles.altFieldset}
                      disabled={disabled}
                    >
                      <legend>图片描述（可选）</legend>
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
                                    ? {
                                        ...candidate,
                                        altMode: mode,
                                        error: null,
                                      }
                                    : candidate,
                                );
                                onChange(
                                  next,
                                  `第 ${index + 1} 张已设为${label}。`,
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
                          <span>描述图片内容</span>
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
                                `已更新第 ${index + 1} 张图片描述。`,
                              )
                            }
                            placeholder="简要描述图片内容"
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
                  </details>
                </div>
              </li>
            );
          })}
          {departingItems.map((item) => (
            <li
              aria-hidden="true"
              className={`${styles.mediaEditorItem} ${styles.mediaItemRemoving}`}
              key={`departing-${item.key}`}
            >
              <div className={styles.editorPreview}>
                {item.previewSrc ? (
                  <img alt="" decoding="async" src={item.previewSrc} />
                ) : (
                  <span className={styles.editorPreviewPending} />
                )}
              </div>
            </li>
          ))}
        </ol>
      ) : null}
      <div className={styles.mediaControls}>
        <div className={styles.mediaToolbar}>
          <button
            aria-label="选择图片"
            disabled={disabled || items.length >= maximum}
            onClick={() => fileInputRef.current?.click()}
            ref={pickerRef}
            title="添加图片"
            type="button"
          >
            <Icon name="media" />
          </button>
          {items.length > 0 ? (
            <span aria-live="polite">
              {items.length} / {maximum}
            </span>
          ) : null}
        </div>
        {footerAction}
      </div>
      <p className={styles.srOnly} id={`${inputId}-guidance`}>
        支持 JPEG、PNG、WebP 或 GIF，单图最大 10 MiB，最多 {maximum} 张。
        图片不会写入本地文字草稿。
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
