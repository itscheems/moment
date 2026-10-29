import { useTranslation } from "react-i18next";
import { useCallback, useRef, useState, type ReactNode } from "react";
import type { Editor } from "@tiptap/core";
import {
  SearchAndReplace,
  SearchAndReplaceButton,
} from "@notion/components/ui/search-and-replace";
import "./article-search.scss";

const SCROLL_OPTIONS: ScrollIntoViewOptions = { block: "center" };

/** Placement and focus restoration belong to the host; search belongs to Tiptap. */
export function ArticleSearch({
  editor,
  onOpen,
  children,
}: {
  editor: Editor;
  onOpen?: () => void;
  children?: (controls: {
    open: boolean;
    toggle: () => void;
    buttonRef: React.RefObject<HTMLButtonElement | null>;
  }) => ReactNode;
}) {
  const { t } = useTranslation();

  const [open, setOpen] = useState(false);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const origin = useRef<HTMLElement | null>(null);
  const show = useCallback(() => {
    origin.current = document.activeElement as HTMLElement | null;
    setOpen(true);
    onOpen?.();
  }, [onOpen]);
  const close = useCallback(() => {
    setOpen(false);
    const target = origin.current?.isConnected
      ? origin.current
      : buttonRef.current;
    target?.focus({ preventScroll: true });
  }, []);
  const toggle = useCallback(() => {
    if (open) close();
    else show();
  }, [open, close, show]);

  return (
    <>
      {children ? (
        children({ open, toggle, buttonRef })
      ) : (
        <SearchAndReplaceButton
          aria-label={t("Search")}
          tooltip={t("Search")}
          ref={buttonRef}
          tabIndex={0}
          aria-expanded={open}
          data-active-state={open ? "on" : "off"}
          onClick={toggle}
        />
      )}
      <SearchAndReplace
        editor={editor}
        className="moment-article-search moment-article-search-readonly"
        showReplace={false}
        open={open}
        onOpen={show}
        onClose={close}
        scrollIntoViewOptions={SCROLL_OPTIONS}
      />
    </>
  );
}
