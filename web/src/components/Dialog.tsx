import { Dialog as BaseDialog } from "@base-ui/react/dialog";
import { useMergedRef } from "@mantine/hooks";
import { type ComponentProps, useRef } from "react";

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
export const Dialog = { ...BaseDialog, Popup: DialogPopup };
