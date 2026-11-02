import { useTranslation } from "react-i18next";
import { useEffect, useState } from "react";
import { Dialog } from "./Dialog";
import { useDocumentScrollLock } from "./useDocumentScrollLock";
import styles from "./SaveDraftDialog.module.css";

export function SaveDraftDialog({
  open,
  onOpenChange,
  onSave,
  onDiscard,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: () => boolean | void | Promise<boolean | void>;
  onDiscard: () => void;
}) {
  const { t } = useTranslation();
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (open) setFailed(false);
  }, [open]);
  useDocumentScrollLock(open);
  return (
    <Dialog.Root
      modal="trap-focus"
      open={open}
      onOpenChange={(next) => {
        if (!saving) onOpenChange(next);
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.popup}>
            <Dialog.Title className={styles.title}>
              {t("保存草稿？")}
            </Dialog.Title>
            <Dialog.Description className={styles.description}>
              {t(
                "文字和链接卡片将保存在此浏览器的草稿中，上传的图片和视频需要重新选择。",
              )}
            </Dialog.Description>
            {failed ? <p role="alert">{t("草稿未能保存，请重试。")}</p> : null}
            <div className={styles.actions}>
              <button
                className={styles.save}
                aria-label={t("保留草稿并退出")}
                disabled={saving}
                onClick={async () => {
                  if (saving) return;
                  setSaving(true);
                  setFailed(false);
                  try {
                    if ((await onSave()) === false) setFailed(true);
                  } catch {
                    setFailed(true);
                  } finally {
                    setSaving(false);
                  }
                }}
                type="button"
              >
                {t("保存")}
              </button>
              <button
                className={styles.discard}
                disabled={saving}
                aria-label={t("丢弃并退出")}
                onClick={onDiscard}
                type="button"
              >
                {t("丢弃")}
              </button>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
