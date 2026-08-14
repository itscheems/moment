import { InternalLink } from "../../app/navigation";
import styles from "./PublicStates.module.css";

export function TimelineLoading() {
  return (
    <section
      aria-busy="true"
      aria-label="正在加载时间流"
      className={styles.loading}
    >
      <span className={styles.srOnly}>正在加载</span>
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
  owner = false,
}: {
  context?: "timeline" | "profile";
  owner?: boolean;
}) {
  const description = owner
    ? "写下第一段 Moment，它会出现在这里，也会进入你的公开归档。"
    : context === "profile"
      ? "这个个人资料还没有公开 Moment；之后发布的内容会按时间出现在这里。"
      : "这里还没有公开 Moment；稍后再来，就能从最新一段开始阅读。";
  return (
    <section className={styles.message}>
      <div>
        <h2>还没有 Moment</h2>
        <p>{description}</p>
        {owner ? (
          <div className={styles.actions}>
            <InternalLink className={styles.action} href="/owner/compose">
              发布第一条 Moment
            </InternalLink>
          </div>
        ) : null}
      </div>
    </section>
  );
}

export function PublicError({
  kind = "timeline",
  onRetry,
  onResetProgress,
}: {
  kind?: "timeline" | "detail" | "missing" | "malformed";
  onRetry: () => void;
  onResetProgress?: () => void;
}) {
  const content = {
    timeline: {
      marker: "暂时中断",
      title: "无法加载时间流",
      detail: "检查网络后再试；已经发布的内容不会受到影响。",
    },
    detail: {
      marker: "暂时中断",
      title: "这个 Moment 没有加载完成",
      detail: "检查网络后重试，或回到时间流继续浏览。",
    },
    missing: {
      marker: "404",
      title: "这个 Moment 不在这里",
      detail: "它可能已被删除，或者链接不完整。",
    },
    malformed: {
      marker: "数据异常",
      title: "这次收到的内容无法显示",
      detail: "服务器返回了不完整的数据。重试后仍有问题，请检查站点诊断。",
    },
  }[kind];

  return (
    <section className={styles.message} role="alert">
      <span className={styles.timeMark}>{content.marker}</span>
      <div>
        <p className={styles.eyebrow}>加载失败</p>
        <h2>{content.title}</h2>
        <p>{content.detail}</p>
        <div className={styles.actions}>
          <button className={styles.action} onClick={onRetry} type="button">
            重试
          </button>
          {onResetProgress ? (
            <button
              className={styles.secondaryAction}
              onClick={onResetProgress}
              type="button"
            >
              从第一页重新加载
            </button>
          ) : null}
        </div>
      </div>
    </section>
  );
}
