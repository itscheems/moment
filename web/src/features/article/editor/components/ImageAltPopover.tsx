import { Popover } from "@base-ui/react/popover";
import type { Editor } from "@tiptap/core";
import { useId, useState } from "react";
import styles from "../Editor.module.css";

/**
 * The image's description, edited where the image is.
 *
 * `alt` is the only thing a reader who cannot see the picture will ever get
 * from it, and it is the one attribute of a picture the owner can write. It is
 * optional on the wire, so an empty field removes it rather than storing "".
 */
export function ImageAltPopover({ editor }: { editor: Editor }) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const fieldId = useId();

  return (
    <Popover.Root
      onOpenChange={(next) => {
        if (next) {
          const current: unknown = editor.getAttributes("image").alt;
          setValue(typeof current === "string" ? current : "");
        }
        setOpen(next);
      }}
      open={open}
    >
      <Popover.Trigger className={styles.toolButton}>图片说明</Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner align="start" side="bottom" sideOffset={6}>
          <Popover.Popup aria-label="图片说明" className={styles.popup}>
            <div className={styles.field}>
              <label className={styles.fieldLabel} htmlFor={fieldId}>
                图片说明
              </label>
              <input
                autoFocus
                className={styles.input}
                id={fieldId}
                onChange={(event) => setValue(event.target.value)}
                placeholder="看不到这张图的人会读到这句话"
                type="text"
                value={value}
              />
            </div>
            <div className={styles.popupActions}>
              <button
                className={styles.primaryAction}
                onClick={() => {
                  const alt = value.trim();
                  editor
                    .chain()
                    .focus()
                    .updateAttributes("image", { alt: alt === "" ? null : alt })
                    .run();
                  setOpen(false);
                }}
                type="button"
              >
                应用
              </button>
            </div>
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
