import { useTranslation } from "react-i18next";
import { Dialog } from "../../components/Dialog";
import { useEffect, useRef, useState, type ComponentProps } from "react";

import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  renderShareCard,
  ShareCardTooLongError,
  type ShareCardInput,
  type ShareCardSource,
} from "./shareCard";
import styles from "./ShareCard.module.css";

type Stage =
  | { kind: "drawing" }
  | { kind: "ready"; url: string; blob: Blob }
  | { kind: "failed"; message: string };

/** What the file is called once it leaves the browser. */
function fileName(input: ShareCardInput): string {
  const handle = input.username.trim() || "moment";
  const stamp = input.publishedLabel
    .replace(/[^\d]+/g, "-")
    .replace(/^-|-$/g, "");
  return `${handle}-${stamp || "moment"}.png`;
}

/**
 * The card, before it goes anywhere.
 *
 * Drawing straight to a download would hand somebody a file they have not
 * seen; a card is a picture of their own words and the first thing they want
 * to know is whether it came out right. So it is shown, and only then saved or
 * copied.
 */
export function ShareCardDialog({
  input,
  onOpenChange,
  open,
  finalFocus,
  onOpenChangeComplete,
}: {
  input: ShareCardSource;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  finalFocus?: ComponentProps<typeof Dialog.Popup>["finalFocus"];
  onOpenChangeComplete?: (open: boolean) => void;
}) {
  const { t } = useTranslation();

  const [stage, setStage] = useState<Stage>({ kind: "drawing" });
  const [busy, setBusy] = useState(false);
  useDocumentScrollLock(open);
  // Object URLs outlive the render that made them, so the last one is kept in
  // hand to be revoked when it is replaced or the dialog closes.
  const objectUrl = useRef<string | null>(null);
  const resolvedInput = useRef<ShareCardInput | null>(null);

  useEffect(() => {
    if (!open) return;
    let live = true;
    const controller = new AbortController();
    setStage({ kind: "drawing" });
    void Promise.resolve()
      .then(() =>
        typeof input === "function" ? input(controller.signal) : input,
      )
      .then((value) => {
        controller.signal.throwIfAborted();
        resolvedInput.current = value;
        return renderShareCard(value, controller.signal);
      })
      .then((blob) => {
        if (!live) return;
        if (objectUrl.current) URL.revokeObjectURL(objectUrl.current);
        objectUrl.current = URL.createObjectURL(blob);
        setStage({ kind: "ready", url: objectUrl.current, blob });
      })
      .catch((error: unknown) => {
        if (live)
          setStage({
            kind: "failed",
            message:
              error instanceof ShareCardTooLongError
                ? error.message
                : t("没能生成图片。可以关掉重试。"),
          });
      });
    return () => {
      live = false;
      controller.abort();
    };
  }, [input, open]);

  useEffect(
    () => () => {
      if (objectUrl.current) URL.revokeObjectURL(objectUrl.current);
    },
    [],
  );

  const save = () => {
    if (stage.kind !== "ready" || !resolvedInput.current) return;
    const link = document.createElement("a");
    link.href = stage.url;
    link.download = fileName(resolvedInput.current);
    link.click();
    notify(t("图片已保存"));
    onOpenChange(false);
  };

  const copy = async () => {
    if (stage.kind !== "ready" || busy) return;
    setBusy(true);
    try {
      // Written as a promise rather than an already-resolved blob because
      // Safari discards a clipboard write that did not start inside the click.
      await navigator.clipboard.write([
        new ClipboardItem({ "image/png": Promise.resolve(stage.blob) }),
      ]);
      notify(t("图片已复制"));
      onOpenChange(false);
    } catch {
      // Firefox writes text and nothing else, and every browser refuses on an
      // insecure origin. Saying which is not useful; saying what still works is.
      notify(t("这个浏览器不能复制图片，可以先保存再发送"), { type: "error" });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog.Root
      onOpenChange={onOpenChange}
      onOpenChangeComplete={onOpenChangeComplete}
      open={open}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.popup} finalFocus={finalFocus}>
            <div
              aria-label={t("分享图片预览区域")}
              className={styles.body}
              role="region"
              tabIndex={0}
            >
              <div className={styles.header}>
                <Dialog.Title className={styles.title}>
                  {t("分享图片")}
                </Dialog.Title>
                <Dialog.Close aria-label={t("关闭")} className={styles.close}>
                  <Icon name="close" />
                </Dialog.Close>
              </div>
              <Dialog.Description className={styles.description}>
                {t("预览此帖子作为图片分享时的外观。")}
              </Dialog.Description>
              <div className={styles.stage}>
                {stage.kind === "ready" ? (
                  <img
                    alt={t("分享图片预览")}
                    className={styles.preview}
                    src={stage.url}
                  />
                ) : stage.kind === "failed" ? (
                  <p className={styles.state} role="alert">
                    {stage.message}
                  </p>
                ) : (
                  <p aria-busy="true" className={styles.state}>
                    {t("正在生成…")}
                  </p>
                )}
              </div>
            </div>
            <div className={styles.actions}>
              {typeof ClipboardItem === "function" ? (
                <button
                  className={styles.quiet}
                  disabled={stage.kind !== "ready" || busy}
                  onClick={() => void copy()}
                  type="button"
                >
                  {t("复制图片")}
                </button>
              ) : null}
              <button
                className={styles.primary}
                disabled={stage.kind !== "ready"}
                onClick={save}
                type="button"
              >
                {t("保存到本地")}
              </button>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
