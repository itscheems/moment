import { Menu } from "@base-ui/react/menu";
import {
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import type { SiteIdentity } from "../api/public";
import { InternalLink, useNavigation } from "../app/navigation";
import type { AppRoute, RouteId } from "../app/routes";
import { SiteAvatar, siteDisplayName } from "./SiteIdentity";
import { Icon, type IconName } from "./icons";
import { ShellState } from "./ShellState";
import { ThemeMenu } from "./ThemeMenu";
import { MOBILE_VIEWPORT_QUERY, useMediaQuery } from "./useMediaQuery";
import styles from "./AppShell.module.css";

interface NavItem {
  label: string;
  href: string;
  icon: IconName;
  active: RouteId[];
}

// `/profile` resolves to whoever is signed in, which is right for the server
// and wrong for a link somebody reads before clicking: it hides the handle, and
// it sends a signed-in member to a page addressed as though it were nobody's.
// The account's own address is used whenever the session can name one, and
// `/profile` remains the fallback for a visitor who has not signed in.
const DEFAULT_PROFILE_HREF = "/profile";

function primaryNavFor(profileHref: string): NavItem[] {
  return [
    { label: "首页", href: "/", icon: "home", active: ["home", "detail"] },
    { label: "搜索", href: "/search", icon: "search", active: ["search"] },
    { label: "归档", href: "/archive", icon: "archive", active: ["archive"] },
    {
      label: "个人资料",
      href: profileHref,
      icon: "owner",
      active: ["profile"],
    },
  ];
}

const publishNav: NavItem = {
  label: "发布",
  href: "/owner/compose",
  icon: "compose",
  active: ["owner-compose"],
};

const HOME_SCROLL_DURATION_MS = 260;

function easeInOutCubic(progress: number): number {
  return progress < 0.5
    ? 4 * progress * progress * progress
    : 1 - Math.pow(-2 * progress + 2, 3) / 2;
}

function useHomeScrollToTop() {
  const animationFrame = useRef<number | null>(null);

  useEffect(
    () => () => {
      if (animationFrame.current !== null) {
        window.cancelAnimationFrame(animationFrame.current);
      }
    },
    [],
  );

  return useCallback((event: ReactMouseEvent<HTMLAnchorElement>) => {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }

    event.preventDefault();
    if (animationFrame.current !== null) {
      window.cancelAnimationFrame(animationFrame.current);
      animationFrame.current = null;
    }

    const startY = window.scrollY;
    if (startY <= 0) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      window.scrollTo({ top: 0, behavior: "auto" });
      return;
    }

    let startedAt: number | null = null;
    const step = (timestamp: number) => {
      startedAt ??= timestamp;
      const progress = Math.min(
        (timestamp - startedAt) / HOME_SCROLL_DURATION_MS,
        1,
      );
      const top = Math.max(0, startY * (1 - easeInOutCubic(progress)));
      window.scrollTo({ top, behavior: "auto" });
      if (progress < 1) {
        animationFrame.current = window.requestAnimationFrame(step);
      } else {
        animationFrame.current = null;
      }
    };

    animationFrame.current = window.requestAnimationFrame(step);
  }, []);
}

function ownerNav(settingsAvailable: boolean): NavItem {
  return {
    label: "所有者",
    href: settingsAvailable ? "/owner/settings" : "/login",
    icon: "owner",
    active: ["owner-login", "owner-settings", "owner-edit"],
  };
}

function focusedComposerControl(): boolean {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement)) return false;
  if (
    !active.matches(
      'input:not([type="hidden"]), textarea, select, [contenteditable="true"]',
    )
  ) {
    return false;
  }
  return Boolean(
    active.closest(
      'section[aria-label="发布 Moment"], section[aria-label="编辑 Moment"], form[aria-label="发表回复"], form[aria-label^="回复 "]',
    ),
  );
}

function useMobileNavigationObscured(mobileViewport: boolean): boolean {
  const [obscured, setObscured] = useState(false);

  useEffect(() => {
    const viewport = window.visualViewport;
    if (!mobileViewport || !viewport) return;
    let frame = 0;
    let layoutHeight = Math.max(window.innerHeight, viewport.height);
    const update = () => {
      const contraction = Math.max(96, layoutHeight * 0.12);
      setObscured(
        focusedComposerControl() &&
          viewport.height < layoutHeight - contraction,
      );
    };
    const updateLayout = () => {
      if (!focusedComposerControl()) {
        layoutHeight = Math.max(window.innerHeight, viewport.height);
      }
      update();
    };
    const updateAfterFocus = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(update);
    };
    update();
    viewport.addEventListener("resize", update);
    viewport.addEventListener("scroll", update);
    window.addEventListener("resize", updateLayout);
    document.addEventListener("focusin", update);
    document.addEventListener("focusout", updateAfterFocus);
    return () => {
      window.cancelAnimationFrame(frame);
      viewport.removeEventListener("resize", update);
      viewport.removeEventListener("scroll", update);
      window.removeEventListener("resize", updateLayout);
      document.removeEventListener("focusin", update);
      document.removeEventListener("focusout", updateAfterFocus);
    };
  }, [mobileViewport]);

  return mobileViewport && obscured;
}

function Brand({ site }: { site: SiteIdentity }) {
  const title = site.title.trim() || "Moment";
  return (
    <InternalLink aria-label={`${title}首页`} className={styles.brand} href="/">
      <svg aria-hidden="true" className={styles.brandMark} viewBox="0 0 40 40">
        <path d="M7 30V10l13 14 13-14v20" />
      </svg>
      <span className={styles.brandLabel}>{title}</span>
    </InternalLink>
  );
}

function NavLink({ item, route }: { item: NavItem; route: AppRoute }) {
  const current = item.active.includes(route.id);
  return (
    <InternalLink
      aria-current={current ? "page" : undefined}
      aria-label={item.label}
      className={styles.navLink}
      data-compact-label={item.label}
      href={item.href}
    >
      <Icon
        name={item.icon}
        variant={current ? "navigation-active" : "navigation"}
      />
      <span>{item.label}</span>
    </InternalLink>
  );
}

function LeftRail({
  route,
  site,
  ownerSettingsAvailable,
  profileHref,
}: {
  route: AppRoute;
  site: SiteIdentity;
  ownerSettingsAvailable: boolean;
  profileHref: string;
}) {
  const { navigateOverlay } = useNavigation();
  return (
    <aside aria-label="站点导航" className={styles.leftRail}>
      <Brand site={site} />
      <nav aria-label="主要导航" className={styles.desktopNav}>
        {primaryNavFor(profileHref).map((item) => (
          <NavLink item={item} key={item.href} route={route} />
        ))}
        <ThemeMenu navigation site={site} />
      </nav>
      <InternalLink
        aria-label="发布"
        className={styles.publishAction}
        data-compact-label="发布"
        data-focus-key="shell:compose"
        href="/owner/compose"
        onClick={(event) => {
          if (!window.matchMedia("(min-width: 720px)").matches) return;
          event.preventDefault();
          navigateOverlay("/owner/compose");
        }}
      >
        <Icon name="compose" />
        <span>发布</span>
      </InternalLink>
      <div className={styles.railBottom}>
        <NavLink item={ownerNav(ownerSettingsAvailable)} route={route} />
      </div>
    </aside>
  );
}

function MemoryRail({
  route,
  years,
}: {
  route: AppRoute;
  years: readonly number[];
}) {
  if (route.id === "search" && years.length === 0) return null;
  return (
    <aside aria-label="记忆导航" className={styles.rightRail}>
      {route.id !== "search" ? (
        <InternalLink className={styles.searchPrompt} href="/search">
          <Icon name="search" />
          <span>
            <strong>搜索 Moment</strong>
            <small>按文字或日期查找</small>
          </span>
        </InternalLink>
      ) : null}
      {years.length > 0 && route.id !== "archive" ? (
        <section
          aria-labelledby="memory-map-title"
          className={styles.memoryMap}
        >
          <header>
            <div>
              <p className={styles.railEyebrow}>归档</p>
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
    </aside>
  );
}

function MobileAccountMenu({
  ownerSettingsAvailable,
  site,
  profileHref,
}: {
  ownerSettingsAvailable: boolean;
  site: SiteIdentity;
  profileHref: string;
}) {
  const { navigate } = useNavigation();
  const ownerItem = ownerNav(ownerSettingsAvailable);

  return (
    <Menu.Root>
      <Menu.Trigger aria-label="打开账户菜单" className={styles.mobileAvatar}>
        <SiteAvatar eager site={site} />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          align="start"
          className={styles.mobileAccountPositioner}
          collisionAvoidance={{ side: "flip", align: "shift" }}
          collisionPadding={8}
          side="bottom"
          sideOffset={6}
        >
          <Menu.Popup aria-label="账户" className={styles.mobileAccountPopup}>
            <Menu.Item
              className={styles.mobileAccountItem}
              onClick={() => navigate(profileHref)}
            >
              <Icon name="owner" />
              <span>个人资料</span>
            </Menu.Item>
            <Menu.Item
              className={styles.mobileAccountItem}
              onClick={() => navigate(ownerItem.href)}
            >
              <Icon name="settings" />
              <span>
                {ownerSettingsAvailable ? "所有者设置" : "所有者登录"}
              </span>
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

function MobileNavigation({
  ownerSettingsAvailable,
  route,
}: {
  ownerSettingsAvailable: boolean;
  route: AppRoute;
}) {
  const mobileViewport = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  const obscured = useMobileNavigationObscured(mobileViewport);
  const items: NavItem[] = [
    ...primaryNavFor(DEFAULT_PROFILE_HREF).slice(0, 3),
    publishNav,
    ownerNav(ownerSettingsAvailable),
  ];
  return (
    <nav
      aria-label="移动导航"
      className={styles.mobileNav}
      data-viewport-obscured={obscured || undefined}
    >
      {items.map((item) => (
        <NavLink item={item} key={item.href} route={route} />
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
  profileHref = DEFAULT_PROFILE_HREF,
}: {
  route: AppRoute;
  state: string | null;
  site: SiteIdentity;
  children?: ReactNode;
  memoryYears?: readonly number[];
  ownerSettingsAvailable?: boolean;
  profileHref?: string;
}) {
  const { goBack } = useNavigation();
  const scrollHomeToTop = useHomeScrollToTop();
  return (
    <div className={styles.viewport}>
      <a className={styles.skipLink} href="#main-content">
        跳到主要内容
      </a>
      <div
        className={`${styles.shell} ${route.id === "detail" ? styles.detailShell : ""}`}
      >
        <LeftRail
          ownerSettingsAvailable={ownerSettingsAvailable}
          profileHref={profileHref}
          route={route}
          site={site}
        />
        <main className={styles.timeline} id="main-content" tabIndex={-1}>
          <header
            className={`${styles.timelineHeader} ${
              route.id === "home" ? styles.homeHeader : ""
            } ${route.id === "detail" ? styles.detailHeader : ""}`}
          >
            {route.id === "detail" ||
            route.id === "profile" ||
            route.id === "owner-edit" ||
            route.id === "owner-compose" ? (
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
              <MobileAccountMenu
                ownerSettingsAvailable={ownerSettingsAvailable}
                profileHref={profileHref}
                site={site}
              />
            )}
            {route.id === "home" ? (
              <nav aria-label="首页时间流" className={styles.homeHeaderNav}>
                <InternalLink
                  aria-current="page"
                  className={styles.homeHeaderTab}
                  href="/"
                  onClick={scrollHomeToTop}
                >
                  <h1>{route.title}</h1>
                  <span aria-hidden="true" />
                </InternalLink>
              </nav>
            ) : (
              <div>
                <p>{route.eyebrow}</p>
                <h1>
                  {route.id === "profile" ? siteDisplayName(site) : route.title}
                </h1>
              </div>
            )}
            {route.id !== "detail" && route.id !== "owner-edit" ? (
              <span className={styles.headerTheme}>
                <ThemeMenu site={site} />
              </span>
            ) : null}
          </header>
          {children ?? <ShellState route={route} state={state} />}
        </main>
        <MemoryRail route={route} years={memoryYears} />
      </div>
      <MobileNavigation
        ownerSettingsAvailable={ownerSettingsAvailable}
        route={route}
      />
    </div>
  );
}
