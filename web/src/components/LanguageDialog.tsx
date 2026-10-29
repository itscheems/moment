import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { UILocale } from "../api/preferences";
import { useSession } from "../features/auth/SessionProvider";
import { useLanguage } from "../i18n/LanguageProvider";
import { Dialog } from "./Dialog";
import { Icon } from "./icons";
import styles from "./LanguageDialog.module.css";

export function LanguageDialog({ onClose }: { onClose: () => void }) {
  const { t } = useTranslation();
  const { locale, saveLanguage } = useLanguage();
  const { account, session, state, retryStatus } = useSession();
  const [selected, setSelected] = useState<UILocale>(locale);
  const [saving, setSaving] = useState(false);
  const [failed, setFailed] = useState(false);
  const request = useRef<AbortController | null>(null);
  const initialIdentity = useRef(
    `${account?.userId ?? "guest"}:${session?.csrfToken ?? ""}`,
  );

  useEffect(() => () => request.current?.abort(), []);
  useEffect(() => {
    // A session change ends this draft, including an in-flight save for it.
    return () => request.current?.abort();
  }, [account?.userId, session?.csrfToken]);

  useEffect(() => {
    if (
      initialIdentity.current !==
      `${account?.userId ?? "guest"}:${session?.csrfToken ?? ""}`
    )
      onClose();
  }, [account?.userId, session?.csrfToken, onClose]);

  const save = async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setSaving(true);
    setFailed(false);
    try {
      if (await saveLanguage(selected, controller.signal)) onClose();
    } catch {
      if (!controller.signal.aborted) setFailed(true);
    } finally {
      if (!controller.signal.aborted) setSaving(false);
    }
  };

  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.dialog}>
            <header className={styles.header}>
              <Dialog.Close
                aria-label={t("关闭语言设置")}
                className={styles.close}
              >
                <Icon name="close" />
              </Dialog.Close>
            </header>
            <div className={styles.content}>
              <Dialog.Title className={styles.title}>
                {t("选择语言")}
              </Dialog.Title>
              <Dialog.Description className={styles.description}>
                {account
                  ? t("语言偏好会保存在你的账号中，供其他设备使用。")
                  : t("选择 Moment 的显示语言，保存在当前浏览器中。")}
              </Dialog.Description>
              <fieldset className={styles.options} disabled={saving}>
                <legend className={styles.legend}>{t("显示语言")}</legend>
                {(
                  [
                    {
                      value: "zh-CN",
                      name: "简体中文",
                      secondary: t("Simplified Chinese"),
                    },
                    { value: "en", name: "English", secondary: "英语" },
                  ] as const
                ).map((option) => (
                  <label className={styles.option} key={option.value}>
                    <span>
                      <strong lang={option.value}>{option.name}</strong>
                      <small lang={option.value === "en" ? "zh-CN" : "en"}>
                        {option.secondary}
                      </small>
                    </span>
                    <input
                      type="radio"
                      name="ui-language"
                      value={option.value}
                      checked={selected === option.value}
                      onChange={() => setSelected(option.value)}
                    />
                  </label>
                ))}
              </fieldset>
              {failed ? (
                <p className={styles.error} role="alert">
                  {t("语言没有保存。当前选择已保留，请重试。")}
                </p>
              ) : null}
              {state === "unavailable" ? (
                <p role="status">{t("暂时无法确认登录状态，请重试。")}</p>
              ) : null}
              {state === "expired" ? (
                <p role="status">{t("登录状态已经失效，请重新登录后重试。")}</p>
              ) : null}
              {state === "unavailable" ? (
                <button
                  className={styles.cancel}
                  type="button"
                  onClick={retryStatus}
                >
                  {t("重试")}
                </button>
              ) : null}
              <button
                className={styles.save}
                disabled={
                  saving ||
                  state === "checking" ||
                  state === "unavailable" ||
                  state === "expired"
                }
                onClick={() => void save()}
                type="button"
              >
                {saving ? t("正在保存…") : t("保存")}
              </button>
              <Dialog.Close className={styles.cancel}>{t("取消")}</Dialog.Close>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
