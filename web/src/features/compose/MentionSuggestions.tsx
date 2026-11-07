import { useTranslation } from "react-i18next";
import { Popover } from "@base-ui/react/popover";
import { createPortal } from "react-dom";
import {
  type RefObject,
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { AccountName } from "../../components/AccountName";
import type { MentionSuggestion, SiteIdentity } from "../../api/public";
import type { ActiveMention } from "./mentions";
import { SiteAvatar } from "../../components/SiteIdentity";
import styles from "../../components/Forms.module.css";

// The mirror measures the same text as the textarea. Floating positioning and
// modal ownership belong to the shared popover, outside clipped form layouts.
const mirroredProperties = [
  "fontFamily",
  "fontSize",
  "fontWeight",
  "fontStyle",
  "fontVariant",
  "lineHeight",
  "letterSpacing",
  "wordSpacing",
  "textIndent",
  "textAlign",
  "textTransform",
  "direction",
  "tabSize",
  "padding",
  "overflowWrap",
  "wordBreak",
] as const;

export function MentionSuggestions({
  active,
  items,
  onPick,
  onDismiss,
  id,
  content,
  mention,
  textareaRef,
  enabled = true,
}: {
  active: number;
  items: MentionSuggestion[];
  onPick: (item: MentionSuggestion) => void;
  onDismiss: () => void;
  id: string;
  content: string;
  mention: ActiveMention | null;
  textareaRef: RefObject<HTMLTextAreaElement | null>;
  enabled?: boolean;
}) {
  const { t } = useTranslation();
  const mirrorRef = useRef<HTMLDivElement>(null);
  const caretRef = useRef<HTMLSpanElement>(null);
  const popupRef = useRef<HTMLDivElement>(null);
  const lineHeight = useRef(28);
  const [width, setWidth] = useState(352);
  const [container, setContainer] = useState<HTMLElement | null>(null);
  const [, refresh] = useState(0);
  const measuring = enabled && mention !== null;
  const update = useCallback(() => refresh((value) => value + 1), []);

  useLayoutEffect(() => {
    setContainer(
      measuring ? (textareaRef.current?.parentElement ?? null) : null,
    );
  }, [measuring, textareaRef]);

  useLayoutEffect(() => {
    const textarea = textareaRef.current;
    const mirror = mirrorRef.current;
    if (!measuring || !textarea || !mirror) return;
    const sync = () => {
      const computed = getComputedStyle(textarea);
      for (const property of mirroredProperties)
        mirror.style[property] = computed[property];
      mirror.style.width = `${textarea.clientWidth}px`;
      mirror.style.left = `${textarea.offsetLeft + textarea.clientLeft}px`;
      mirror.style.top = `${textarea.offsetTop + textarea.clientTop}px`;
      mirror.style.transform = `translate(${-textarea.scrollLeft}px, ${-textarea.scrollTop}px)`;
      const measuredLineHeight = Number.parseFloat(computed.lineHeight);
      lineHeight.current = Number.isFinite(measuredLineHeight)
        ? measuredLineHeight
        : 28;
      setWidth(Math.min(352, textarea.clientWidth || 352));
      update();
    };
    sync();
    const observer = new ResizeObserver(sync);
    observer.observe(textarea);
    textarea.addEventListener("scroll", sync);
    return () => {
      observer.disconnect();
      textarea.removeEventListener("scroll", sync);
    };
  }, [container, measuring, textareaRef, update]);

  useLayoutEffect(() => {
    popupRef.current
      ?.querySelector<HTMLElement>('[aria-selected="true"]')
      ?.scrollIntoView?.({ block: "nearest" });
  }, [active, items]);

  // Recreate the virtual anchor after text, selection, or scroll updates.
  const anchor = {
    get contextElement() {
      return caretRef.current ?? undefined;
    },
    getBoundingClientRect() {
      const textarea = textareaRef.current;
      const mirror = mirrorRef.current;
      const caret = caretRef.current;
      if (!textarea || !mirror || !caret) return new DOMRect();
      const field = textarea.getBoundingClientRect();
      if (field.width === 0 || field.height === 0) return new DOMRect();
      const cursor = caret.getBoundingClientRect();
      return new DOMRect(
        Math.max(field.left, Math.min(cursor.left, field.right - width)),
        cursor.top,
        0,
        lineHeight.current,
      );
    },
  };

  return (
    <>
      {measuring &&
        container &&
        createPortal(
          <div
            aria-hidden="true"
            className={styles.mentionCaretMeasure}
            ref={mirrorRef}
          >
            {content.slice(0, mention.end)}
            <span ref={caretRef}>{"\u200b"}</span>
          </div>,
          container,
        )}
      <Popover.Root
        modal={false}
        open={measuring && container !== null && items.length > 0}
        onOpenChange={(open, details) => {
          if (open) return;
          if (
            details.reason === "outside-press" &&
            details.event.target === textareaRef.current
          ) {
            details.cancel();
            return;
          }
          onDismiss();
        }}
      >
        <Popover.Portal>
          <Popover.Positioner
            anchor={anchor}
            align="start"
            arrowPadding={0}
            className={styles.mentionPositioner}
            collisionAvoidance={{
              side: "flip",
              align: "shift",
              fallbackAxisSide: "none",
            }}
            collisionPadding={8}
            positionMethod="fixed"
            side="bottom"
            sideOffset={6}
          >
            <Popover.Popup
              ref={popupRef}
              aria-label={t("提及用户建议")}
              id={id}
              data-mention-suggestions
              className={`${styles.mentionSuggestions} ${styles.mentionSuggestionsCaret}`}
              data-placement="caret"
              finalFocus={false}
              initialFocus={false}
              role="listbox"
              style={{ inlineSize: width }}
            >
              {items.map((item, index) => {
                const site = {
                  avatarMediaId: item.avatarMediaId,
                  displayName: item.displayName,
                } as SiteIdentity;
                return (
                  <button
                    aria-selected={index === active}
                    className={styles.mentionSuggestion}
                    key={item.userId}
                    id={`${id}-${item.userId}`}
                    onPointerDown={(event) => event.preventDefault()}
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => onPick(item)}
                    role="option"
                    type="button"
                  >
                    <SiteAvatar site={site} />
                    <span className={styles.mentionIdentity}>
                      <strong>
                        <AccountName identity={item} />
                      </strong>
                      <small>@{item.username}</small>
                    </span>
                    <em>{t("提及")}</em>
                  </button>
                );
              })}
            </Popover.Popup>
          </Popover.Positioner>
        </Popover.Portal>
      </Popover.Root>
    </>
  );
}
