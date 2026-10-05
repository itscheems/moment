import type { Editor } from "@tiptap/core";
import { useEditorState } from "@tiptap/react";
import styles from "../Editor.module.css";

export function CharacterCount({ editor }: { editor: Editor }) {
  const count = useEditorState({
    editor,
    selector: ({ editor: current }) =>
      current.storage.characterCount.characters(),
  });
  return (
    <p className={styles.characterCount}>
      正文 {count.toLocaleString("zh-CN")} 字
    </p>
  );
}
