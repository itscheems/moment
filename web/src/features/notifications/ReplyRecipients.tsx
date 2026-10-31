import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Popover } from "@base-ui/react/popover";
import {
  interactionsApi,
  type ReplyAddressing,
  type SubjectKind,
} from "../../api/interactions";
import styles from "./Notifications.module.css";
export function ReplyRecipients({
  kind,
  id,
  enabled,
  disabled,
  fallback,
  excluded,
  onChange,
}: {
  kind: SubjectKind;
  id: string;
  enabled: boolean;
  disabled: boolean;
  fallback: string;
  excluded: string[];
  onChange: (ids: string[]) => void;
}) {
  const { t } = useTranslation();
  const [context, setContext] = useState<ReplyAddressing | null>(null);
  const [error, setError] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    void interactionsApi
      .replyContext(kind, id, controller.signal)
      .then((value) => {
        if (!controller.signal.aborted) {
          setContext(value);
          setError(false);
        }
      })
      .catch(() => {
        if (!controller.signal.aborted) setError(true);
      });
    return () => controller.abort();
  }, [kind, id, enabled, attempt]);
  const selected = context?.participants.filter(
    (p) => !excluded.includes(p.userId),
  );
  const label = selected?.length
    ? selected.map((p) => "@" + p.username).join(", ")
    : fallback;
  if (!context && !fallback) return null;
  return (
    <Popover.Root>
      <Popover.Trigger
        className={styles.recipientTrigger}
        disabled={!enabled || disabled}
      >
        {t("正在回复")} <span>{label}</span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          side="bottom"
          align="start"
          sideOffset={6}
          className={styles.recipientPositioner}
        >
          <Popover.Popup
            className={styles.recipientPopup}
            aria-label={t("回复收件人")}
          >
            <h3>{t("回复收件人")}</h3>
            <p>{t("直接回复的作者必须保留，正文中的 @ 提及会单独通知。")}</p>
            {error ? (
              <>
                <p role="alert">{t("收件人暂时无法加载")}</p>
                <button
                  type="button"
                  onClick={() => setAttempt((value) => value + 1)}
                >
                  {t("重试")}
                </button>
              </>
            ) : context ? (
              context.participants.length ? (
                context.participants.map((p) => (
                  <label key={p.userId}>
                    <span>
                      {p.displayName}
                      <small>
                        @{p.username}
                        {p.required ? " · " + t("必选") : ""}
                      </small>
                    </span>
                    <input
                      type="checkbox"
                      checked={!excluded.includes(p.userId)}
                      disabled={disabled || p.required}
                      onChange={(event) =>
                        onChange(
                          event.target.checked
                            ? excluded.filter((id) => id !== p.userId)
                            : [...excluded, p.userId],
                        )
                      }
                    />
                  </label>
                ))
              ) : (
                <p>{t("此回复没有可通知的账号")}</p>
              )
            ) : (
              <p role="status">{t("正在加载…")}</p>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}
