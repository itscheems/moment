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
    marker: "空",
    title: "还没有 Moment",
    detail: "发布第一条 Moment 后会显示在这里。",
    action: { label: "发布第一个 Moment", href: "/compose" },
  },
  detail: {
    marker: "未找到",
    title: "这个 Moment 不在这里",
    detail: "它可能已被删除，或者链接不完整。",
    action: { label: "回到时间流", href: "/" },
  },
  "article-focus": {
    marker: "未找到",
    title: "这篇文章不在这里",
    detail: "它可能已被删除，或者链接不完整。",
    action: { label: "回到时间流", href: "/" },
  },
  search: {
    marker: "搜索",
    title: "搜索 Moment",
    detail: "输入关键词，也可以指定日期范围。",
  },
  archive: {
    marker: "归档",
    title: "还没有归档",
    detail: "发布内容后会按年份、月份和日期归档。",
  },
  profile: {
    marker: "资料",
    title: "还没有公开 Moment",
    detail: "发布后的 Moment 会显示在个人资料页。",
    action: { label: "发布 Moment", href: "/compose" },
  },
  login: {
    marker: "所有者",
    title: "输入所有者令牌",
    detail: "登录后可以发布和管理内容。",
  },
  "owner-compose": {
    marker: "发布",
    title: "发布 Moment",
    detail: "写下内容或添加图片。",
  },
  "article-workspace": {
    marker: "文章",
    title: "写文章",
    detail: "标题、封面和正文，写到一半也会自己存成草稿。",
  },
  "owner-edit": {
    marker: "编辑",
    title: "编辑 Moment",
    detail: "修改内容不会改变原发布时间。",
  },
  "owner-settings": {
    marker: "所有者",
    title: "站点设置",
    detail: "管理站点资料、评论、凭据和备份。",
  },
  "not-found": {
    marker: "404",
    title: "页面不存在",
    detail: "检查地址，或返回时间流。",
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
        <h2>无法加载内容</h2>
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
