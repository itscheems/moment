import { type ChangeEvent, useEffect, useId, useRef, useState } from "react";

import type { ComposerMediaItem } from "./mediaState";
import {
  fileValidation,
  MAX_MEDIA_COUNT,
  MAX_ALT_CODE_POINTS,
} from "./mediaState";
import styles from "./Media.module.css";

export function MediaComposer({
  items,
  disabled,
  onAdd,
  onChange,
  onRemove,
  onRetry,
}: {
  items: ComposerMediaItem[];
  disabled: boolean;
  onAdd: (files: File[]) => void;
  onChange: (items: ComposerMediaItem[], message: string) => void;
  onRemove: (key: string) => void;
  onRetry: (key: string) => void;
}) {
  const inputId = useId();
  const [selectionError, setSelectionError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pickerRef = useRef<HTMLButtonElement>(null);
  const actionRefs = useRef(new Map<string, HTMLButtonElement>());
  const focusAfterChange = useRef<string | null | undefined>(undefined);

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
      if (nextItems.length + accepted.length >= MAX_MEDIA_COUNT) {
        error = "每条 Moment 最多附加 9 张图片；多出的文件没有加入。";
        break;
      }
      const candidateItems = [
        ...nextItems,
        ...accepted.map(
          (selected) => ({ file: selected }) as ComposerMediaItem,
        ),
      ];
      const invalid = fileValidation(file, candidateItems);
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
    focusAfterChange.current =
      items[index + 1]?.key ?? items[index - 1]?.key ?? null;
    onRemove(items[index].key);
  };

  return (
    <section
      aria-labelledby={`${inputId}-title`}
      className={styles.composerMedia}
    >
      <div className={styles.mediaToolbar}>
        <div>
          <strong id={`${inputId}-title`}>图片</strong>
          <span>
            {items.length} / {MAX_MEDIA_COUNT}
          </span>
        </div>
        <button
          aria-disabled={disabled || items.length >= MAX_MEDIA_COUNT}
          disabled={disabled || items.length >= MAX_MEDIA_COUNT}
          onClick={() => fileInputRef.current?.click()}
          ref={pickerRef}
          type="button"
        >
          选择图片
        </button>
        <input
          accept="image/jpeg,image/png,image/webp,image/gif"
          aria-label="图片文件"
          disabled={disabled || items.length >= MAX_MEDIA_COUNT}
          id={inputId}
          multiple
          onChange={selectFiles}
          ref={fileInputRef}
          tabIndex={-1}
          type="file"
        />
      </div>
      <p className={styles.mediaGuidance}>
        JPEG、PNG、WebP 或 GIF；单图 10 MiB，最多 9
        张。图片只保留在当前页面内存，不写入本地文字草稿。
      </p>
      {selectionError ? (
        <p className={styles.mediaError} role="alert">
          {selectionError}
        </p>
      ) : null}
      {items.length > 0 ? (
        <ol
          aria-label="待发布图片，按发布顺序"
          className={styles.mediaEditorList}
        >
          {items.map((item, index) => {
            const editorPreviewUrl =
              item.localPreviewUrl || item.previewUrl || null;
            const providedInvalid =
              item.altMode === "provided" &&
              (!item.altText.trim() ||
                Array.from(item.altText).length > MAX_ALT_CODE_POINTS);
            return (
              <li key={item.key}>
                <div className={styles.editorPreview}>
                  {editorPreviewUrl ? (
                    <img alt="" decoding="async" src={editorPreviewUrl} />
                  ) : (
                    <span className={styles.editorPreviewPending}>
                      正在准备预览
                    </span>
                  )}
                  <span aria-hidden="true">{index + 1}</span>
                </div>
                <div className={styles.editorMain}>
                  <div className={styles.editorStatus}>
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
                  <fieldset className={styles.altFieldset} disabled={disabled}>
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
                                  ? { ...candidate, altMode: mode, error: null }
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
                          placeholder="例如：雨后窗边的一束薄荷"
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
                  <div className={styles.editorActions}>
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
                    <button
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
                      移除
                      <span className={styles.srOnly}>
                        第 {index + 1} 张图片
                      </span>
                    </button>
                  </div>
                </div>
              </li>
            );
          })}
        </ol>
      ) : null}
    </section>
  );
}
