import { Dialog } from "@base-ui/react/dialog";
import { type ReactElement, useRef, useState } from "react";

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
  cancelLabel = "保留",
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
  finalFocus?: Dialog.Popup.Props["finalFocus"];
  onConfirm: () => void | Promise<void>;
}) {
  const [localOpen, setLocalOpen] = useState(false);
  const open = controlledOpen ?? localOpen;
  const setOpen = (next: boolean) => {
    if (controlledOpen === undefined) setLocalOpen(next);
    onOpenChange?.(next);
  };
  const cancelRef = useRef<HTMLButtonElement>(null);
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
          <Dialog.Popup
            className={styles.dialogPopup}
            // The safe answer takes the opening focus even though the
            // destructive one is read first: stacking put 删除 at the top of
            // the sheet, and Base UI would otherwise focus whatever leads.
            initialFocus={cancelRef}
            finalFocus={finalFocus}
          >
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
                {pending ? "正在处理…" : confirmLabel}
              </button>
              <Dialog.Close
                className={styles.secondaryButton}
                disabled={pending}
                ref={cancelRef}
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
