import type { Editor } from "@tiptap/core";
import { BubbleMenu } from "@tiptap/react/menus";
import { ImageAltPopover } from "./ImageAltPopover";
import styles from "../Editor.module.css";

import { useBubbleMotion } from "./useBubbleMotion";

/** Image actions live at the selected image, also reachable on touch screens. */
export function ImageMenu({ editor }: { editor: Editor }) {
  const { ref, options } = useBubbleMotion();
  return (
    <BubbleMenu
      ref={ref}
      pluginKey="articleImageMenu"
      editor={editor}
      className={styles.bubble}
      options={options}
      shouldShow={({ editor: current }) =>
        current.isEditable && current.isActive("image")
      }
    >
      <ImageAltPopover editor={editor} />
      <button
        className={styles.toolButton}
        onClick={() => editor.chain().focus().deleteSelection().run()}
        type="button"
      >
        删除图片
      </button>
    </BubbleMenu>
  );
}
