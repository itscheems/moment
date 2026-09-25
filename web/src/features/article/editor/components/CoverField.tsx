import { mediaPreviewUrl } from "../../../../api/mediaUrls";
import styles from "../Editor.module.css";
import { EditorIcon } from "./EditorIcons";

/**
 * The cover, full bleed across the sheet.
 *
 * Its controls sit on the picture and appear on hover where there is a
 * pointer -- and stay put where there is not, because a touch screen has no
 * hover to reveal them with and a control nobody can reach is not a control.
 * They also appear on focus, so the keyboard reaches them in the same order
 * the eye does.
 */
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
      <button
        className={styles.coverAdd}
        disabled={busy}
        onClick={onPick}
        type="button"
      >
        <EditorIcon name="image" />
        <span>{busy ? "正在上传封面…" : "添加封面图"}</span>
      </button>
    );
  }

  return (
    <figure className={styles.cover}>
      <img
        alt=""
        className={styles.coverImage}
        src={mediaPreviewUrl(mediaId)}
      />
      {readOnly ? null : (
        <div className={styles.coverActions}>
          <button
            className={styles.coverAction}
            disabled={busy}
            onClick={onPick}
            type="button"
          >
            换图
          </button>
          <button
            className={styles.coverAction}
            disabled={busy}
            onClick={onRemove}
            type="button"
          >
            移除
          </button>
        </div>
      )}
    </figure>
  );
}
