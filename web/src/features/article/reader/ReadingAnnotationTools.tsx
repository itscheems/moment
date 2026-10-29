import { useTranslation } from "react-i18next";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useEditorState, type Editor } from "@tiptap/react";
import { flip, offset, shift } from "@floating-ui/react";
import {
  ColorActionAdapterContext,
  type ActionColor,
} from "@notion/hooks/use-color-action-adapter";
import { FloatingElement } from "@notion/components/utilities/floating-element";
import {
  Toolbar,
  ToolbarGroup,
  ToolbarSeparator,
} from "@notion/components/primitives/toolbar";
import { Button } from "@notion/components/primitives/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@notion/components/primitives/popover";
import { Card, CardBody } from "@notion/components/primitives/card";
import { BanIcon } from "@notion/components/icons/ban-icon";
import { MoreVerticalIcon } from "@notion/components/icons/more-vertical-icon";
import { ColorTextPopover } from "@notion/components/ui/color-text-popover";
import { ColorHighlightButton } from "@notion/components/ui/color-highlight-button";
import { HeaderActions } from "../../../components/headerActions";
import { ClipboardCopyButton } from "../../../components/content/ClipboardCopyButton";
import { Icon } from "../../../components/icons";
import { ReadingExcerptPanel } from "./ReadingExcerptPanel";
import { ShareCardDialog } from "../../share/ShareCardDialog";
import type { ShareCardInput } from "../../share/shareCard";
import {
  readingExcerptCard,
  type ReadingExcerptSource,
} from "./readingExcerpt";
import {
  activeReadingColor,
  applyReadingColor,
  clearReadingAnnotations,
  readingExcerptRanges,
  readingSelection,
  readingSelectionText,
  type ReadingRange,
} from "./readingAnnotations";

const READING_FLOATING_OPTIONS = {
  middleware: [offset(4), flip({ padding: 8 }), shift({ padding: 8 })],
};
const READING_AUTO_UPDATE_OPTIONS = { animationFrame: true };

/** Read-only actions reuse the writing UI, with an explicit decoration adapter. */
export function ReadingAnnotationTools({
  editor,
  actionsSlot,
  showClearAllAnnotations = true,
  enableExcerptPanel = false,
  excerptSource,
}: {
  editor: Editor;
  actionsSlot?: HTMLElement | null;
  showClearAllAnnotations?: boolean;
  enableExcerptPanel?: boolean;
  excerptSource?: ReadingExcerptSource;
}) {
  const { t } = useTranslation();

  const snapshot = useEditorState({
    editor,
    selector: ({ editor }) =>
      editor.isDestroyed
        ? { selection: null, ranges: [] }
        : {
            selection: readingSelection(editor),
            ranges: editor.storage.readingAnnotations.ranges,
          },
  });
  const selected = snapshot.selection;
  const from = selected?.from;
  const to = selected?.to;
  const selectionRef = useRef<ReadingRange | null>(null);
  const [visible, setVisible] = useState(false);
  const [recentColors, setRecentColors] = useState<ActionColor[]>([]);
  const [highlightColor, setHighlightColor] = useState(
    "var(--tt-color-highlight-yellow)",
  );
  const [menuOpen, setMenuOpen] = useState(false);
  const [excerptOpen, setExcerptOpen] = useState(false);
  const shareFromPanel = useRef(false);
  const [shareOpen, setShareOpen] = useState(false);
  const [shareInput, setShareInput] = useState<ShareCardInput | null>(null);
  useEffect(() => {
    // Mount the native dialog closed first so its opening transition runs.
    if (shareInput) setShareOpen(true);
  }, [shareInput]);
  useEffect(() => {
    if (from !== undefined && to !== undefined)
      selectionRef.current = { from, to };
    setVisible(from !== undefined && to !== undefined);
    // Decoration transactions must not reopen a toolbar that was dismissed.
  }, [editor, from, to]);
  const addRecentColor = (color: ActionColor) => {
    if (color.type === "highlight" && color.value !== "var(--tt-bg-color)")
      setHighlightColor(color.value);
    setRecentColors((previous) =>
      [
        color,
        ...previous.filter(
          (item) => item.type !== color.type || item.value !== color.value,
        ),
      ].slice(0, 3),
    );
  };
  const hasSelectedMarks = snapshot.ranges.some(
    (range) => selected && range.to > selected.from && range.from < selected.to,
  );
  const excerpts = enableExcerptPanel ? readingExcerptRanges(editor) : [];
  const shareRange = (range: ReadingRange, fromPanel = false) => {
    if (!excerptSource) return;
    shareFromPanel.current = fromPanel;
    setShareInput(readingExcerptCard(editor, range, excerptSource));
  };
  const clearAllTools =
    showClearAllAnnotations && snapshot.ranges.length > 0 ? (
      <Popover open={menuOpen} onOpenChange={setMenuOpen}>
        <PopoverTrigger asChild>
          <Button
            aria-label={t("Reading tools")}
            tooltip={t("Reading tools")}
            variant="ghost"
          >
            <MoreVerticalIcon className="tiptap-button-icon" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" aria-label={t("Reading tools")}>
          <Card>
            <CardBody>
              <Button
                variant="ghost"
                onClick={() => {
                  clearReadingAnnotations(editor);
                  setMenuOpen(false);
                }}
              >
                <BanIcon className="tiptap-button-icon" />
                <span className="tiptap-button-text">
                  {t("Clear all marks")}
                </span>
              </Button>
            </CardBody>
          </Card>
        </PopoverContent>
      </Popover>
    ) : null;

  const tools = (
    <>
      {enableExcerptPanel && (
        <ReadingExcerptPanel
          editor={editor}
          ranges={excerpts}
          open={excerptOpen}
          onOpenChange={setExcerptOpen}
          suspended={Boolean(shareInput)}
          onShare={
            excerptSource ? (range) => shareRange(range, true) : undefined
          }
        />
      )}
      {clearAllTools}
    </>
  );

  return (
    <>
      {actionsSlot === undefined ? (
        <HeaderActions>{tools}</HeaderActions>
      ) : actionsSlot ? (
        createPortal(tools, actionsSlot)
      ) : null}
      <ColorActionAdapterContext.Provider
        value={{
          editor,
          canApply: Boolean(selectionRef.current),
          activeColor: (type) =>
            activeReadingColor(editor, selectionRef.current, type),
          applyColor: (type, color) => {
            if (!selectionRef.current) return false;
            applyReadingColor(editor, selectionRef.current, type, color);
            return true;
          },
          recentColors,
          addRecentColor,
        }}
      >
        <FloatingElement
          editor={editor}
          shouldShow={
            visible && Boolean(selected) && !shareInput && !excerptOpen
          }
          resetTextSelectionOnClose={false}
          floatingOptions={READING_FLOATING_OPTIONS}
          autoUpdateOptions={READING_AUTO_UPDATE_OPTIONS}
        >
          <Toolbar variant="floating" aria-label={t("Reading annotations")}>
            <ToolbarGroup>
              <ClipboardCopyButton
                source={() => {
                  const range = selectionRef.current;
                  return range ? readingSelectionText(editor, range) : "";
                }}
                tooltip={t("Copy")}
              />
              {excerptSource && (
                <Button
                  variant="ghost"
                  aria-label={t("Share excerpt")}
                  tooltip={t("Share excerpt")}
                  onClick={() => {
                    if (!selectionRef.current) return;
                    shareRange(selectionRef.current);
                  }}
                >
                  <Icon name="share" className="tiptap-button-icon" />
                </Button>
              )}
              <ColorHighlightButton
                highlightColor={highlightColor}
                aria-label={t("Highlight")}
                tooltip={t("Highlight")}
                onApplied={({ color, label }) => {
                  addRecentColor({
                    type: "highlight",
                    label,
                    value: color,
                  });
                }}
              />
              <ColorTextPopover closeOnColorChanged />
            </ToolbarGroup>
            <ToolbarSeparator />
            <ToolbarGroup>
              <Button
                variant="ghost"
                aria-label={t("Clear selection marks")}
                tooltip={t("Clear selection marks")}
                disabled={!hasSelectedMarks}
                onClick={() => {
                  if (selectionRef.current)
                    clearReadingAnnotations(editor, selectionRef.current);
                }}
              >
                <BanIcon className="tiptap-button-icon" />
              </Button>
            </ToolbarGroup>
          </Toolbar>
        </FloatingElement>
      </ColorActionAdapterContext.Provider>
      {shareInput && (
        <ShareCardDialog
          input={shareInput}
          open={shareOpen}
          onOpenChange={setShareOpen}
          finalFocus={() => {
            if (shareFromPanel.current) return false;
            if (!editor.isDestroyed) {
              // A read-only view is not tabbable. Returning its element lets
              // the dialog pick a link inside it instead of the text selection.
              editor.view.dom.focus({ preventScroll: true });
              editor.view.focus();
            }
            return false;
          }}
          onOpenChangeComplete={(open) => {
            if (!open) setShareInput(null);
          }}
        />
      )}
    </>
  );
}
