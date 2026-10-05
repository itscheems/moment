import { Menu } from "@base-ui/react/menu";
import type { Editor } from "@tiptap/core";
import { TableMap } from "@tiptap/pm/tables";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  extendTable,
  selectTableAxis,
  tableCellAt,
  type TableAxis,
} from "./tableSelection";
import styles from "../Editor.module.css";

type Anchor = {
  cell: number;
  row: number;
  column: number;
  left: number;
  top: number;
  right: number;
  bottom: number;
  x: number;
  y: number;
};

/** Only presentation and DOM anchoring live here; the native table plugin owns selections and edits. */
export function TableControls({ editor }: { editor: Editor }) {
  const [anchor, setAnchor] = useState<Anchor | null>(null);
  const locked = useRef(false);
  useEffect(() => {
    let cell: number | null = null;
    let frame = 0;
    const refresh = () => {
      frame = 0;
      if (editor.isDestroyed || !editor.isEditable || cell === null) {
        setAnchor(null);
        return;
      }
      const dom = editor.view.nodeDOM(cell);
      if (!(dom instanceof HTMLTableCellElement)) {
        cell = null;
        setAnchor(null);
        return;
      }
      const table = dom.closest("table");
      if (!table) return;
      const rect = table.getBoundingClientRect();
      const box = dom.getBoundingClientRect();
      const clip = table.parentElement?.getBoundingClientRect() ?? rect;
      // The table scrolls horizontally within its content surface. Keep handles inside that viewport.
      const left = Math.max(rect.left, clip.left);
      const right = Math.min(rect.right, clip.right);
      const context = tableCellAt(editor, cell + 1);
      if (!context) return;
      const logicalCell = TableMap.get(context.table).findCell(
        cell - context.start,
      );
      const row = logicalCell.top;
      const column = logicalCell.left;
      if (rect.bottom < 60 || rect.top > window.innerHeight || right <= left) {
        setAnchor(null);
        return;
      }
      setAnchor({
        cell,
        row,
        column,
        left,
        top: rect.top,
        right,
        bottom: rect.bottom,
        x: Math.max(
          left + 12,
          Math.min(right - 12, (box.left + box.right) / 2),
        ),
        y: (box.top + box.bottom) / 2,
      });
    };
    const schedule = () => {
      if (!frame) frame = requestAnimationFrame(refresh);
    };
    const followSelection = () => {
      if (locked.current) return;
      const selection = editor.state.selection;
      cell =
        tableCellAt(
          editor,
          selection.from +
            (selection.$from.nodeAfter?.type.spec.tableRole ? 1 : 0),
        )?.cell ?? null;
      schedule();
    };
    const followPointer = (event: MouseEvent) => {
      if (locked.current || !(event.target instanceof Element)) return;
      const target = event.target.closest("td, th");
      if (target && editor.view.dom.contains(target)) {
        cell =
          tableCellAt(editor, editor.view.posAtDOM(target, 0))?.cell ?? null;
        schedule();
      } else if (
        editor.view.dom.contains(event.target) &&
        !editor.isActive("table")
      ) {
        cell = null;
        schedule();
      }
    };
    const onTransaction = ({
      transaction,
    }: {
      transaction: import("@tiptap/pm/state").Transaction;
    }) => {
      if (cell !== null && transaction.docChanged) {
        const mapped = transaction.mapping.mapResult(cell);
        cell = mapped.deleted ? null : mapped.pos;
      }
      if (transaction.selectionSet && !locked.current) followSelection();
      else schedule();
    };
    editor.view.dom.addEventListener("mousemove", followPointer);
    editor.on("transaction", onTransaction);
    window.addEventListener("scroll", schedule, true);
    window.addEventListener("resize", schedule);
    const resize = new ResizeObserver(schedule);
    resize.observe(editor.view.dom);
    return () => {
      cancelAnimationFrame(frame);
      resize.disconnect();
      editor.view.dom.removeEventListener("mousemove", followPointer);
      editor.off("transaction", onTransaction);
      window.removeEventListener("scroll", schedule, true);
      window.removeEventListener("resize", schedule);
    };
  }, [editor]);

  if (!anchor) return null;
  return createPortal(
    <div className={styles.tableControls} data-table-controls>
      {(["row", "column"] as const).map((axis) => (
        <TableAxisMenu
          key={axis}
          editor={editor}
          anchor={anchor}
          axis={axis}
          onOpenChange={(open) => {
            locked.current = open;
          }}
        />
      ))}
      <button
        className={`${styles.tableHandle} ${styles.tableExtendColumn}`}
        style={{
          left: anchor.right + 2,
          top: anchor.top,
          height: anchor.bottom - anchor.top,
        }}
        aria-label="在表格末尾添加一列"
        type="button"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => extendTable(editor, anchor.cell, "column")}
      >
        +
      </button>
      <button
        className={`${styles.tableHandle} ${styles.tableExtendRow}`}
        style={{
          left: anchor.left,
          top: anchor.bottom + 2,
          width: anchor.right - anchor.left,
        }}
        aria-label="在表格末尾添加一行"
        type="button"
        onMouseDown={(e) => e.preventDefault()}
        onClick={() => extendTable(editor, anchor.cell, "row")}
      >
        +
      </button>
    </div>,
    document.body,
  );
}

function TableAxisMenu({
  editor,
  anchor,
  axis,
  onOpenChange,
}: {
  editor: Editor;
  anchor: Anchor;
  axis: TableAxis;
  onOpenChange: (open: boolean) => void;
}) {
  const row = axis === "row";
  const label = row ? "行" : "列";
  return (
    <Menu.Root
      modal={false}
      onOpenChange={(open) => {
        onOpenChange(open);
        if (open) selectTableAxis(editor, anchor.cell, axis);
      }}
    >
      <Menu.Trigger
        className={styles.tableHandle}
        style={{
          left: row ? anchor.left - 25 : anchor.x - 12,
          top: row ? anchor.y - 12 : anchor.top - 25,
        }}
        aria-label={`第 ${(row ? anchor.row : anchor.column) + 1} ${label}操作`}
      >
        {row ? "⋮" : "⋯"}
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          side={row ? "left" : "top"}
          sideOffset={6}
          collisionPadding={8}
        >
          <Menu.Popup className={styles.menuPopup} data-table-controls>
            <Menu.Item
              className={styles.menuItem}
              onClick={() => {
                selectTableAxis(editor, anchor.cell, axis);
                editor.view.focus();
              }}
            >
              选择整{label}
            </Menu.Item>
            <Menu.Item
              className={styles.menuItem}
              onClick={() => {
                const c = editor.chain().focus();
                if (row) c.addRowBefore().run();
                else c.addColumnBefore().run();
              }}
            >
              在{row ? "上方" : "左侧"}插入{label}
            </Menu.Item>
            <Menu.Item
              className={styles.menuItem}
              onClick={() => {
                const c = editor.chain().focus();
                if (row) c.addRowAfter().run();
                else c.addColumnAfter().run();
              }}
            >
              在{row ? "下方" : "右侧"}插入{label}
            </Menu.Item>
            <Menu.Item
              className={`${styles.menuItem} ${styles.menuItemDanger}`}
              onClick={() => {
                const c = editor.chain().focus();
                if (row) c.deleteRow().run();
                else c.deleteColumn().run();
              }}
            >
              删除{label}
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
