import { Icon } from "../../../../components/icons";
import styles from "../Editor.module.css";

export interface UploadTask {
  id: string;
  name: string;
  percent: number;
  /** Set once the upload has failed; the row stays so the owner can see why. */
  failed: string | null;
}

/**
 * What stands in for a picture while it is still arriving.
 *
 * UploadPlaceholder shows the local picture at its mapped insertion point.
 * This strip announces progress and keeps failures visible after the local
 * placeholder has been removed. Neither becomes part of the saved body.
 */
export function ImageUploads({
  uploads,
  onDismiss,
}: {
  uploads: readonly UploadTask[];
  onDismiss: (id: string) => void;
}) {
  if (uploads.length === 0) return null;
  return (
    <div aria-live="polite" className={styles.uploads}>
      {uploads.map((upload) => (
        <div
          className={`${styles.upload} ${upload.failed ? styles.uploadFailed : ""}`}
          key={upload.id}
        >
          <span>
            {upload.failed
              ? `${upload.name}：${upload.failed}`
              : `正在上传 ${upload.name}`}
          </span>
          {upload.failed ? (
            // A failure has nothing left to do but be read and cleared; a row
            // that could only be left there would sit above the article for
            // as long as the page stayed open.
            <button
              aria-label={`忽略 ${upload.name} 的上传失败`}
              className={styles.draftDiscard}
              onClick={() => onDismiss(upload.id)}
              type="button"
            >
              <Icon name="close" />
            </button>
          ) : (
            <span>{`${Math.round(upload.percent)}%`}</span>
          )}
          {upload.failed ? null : (
            <span className={styles.uploadBar}>
              <span
                className={styles.uploadFill}
                style={{
                  inlineSize: `${Math.min(100, Math.max(0, upload.percent))}%`,
                }}
              />
            </span>
          )}
        </div>
      ))}
    </div>
  );
}
