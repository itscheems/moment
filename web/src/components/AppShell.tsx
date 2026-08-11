import type { ReactNode } from "react";

import type { SiteIdentity } from "../api/public";
import { InternalLink, useNavigation } from "../app/navigation";
import type { AppRoute, RouteId } from "../app/routes";
import { SiteAvatar } from "../features/posts/PostPresentation";
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

function ownerNav(settingsAvailable: boolean): NavItem {
  return {
    label: "所有者",
    href: settingsAvailable ? "/owner/settings" : "/owner/login",
    icon: "owner",
    active: ["owner-login", "owner-settings", "owner-edit"],
  };
}

function Brand({ site }: { site: SiteIdentity }) {
  const title = site.title.trim() || "Moment";
  return (
    <InternalLink aria-label={`${title}首页`} className={styles.brand} href="/">
      {site.avatarMediaId ? (
        <SiteAvatar eager site={site} />
      ) : (
        <svg
          aria-hidden="true"
          className={styles.brandMark}
          viewBox="0 0 40 40"
        >
          <path d="M7 30V10l13 14 13-14v20" />
        </svg>
      )}
      <span>
        <strong>{title}</strong>
        <small>{site.bio.trim() || "记下此刻，找回后来"}</small>
      </span>
    </InternalLink>
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
    <InternalLink
      aria-current={current ? "page" : undefined}
      aria-label={compact ? item.label : undefined}
      className={styles.navLink}
      href={item.href}
    >
      <Icon name={item.icon} />
      <span>{item.label}</span>
    </InternalLink>
  );
}

function LeftRail({
  route,
  site,
  ownerSettingsAvailable,
}: {
  route: AppRoute;
  site: SiteIdentity;
  ownerSettingsAvailable: boolean;
}) {
  return (
    <aside aria-label="站点导航" className={styles.leftRail}>
      <Brand site={site} />
      <nav aria-label="主要导航" className={styles.desktopNav}>
        {primaryNav.map((item) => (
          <NavLink item={item} key={item.href} route={route} />
        ))}
      </nav>
      <InternalLink className={styles.publishAction} href="/owner/compose">
        <Icon name="publish" />
        <span>发布 Moment</span>
      </InternalLink>
      <div className={styles.railBottom}>
        <NavLink item={ownerNav(ownerSettingsAvailable)} route={route} />
        <ThemeMenu />
      </div>
    </aside>
  );
}

function MemoryRail({ years }: { years: readonly number[] }) {
  return (
    <aside aria-label="记忆导航" className={styles.rightRail}>
      <InternalLink className={styles.searchPrompt} href="/search">
        <Icon name="search" />
        <span>
          <strong>搜索 Moment</strong>
          <small>按文字或日期查找</small>
        </span>
      </InternalLink>
      {years.length > 0 ? (
        <section
          aria-labelledby="memory-map-title"
          className={styles.memoryMap}
        >
          <header>
            <div>
              <p className={styles.railEyebrow}>你的归档</p>
              <h2 id="memory-map-title">按年份回看</h2>
            </div>
            <InternalLink href="/archive">查看全部</InternalLink>
          </header>
          <ol>
            {years.map((value, index) => (
              <li key={value}>
                <InternalLink href={`/archive?year=${value}`}>
                  <span>{value}</span>
                  <small>{index === 0 ? "现在" : "过去"}</small>
                </InternalLink>
              </li>
            ))}
          </ol>
        </section>
      ) : null}
      <p className={styles.rightFooter}>Moment · 私人的公开时间流</p>
    </aside>
  );
}

function MobileNavigation({
  route,
  ownerSettingsAvailable,
}: {
  route: AppRoute;
  ownerSettingsAvailable: boolean;
}) {
  const items: NavItem[] = [
    ...primaryNav,
    {
      label: "发布",
      href: "/owner/compose",
      icon: "publish",
      active: ["owner-compose"],
    },
    ownerNav(ownerSettingsAvailable),
  ];
  return (
    <nav aria-label="移动导航" className={styles.mobileNav}>
      {items.map((item) => (
        <NavLink compact item={item} key={item.href} route={route} />
      ))}
    </nav>
  );
}

export function AppShell({
  route,
  state,
  site,
  children,
  memoryYears = [],
  ownerSettingsAvailable = false,
}: {
  route: AppRoute;
  state: string | null;
  site: SiteIdentity;
  children?: ReactNode;
  memoryYears?: readonly number[];
  ownerSettingsAvailable?: boolean;
}) {
  const { goBack } = useNavigation();
  return (
    <div className={styles.viewport}>
      <a className={styles.skipLink} href="#main-content">
        跳到主要内容
      </a>
      <div className={styles.shell}>
        <LeftRail
          ownerSettingsAvailable={ownerSettingsAvailable}
          route={route}
          site={site}
        />
        <main className={styles.timeline} id="main-content" tabIndex={-1}>
          <header className={styles.timelineHeader}>
            {route.id === "detail" || route.id === "owner-edit" ? (
              <InternalLink
                aria-label="返回时间流"
                className={styles.backLink}
                href="/"
                onClick={(event) => {
                  event.preventDefault();
                  goBack("/");
                }}
              >
                <Icon name="back" />
              </InternalLink>
            ) : (
              <span className={styles.mobileAvatar}>
                <SiteAvatar eager site={site} />
              </span>
            )}
            <div>
              <p>{route.eyebrow}</p>
              <h1>{route.title}</h1>
            </div>
            {route.ownerOnly ? (
              <span className={styles.ownerBadge}>Owner</span>
            ) : null}
            <span className={styles.headerTheme}>
              <ThemeMenu />
            </span>
          </header>
          {children ?? <ShellState route={route} state={state} />}
        </main>
        <MemoryRail years={memoryYears} />
      </div>
      <MobileNavigation
        ownerSettingsAvailable={ownerSettingsAvailable}
        route={route}
      />
    </div>
  );
}
