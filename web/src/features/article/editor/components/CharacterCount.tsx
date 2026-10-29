import { getLocale } from "../../../../i18n";
import { useTranslation } from "react-i18next";
import type { Editor } from "@tiptap/core";
import { useEditorState } from "@tiptap/react";
import styles from "../Editor.module.css";

export function CharacterCount({ editor }: { editor: Editor }) {
  const { t } = useTranslation();

  const count = useEditorState({
    editor,
    selector: ({ editor: current }) =>
      Array.from(current.getText().replace(/\s/gu, "")).length,
  });
  return (
    <p className={styles.characterCount}>
      {t("正文")} {count.toLocaleString(getLocale())} {t("字")}
    </p>
  );
}
