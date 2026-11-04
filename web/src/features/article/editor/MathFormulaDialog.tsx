import { useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { Editor } from "@tiptap/react";
import { closeHistory } from "@tiptap/pm/history";
import katex from "katex";
import { Dialog } from "../../../components/Dialog";
import { useDocumentScrollLock } from "../../../components/useDocumentScrollLock";
import { mathRenderOptions } from "../../../components/content/mathematics";
import forms from "../../../components/Forms.module.css";
import styles from "./MathFormulaDialog.module.css";

export type MathTarget = {
  type: "inlineMath" | "blockMath";
  latex: string;
  pos?: number;
  range?: { from: number; to: number };
};

export function MathFormulaDialog({
  editor,
  target,
  onClose,
}: {
  editor: Editor;
  target: MathTarget;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [latex, setLatex] = useState(target.latex);
  const [block, setBlock] = useState(target.type === "blockMath");
  const [failure, setFailure] = useState(false);
  const input = useRef<HTMLTextAreaElement>(null);
  useDocumentScrollLock(true);
  const preview = useMemo(() => {
    try {
      return {
        html: katex.renderToString(latex, {
          ...mathRenderOptions,
          displayMode: block,
          throwOnError: true,
        }),
        error: null,
      };
    } catch (error) {
      return {
        html: "",
        error: error instanceof Error ? error.message : String(error),
      };
    }
  }, [latex, block]);
  const editing = target.pos !== undefined;

  const apply = () => {
    if (!latex.trim() || preview.error || editor.isDestroyed) return;
    editor.view.dispatch(closeHistory(editor.state.tr));
    let chain = editor.chain();
    if (editing) {
      chain =
        target.type === "blockMath"
          ? chain.updateBlockMath({ latex, pos: target.pos })
          : chain.updateInlineMath({ latex, pos: target.pos });
    } else {
      if (target.range)
        chain = chain.setTextSelection(target.range).deleteSelection();
      chain = block
        ? chain.insertBlockMath({ latex })
        : chain.insertInlineMath({ latex });
    }
    if (!chain.run()) {
      setFailure(true);
      return;
    }
    onClose();
  };

  return (
    <Dialog.Root
      open
      modal="trap-focus"
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={forms.dialogBackdrop} />
        <Dialog.Viewport className={forms.dialogViewport}>
          <Dialog.Popup
            className={`${forms.dialogPopup} ${styles.popup}`}
            initialFocus={input}
            finalFocus={() => editor.view.dom}
          >
            <Dialog.Title className={forms.dialogTitle}>
              {editing ? t("编辑公式") : t("插入公式")}
            </Dialog.Title>
            <Dialog.Description className={forms.dialogDescription}>
              {t("输入 LaTeX 源码，预览后插入。点击正文中的公式可以再次编辑。")}
            </Dialog.Description>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                apply();
              }}
            >
              <label className={styles.source}>
                <span>{t("LaTeX 源码")}</span>
                <textarea
                  ref={input}
                  value={latex}
                  maxLength={4096}
                  spellCheck={false}
                  placeholder={"\\frac{a}{b}"}
                  aria-invalid={Boolean(preview.error)}
                  aria-describedby={
                    preview.error ? "article-math-error" : undefined
                  }
                  onChange={(event) => {
                    setLatex(event.target.value);
                    setFailure(false);
                  }}
                />
              </label>
              {!editing && (
                <label className={styles.mode}>
                  <input
                    type="checkbox"
                    checked={block}
                    onChange={(event) => setBlock(event.target.checked)}
                  />
                  {t("独立公式块")}
                </label>
              )}
              <div
                className={styles.preview}
                aria-label={t("公式预览")}
                dangerouslySetInnerHTML={{ __html: preview.html }}
              />
              {preview.error && (
                <p
                  id="article-math-error"
                  className={forms.dialogError}
                  role="alert"
                >
                  {t("公式格式有误，请检查 LaTeX 源码。")}
                </p>
              )}
              {failure && (
                <p className={forms.dialogError} role="alert">
                  {t("公式未能更新，请重新选择插入位置。")}
                </p>
              )}
              <div className={styles.actions}>
                {editing && (
                  <button
                    type="button"
                    className={forms.secondaryButton}
                    onClick={() => {
                      editor.view.dispatch(closeHistory(editor.state.tr));
                      const removed =
                        target.type === "blockMath"
                          ? editor.commands.deleteBlockMath({ pos: target.pos })
                          : editor.commands.deleteInlineMath({
                              pos: target.pos,
                            });
                      if (removed) onClose();
                      else setFailure(true);
                    }}
                  >
                    {t("删除公式")}
                  </button>
                )}
                <Dialog.Close className={forms.secondaryButton}>
                  {t("取消")}
                </Dialog.Close>
                <button
                  type="submit"
                  className={forms.primaryButton}
                  disabled={!latex.trim() || Boolean(preview.error)}
                >
                  {editing ? t("保存") : t("插入")}
                </button>
              </div>
            </form>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
