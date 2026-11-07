import { Dialog as BaseDialog } from "@base-ui/react/dialog";
import { useMergedRef } from "@mantine/hooks";
import { type ComponentProps, useRef } from "react";

function DialogRoot({
  onOpenChange,
  ...props
}: ComponentProps<typeof BaseDialog.Root>) {
  return (
    <BaseDialog.Root
      {...props}
      onOpenChange={(open, details) => {
        const target = details.event.target;
        const controlled =
          target instanceof HTMLElement
            ? target.getAttribute("aria-controls")
            : null;
        // Escape belongs to the field's open suggestions before the writing dialog.
        if (
          !open &&
          details.reason === "escape-key" &&
          controlled &&
          target instanceof HTMLElement &&
          target.ownerDocument
            .getElementById(controlled)
            ?.matches('[role="listbox"][data-mention-suggestions]')
        ) {
          details.cancel();
          return;
        }
        onOpenChange?.(open, details);
      }}
    />
  );
}

function DialogPopup({
  initialFocus,
  ref,
  ...props
}: ComponentProps<typeof BaseDialog.Popup>) {
  const popup = useRef<HTMLDivElement>(null);
  const mergedRef = useMergedRef(popup, ref);
  return (
    <BaseDialog.Popup
      {...props}
      ref={mergedRef}
      initialFocus={initialFocus ?? popup}
    />
  );
}

// Base UI owns focus trapping and restoration. Only forms opt into field focus;
// other dialogs open on their container, without selecting an action.
export const Dialog = { ...BaseDialog, Root: DialogRoot, Popup: DialogPopup };
