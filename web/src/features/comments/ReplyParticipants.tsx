import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  interactionsApi,
  type ReplyParticipant,
  type SubjectKind,
} from "../../api/interactions";
import styles from "./Comments.module.css";

export function ReplyParticipants({
  kind,
  id,
  enabled,
  fallback,
}: {
  kind: SubjectKind;
  id: string;
  enabled: boolean;
  fallback: string;
}) {
  const { t } = useTranslation();
  const [participants, setParticipants] = useState<ReplyParticipant[] | null>(
    null,
  );
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    void interactionsApi.replyContext(kind, id, controller.signal).then(
      (context) => {
        if (controller.signal.aborted) return;
        setParticipants(context.participants);
        setFailed(false);
      },
      () => {
        if (!controller.signal.aborted) setFailed(true);
      },
    );
    return () => controller.abort();
  }, [kind, id, enabled, attempt]);

  if (failed) {
    return (
      <p className={styles.formError} role="alert">
        {t("收件人暂时无法加载")}{" "}
        <button
          type="button"
          onClick={() => {
            setFailed(false);
            setAttempt((n) => n + 1);
          }}
        >
          {t("重试")}
        </button>
      </p>
    );
  }
  const names =
    participants?.map((p) => (p.username ? "@" + p.username : p.displayName)) ??
    (fallback ? [fallback] : []);
  if (!names.length) return null;
  // Summarize longer participant sets without changing server-derived delivery.
  const values = { value1: names[0], value2: names[1], value3: names[2] };
  const summary =
    names.length > 3
      ? t("{{value1}} {{value2}} 和其他 {{value3}} 人", {
          ...values,
          value3: names.length - 2,
        })
      : names.length === 3
        ? t("{{value1}} {{value2}}和{{value3}}", values)
        : names.length === 2
          ? t("{{value1}}和{{value2}}", values)
          : names[0];
  return (
    <p className={styles.replyTarget}>
      {t("正在回复")} <span title={names.join(", ")}>{summary}</span>
    </p>
  );
}
