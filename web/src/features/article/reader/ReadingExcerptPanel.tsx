import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { useMediaQuery } from "@mantine/hooks";
import type { Editor } from "@tiptap/react";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@notion/components/primitives/popover";
import { Card, CardHeader } from "@notion/components/primitives/card";
import { Button } from "@notion/components/primitives/button";
import { HighlighterIcon } from "@notion/components/icons/highlighter-icon";
import { BanIcon } from "@notion/components/icons/ban-icon";
import { useToc } from "@content-notion/components/nodes/toc-node/context/toc-context";
import { useScrollEdges } from "../../../components/content/useScrollEdges";
import { ClipboardCopyButton } from "../../../components/content/ClipboardCopyButton";
import { Icon } from "../../../components/icons";
import { readingExcerptBlocks } from "./readingExcerpt";
import {
  clearReadingAnnotations,
  readingRangeBlocks,
  readingSelectionText,
  type ReadingRange,
} from "./readingAnnotations";
import styles from "../Article.module.css";

/** A derived view of the reader's decorations, using the existing popover lifecycle. */
export function ReadingExcerptPanel({
  editor,
  ranges,
  open,
  onOpenChange,
  suspended,
  onShare,
}: {
  editor: Editor;
  ranges: ReadingRange[];
  open: boolean;
  onOpenChange: (open: boolean) => void;
  suspended: boolean;
  onShare?: (range: ReadingRange) => void;
}) {
  const mobile = useMediaQuery("(max-width: 719px)");
  const { navigateToReadingBlocks, clearReadingEmphasis } = useToc();
  const { viewportRef, scrollRef, scrollEdges, updateScrollEdges, contentRef } =
    useScrollEdges();
  const popupRef = useRef<HTMLDivElement | null>(null);
  const scrollPosition = useRef(0);
  const shareFocus = useRef<string | null>(null);
  const shareScroll = useRef<number | null>(null);
  const restoreFrame = useRef<number | undefined>(undefined);
  const [contentMounted, setContentMounted] = useState(false);
  const attachPopup = useCallback((element: HTMLDivElement | null) => {
    popupRef.current = element;
    // Keep the trigger through the popover's exit animation after the last clear.
    setContentMounted(Boolean(element));
  }, []);
  const attachViewport = useCallback(
    (element: HTMLDivElement | null) => {
      viewportRef(element);
      if (element) element.scrollTop = scrollPosition.current;
    },
    [viewportRef],
  );
  useEffect(() => {
    if (!ranges.length && open) onOpenChange(false);
  }, [ranges.length, open, onOpenChange]);
  useEffect(() => () => cancelAnimationFrame(restoreFrame.current ?? 0), []);
  useEffect(() => clearReadingEmphasis, [clearReadingEmphasis]);
  // Restore after positioning, including reopen during the original exit animation.
  useLayoutEffect(() => {
    if (!open || suspended || !contentMounted) return;
    const top = shareScroll.current ?? scrollPosition.current;
    restoreFrame.current = requestAnimationFrame(() => {
      if (popupRef.current?.dataset.state !== "open") return;
      if (scrollRef.current) scrollRef.current.scrollTop = top;
      const key = shareFocus.current;
      if (key)
        popupRef.current
          .querySelector<HTMLButtonElement>(`[data-excerpt-share="${key}"]`)
          ?.focus({ preventScroll: true });
      shareFocus.current = null;
      shareScroll.current = null;
      updateScrollEdges();
    });
    return () => cancelAnimationFrame(restoreFrame.current ?? 0);
  }, [open, suspended, contentMounted, scrollRef, updateScrollEdges]);

  return (
    <Popover open={open && !suspended} onOpenChange={onOpenChange}>
      {(ranges.length > 0 || contentMounted) && (
        <PopoverTrigger asChild>
          <Button
            type="button"
            variant="ghost"
            tooltip="本次摘录"
            aria-label="本次摘录"
            disabled={!ranges.length}
          >
            <HighlighterIcon className="tiptap-button-icon" />
          </Button>
        </PopoverTrigger>
      )}
      <PopoverContent
        ref={attachPopup}
        className={styles.excerptPopup}
        align="end"
        side="bottom"
        fade
        collisionPadding={16}
        aria-label="本次摘录面板"
        onOpenAutoFocus={(event) => {
          if (shareFocus.current) event.preventDefault();
        }}
        onCloseAutoFocus={(event) => {
          if (suspended || !ranges.length) event.preventDefault();
          if (!suspended && !ranges.length && !editor.isDestroyed)
            editor.view.dom.focus({ preventScroll: true });
        }}
      >
        <Card className={styles.excerptCard}>
          <CardHeader className={styles.excerptHeader}>
            <strong>
              本次摘录 <span>{ranges.length}</span>
            </strong>
            <Button
              type="button"
              variant="ghost"
              aria-label="关闭本次摘录"
              tooltip="关闭"
              onClick={() => onOpenChange(false)}
            >
              <Icon name="close" className="tiptap-button-icon" />
            </Button>
          </CardHeader>
          <div className={styles.excerptBody}>
            <div
              ref={attachViewport}
              className={`${styles.excerptScroll} tiptap-scroll-fade`}
              data-scroll-top={scrollEdges.top}
              data-scroll-bottom={scrollEdges.bottom}
              onScroll={(event) => {
                scrollPosition.current = event.currentTarget.scrollTop;
                updateScrollEdges();
              }}
            >
              <div ref={contentRef}>
                <ol className={styles.excerptList}>
                  {ranges.map((range, index) => {
                    const key = `${range.from}:${range.to}`;
                    const blocks = readingExcerptBlocks(
                      editor,
                      range,
                      (value) => value,
                    );
                    return (
                      <li key={key} className={styles.excerptItem}>
                        <Button
                          type="button"
                          variant="ghost"
                          className={styles.excerptText}
                          data-excerpt-nav={range.from}
                          aria-label={`跳转到摘录 ${index + 1}`}
                          aria-description={readingSelectionText(editor, range)}
                          onClick={() => {
                            navigateToReadingBlocks(
                              readingRangeBlocks(editor, range),
                            );
                            if (mobile) onOpenChange(false);
                          }}
                        >
                          {blocks.map(
                            (block, blockIndex) =>
                              block.kind !== "code" && (
                                <span
                                  key={blockIndex}
                                  className={styles.excerptBlock}
                                  data-kind={block.kind}
                                  data-heading={
                                    block.kind === "text" && block.heading
                                      ? ""
                                      : undefined
                                  }
                                >
                                  {block.kind === "item" && (
                                    <span>{block.marker} </span>
                                  )}
                                  {block.runs?.map((run, runIndex) => (
                                    <span
                                      key={runIndex}
                                      style={{
                                        color: run.colour,
                                        backgroundColor: run.background,
                                      }}
                                    >
                                      {run.text}
                                    </span>
                                  )) ?? block.text}
                                </span>
                              ),
                          )}
                        </Button>
                        <div
                          className={styles.excerptActions}
                          role="group"
                          aria-label={`摘录 ${index + 1} 操作`}
                        >
                          <ClipboardCopyButton
                            label={`复制摘录 ${index + 1}`}
                            tooltip="复制"
                            source={() => readingSelectionText(editor, range)}
                          />
                          {onShare && (
                            <Button
                              type="button"
                              variant="ghost"
                              tooltip="分享"
                              aria-label={`分享摘录 ${index + 1}`}
                              data-excerpt-share={key}
                              onClick={() => {
                                shareFocus.current = key;
                                shareScroll.current =
                                  scrollRef.current?.scrollTop ?? 0;
                                onShare(range);
                              }}
                            >
                              <Icon
                                name="share"
                                className="tiptap-button-icon"
                              />
                            </Button>
                          )}
                          <Button
                            type="button"
                            variant="ghost"
                            tooltip="清除临时标记"
                            aria-label={`清除摘录 ${index + 1}`}
                            onClick={() => {
                              const next =
                                ranges[index + 1] ?? ranges[index - 1];
                              clearReadingAnnotations(editor, range);
                              if (next)
                                requestAnimationFrame(() =>
                                  popupRef.current
                                    ?.querySelector<HTMLButtonElement>(
                                      `[data-excerpt-nav="${next.from}"]`,
                                    )
                                    ?.focus({ preventScroll: true }),
                                );
                            }}
                          >
                            <BanIcon className="tiptap-button-icon" />
                          </Button>
                        </div>
                      </li>
                    );
                  })}
                </ol>
              </div>
            </div>
          </div>
        </Card>
      </PopoverContent>
    </Popover>
  );
}
