import { useScrollToHash } from "@notion/components/ui/copy-anchor-link-button";
import { useUiEditorState } from "@notion/hooks/use-ui-editor-state";
import { useEffect } from "react";
import { EditorContent, EditorContext, type Editor } from "@tiptap/react";
import { DragContextMenu } from "@notion/components/ui/drag-context-menu";
import { SlashDropdownMenu } from "@notion/components/ui/slash-dropdown-menu";
import { NotionToolbarFloating } from "@notion/components/templates/notion-like/notion-like-editor-toolbar-floating";
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
import { EmojiDropdownMenu } from "@notion/components/ui/emoji-dropdown-menu";
import { MentionDropdownMenu } from "@notion/components/ui/mention-dropdown-menu";
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
}

function Surface({ editor, readOnly }: SurfaceProps) {
  const { isDragging } = useUiEditorState(editor);
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
      <EditorContent
        editor={editor}
        className="notion-like-editor-content"
        style={{ cursor: isDragging ? "grabbing" : "auto" }}
      />
      <TocSidebar topOffset={92} maxShowCount={100} />
      {!readOnly && (
        <>
          <DragContextMenu />
          <SlashDropdownMenu />
          <EmojiDropdownMenu />
          <MentionDropdownMenu />
          <NotionToolbarFloating />
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
