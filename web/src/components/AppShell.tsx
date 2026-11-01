import { NotificationBadge } from "../features/notifications/NotificationBadge";
import { BackControl } from "./BackControl";
import { t } from "../i18n";
import { useTranslation } from "react-i18next";
import { AccountName } from "./AccountName";
import { useLoginNavigation } from "../features/auth/loginNavigation";
import { Menu } from "@base-ui/react/menu";
import { Tooltip } from "@base-ui/react/tooltip";
import {
  type MouseEvent as ReactMouseEvent,
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import type { Account, SiteIdentity } from "../api/public";
import { InternalLink, useNavigation } from "../app/navigation";
import type { AppRoute, RouteId } from "../app/routes";
import { SiteAvatar, shellTitle } from "./SiteIdentity";
import { Icon, type IconName } from "./icons";
import { RailSlotContext } from "./railSlot";
import { HeaderActionsContext } from "./headerActions";
import {
  ProfileHeaderContext,
  type ProfileHeaderIdentity,
} from "../features/profile/ProfileHeader";
import { ShellState } from "./ShellState";
import { ThemeMenu } from "./ThemeMenu";
import { RailSearch } from "../features/memory/RailSearch";
import { recentSearchScope } from "../features/memory/recentSearches";
import { SidebarTooltip } from "./SidebarTooltip";
import { ThemeToggle } from "./content/notion/components/templates/notion-like/notion-like-editor-theme-toggle";
import { MOBILE_VIEWPORT_QUERY, useMediaQuery } from "./useMediaQuery";
import styles from "./AppShell.module.css";
import {
  readingLayoutStyle,
  useReadingLayout,
} from "../features/article/ReadingLayout";

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

function primaryNavFor(profileHref: string, homeLabel = t("首页")): NavItem[] {
  return [
    {
      label: homeLabel,
      href: "/",
      icon: "home",
      active: ["home", "detail", "comment-detail"],
    },
    { label: t("搜索"), href: "/search", icon: "search", active: ["search"] },
    {
      label: t("归档"),
      href: "/archive",
      icon: "archive",
      active: ["archive"],
    },
    {
      label: t("个人资料"),
      href: profileHref,
      icon: "account",
      active: ["profile"],
    },
    {
      label: t("文章"),
      href: "/compose/articles",
      icon: "article",
      active: ["article-workspace"],
    },
  ];
}

const publishNav: NavItem = {
  get label() {
    return t("发布");
  },
  href: "/compose",
  icon: "compose",
  active: ["compose", "article-workspace"],
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

function accountNav(
  settingsAvailable: boolean,
  loginDestination: string,
): NavItem {
  return {
    label: t("账号"),
    href: settingsAvailable ? "/settings" : loginDestination,
    icon: "account",
    active: ["login", "settings", "edit"],
  };
}

function AccountRail({
  account,
  onSignOut,
  route,
  settingsAvailable,
}: {
  account: Account | null;
  onSignOut: () => void;
  route: AppRoute;
  settingsAvailable: boolean;
}) {
  const { t } = useTranslation();

  const { navigate } = useNavigation();
  const { href: loginDestination } = useLoginNavigation();
  // Nobody is signed in, so there is no face to show and one thing to offer.
  if (!account) {
    return (
      <NavLink
        item={accountNav(settingsAvailable, loginDestination)}
        route={route}
      />
    );
  }
  // SiteAvatar reads an identity face, and an account is one: the cover is
  // never drawn at this size, so it travels only to satisfy the shape.
  const face = {
    username: account.username,
    displayName: account.displayName,
    title: account.displayName,
    avatarMediaId: account.avatarMediaId,
    coverMediaId: account.coverMediaId,
  };
  return (
    <Menu.Root>
      <SidebarTooltip label={t("账户")}>
        <Menu.Trigger
          aria-label={t("{{value1}} 的账户菜单", {
            value1: account.displayName,
          })}
          className={styles.accountRail}
        >
          <span className={styles.accountRailAvatar}>
            <SiteAvatar site={face} />
          </span>
          <span className={styles.accountRailIdentity}>
            <span className={styles.accountRailName}>
              <AccountName identity={account} />
            </span>
            <span className={styles.accountRailHandle}>
              @{account.username}
            </span>
          </span>
          <Icon name="ellipsis" />
        </Menu.Trigger>
      </SidebarTooltip>
      <Menu.Portal>
        <Menu.Positioner
          align="center"
          className={styles.mobileAccountPositioner}
          collisionAvoidance={{ side: "flip", align: "shift" }}
          collisionPadding={8}
          side="top"
          sideOffset={10}
        >
          <Menu.Popup
            aria-label={t("账户")}
            className={`${styles.mobileAccountPopup} ${styles.accountRailPopup}`}
          >
            {/* No profile entry: the rail already carries one, and a menu that
                repeats the row above it is a longer way to the same page. */}
            <Menu.Item
              className={styles.mobileAccountItem}
              onClick={() => navigate("/compose/articles")}
            >
              <Icon name="article" />
              <span>{t("文章")}</span>
            </Menu.Item>
            <Menu.Item className={styles.mobileAccountItem} onClick={onSignOut}>
              <Icon name="close" />
              <span>
                {t("退出 @")}
                {account.username}
              </span>
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
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
  return Boolean(active.closest("[data-writing-surface]"));
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

function Brand({
  site,
  account,
}: {
  site: SiteIdentity;
  account: Account | null;
}) {
  const { t } = useTranslation();

  const title = shellTitle(site, account);
  const { pathname } = useNavigation();
  const scrollHomeToTop = useHomeScrollToTop();
  return (
    <InternalLink
      aria-label={t("{{value1}}首页", { value1: title })}
      className={styles.brand}
      href="/"
      onClick={pathname === "/" ? scrollHomeToTop : undefined}
    >
      <span aria-hidden="true" className={styles.brandMark} />
      <span className={styles.brandLabel}>{title}</span>
    </InternalLink>
  );
}

function NavLink({ item, route }: { item: NavItem; route: AppRoute }) {
  const current = item.active.includes(route.id);
  return (
    <SidebarTooltip label={item.label}>
      <InternalLink
        aria-current={current ? "page" : undefined}
        aria-label={item.label}
        className={styles.navLink}
        data-compact-label={item.label}
        href={item.href}
      >
        <span className={styles.navPill}>
          {item.icon === "notification" ? (
            <span className={styles.notificationIcon}>
              <Icon
                name="notification"
                variant={current ? "navigation-active" : "navigation"}
              />
              <NotificationBadge />
            </span>
          ) : (
            <Icon
              name={item.icon}
              variant={current ? "navigation-active" : "navigation"}
            />
          )}
          <span className={styles.navLabel}>{item.label}</span>
        </span>
      </InternalLink>
    </SidebarTooltip>
  );
}

function LeftRail({
  route,
  site,
  accountSettingsAvailable,
  profileHref,
  account,
  onSignOut,
}: {
  route: AppRoute;
  site: SiteIdentity;
  accountSettingsAvailable: boolean;
  profileHref: string;
  account: Account | null;
  onSignOut: () => void;
}) {
  const { t } = useTranslation();

  const { navigateOverlay } = useNavigation();
  const items = primaryNavFor(profileHref, t("主页"));
  return (
    <Tooltip.Provider delay={400}>
      <aside aria-label={t("站点导航")} className={styles.leftRail}>
        <div className={styles.leftRailInner}>
          <Brand site={site} account={account} />
          <nav aria-label={t("主要导航")} className={styles.desktopNav}>
            {items.slice(0, 2).map((item) => (
              <NavLink item={item} key={item.href} route={route} />
            ))}
            <NavLink
              item={{
                label: t("通知"),
                href: "/account/notifications",
                icon: "notification",
                active: ["notifications"],
              }}
              route={route}
            />
            {items.slice(2).map((item) => (
              <NavLink item={item} key={item.href} route={route} />
            ))}
            <NavLink
              item={{
                label: t("设置"),
                href: "/settings",
                icon: "settings",
                active: ["settings"],
              }}
              route={route}
            />
            <ThemeMenu account={account} navigation site={site} />
          </nav>
          <SidebarTooltip label={t("发帖")}>
            <InternalLink
              aria-label={t("发帖")}
              className={styles.publishAction}
              data-compact-label={t("发帖")}
              data-focus-key="shell:compose"
              href="/compose"
              onMouseDown={(event) => {
                // The dialog owns the focus transfer. Retain the background
                // editor until it opens, including a held mouse press.
                if (
                  event.button === 0 &&
                  !event.metaKey &&
                  !event.ctrlKey &&
                  !event.shiftKey &&
                  !event.altKey &&
                  window.matchMedia("(min-width: 720px)").matches
                )
                  event.preventDefault();
              }}
              onClick={(event) => {
                if (
                  event.button !== 0 ||
                  event.metaKey ||
                  event.ctrlKey ||
                  event.shiftKey ||
                  event.altKey
                )
                  return;
                if (!window.matchMedia("(min-width: 720px)").matches) return;
                event.preventDefault();
                navigateOverlay("/compose");
              }}
            >
              <Icon name="sidebar-publish" />
              <span>{t("发帖")}</span>
            </InternalLink>
          </SidebarTooltip>
          <div className={styles.railBottom}>
            <AccountRail
              account={account}
              onSignOut={onSignOut}
              route={route}
              settingsAvailable={accountSettingsAvailable}
            />
          </div>
        </div>
      </aside>
    </Tooltip.Provider>
  );
}

// The search box, and a slot the page may fill -- the archive puts its
// calendar there. A card of years used to hang under the search box, built
// from whichever posts the feed had loaded so far (a repost of a 2019 Moment
// near the top put 2019 second in a list headed 2026), and it opened a page
// the rail beside it already links to. The archive has one entry, in the
// navigation; the rail is for what a page cannot otherwise reach.
function MemoryRail({
  route,
  scope,
  slotRef,
}: {
  route: AppRoute;
  scope: string | null;
  slotRef: (element: HTMLDivElement | null) => void;
}) {
  const { t } = useTranslation();

  if (route.id === "search") return null;
  return (
    <aside aria-label={t("记忆导航")} className={styles.rightRail}>
      <RailSearch scope={scope} />
      <div className={styles.railSlot} ref={slotRef} />
    </aside>
  );
}

function MobileAccountMenu({
  accountSettingsAvailable,
  account,
  profileHref,
}: {
  accountSettingsAvailable: boolean;
  account: Account | null;
  profileHref: string;
}) {
  const { t } = useTranslation();

  const { navigate } = useNavigation();
  const { href: loginDestination } = useLoginNavigation();
  const accountItem = accountNav(accountSettingsAvailable, loginDestination);

  return (
    <Menu.Root>
      <Menu.Trigger
        aria-label={t("打开账户菜单")}
        className={styles.mobileAvatar}
      >
        {account ? (
          <SiteAvatar eager site={account} />
        ) : (
          <Icon name="account" />
        )}
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
          <Menu.Popup
            aria-label={t("账户")}
            className={styles.mobileAccountPopup}
          >
            <Menu.Item
              className={styles.mobileAccountItem}
              onClick={() => navigate(profileHref)}
            >
              <Icon name="account" />
              <span>{t("个人资料")}</span>
            </Menu.Item>
            <Menu.Item
              className={styles.mobileAccountItem}
              onClick={() => navigate("/compose/articles")}
            >
              <Icon name="article" />
              <span>{t("文章")}</span>
            </Menu.Item>
            <Menu.Item
              className={styles.mobileAccountItem}
              onClick={() => navigate(accountItem.href)}
            >
              <Icon name="settings" />
              <span>
                {accountSettingsAvailable ? t("账号设置") : t("账号登录")}
              </span>
            </Menu.Item>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}

function MobileNavigation({
  accountSettingsAvailable,
  route,
}: {
  accountSettingsAvailable: boolean;
  route: AppRoute;
}) {
  const { t } = useTranslation();

  const { href: loginDestination } = useLoginNavigation();
  const mobileViewport = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  const obscured = useMobileNavigationObscured(mobileViewport);
  const items: NavItem[] = [
    ...primaryNavFor(DEFAULT_PROFILE_HREF).slice(0, 2),
    {
      label: t("通知"),
      href: "/account/notifications",
      icon: "notification",
      active: ["notifications"],
    },
    primaryNavFor(DEFAULT_PROFILE_HREF)[2],
    publishNav,
    accountNav(accountSettingsAvailable, loginDestination),
  ];
  return (
    <nav
      data-mobile-navigation
      aria-label={t("移动导航")}
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
  accountSettingsAvailable = false,
  profileHref = DEFAULT_PROFILE_HREF,
  account = null,
  onSignOut = () => {},
}: {
  route: AppRoute;
  state: string | null;
  site: SiteIdentity;
  children?: ReactNode;
  accountSettingsAvailable?: boolean;
  profileHref?: string;
  account?: Account | null;
  onSignOut?: () => void;
}) {
  const { t } = useTranslation();

  const { goBack, pathname } = useNavigation();
  const workspace = route.id === "article-workspace";
  const focusWriting =
    workspace && pathname.replace(/\/$/, "").endsWith("/focus");
  const scrollHomeToTop = useHomeScrollToTop();
  // State rather than a ref, so a page rendering into the slot re-renders
  // once the rail has mounted it.
  const [railSlot, setRailSlot] = useState<HTMLDivElement | null>(null);
  const [headerSlot, setHeaderSlot] = useState<HTMLSpanElement | null>(null);
  const [profileHeader, setProfileHeader] =
    useState<ProfileHeaderIdentity | null>(null);
  const focusReading = route.id === "article-focus";
  const { preference: readingLayout } = useReadingLayout();
  return (
    <RailSlotContext.Provider value={railSlot}>
      <HeaderActionsContext.Provider value={headerSlot}>
        <div
          className={`${styles.viewport} ${focusReading ? styles.focusViewport : ""}`}
          data-article-theme={focusReading || undefined}
          style={focusReading ? readingLayoutStyle(readingLayout) : undefined}
        >
          <div
            className={`${styles.shell} ${route.id === "detail" || route.id === "comment-detail" ? styles.detailShell : ""} ${focusReading ? styles.focusShell : ""} ${workspace ? styles.workspaceShell : ""} ${focusWriting ? styles.workspaceFocusShell : ""}`}
          >
            {!focusReading && !focusWriting ? (
              <LeftRail
                account={account}
                onSignOut={onSignOut}
                accountSettingsAvailable={accountSettingsAvailable}
                profileHref={profileHref}
                route={route}
                site={site}
              />
            ) : null}
            <div
              className={`${styles.contentColumns} ${route.id === "settings" ? styles.settingsColumns : ""}`}
            >
              <main
                className={`${styles.timeline} ${route.id === "settings" ? styles.settingsMain : ""}`}
                id="main-content"
              >
                <header
                  style={
                    route.id === "search" || route.id === "settings"
                      ? { display: "none" }
                      : undefined
                  }
                  className={`${styles.timelineHeader} ${route.id === "notifications" ? styles.notificationsHeader : ""} ${
                    route.id === "home" ? styles.homeHeader : ""
                  } ${route.id === "detail" || route.id === "comment-detail" ? styles.detailHeader : ""} ${route.id === "profile" ? styles.profileHeader : ""} ${workspace ? styles.workspaceHiddenHeader : ""}`}
                >
                  {route.id === "detail" ||
                  route.id === "comment-detail" ||
                  focusReading ||
                  route.id === "profile" ||
                  route.id === "edit" ||
                  route.id === "compose" ? (
                    <BackControl>
                      <InternalLink
                        aria-label={
                          focusReading
                            ? t("退出全屏阅读")
                            : route.id === "comment-detail"
                              ? t("返回上一级")
                              : t("返回时间流")
                        }
                        className={styles.backLink}
                        href={
                          focusReading || route.id === "comment-detail"
                            ? route.href
                            : "/"
                        }
                        onClick={(event) => {
                          if (
                            event.button !== 0 ||
                            event.metaKey ||
                            event.ctrlKey ||
                            event.shiftKey ||
                            event.altKey
                          )
                            return;
                          event.preventDefault();
                          goBack(
                            focusReading || route.id === "comment-detail"
                              ? route.href
                              : "/",
                          );
                        }}
                      />
                    </BackControl>
                  ) : (
                    <MobileAccountMenu
                      accountSettingsAvailable={accountSettingsAvailable}
                      profileHref={profileHref}
                      account={account}
                    />
                  )}
                  {route.id === "home" ? (
                    <nav
                      aria-label={t("首页时间流")}
                      className={styles.homeHeaderNav}
                    >
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
                    <div
                      className={
                        route.id === "profile" ? styles.profileTitle : undefined
                      }
                    >
                      <p>{route.eyebrow}</p>
                      <h1>
                        {route.id === "profile" ? (
                          <span>
                            <AccountName
                              identity={{
                                userId: profileHeader?.userId ?? null,
                                displayName:
                                  profileHeader?.title ?? route.title,
                              }}
                              prominent
                            />
                          </span>
                        ) : (
                          route.title
                        )}
                      </h1>
                      {route.id === "profile" ? (
                        <span className={styles.profileCount}>
                          {profileHeader?.postCount == null
                            ? null
                            : t("{{value1}} 条帖子", {
                                count:
                                  profileHeader.postCount.toLocaleString(
                                    "zh-CN",
                                  ),
                                value1:
                                  profileHeader.postCount.toLocaleString(
                                    "zh-CN",
                                  ),
                              })}
                        </span>
                      ) : null}
                    </div>
                  )}
                  <span className={styles.headerActions} ref={setHeaderSlot}>
                    {focusReading ? <ThemeToggle headerAction /> : null}
                  </span>
                  {route.id !== "detail" &&
                  route.id !== "comment-detail" &&
                  route.id !== "profile" &&
                  route.id !== "edit" &&
                  route.id !== "notifications" &&
                  !focusReading ? (
                    <span className={styles.headerTheme}>
                      <ThemeMenu account={account} site={site} />
                    </span>
                  ) : null}
                </header>
                <ProfileHeaderContext.Provider value={setProfileHeader}>
                  {children ?? <ShellState route={route} state={state} />}
                </ProfileHeaderContext.Provider>
              </main>
              {!focusReading && !workspace && route.id !== "settings" ? (
                <MemoryRail
                  key={recentSearchScope(
                    site.instanceId,
                    account?.userId ?? null,
                  )}
                  route={route}
                  scope={recentSearchScope(
                    site.instanceId,
                    account?.userId ?? null,
                  )}
                  slotRef={setRailSlot}
                />
              ) : null}
            </div>
          </div>
          {!focusReading && !focusWriting ? (
            <MobileNavigation
              accountSettingsAvailable={accountSettingsAvailable}
              route={route}
            />
          ) : null}
        </div>
      </HeaderActionsContext.Provider>
    </RailSlotContext.Provider>
  );
}
