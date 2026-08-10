import { Dialog } from "@base-ui/react/dialog";
import { type ReactElement, useState } from "react";

import styles from "./Owner.module.css";

export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel,
  danger = false,
  pending = false,
  onConfirm,
}: {
  trigger: ReactElement;
  title: string;
  description: string;
  confirmLabel: string;
  danger?: boolean;
  pending?: boolean;
  onConfirm: () => void | Promise<void>;
}) {
  const [open, setOpen] = useState(false);
  return (
    <Dialog.Root onOpenChange={setOpen} open={open}>
      <Dialog.Trigger className={styles.dialogTrigger} render={trigger} />
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup className={styles.dialogPopup}>
            <Dialog.Title className={styles.dialogTitle}>{title}</Dialog.Title>
            <Dialog.Description className={styles.dialogDescription}>
              {description}
            </Dialog.Description>
            <div className={styles.dialogActions}>
              <Dialog.Close
                className={styles.secondaryButton}
                disabled={pending}
              >
                保留
              </Dialog.Close>
              <button
                className={danger ? styles.dangerButton : styles.primaryButton}
                disabled={pending}
                onClick={() => {
                  void Promise.resolve(onConfirm()).finally(() =>
                    setOpen(false),
                  );
                }}
                type="button"
              >
                {pending ? "正在处理…" : confirmLabel}
              </button>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
