import { Popover } from "@base-ui/react/popover";
import type { Editor } from "@tiptap/core";
import { type ReactElement, useId, useState } from "react";

import { normalizeArticleHref } from "../links";
import styles from "../Editor.module.css";

/**
 * The link sheet.
 *
 * An article may point at this site, at the web over http or https, or at an
 * address -- and at nothing else, because the server refuses the rest and a
 * link that cannot be saved is worse than one that was never offered. What the
 * owner types is read generously (a bare host becomes https) and then checked
 * against the same rule the document is checked against, so the refusal
 * arrives here, next to the field, rather than as a failed save minutes later.
 */
export function LinkPopover({
  editor,
  trigger,
}: {
  editor: Editor;
  trigger: ReactElement;
}) {
  const [open, setOpen] = useState(false);
  const [value, setValue] = useState("");
  const [rejected, setRejected] = useState(false);
  const fieldId = useId();
  const errorId = useId();
  const linked = editor.isActive("link");

  const apply = () => {
    const href = normalizeArticleHref(value);
    if (href === null) {
      setRejected(true);
      return;
    }
    editor.chain().focus().extendMarkRange("link").setLink({ href }).run();
    setOpen(false);
  };

  return (
    <Popover.Root
      onOpenChange={(next) => {
        if (next) {
          // Opening reads what is under the cursor, so editing a link starts
          // from the address it already has rather than from an empty field.
          const current: unknown = editor.getAttributes("link").href;
          setValue(typeof current === "string" ? current : "");
          setRejected(false);
        }
        setOpen(next);
      }}
      open={open}
    >
      <Popover.Trigger render={trigger} />
      <Popover.Portal>
        <Popover.Positioner
          align="start"
          collisionPadding={8}
          side="bottom"
          sideOffset={6}
        >
          <Popover.Popup aria-label="链接" className={styles.popup}>
            <div className={styles.field}>
              <label className={styles.fieldLabel} htmlFor={fieldId}>
                链接地址
              </label>
              <input
                aria-describedby={rejected ? errorId : undefined}
                aria-invalid={rejected || undefined}
                autoFocus
                className={styles.input}
                id={fieldId}
                inputMode="url"
                onChange={(event) => {
                  setValue(event.target.value);
                  setRejected(false);
                }}
                onKeyDown={(event) => {
                  if (event.key !== "Enter") return;
                  event.preventDefault();
                  apply();
                }}
                placeholder="example.com 或 /archive"
                type="text"
                value={value}
              />
            </div>
            {rejected ? (
              <p className={styles.popupError} id={errorId}>
                只能链接到这个站点，或者 http、https、mailto 地址。
              </p>
            ) : null}
            <div className={styles.popupActions}>
              {linked ? (
                <button
                  className={styles.secondaryAction}
                  onClick={() => {
                    editor
                      .chain()
                      .focus()
                      .extendMarkRange("link")
                      .unsetLink()
                      .run();
                    setOpen(false);
                  }}
                  type="button"
                >
                  移除
                </button>
              ) : null}
              <button
                className={styles.primaryAction}
                onClick={apply}
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
