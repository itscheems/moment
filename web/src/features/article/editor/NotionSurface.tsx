import { useScrollToHash } from "@notion/components/ui/copy-anchor-link-button";
import { createPortal } from "react-dom";
import { useEffect } from "react";
import { EditorContent, EditorContext, type Editor } from "@tiptap/react";
import { DragContextMenu } from "@notion/components/ui/drag-context-menu";
import { SlashDropdownMenu } from "@notion/components/ui/slash-dropdown-menu";
import { NotionToolbarFloating } from "@notion/components/templates/notion-like/notion-like-editor-toolbar-floating";
import { MobileToolbar } from "@notion/components/templates/notion-like/notion-like-editor-mobile-toolbar";
import { TableHandle } from "@notion/components/nodes/table-node/ui/table-handle/table-handle";
import { TableSelectionOverlay } from "@notion/components/nodes/table-node/ui/table-selection-overlay";
import type { TableSelectionOverlayProps } from "@notion/components/nodes/table-node/ui/table-selection-overlay";
import { TableCellHandleMenu } from "@notion/components/nodes/table-node/ui/table-cell-handle-menu";
import { TableExtendRowColumnButtons } from "@notion/components/nodes/table-node/ui/table-extend-row-column-button";
import { TocSidebar } from "@notion/components/nodes/toc-node/ui/toc-sidebar";
import {
  TocProvider,
  useToc,
} from "@notion/components/nodes/toc-node/context/toc-context";
import {
  Toolbar,
  ToolbarGroup,
} from "@notion/components/primitives/toolbar";
import { MarkButton } from "@notion/components/ui/mark-button";
import { TurnIntoDropdown } from "@notion/components/ui/turn-into-dropdown";
import { LinkPopover } from "@notion/components/ui/link-popover";
import { ImageUploadButton } from "@notion/components/ui/image-upload-button";
import { UndoRedoButton } from "@notion/components/ui/undo-redo-button";
import { EmojiDropdownMenu } from "@notion/components/ui/emoji-dropdown-menu";
import { MentionDropdownMenu } from "@notion/components/ui/mention-dropdown-menu";
import { TableTriggerButton } from "@notion/components/nodes/table-node/ui/table-trigger-button";
import {
  SearchAndReplace,
  SearchAndReplaceButton,
} from "@notion/components/ui/search-and-replace";
import { EmojiTriggerButton } from "@notion/components/ui/emoji-trigger-button";
import { MentionTriggerButton } from "@notion/components/ui/mention-trigger-button";
import "./notion.scss";

const ignoreHashResult = () => {};

// The overlay treats this as a component type; keep it stable across transactions.
const CellMenu: NonNullable<TableSelectionOverlayProps["cellMenu"]> = (
  props,
) => (
  <TableCellHandleMenu
    editor={props.editor}
    onOpenChange={props.onOpenChange}
    onMouseDown={(event) => props.onResizeStart?.("br")(event)}
  />
);

interface SurfaceProps {
  editor: Editor;
  readOnly: boolean;
  searchOpen?: boolean;
  onSearchOpenChange?: (open: boolean) => void;
}

function Surface({
  editor,
  readOnly,
  searchOpen = false,
  onSearchOpenChange,
}: SurfaceProps) {
  useScrollToHash({
    editor: readOnly ? null : editor,
    onTargetFound: ignoreHashResult,
    onTargetNotFound: ignoreHashResult,
  });
  const { setTocContent } = useToc();
  useEffect(() => {
    const update = () => setTocContent(editor.storage.tableOfContents.content);
    update();
    editor.on("transaction", update);
    return () => {
      editor.off("transaction", update);
    };
  }, [editor, setTocContent]);
  return (
    <EditorContext.Provider value={{ editor }}>
      <EditorContent editor={editor} className="notion-like-editor-content" />
      <TocSidebar topOffset={92} maxShowCount={100} />
      {!readOnly && (
        <>
          <DragContextMenu mobileBreakpoint={480} />
          <SlashDropdownMenu />
          <EmojiDropdownMenu />
          <MentionDropdownMenu />
          <SearchAndReplace
            open={searchOpen}
            onOpen={() => onSearchOpenChange?.(true)}
            onClose={() => onSearchOpenChange?.(false)}
            className="moment-article-search"
          />
          <NotionToolbarFloating />
          {createPortal(<MobileToolbar />, document.body)}
          <TableExtendRowColumnButtons />
          <TableHandle />
          <TableSelectionOverlay showResizeHandles cellMenu={CellMenu} />
        </>
      )}
    </EditorContext.Provider>
  );
}
export function NotionSurface(props: SurfaceProps) {
  return (
    <TocProvider>
      <Surface {...props} />
    </TocProvider>
  );
}
export function NotionFormatToolbar({
  editor,
  onSearch,
}: {
  editor: Editor;
  onSearch?: () => void;
}) {
  return (
    <EditorContext.Provider value={{ editor }}>
      <Toolbar aria-label="文章格式" className="moment-article-format">
        <ToolbarGroup>
          <UndoRedoButton action="undo" />
          <UndoRedoButton action="redo" />
          <TurnIntoDropdown />
          <MarkButton type="bold" />
          <MarkButton type="italic" />
          <MarkButton type="underline" />
          <MarkButton type="strike" />
          <MarkButton type="code" />
          <LinkPopover autoOpenOnLinkActive={false} />
          <ImageUploadButton />
          <TableTriggerButton />
          <EmojiTriggerButton />
          <MentionTriggerButton />
          <SearchAndReplaceButton onClick={onSearch} />
        </ToolbarGroup>
      </Toolbar>
    </EditorContext.Provider>
  );
}
