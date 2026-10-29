import { t } from "../i18n";
import { useTranslation } from "react-i18next";
import { Dialog } from "./Dialog";
import { type ComponentProps, type ReactElement, useState } from "react";

import { useDocumentScrollLock } from "./useDocumentScrollLock";
import styles from "./Forms.module.css";

export function ConfirmDialog({
  trigger,
  title,
  description,
  confirmLabel,
  danger = false,
  pending = false,
  menuItem = false,
  open: controlledOpen,
  onOpenChange,
  cancelLabel = t("保留"),
  finalFocus,
  onConfirm,
}: {
  trigger?: ReactElement;
  title: string;
  description: string;
  confirmLabel: string;
  danger?: boolean;
  pending?: boolean;
  menuItem?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  cancelLabel?: string;
  finalFocus?: ComponentProps<typeof Dialog.Popup>["finalFocus"];
  onConfirm: () => void | Promise<void>;
}) {
  const { t } = useTranslation();

  const [localOpen, setLocalOpen] = useState(false);
  const open = controlledOpen ?? localOpen;
  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setLocalOpen(next);
    onOpenChange?.(next);
  };
  useDocumentScrollLock(open);

  return (
    <Dialog.Root onOpenChange={setOpen} open={open}>
      {trigger && (
        <Dialog.Trigger
          className={styles.dialogTrigger}
          tabIndex={menuItem ? -1 : undefined}
          render={trigger}
        />
      )}
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup className={styles.dialogPopup} finalFocus={finalFocus}>
            <Dialog.Title className={styles.dialogTitle}>{title}</Dialog.Title>
            <Dialog.Description className={styles.dialogDescription}>
              {description}
            </Dialog.Description>
            {/* The reference stacks these full width with the answer on
                top, which is the order they are read in: what you came to
                do, then the way out of it. */}
            <div className={styles.dialogActions}>
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
                {pending ? t("正在处理…") : confirmLabel}
              </button>
              <Dialog.Close
                className={styles.secondaryButton}
                disabled={pending}
              >
                {cancelLabel}
              </Dialog.Close>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
