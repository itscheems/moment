import { Dialog } from "@base-ui/react/dialog";
import { useRef, useState } from "react";

import { mediaPosterUrl } from "../../api/mediaUrls";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  isVideoMimeType,
  MAX_ALT_CODE_POINTS,
  type ComposerMediaItem,
} from "./mediaState";
import styles from "./MediaDescriptionEditor.module.css";

export function MediaDescriptionEditor({
  items,
  disabled,
  onChange,
}: {
  items: ComposerMediaItem[];
  disabled: boolean;
  onChange: (items: ComposerMediaItem[], message: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const selectedIndex = Math.max(
    0,
    items.findIndex((item) => item.key === selectedKey),
  );
  const item = items[selectedIndex];
  const describing = open && Boolean(item);
  useDocumentScrollLock(describing);
  const count = items.filter(
    (candidate) => candidate.altMode === "provided",
  ).length;
  const text = item
    ? (drafts[item.key] ?? (item.altMode === "provided" ? item.altText : ""))
    : "";
  const video = item && isVideoMimeType(item.mimeType);
  const preview = item
    ? video
      ? item.processingPosterUrl ||
        (item.mediaId ? mediaPosterUrl(item.mediaId) : null)
      : item.localPreviewUrl || item.previewUrl
    : null;

  const changeOpen = (next: boolean) => {
    if (next) {
      setDrafts({});
      setSelectedKey(items[0]?.key ?? null);
    }
    setOpen(next);
  };
  const save = () => {
    // Merge only edited descriptions into the latest items: uploads can finish
    // while this dialog is open. Untouched legacy decorative values stay intact.
    let changed = false;
    const next = items.map((candidate) => {
      const draft = drafts[candidate.key];
      if (draft === undefined) return candidate;
      const altText = draft.trim();
      const altMode = altText ? "provided" : "missing";
      if (candidate.altMode === altMode && candidate.altText === altText)
        return candidate;
      changed = true;
      return {
        ...candidate,
        altText,
        altMode: altText ? ("provided" as const) : ("missing" as const),
        error: null,
      };
    });
    if (changed) onChange(next, "图片描述已保存。");
    setOpen(false);
  };

  return (
    <Dialog.Root open={describing} onOpenChange={changeOpen}>
      <Dialog.Trigger className={styles.trigger} disabled={disabled}>
        <Icon name="description" />
        <span>
          {count > 0
            ? `${count} ${count === 1 ? "image description" : "image descriptions"}`
            : "Add descriptions"}
        </span>
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.popup} initialFocus={inputRef}>
            <header className={styles.header}>
              <Dialog.Close className={styles.back} aria-label="Back">
                <Icon name="arrow-left" />
              </Dialog.Close>
              <Dialog.Title>
                Edit {video ? "media" : "image"} description
              </Dialog.Title>
              {items.length > 1 ? (
                <div className={styles.pager}>
                  <button
                    aria-label="Previous image"
                    disabled={selectedIndex === 0}
                    onClick={() =>
                      setSelectedKey(items[selectedIndex - 1]?.key ?? null)
                    }
                    type="button"
                  >
                    <Icon name="arrow-left" />
                  </button>
                  <button
                    aria-label="Next image"
                    disabled={selectedIndex === items.length - 1}
                    onClick={() =>
                      setSelectedKey(items[selectedIndex + 1]?.key ?? null)
                    }
                    type="button"
                  >
                    <Icon name="arrow-right" />
                  </button>
                </div>
              ) : null}
              <button
                className={styles.save}
                disabled={disabled}
                onClick={save}
                type="button"
              >
                Save
              </button>
            </header>
            <div className={styles.altTab}>
              <span>ALT</span>
            </div>
            <div className={styles.stage}>
              {preview ? (
                <img
                  alt={`Image ${selectedIndex + 1} of ${items.length}`}
                  src={preview}
                />
              ) : (
                <span className={styles.pending}>Preparing media…</span>
              )}
            </div>
            <div className={styles.description}>
              <label className={styles.field}>
                <span className={styles.label}>Description</span>
                <span className={styles.counter}>
                  {Array.from(text).length} / 1,000
                </span>
                <textarea
                  aria-label="Description"
                  ref={inputRef}
                  rows={2}
                  value={text}
                  onChange={(event) => {
                    if (!item) return;
                    const value = Array.from(event.target.value)
                      .slice(0, MAX_ALT_CODE_POINTS)
                      .join("");
                    setDrafts((current) => ({ ...current, [item.key]: value }));
                  }}
                />
              </label>
              <Dialog.Root>
                <Dialog.Trigger className={styles.help}>
                  What is alt text?
                </Dialog.Trigger>
                <Dialog.Portal>
                  <Dialog.Backdrop className={styles.backdrop} />
                  <Dialog.Viewport className={styles.viewport}>
                    <Dialog.Popup className={styles.helpPopup}>
                      <Dialog.Title>Add descriptions</Dialog.Title>
                      <Dialog.Description>
                        Alt text describes what is in an image for people who
                        cannot see it. Keep it concise and include the details
                        needed to understand the picture. Adding a description
                        is optional.
                      </Dialog.Description>
                      <Dialog.Close className={styles.save}>
                        Got it
                      </Dialog.Close>
                    </Dialog.Popup>
                  </Dialog.Viewport>
                </Dialog.Portal>
              </Dialog.Root>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
