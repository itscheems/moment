import { ArticleCover } from "../../../../components/content/ArticleSheet";
import styles from "../Editor.module.css";

function AddCoverIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" fill="none">
      <circle cx="14.75" cy="9.25" fill="currentColor" r="1.75" />
      <path
        d="M20 12V8c0-2.21-1.79-4-4-4H8C5.79 4 4 5.79 4 8v8c0 2.21 1.79 4 4 4h4"
        stroke="currentColor"
        strokeWidth="2"
      />
      <path
        d="M19 15c0 1.657 1.343 3 3 3v1c-1.657 0-3 1.343-3 3h-1c0-1.657-1.343-3-3-3v-1c1.657 0 3-1.343 3-3h1z"
        fill="currentColor"
        stroke="currentColor"
      />
      <path d="M4 14.333L8 11l4 4" stroke="currentColor" strokeWidth="2" />
    </svg>
  );
}

function RemoveCoverIcon() {
  return (
    <svg viewBox="0 0 24 24" aria-hidden="true" fill="currentColor">
      <path d="M10.59 12L4.54 5.96l1.42-1.42L12 10.59l6.04-6.05 1.42 1.42L13.41 12l6.05 6.04-1.42 1.42L12 13.41l-6.04 6.05-1.42-1.42L10.59 12z" />
    </svg>
  );
}

/** Full-bleed cover with visible, keyboard-accessible upload and removal controls. */
export function CoverField({
  mediaId,
  busy,
  readOnly = false,
  onPick,
  onRemove,
}: {
  mediaId: string | null;
  busy: boolean;
  /** The preview shows the cover and offers nothing to do to it. */
  readOnly?: boolean;
  onPick: () => void;
  onRemove: () => void;
}) {
  if (mediaId === null) {
    if (readOnly) return null;
    return (
      <figure className={styles.coverPlaceholder} aria-busy={busy}>
        <div className={styles.coverStage}>
          <div className={styles.coverAddControl}>
            <button
              aria-label={busy ? "正在上传封面…" : "添加封面图"}
              className={styles.coverAdd}
              disabled={busy}
              onClick={onPick}
              type="button"
            >
              <AddCoverIcon />
            </button>
          </div>
          <p className={styles.coverHint}>
            建议使用宽高比为 5:2 的图片，以获得最佳效果。
          </p>
        </div>
      </figure>
    );
  }

  return (
    <ArticleCover mediaId={mediaId}>
      {readOnly ? null : (
        <div className={styles.coverActions}>
          <button
            className={styles.coverAction}
            aria-label="更换封面图"
            disabled={busy}
            onClick={onPick}
            type="button"
          >
            <AddCoverIcon />
          </button>
          <button
            className={styles.coverAction}
            aria-label="移除封面图"
            disabled={busy}
            onClick={onRemove}
            type="button"
          >
            <RemoveCoverIcon />
          </button>
        </div>
      )}
    </ArticleCover>
  );
}
