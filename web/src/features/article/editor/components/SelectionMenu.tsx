import { isTextSelection, type Editor } from "@tiptap/core";
import { useBubbleMotion } from "./useBubbleMotion";
import { useEditorState } from "@tiptap/react";
import { BubbleMenu } from "@tiptap/react/menus";

import styles from "../Editor.module.css";
import { EditorIcon } from "./EditorIcons";
import { LinkPopover } from "./LinkPopover";

/** Formatting actions for the native text selection. */
export function SelectionMenu({ editor }: { editor: Editor }) {
  const { ref, options } = useBubbleMotion();
  const state = useEditorState({
    editor,
    selector: ({ editor: current }) => ({
      bold: current.isActive("bold"),
      italic: current.isActive("italic"),
      strike: current.isActive("strike"),
      underline: current.isActive("underline"),
      code: current.isActive("code"),
      link: current.isActive("link"),
    }),
  });

  return (
    <BubbleMenu
      ref={ref}
      updateDelay={100}
      className={styles.bubble}
      editor={editor}
      options={options}
      // A picture is a selection too, and a bold button over a picture does
      // nothing. The toolbar carries what a picture can actually be told.
      shouldShow={({ editor: current, from, to }) =>
        current.isEditable &&
        isTextSelection(current.state.selection) &&
        from !== to &&
        !!current.state.doc.textBetween(from, to).trim()
      }
    >
      <div className={styles.bubbleContent}>
        <button
          aria-label="加粗"
          aria-pressed={state.bold}
          className={styles.toolButton}
          onClick={() => editor.chain().focus().toggleBold().run()}
          type="button"
        >
          <EditorIcon name="bold" />
        </button>
        <button
          aria-label="斜体"
          aria-pressed={state.italic}
          className={styles.toolButton}
          onClick={() => editor.chain().focus().toggleItalic().run()}
          type="button"
        >
          <EditorIcon name="italic" />
        </button>
        <button
          aria-label="删除线"
          aria-pressed={state.strike}
          className={styles.toolButton}
          onClick={() => editor.chain().focus().toggleStrike().run()}
          type="button"
        >
          <EditorIcon name="strike" />
        </button>
        <button
          aria-label="下划线"
          aria-pressed={state.underline}
          className={styles.toolButton}
          onClick={() => editor.chain().focus().toggleUnderline().run()}
          type="button"
        >
          <EditorIcon name="underline" />
        </button>
        <button
          aria-label="行内代码"
          aria-pressed={state.code}
          className={styles.toolButton}
          onClick={() => editor.chain().focus().toggleCode().run()}
          type="button"
        >
          <EditorIcon name="code" />
        </button>
        <LinkPopover
          editor={editor}
          trigger={
            <button
              aria-label="链接"
              aria-pressed={state.link}
              className={styles.toolButton}
              type="button"
            >
              <EditorIcon name="link" />
            </button>
          }
        />
      </div>
    </BubbleMenu>
  );
}
