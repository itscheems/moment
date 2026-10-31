import { useEffect, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
  interactionsApi,
  type ReplyParticipant,
  type SubjectKind,
} from "../../api/interactions";
import { InternalLink } from "../../app/navigation";
import { AccountIdentityPreview } from "../../components/AccountIdentityPreview";
import styles from "./Comments.module.css";

function ParticipantName({
  participant,
  children,
}: {
  participant?: ReplyParticipant;
  children?: ReactNode;
}) {
  if (!participant?.username) return children;
  return (
    <AccountIdentityPreview author={participant}>
      <InternalLink
        className={styles.replyParticipant}
        href={`/${participant.username}`}
      >
        {children}
      </InternalLink>
    </AccountIdentityPreview>
  );
}

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
  const summaryKey =
    names.length > 3
      ? "{{value1}} {{value2}} 和其他 {{value3}} 人"
      : names.length === 3
        ? "{{value1}} {{value2}}和{{value3}}"
        : "{{value1}}和{{value2}}";
  return (
    <p className={styles.replyTarget}>
      {t("正在回复")}{" "}
      <span>
        {names.length === 1 ? (
          <ParticipantName participant={participants?.[0]}>
            {names[0]}
          </ParticipantName>
        ) : (
          <Trans
            i18nKey={summaryKey}
            values={{
              ...values,
              value3: names.length > 3 ? names.length - 2 : values.value3,
            }}
            components={[
              <ParticipantName key="first" participant={participants?.[0]} />,
              <ParticipantName key="second" participant={participants?.[1]} />,
              names.length > 3 ? (
                <span key="others" title={names.join(", ")} />
              ) : (
                <ParticipantName key="third" participant={participants?.[2]} />
              ),
            ]}
          />
        )}
      </span>
    </p>
  );
}
