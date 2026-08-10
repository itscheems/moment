import type { AppRoute, RouteId } from "../app/routes";
import { Icon, type IconName } from "./icons";
import { ShellState } from "./ShellState";
import { ThemeMenu } from "./ThemeMenu";
import styles from "./AppShell.module.css";

interface NavItem {
  label: string;
  href: string;
  icon: IconName;
  active: RouteId[];
}

const primaryNav: NavItem[] = [
  { label: "时间流", href: "/", icon: "home", active: ["home", "detail"] },
  { label: "搜索", href: "/search", icon: "search", active: ["search"] },
  { label: "归档", href: "/archive", icon: "archive", active: ["archive"] },
];

const ownerNav: NavItem = {
  label: "所有者",
  href: "/owner/settings",
  icon: "owner",
  active: ["owner-login", "owner-settings", "owner-edit"],
};

const mobileNav = [
  ...primaryNav,
  {
    label: "发布",
    href: "/owner/compose",
    icon: "publish" as const,
    active: ["owner-compose" as const],
  },
  ownerNav,
];

function Brand() {
  return (
    <a aria-label="Moment 首页" className={styles.brand} href="/">
      <svg aria-hidden="true" className={styles.brandMark} viewBox="0 0 40 40">
        <path d="M9 31V11l11 10 11-10v20" />
        <circle cx="9" cy="9" r="2.4" />
        <circle cx="31" cy="9" r="2.4" />
      </svg>
      <span>
        <strong>Moment</strong>
        <small>记下此刻，找回后来</small>
      </span>
    </a>
  );
}

function NavLink({
  item,
  route,
  compact = false,
}: {
  item: NavItem;
  route: AppRoute;
  compact?: boolean;
}) {
  const current = item.active.includes(route.id);
  return (
    <a
      aria-current={current ? "page" : undefined}
      aria-label={compact ? item.label : undefined}
      className={styles.navLink}
      href={item.href}
    >
      <Icon name={item.icon} />
      <span>{item.label}</span>
    </a>
  );
}

function LeftRail({ route }: { route: AppRoute }) {
  return (
    <aside aria-label="站点导航" className={styles.leftRail}>
      <Brand />
      <nav aria-label="主要导航" className={styles.desktopNav}>
        {primaryNav.map((item) => (
          <NavLink item={item} key={item.href} route={route} />
        ))}
      </nav>
      <a className={styles.publishAction} href="/owner/compose">
        <Icon name="publish" />
        <span>发布 Moment</span>
      </a>
      <div className={styles.railBottom}>
        <NavLink item={ownerNav} route={route} />
        <ThemeMenu />
      </div>
    </aside>
  );
}

function MemoryRail({ years }: { years: readonly number[] }) {
  if (years.length === 0) {
    return null;
  }

  return (
    <aside aria-label="记忆导航" className={styles.rightRail}>
      <section className={styles.siteNote}>
        <p className={styles.railEyebrow}>个人时间流</p>
        <p>把今天写下来，也让未来的自己找得到。</p>
      </section>
      <section aria-labelledby="memory-map-title" className={styles.memoryMap}>
        <header>
          <div>
            <p className={styles.railEyebrow}>记忆坐标</p>
            <h2 id="memory-map-title">按年份回看</h2>
          </div>
          <a href="/archive">全部</a>
        </header>
        <ol>
          {years.map((value, index) => (
            <li key={value}>
              <a href={`/archive?year=${value}`}>
                <span>{value}</span>
                <small>{index === 0 ? "现在" : "过去"}</small>
              </a>
            </li>
          ))}
        </ol>
      </section>
      <a className={styles.searchPrompt} href="/search">
        <Icon name="search" />
        <span>
          <strong>找回一句话</strong>
          <small>从文字与日期开始</small>
        </span>
      </a>
    </aside>
  );
}

function MobileHeader() {
  return (
    <header className={styles.mobileHeader}>
      <Brand />
      <ThemeMenu />
    </header>
  );
}

function MobileNavigation({ route }: { route: AppRoute }) {
  return (
    <nav aria-label="移动导航" className={styles.mobileNav}>
      {mobileNav.map((item) => (
        <NavLink compact item={item} key={item.href} route={route} />
      ))}
    </nav>
  );
}

export function AppShell({
  route,
  state,
  memoryYears = [],
}: {
  route: AppRoute;
  state: string | null;
  memoryYears?: readonly number[];
}) {
  return (
    <div className={styles.viewport}>
      <a className={styles.skipLink} href="#main-content">
        跳到主要内容
      </a>
      <MobileHeader />
      <div className={styles.shell}>
        <LeftRail route={route} />
        <main className={styles.timeline} id="main-content">
          <header className={styles.timelineHeader}>
            {route.id === "detail" || route.id === "owner-edit" ? (
              <a aria-label="返回时间流" className={styles.backLink} href="/">
                <Icon name="back" />
              </a>
            ) : null}
            <div>
              <p>{route.eyebrow}</p>
              <h1>{route.title}</h1>
            </div>
            {route.ownerOnly ? (
              <span className={styles.ownerBadge}>Owner</span>
            ) : null}
          </header>
          <ShellState route={route} state={state} />
        </main>
        <MemoryRail years={memoryYears} />
      </div>
      <MobileNavigation route={route} />
    </div>
  );
}
