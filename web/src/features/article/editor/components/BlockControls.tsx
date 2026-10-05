import { Menu } from "@base-ui/react/menu";
import type { Editor } from "@tiptap/core";
import { DragHandle } from "@tiptap/extension-drag-handle-react";
import { useRef, useState } from "react";
import {
  moveSelectedBlock,
  selectedBlockPosition,
} from "../extensions/BlockHandles";
import styles from "../Editor.module.css";

const positionOptions = {
  placement: "left-start" as const,
  strategy: "absolute" as const,
};

function insertBelow(editor: Editor, pos: number) {
  const node = editor.state.doc.nodeAt(pos);
  if (!node) return;
  const at = pos + node.nodeSize;
  editor
    .chain()
    .focus()
    .insertContentAt(at, {
      type: "paragraph",
      content: [{ type: "text", text: "/" }],
    })
    .setTextSelection(at + 2)
    .run();
}

export function BlockActionsMenu({
  editor,
  position,
}: {
  editor: Editor;
  position?: number;
}) {
  const [open, setOpen] = useState(false);
  const target = useRef(0);
  const changeOpen = (next: boolean) => {
    if (next) target.current = position ?? selectedBlockPosition(editor);
    editor.view.dispatch(editor.state.tr.setMeta("lockDragHandle", next));
    setOpen(next);
  };
  return (
    <Menu.Root modal={false} open={open} onOpenChange={changeOpen}>
      <Menu.Trigger
        aria-label={position === undefined ? "当前块操作" : "块操作"}
        title="拖动排序，点击打开块菜单"
        onMouseDown={(event) => {
          // Opening on mousedown would lock the native handle before dragstart.
          if (position !== undefined) event.preventBaseUIHandler();
        }}
        onClick={(event) => {
          if (position === undefined) return;
          event.preventBaseUIHandler();
          changeOpen(!open);
        }}
        className={styles.toolButton}
      >
        ⠿
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          side="bottom"
          align="start"
          sideOffset={4}
          collisionPadding={8}
        >
          <Menu.Popup className={styles.menuPopup}>
            {(
              [
                [-1, "上移"],
                [1, "下移"],
              ] as const
            ).map(([direction, label]) => (
              <Menu.Item
                key={label}
                className={styles.menuItem}
                onClick={() =>
                  moveSelectedBlock(editor, direction, target.current)
                }
              >
                {label}
              </Menu.Item>
            ))}
            <Menu.Item
              className={styles.menuItem}
              onClick={() => insertBelow(editor, target.current)}
            >
              在下方插入
            </Menu.Item>
            <Menu.Item
              className={styles.menuItem}
              onClick={() => {
                const node = editor.state.doc.nodeAt(target.current);
                if (node)
                  editor
                    .chain()
                    .focus()
                    .insertContentAt(
                      target.current + node.nodeSize,
                      node.toJSON(),
                    )
                    .run();
              }}
            >
              复制块
            </Menu.Item>
            <Menu.Item
              className={`${styles.menuItem} ${styles.menuItemDanger}`}
              onClick={() => {
                const node = editor.state.doc.nodeAt(target.current);
                if (node)
                  editor
                    .chain()
                    .focus()
                    .deleteRange({
                      from: target.current,
                      to: target.current + node.nodeSize,
                    })
                    .run();
              }}
            >
              删除块
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

/** A single native handle: upstream owns hit testing, drag ranges and drop transactions. */
export function BlockControls({ editor }: { editor: Editor }) {
  const [position, setPosition] = useState(-1);
  return (
    <DragHandle
      editor={editor}
      className={styles.blockHandle}
      computePositionConfig={positionOptions}
      onNodeChange={({ pos }) => setPosition(pos)}
    >
      <button
        className={`${styles.toolButton} ${styles.blockInsert}`}
        type="button"
        aria-label="在块下方插入"
        onClick={() => insertBelow(editor, position)}
      >
        +
      </button>
      <BlockActionsMenu editor={editor} position={position} />
    </DragHandle>
  );
}
