import { Dialog } from "@base-ui/react/dialog";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { PublicApi, type SiteIdentity } from "../api/public";
import { AppShell } from "../components/AppShell";
import { ShellState } from "../components/ShellState";
import { DetailPage } from "../features/timeline/DetailPage";
import { TimelinePage } from "../features/timeline/TimelinePage";
import { Composer, type ComposerHandle } from "../features/owner/Composer";
import { EditPage } from "../features/owner/EditPage";
import { LoginPage } from "../features/owner/LoginPage";
import { MentionProvider } from "./mentions";
import { OwnerLogin } from "../features/owner/OwnerLogin";
import {
  OwnerProvider,
  ownsSite,
  useOwner,
} from "../features/owner/OwnerProvider";
import { SettingsPage } from "../features/owner/SettingsPage";
import { UsernameSetup } from "../features/owner/UsernameSetup";
import { ArchivePage } from "../features/memory/ArchivePage";
import { hasValidRawQueryEncoding } from "../features/memory/memoryState";
import { SearchPage } from "../features/memory/SearchPage";
import { MemberProfilePage } from "../features/profile/MemberProfilePage";
import { ProfilePage } from "../features/profile/ProfilePage";
import { ThemeProvider } from "../theme/ThemeProvider";
import { FeedbackProvider } from "../components/Feedback";
import { useDocumentScrollLock } from "../components/useDocumentScrollLock";
import {
  MOBILE_VIEWPORT_QUERY,
  useMediaQuery,
} from "../components/useMediaQuery";
import { NavigationProvider, useNavigation } from "./navigation";
import { resolveRoute } from "./routes";
import styles from "./App.module.css";

const fallbackSite: SiteIdentity = {
  ownerId: "00000000000000000000000000000000",
  username: "owner",
  displayName: "Moment",
  title: "Moment",
  bio: "",
  timezone: "UTC",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
  instanceInitializedAt: null,
};

function detailId(pathname: string): string {
  const raw = pathname.match(/^\/m\/([^/]+)\/?$/)?.[1] ?? "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

function editId(pathname: string): string {
  const raw = pathname.match(/^\/owner\/m\/([^/]+)\/edit\/?$/)?.[1] ?? "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

function profileUsername(pathname: string): string | null {
  // `/profile` is the owner's own handle-free entry point, not a handle lookup.
  if (pathname === "/profile" || pathname === "/profile/") return null;
  return pathname.match(/^\/([a-z][a-z0-9_]{2,29})\/?$/)?.[1] ?? null;
}

function editableTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    (target.matches("input, textarea, select") ||
      target.isContentEditable ||
      Boolean(target.closest('[contenteditable="true"]')))
  );
}

function routeSearchKey(search: string): string {
  if (!hasValidRawQueryEncoding(search)) return search;
  const params = new URLSearchParams(search);
  return JSON.stringify(
    Array.from(params.entries()).filter(([name]) => name !== "loaded_through"),
  );
}

function PublicApp() {
  const location = useNavigation();
  const requestedRoute = resolveRoute(location.pathname);
  const overlayCompose =
    requestedRoute.id === "owner-compose" && location.overlayFrom !== null;
  const displayLocation = overlayCompose ? location.overlayFrom! : location;
  const quotedPostId = hasValidRawQueryEncoding(location.search)
    ? (new URLSearchParams(location.search).get("quote") ?? undefined)
    : undefined;
  const route = resolveRoute(displayLocation.pathname);
  const state =
    route.id === "search" ||
    route.id === "archive" ||
    !hasValidRawQueryEncoding(displayLocation.search)
      ? null
      : new URLSearchParams(displayLocation.search).get("state");
  const api = useMemo(() => new PublicApi(), []);
  const owner = useOwner();
  const mobileViewport = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  const [site, setSite] = useState(fallbackSite);
  const [siteLoading, setSiteLoading] = useState(true);
  const [siteError, setSiteError] = useState(false);
  const [siteAttempt, setSiteAttempt] = useState(0);
  const [homeVisited, setHomeVisited] = useState(route.id === "home");
  const initialHomeSearch = useRef(
    route.id === "home" ? displayLocation.search : "",
  );
  const overlayComposer = useRef<ComposerHandle>(null);
  const [composeExitOpen, setComposeExitOpen] = useState(false);
  const siteIdentityReady = !siteLoading && !siteError;
  // Accounts arrived, so being signed in stopped meaning owning the site.
  // Every owner-only surface asks the account instead of the session state. A
  // session with no account behind it -- an instance that cannot answer for
  // accounts, or a lookup that failed -- keeps the pre-accounts answer, so the
  // single owner such an instance has is never locked out of it.
  const siteOwner = ownsSite(owner.account);
  const ownerSession = owner.state === "owner" && siteOwner;
  // An expired session still renders the pages behind it; they carry their own
  // inline re-login rather than throwing the reader back to a login screen.
  const signedIn = owner.state === "owner" || owner.state === "expired";
  const ownerCapable = signedIn && siteOwner;

  useDocumentScrollLock(
    (overlayCompose && siteIdentityReady) || composeExitOpen,
  );

  const closeOverlayComposer = () => {
    location.goBack(`${displayLocation.pathname}${displayLocation.search}`);
  };

  const requestOverlayComposerClose = () => {
    if (overlayComposer.current?.hasUnpublishedChanges()) {
      setComposeExitOpen(true);
      return;
    }
    closeOverlayComposer();
  };

  useEffect(() => {
    const controller = new AbortController();
    setSiteLoading(true);
    setSiteError(false);
    void api
      .site(controller.signal)
      .then(setSite)
      .catch(() => {
        if (!controller.signal.aborted) setSiteError(true);
      })
      .finally(() => {
        if (!controller.signal.aborted) setSiteLoading(false);
      });
    return () => controller.abort();
  }, [api, siteAttempt]);

  useEffect(() => {
    if (route.id === "home") {
      setHomeVisited(true);
      if (!homeVisited) initialHomeSearch.current = displayLocation.search;
    }
  }, [displayLocation.search, homeVisited, route.id]);

  // Signing out from the rail. The provider clears its own stored session
  // either way: a session the server has already forgotten is signed out, and
  // reporting a failure there would describe the transport rather than the
  // outcome somebody asked for.
  const signOutFromRail = useCallback(async () => {
    try {
      await owner.logout(site.instanceId);
    } catch {
      owner.forgetSession(site.instanceId);
    }
    location.navigate("/");
  }, [location, owner, site.instanceId]);

  // `/profile` means "mine", and only a session says whose. A visitor without
  // one is asking for a page that does not exist for them, so they are sent to
  // sign in; anyone signed in is sent to their own handle, which is the page
  // they were actually asking for. It used to render the site owner's profile
  // to whoever opened it, so a signed-in member met somebody else's page.
  useEffect(() => {
    const pathname = displayLocation.pathname;
    if (pathname !== "/profile" && pathname !== "/profile/") return;
    // Still resolving, or unable to say. Neither is an answer to act on.
    if (owner.state === "checking" || owner.state === "unavailable") return;
    const own = owner.account?.profileUrl;
    // A session handed over by a sibling tab arrives before the account behind
    // it does, so "signed in but nobody yet" is a third state and not a guest.
    // Treating it as one sent a fully signed-in reader to the sign-in page.
    if (own === undefined && owner.state !== "guest") return;
    location.navigate(own ?? "/login?return=%2Fprofile");
  }, [displayLocation.pathname, location, owner.account, owner.state]);

  useEffect(() => {
    const siteTitle = site.title.trim() || "Moment";
    const titleRoute = overlayCompose ? requestedRoute : route;
    document.title =
      titleRoute.id === "home"
        ? siteTitle
        : `${titleRoute.title} · ${siteTitle}`;
  }, [overlayCompose, requestedRoute, route, site.title]);

  useEffect(() => {
    let composing = false;
    const startComposition = () => {
      composing = true;
    };
    const endComposition = () => {
      composing = false;
    };
    const newMoment = (event: KeyboardEvent) => {
      if (
        !siteIdentityReady ||
        !ownerSession ||
        event.defaultPrevented ||
        event.key.toLowerCase() !== "n" ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey ||
        event.shiftKey ||
        event.isComposing ||
        composing ||
        editableTarget(event.target) ||
        document.querySelector('[role="dialog"]:not([aria-modal="false"])')
      ) {
        return;
      }
      event.preventDefault();
      if (window.matchMedia("(min-width: 720px)").matches) {
        location.navigateOverlay("/owner/compose");
      } else {
        location.navigate("/owner/compose");
      }
    };
    document.addEventListener("compositionstart", startComposition);
    document.addEventListener("compositionend", endComposition);
    window.addEventListener("keydown", newMoment);
    return () => {
      document.removeEventListener("compositionstart", startComposition);
      document.removeEventListener("compositionend", endComposition);
      window.removeEventListener("keydown", newMoment);
    };
  }, [location, ownerSession, siteIdentityReady]);

  useEffect(() => {
    let composing = false;
    const startComposition = () => {
      composing = true;
    };
    const endComposition = () => {
      composing = false;
    };
    const focusSearch = (event: KeyboardEvent) => {
      if (
        event.defaultPrevented ||
        event.key !== "/" ||
        event.metaKey ||
        event.ctrlKey ||
        event.altKey ||
        event.shiftKey ||
        event.isComposing ||
        composing ||
        editableTarget(event.target) ||
        document.querySelector('[role="dialog"]:not([aria-modal="false"])')
      ) {
        return;
      }
      event.preventDefault();
      if (route.id !== "search") location.navigate("/search");
      window.requestAnimationFrame(() => {
        document
          .querySelector<HTMLInputElement>("#moment-search-input")
          ?.focus();
      });
    };
    document.addEventListener("compositionstart", startComposition);
    document.addEventListener("compositionend", endComposition);
    window.addEventListener("keydown", focusSearch);
    return () => {
      document.removeEventListener("compositionstart", startComposition);
      document.removeEventListener("compositionend", endComposition);
      window.removeEventListener("keydown", focusSearch);
    };
  }, [location, route.id]);

  const hasTimeline = route.id === "home" || homeVisited;
  const placeholderRoute =
    route.id !== "home" &&
    route.id !== "detail" &&
    route.id !== "login" &&
    route.id !== "owner-compose" &&
    route.id !== "owner-edit" &&
    route.id !== "owner-settings" &&
    route.id !== "search" &&
    route.id !== "archive" &&
    route.id !== "profile";
  const requestedProfileUsername = profileUsername(displayLocation.pathname);
  // `/profile` and the owner's own handle are the site page, which already has
  // every fact it needs. Any other handle is somebody else on this instance,
  // and only the server can say whether that somebody exists.
  const ownProfile =
    requestedProfileUsername === null ||
    requestedProfileUsername === site.username;

  // Sign-in is the whole page, not a panel inside the shell. Every hook above
  // has already run, so returning here changes what is rendered without
  // changing what is evaluated.
  if (route.id === "login") return <LoginPage site={site} />;

  return (
    // The owner's handle is the one this app knows without asking. Anything
    // else a body mentions is resolved against the server before it becomes a
    // link, because a mention is text somebody typed and not a reference
    // anything checked.
    <MentionProvider known={siteIdentityReady ? [site.username] : []}>
      <AppShell
        account={owner.account}
        onSignOut={() => void signOutFromRail()}
        ownerSettingsAvailable={siteIdentityReady && signedIn}
        profileHref={owner.account?.profileUrl ?? undefined}
        route={route}
        site={site}
        state={state}
      >
        {siteError ? (
          <div className={styles.siteError} role="alert">
            <span>站点资料没有加载完成，暂时使用默认身份。</span>
            <button
              onClick={() => setSiteAttempt((value) => value + 1)}
              type="button"
            >
              重试资料
            </button>
          </div>
        ) : null}
        {hasTimeline ? (
          <div hidden={route.id !== "home"}>
            {siteIdentityReady && !mobileViewport && ownerCapable ? (
              <div className={styles.homeComposer}>
                <Composer
                  active={route.id === "home"}
                  site={site}
                  variant="inline"
                />
              </div>
            ) : null}
            <TimelinePage
              api={api}
              initialSearch={initialHomeSearch.current}
              onSiteChanged={setSite}
              site={site}
              owner={siteIdentityReady && ownerSession}
            />
          </div>
        ) : null}
        {route.id === "detail" ? (
          <DetailPage
            api={api}
            id={detailId(displayLocation.pathname)}
            owner={siteIdentityReady && ownerSession}
            onSiteChanged={setSite}
            site={site}
          />
        ) : null}
        {route.id === "search" ? (
          <SearchPage
            api={api}
            key={routeSearchKey(displayLocation.search)}
            onSiteChanged={setSite}
            owner={siteIdentityReady && ownerSession}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {route.id === "archive" ? (
          // No key: the page follows its own search string, so a day picked on
          // the calendar moves the list without throwing the calendar away.
          <ArchivePage
            api={api}
            owner={siteIdentityReady && ownerSession}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {siteIdentityReady && route.id === "profile" && ownProfile ? (
          <ProfilePage api={api} onSiteSaved={setSite} site={site} />
        ) : null}
        {siteIdentityReady &&
        route.id === "profile" &&
        !ownProfile &&
        requestedProfileUsername ? (
          <MemberProfilePage api={api} username={requestedProfileUsername} />
        ) : null}
        {siteIdentityReady && route.id === "owner-compose" ? (
          ownerCapable ? (
            <Composer quotedPostId={quotedPostId} site={site} variant="page" />
          ) : (
            <OwnerLogin embedded site={site} />
          )
        ) : null}
        {siteIdentityReady && route.id === "owner-edit" ? (
          ownerCapable ? (
            <EditPage
              api={api}
              id={editId(displayLocation.pathname)}
              site={site}
            />
          ) : (
            <OwnerLogin embedded site={site} />
          )
        ) : null}
        {siteIdentityReady && route.id === "owner-settings" ? (
          // Any signed-in account may open this page. What it shows them is the
          // page's own decision, section by section.
          signedIn ? (
            <SettingsPage onSaved={setSite} site={site} />
          ) : (
            <OwnerLogin embedded site={site} />
          )
        ) : null}
        {!siteLoading && placeholderRoute ? (
          <ShellState route={route} state={state} />
        ) : null}
      </AppShell>
      {overlayCompose && siteIdentityReady ? (
        <Dialog.Root
          onOpenChange={(open) => {
            if (!open) requestOverlayComposerClose();
          }}
          open
        >
          <Dialog.Portal>
            <Dialog.Backdrop className={styles.composeBackdrop} />
            <Dialog.Viewport className={styles.composeViewport}>
              <Dialog.Popup className={styles.composeDialog}>
                <Dialog.Title className={styles.srOnly}>
                  发布 Moment
                </Dialog.Title>
                {ownerCapable ? (
                  <Composer
                    quotedPostId={quotedPostId}
                    ref={overlayComposer}
                    site={site}
                    variant="page"
                  />
                ) : (
                  <OwnerLogin embedded site={site} />
                )}
              </Dialog.Popup>
            </Dialog.Viewport>
          </Dialog.Portal>
        </Dialog.Root>
      ) : null}
      {/* An expired session cannot write a profile, and a prompt whose only
          two buttons are both refused is a trap; it comes back with the next
          session, which is the point at which it can be answered. */}
      {owner.session && owner.account?.usernameChosen === false ? (
        <UsernameSetup
          account={owner.account}
          api={api}
          onSiteChanged={setSite}
        />
      ) : null}
      <Dialog.Root onOpenChange={setComposeExitOpen} open={composeExitOpen}>
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.exitBackdrop} />
          <Dialog.Viewport className={styles.exitViewport}>
            <Dialog.Popup className={styles.exitDialog}>
              <Dialog.Title>退出发布？</Dialog.Title>
              <Dialog.Description>
                文字可以保留为这台浏览器里的草稿；已添加的图片不会保存。
              </Dialog.Description>
              <div className={styles.exitActions}>
                <Dialog.Close className={styles.exitSecondary}>
                  继续编辑
                </Dialog.Close>
                <button
                  className={styles.exitSecondary}
                  onClick={() => {
                    overlayComposer.current?.leaveAndKeepDraft();
                    setComposeExitOpen(false);
                    closeOverlayComposer();
                  }}
                  type="button"
                >
                  保留草稿并退出
                </button>
                <button
                  className={styles.exitDanger}
                  onClick={() => {
                    overlayComposer.current?.leaveAndDiscard();
                    setComposeExitOpen(false);
                    closeOverlayComposer();
                  }}
                  type="button"
                >
                  丢弃并退出
                </button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
    </MentionProvider>
  );
}

export function App() {
  return (
    <ThemeProvider>
      <FeedbackProvider>
        <NavigationProvider>
          <OwnerProvider>
            <PublicApp />
          </OwnerProvider>
        </NavigationProvider>
      </FeedbackProvider>
    </ThemeProvider>
  );
}
