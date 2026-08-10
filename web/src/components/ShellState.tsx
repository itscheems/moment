import type { AppRoute } from "../app/routes";
import styles from "./ShellState.module.css";

type ExampleState = "loading" | "empty" | "error";

const emptyCopy: Record<
  AppRoute["id"],
  {
    marker: string;
    title: string;
    detail: string;
    action?: { label: string; href: string };
  }
> = {
  home: {
    marker: "今天",
    title: "时间流从一个 Moment 开始",
    detail: "写下一句话、一段近况，或一件不想忘记的小事。",
    action: { label: "发布第一个 Moment", href: "/owner/compose" },
  },
  detail: {
    marker: "未找到",
    title: "这个 Moment 不在这里",
    detail: "它可能已被删除，或者链接不完整。",
    action: { label: "回到时间流", href: "/" },
  },
  search: {
    marker: "文字",
    title: "从记得的片段开始",
    detail: "搜索一句话，再用日期缩小范围。",
  },
  archive: {
    marker: "年月日",
    title: "时间会在这里形成坐标",
    detail: "Moment 发布后，可按年份、月份和日期逐层回看。",
  },
  "owner-login": {
    marker: "Owner",
    title: "进入你的私人入口",
    detail: "使用站点所有者凭据登录后即可发布与管理内容。",
  },
  "owner-compose": {
    marker: "现在",
    title: "留出一块安静的书写位置",
    detail: "发布器会在这里接入，同时保留一步可达的入口。",
  },
  "owner-edit": {
    marker: "编辑",
    title: "补充这段记忆",
    detail: "编辑器会保留原发布时间与时间流位置。",
  },
  "owner-settings": {
    marker: "Owner",
    title: "让这里像你自己的空间",
    detail: "站点身份、评论、备份与诊断会集中在这里。",
  },
  "not-found": {
    marker: "404",
    title: "这条路没有留下 Moment",
    detail: "检查地址，或回到时间流继续浏览。",
    action: { label: "回到时间流", href: "/" },
  },
};

function normalizeState(value: string | null): ExampleState {
  return value === "loading" || value === "error" ? value : "empty";
}

function LoadingState() {
  return (
    <section
      aria-busy="true"
      aria-label="正在加载时间流"
      className={styles.loading}
    >
      <span className={styles.srOnly}>正在加载</span>
      {[0, 1, 2].map((item) => (
        <article aria-hidden="true" className={styles.skeleton} key={item}>
          <span className={styles.avatarSkeleton} />
          <div>
            <span className={styles.shortLine} />
            <span className={styles.longLine} />
            <span className={styles.mediumLine} />
          </div>
        </article>
      ))}
    </section>
  );
}

function ErrorState({ route }: { route: AppRoute }) {
  return (
    <section className={styles.message} role="alert">
      <span className={styles.timeMark}>暂时中断</span>
      <div>
        <p className={styles.eyebrow}>没有加载完成</p>
        <h2>这次没有取回时间流</h2>
        <p>检查网络后再试；已保存的内容不会受到影响。</p>
        <a className={styles.action} href={route.href}>
          重试
        </a>
      </div>
    </section>
  );
}

function EmptyState({ route }: { route: AppRoute }) {
  const copy = emptyCopy[route.id];
  return (
    <section className={styles.message}>
      <span className={styles.timeMark}>{copy.marker}</span>
      <div>
        <p className={styles.eyebrow}>Capture now · Find later</p>
        <h2>{copy.title}</h2>
        <p>{copy.detail}</p>
        {copy.action ? (
          <a className={styles.action} href={copy.action.href}>
            {copy.action.label}
          </a>
        ) : null}
      </div>
    </section>
  );
}

export function ShellState({
  route,
  state,
}: {
  route: AppRoute;
  state: string | null;
}) {
  const example = normalizeState(state);
  if (example === "loading") return <LoadingState />;
  if (example === "error") return <ErrorState route={route} />;
  return <EmptyState route={route} />;
}
