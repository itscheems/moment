import { t } from "../i18n";
import { useTranslation } from "react-i18next";
import { loginHref } from "../features/auth/returnPath";
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
    get marker() {
      return t("空");
    },
    get title() {
      return t("还没有 Moment");
    },
    get detail() {
      return t("发布第一条 Moment 后会显示在这里。");
    },
    action: {
      get label() {
        return t("发布第一个 Moment");
      },
      href: "/compose",
    },
  },
  detail: {
    get marker() {
      return t("未找到");
    },
    get title() {
      return t("这个 Moment 不在这里");
    },
    get detail() {
      return t("它可能已被删除，或者链接不完整。");
    },
    action: {
      get label() {
        return t("回到时间流");
      },
      href: "/",
    },
  },
  "comment-detail": {
    get marker() {
      return t("未找到");
    },
    get title() {
      return t("这条评论不在这里");
    },
    get detail() {
      return t("它可能已被删除，或者链接不完整。");
    },
    action: {
      get label() {
        return t("回到时间流");
      },
      href: "/",
    },
  },
  "article-focus": {
    get marker() {
      return t("未找到");
    },
    get title() {
      return t("这篇文章不在这里");
    },
    get detail() {
      return t("它可能已被删除，或者链接不完整。");
    },
    action: {
      get label() {
        return t("回到时间流");
      },
      href: "/",
    },
  },
  search: {
    get marker() {
      return t("搜索");
    },
    get title() {
      return t("搜索 Moment");
    },
    get detail() {
      return t("输入关键词，也可以指定日期范围。");
    },
  },
  archive: {
    get marker() {
      return t("归档");
    },
    get title() {
      return t("还没有归档");
    },
    get detail() {
      return t("发布内容后会按年份、月份和日期归档。");
    },
  },
  "profile-edit": {
    get marker() {
      return t("编辑资料");
    },
    get title() {
      return t("编辑个人资料");
    },
    get detail() {
      return t("登录后可以更新你的个人资料。");
    },
    action: {
      get label() {
        return t("登录");
      },
      href: loginHref("/settings/profile"),
    },
  },
  profile: {
    get marker() {
      return t("资料");
    },
    get title() {
      return t("还没有公开 Moment");
    },
    get detail() {
      return t("发布后的 Moment 会显示在个人资料页。");
    },
    action: {
      get label() {
        return t("发布 Moment");
      },
      href: "/compose",
    },
  },
  login: {
    get marker() {
      return t("账号");
    },
    get title() {
      return t("登录账号");
    },
    get detail() {
      return t("登录后可以发布和管理内容。");
    },
  },
  register: {
    get marker() {
      return t("账号");
    },
    get title() {
      return t("注册账号");
    },
    get detail() {
      return t("创建账号后可以登录本站。");
    },
  },
  compose: {
    get marker() {
      return t("发布");
    },
    get title() {
      return t("发布 Moment");
    },
    get detail() {
      return t("写下内容或添加图片。");
    },
  },
  "article-workspace": {
    get marker() {
      return t("文章");
    },
    get title() {
      return t("写文章");
    },
    get detail() {
      return t("标题、封面和正文，写到一半也会自己存成草稿。");
    },
  },
  edit: {
    get marker() {
      return t("编辑");
    },
    get title() {
      return t("编辑 Moment");
    },
    get detail() {
      return t("修改内容不会改变原发布时间。");
    },
  },
  settings: {
    get marker() {
      return t("账号");
    },
    get title() {
      return t("站点设置");
    },
    get detail() {
      return t("管理站点资料、评论、凭据和备份。");
    },
  },
  "not-found": {
    marker: "404",
    get title() {
      return t("页面不存在");
    },
    get detail() {
      return t("检查地址，或返回时间流。");
    },
    action: {
      get label() {
        return t("回到时间流");
      },
      href: "/",
    },
  },
};

function normalizeState(value: string | null): ExampleState {
  return value === "loading" || value === "error" ? value : "empty";
}

function LoadingState() {
  const { t } = useTranslation();

  return (
    <section
      aria-busy="true"
      aria-label={t("正在加载时间流")}
      className={styles.loading}
    >
      <span className={styles.srOnly}>{t("正在加载")}</span>
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
  const { t } = useTranslation();

  return (
    <section className={styles.message} role="alert">
      <span className={styles.timeMark}>{t("暂时中断")}</span>
      <div>
        <h2>{t("无法加载内容")}</h2>
        <p>{t("检查网络后再试；已保存的内容不会受到影响。")}</p>
        <a className={styles.action} href={route.href}>
          {t("重试")}
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
