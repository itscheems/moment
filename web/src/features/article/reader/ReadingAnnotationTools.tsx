import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useEditorState, type Editor } from "@tiptap/react";
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
import {
  activeReadingColor,
  applyReadingColor,
  clearReadingAnnotations,
  readingSelection,
  readingSelectionText,
  type ReadingRange,
} from "./readingAnnotations";

/** Read-only actions reuse the writing UI, with an explicit decoration adapter. */
export function ReadingAnnotationTools({
  editor,
  actionsSlot,
}: {
  editor: Editor;
  actionsSlot?: HTMLElement | null;
}) {
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
  useEffect(() => {
    if (from !== undefined && to !== undefined)
      selectionRef.current = { from, to };
    setVisible(from !== undefined && to !== undefined);
    // Decoration transactions must not reopen a toolbar that was dismissed.
  }, [editor, from, to]);
  const dismiss = () => {
    if (selectionRef.current)
      editor.commands.setTextSelection(selectionRef.current.from);
    setVisible(false);
  };
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
  const tools =
    snapshot.ranges.length > 0 ? (
      <Popover open={menuOpen} onOpenChange={setMenuOpen}>
        <PopoverTrigger asChild>
          <Button
            aria-label="Reading tools"
            tooltip="Reading tools"
            variant="ghost"
          >
            <MoreVerticalIcon className="tiptap-button-icon" />
          </Button>
        </PopoverTrigger>
        <PopoverContent align="end" aria-label="Reading tools">
          <Card>
            <CardBody>
              <Button
                variant="ghost"
                onClick={() => {
                  clearReadingAnnotations(editor);
                  setMenuOpen(false);
                  dismiss();
                }}
              >
                <BanIcon className="tiptap-button-icon" />
                <span className="tiptap-button-text">Clear all marks</span>
              </Button>
            </CardBody>
          </Card>
        </PopoverContent>
      </Popover>
    ) : null;

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
          shouldShow={visible && Boolean(selected)}
          resetTextSelectionOnClose={false}
        >
          <Toolbar variant="floating" aria-label="Reading annotations">
            <ToolbarGroup>
              <ClipboardCopyButton
                source={() => {
                  const range = selectionRef.current;
                  return range ? readingSelectionText(editor, range) : "";
                }}
                tooltip="Copy"
                onCopied={dismiss}
              />
              <ColorHighlightButton
                highlightColor={highlightColor}
                aria-label="Highlight"
                tooltip="Highlight"
                onApplied={({ color, label }) => {
                  addRecentColor({
                    type: "highlight",
                    label,
                    value: color,
                  });
                  dismiss();
                }}
              />
              <ColorTextPopover closeOnColorChanged onColorChanged={dismiss} />
            </ToolbarGroup>
            <ToolbarSeparator />
            <ToolbarGroup>
              <Button
                variant="ghost"
                aria-label="Clear selection marks"
                tooltip="Clear selection marks"
                disabled={!hasSelectedMarks}
                onClick={() => {
                  if (selectionRef.current)
                    clearReadingAnnotations(editor, selectionRef.current);
                  dismiss();
                }}
              >
                <BanIcon className="tiptap-button-icon" />
              </Button>
            </ToolbarGroup>
          </Toolbar>
        </FloatingElement>
      </ColorActionAdapterContext.Provider>
    </>
  );
}
