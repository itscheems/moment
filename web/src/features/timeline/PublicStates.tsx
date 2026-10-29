import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { InternalLink } from "../../app/navigation";
import { useEffect, useId } from "react";
import { canRecoverRead } from "../../components/useReadRecovery";
import { feedback, notify } from "../../components/Feedback";
import styles from "./PublicStates.module.css";

export function ReadRecoveryToast({
  children = t("正在重新获取内容…"),
  error,
  active = true,
  onRetry,
}: {
  children?: string;
  error?: unknown;
  active?: boolean;
  onRetry?: () => void;
}) {
  const { t } = useTranslation();

  const id = useId();
  const recovering = !error || canRecoverRead(error);
  const title = recovering ? children : t("这部分内容暂时无法访问。");
  useEffect(() => {
    if (!active) return;
    let mounted = true;
    queueMicrotask(() => {
      if (mounted)
        notify(title, {
          id,
          type: recovering ? "loading" : "error",
          placement: "corner",
          action: onRetry ? { label: t("重试"), onAction: onRetry } : undefined,
        });
    });
    return () => {
      mounted = false;
      feedback.close(id);
    };
  }, [active, title, id, recovering, onRetry]);
  return null;
}

export function TimelineLoading() {
  const { t } = useTranslation();

  return (
    <section
      aria-busy="true"
      aria-label={t("正在加载时间流")}
      className={styles.loading}
    >
      <span className={styles.srOnly}>{t("正在加载")}</span>
      {[0, 1, 2].map((item) => (
        <div aria-hidden="true" className={styles.skeleton} key={item}>
          <span className={styles.avatarSkeleton} />
          <div>
            <span className={styles.shortLine} />
            <span className={styles.longLine} />
            <span className={styles.mediumLine} />
          </div>
        </div>
      ))}
    </section>
  );
}

export function PublicEmpty({
  context = "timeline",
  canPublish = false,
}: {
  context?: "timeline" | "profile" | "archive";
  canPublish?: boolean;
}) {
  const { t } = useTranslation();

  // An archive list positioned before the first post is empty for a reason
  // of its own: nothing is older than where it starts. That is not "write
  // your first Moment", whoever is reading.
  if (context === "archive") {
    return (
      <section className={styles.message}>
        <div>
          <h2>{t("这个日期之前没有 Moment")}</h2>
          <p>{t("从这里往前没有更早的内容；换一天，或者回到最新。")}</p>
          <div className={styles.actions}>
            <InternalLink className={styles.action} href="/archive">
              {t("回到最新")}
            </InternalLink>
          </div>
        </div>
      </section>
    );
  }
  const description = canPublish
    ? t("写下第一段 Moment，它会出现在这里，也会进入你的公开归档。")
    : context === "profile"
      ? t("这个个人资料还没有公开 Moment；之后发布的内容会按时间出现在这里。")
      : t("这里还没有公开 Moment；稍后再来，就能从最新一段开始阅读。");
  return (
    <section className={styles.message}>
      <div>
        <h2>{t("还没有 Moment")}</h2>
        <p>{description}</p>
        {canPublish ? (
          <div className={styles.actions}>
            <InternalLink className={styles.action} href="/compose">
              {t("发布第一条 Moment")}
            </InternalLink>
          </div>
        ) : null}
      </div>
    </section>
  );
}

export function PublicError({
  kind = "timeline",
  recovering = false,
  active = true,
}: {
  kind?: "timeline" | "detail" | "missing";
  recovering?: boolean;
  active?: boolean;
}) {
  const { t } = useTranslation();

  if (recovering) return <ReadRecoveryToast active={active} />;
  const content = {
    timeline: {
      marker: t("无法访问"),
      title: t("无法加载时间流"),
      detail: t("请确认登录状态和访问链接。"),
    },
    detail: {
      marker: t("无法访问"),
      title: t("暂时无法查看这个 Moment"),
      detail: t("请确认登录状态，或回到时间流继续浏览。"),
    },
    missing: {
      marker: "404",
      title: t("这个 Moment 不在这里"),
      detail: t("它可能已被删除，或者链接不完整。"),
    },
  }[kind];

  return (
    <section className={styles.message} role="alert">
      <span className={styles.timeMark}>{content.marker}</span>
      <div>
        <p className={styles.eyebrow}>{t("加载失败")}</p>
        <h2>{content.title}</h2>
        <p>{content.detail}</p>
        <div className={styles.actions}>
          <InternalLink className={styles.action} href="/">
            {t("回到时间流")}
          </InternalLink>
        </div>
      </div>
    </section>
  );
}
