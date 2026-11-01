import { useTranslation } from "react-i18next";
import { Dialog } from "./Dialog";
import styles from "./SaveDraftDialog.module.css";

export function SaveDraftDialog({
  open,
  onOpenChange,
  onSave,
  onDiscard,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSave: () => void;
  onDiscard: () => void;
}) {
  const { t } = useTranslation();
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.popup}>
            <Dialog.Title className={styles.title}>
              {t("保存草稿？")}
            </Dialog.Title>
            <Dialog.Description className={styles.description}>
              {t("文字将保存在此浏览器的草稿中，媒体不会保存。")}
            </Dialog.Description>
            <div className={styles.actions}>
              <button
                className={styles.save}
                aria-label={t("保留草稿并退出")}
                onClick={onSave}
                type="button"
              >
                {t("保存")}
              </button>
              <button
                className={styles.discard}
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
