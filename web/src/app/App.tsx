import { accountCacheScope } from "../features/auth/accountScope";
import { Dialog } from "../components/Dialog";
import {
  Suspense,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import { PublicApi, type SiteIdentity } from "../api/public";
import { AppShell } from "../components/AppShell";
import { recentSearchScope } from "../features/memory/recentSearches";
import { Icon } from "../components/icons";
import { ShellState } from "../components/ShellState";
import { ArticleWorkspacePage } from "../features/article/editor";
import { DetailPage } from "../features/timeline/DetailPage";
import { useDiscussionIdentity } from "../features/comments/useDiscussionIdentity";
import { TimelinePage } from "../features/timeline/TimelinePage";
import { Composer, type ComposerHandle } from "../features/compose/Composer";
import { ComposeDrafts } from "../features/compose/ComposeDrafts";
import { listKeptDrafts } from "../features/compose/drafts";
import { EditPage } from "../features/posts/EditPage";
import { LoginPage } from "../features/auth/LoginPage";
import { RegisterPage } from "../features/auth/RegisterPage";
import { MentionProvider } from "./mentions";
import { CredentialLogin } from "../features/auth/CredentialLogin";
import { SessionProvider, useSession } from "../features/auth/SessionProvider";
import { SettingsPage } from "../features/settings/SettingsPage";
import { UsernameSetup } from "../features/auth/UsernameSetup";
import { ArchivePage } from "../features/memory/ArchivePage";
import { hasValidRawQueryEncoding } from "../features/memory/memoryState";
import { SearchPage } from "../features/memory/SearchPage";
import { MemberProfilePage } from "../features/profile/MemberProfilePage";
import { ProfilePage } from "../features/profile/ProfilePage";
import { SuggestedPeople } from "../features/people/SuggestedPeople";
import { ThemeProvider } from "../theme/ThemeProvider";
import { ReadingLayoutProvider } from "../features/article/ReadingLayout";
import { FeedbackProvider } from "../components/Feedback";
import { useDocumentScrollLock } from "../components/useDocumentScrollLock";
import { useEscapeFocus } from "../components/useEscapeFocus";
import { useReadRecovery } from "../components/useReadRecovery";
import {
  COMPOSE_DIALOG_VIEWPORT_QUERY,
  MOBILE_VIEWPORT_QUERY,
  useMediaQuery,
} from "../components/useMediaQuery";
import { NavigationProvider, useNavigation } from "./navigation";
import {
  COMPOSE_DRAFTS_PATH,
  COMPOSE_PATH,
  COMPOSE_MEDIA_PATH,
  articleFocusIdFromPath,
  commentDetailFromPath,
  isComposeDraftsPath,
  profileFromPath,
  resolveRoute,
} from "./routes";
import { MediaViewerProvider } from "../features/media/MediaViewerProvider";
import styles from "./App.module.css";

/** What sits behind the compose dialog when its address was loaded cold. */
const HOME_LOCATION = { pathname: "/", search: "" };

const fallbackSite: SiteIdentity = {
  profileUserId: "",
  username: "",
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
  const commentRoute = commentDetailFromPath(pathname);
  if (commentRoute) return commentRoute.postId;
  const raw = pathname.match(/^\/m\/([^/]+)\/?$/)?.[1] ?? "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

function editId(pathname: string): string {
  const raw = pathname.match(/^\/m\/([^/]+)\/edit\/?$/)?.[1] ?? "";
  try {
    return decodeURIComponent(raw);
  } catch {
    return raw;
  }
}

function profileUsername(pathname: string): string | null {
  return profileFromPath(pathname)?.username ?? null;
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
    Array.from(params.entries()).filter(
      ([name]) => name !== "loaded_through" && name !== "loaded_newer",
    ),
  );
}

function PublicApp() {
  const location = useNavigation();
  const authPage = resolveRoute(location.pathname).id;
  const authRoute = authPage === "login" || authPage === "register";
  // Reauthentication changes the address, not the work in progress. Keep the
  // original reading/editing surface mounted while the standalone login page
  // is visible, so a successful login returns to the same buffers and media.
  const [reauthLocation, setReauthLocation] = useState<{
    pathname: string;
    search: string;
    overlayFrom: { pathname: string; search: string } | null;
  } | null>(null);
  const expiryRedirected = useRef(false);
  const surfaceLocation = location.panel?.from ?? location;
  const contentLocation =
    reauthLocation &&
    (authRoute ||
      (location.pathname === reauthLocation.pathname &&
        location.search === reauthLocation.search))
      ? reauthLocation
      : surfaceLocation;

  useEffect(() => {
    // Files are intentionally not stored in text drafts. A cold media URL has
    // no buffer to edit, so return to composing rather than an empty modal.
    if (location.pathname === COMPOSE_MEDIA_PATH && !location.panel)
      location.replace(COMPOSE_PATH);
  }, [location]);
  const auth = useSession();
  const requestedRoute = resolveRoute(contentLocation.pathname);
  // Composing is a dialog wherever the page has room for one, whether it was
  // opened from the timeline or the address was loaded cold. Reloading it used
  // to drop to a bare compose screen with nothing behind it; the reference
  // keeps the dialog over the timeline, and so does this.
  const composeDialogViewport = useMediaQuery(COMPOSE_DIALOG_VIEWPORT_QUERY);
  const overlayCompose =
    requestedRoute.id === "compose" && composeDialogViewport;
  const displayLocation = overlayCompose
    ? (contentLocation.overlayFrom ?? HOME_LOCATION)
    : requestedRoute.id === "profile-edit"
      ? { pathname: auth.account?.profileUrl ?? "/profile", search: "" }
      : contentLocation;
  const quotedPostId = hasValidRawQueryEncoding(contentLocation.search)
    ? (new URLSearchParams(contentLocation.search).get("quote") ?? undefined)
    : undefined;
  const quoteParams = hasValidRawQueryEncoding(contentLocation.search)
    ? new URLSearchParams(contentLocation.search)
    : null;
  const quotedCommentId = quoteParams?.get("quoteComment") ?? undefined;
  const quotedCommentPostId = quoteParams?.get("quotePost") ?? undefined;
  const route = resolveRoute(displayLocation.pathname);
  const discussionRouteActive =
    route.id === "detail" ||
    route.id === "comment-detail" ||
    route.id === "article-focus";
  const discussionIdentity = useDiscussionIdentity(
    auth.account?.userId ?? null,
    auth.state,
  );
  const discussionActive =
    discussionRouteActive && !discussionIdentity.confirming;
  const [retainedDiscussion, setRetainedDiscussion] = useState<{
    pathname: string;
    search: string;
  } | null>(null);
  useEffect(() => {
    if (discussionRouteActive)
      setRetainedDiscussion({
        pathname: displayLocation.pathname,
        search: displayLocation.search,
      });
  }, [discussionRouteActive, displayLocation.pathname, displayLocation.search]);
  const discussionLocation = discussionRouteActive
    ? displayLocation
    : retainedDiscussion;
  const state =
    route.id === "search" ||
    route.id === "archive" ||
    !hasValidRawQueryEncoding(displayLocation.search)
      ? null
      : new URLSearchParams(displayLocation.search).get("state");
  const api = useMemo(() => new PublicApi(), []);
  const mobileViewport = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  const [site, setSite] = useState(fallbackSite);
  const draftScope = accountCacheScope(
    site.instanceId,
    auth.account?.userId ?? null,
  );
  const [siteLoading, setSiteLoading] = useState(true);
  const [siteError, setSiteError] = useState<unknown>(null);
  const [siteAttempt, setSiteAttempt] = useState(0);
  const [homeVisited, setHomeVisited] = useState(route.id === "home");
  const initialHomeSearch = useRef(
    route.id === "home" ? displayLocation.search : "",
  );
  // The drafts list lives in the address, so a reload comes back to it.
  const draftsOpen =
    overlayCompose && isComposeDraftsPath(contentLocation.pathname);
  const overlayComposer = useRef<ComposerHandle>(null);
  const composeDialogRef = useRef<HTMLDivElement>(null);
  const draftsBackRef = useRef<HTMLButtonElement>(null);
  const draftsTriggerRef = useRef<HTMLButtonElement>(null);
  const [composeExitOpen, setComposeExitOpen] = useState(false);
  // Selection is a mode inside the drafts list, so it stays out of the URL.
  const [draftsEditing, setDraftsEditing] = useState(false);
  const [keptDrafts, setKeptDrafts] = useState(0);
  const siteIdentityReady = site.profileUserId !== "";
  const siteRecovering = useReadRecovery({
    error: siteError,
    loading: siteLoading,
    onRetry: () => setSiteAttempt((value) => value + 1),
  });
  const canPublish =
    auth.state === "signed-in" && auth.account?.status === "active";
  // Retain the expired account's editing surfaces behind reauthentication so
  // the new session can resume the same buffers.
  const signedIn = auth.state === "signed-in" || auth.state === "expired";
  const publishingAvailable = signedIn && auth.account?.status === "active";

  useDocumentScrollLock(
    !authRoute && ((overlayCompose && siteIdentityReady) || composeExitOpen),
  );

  useEffect(() => {
    if (auth.state !== "expired") {
      expiryRedirected.current = false;
      return;
    }
    if (authRoute || expiryRedirected.current) return;
    expiryRedirected.current = true;
    setReauthLocation({
      pathname: location.pathname,
      search: location.search,
      overlayFrom: location.overlayFrom,
    });
    location.replace(
      `/login?return=${encodeURIComponent(`${location.pathname}${location.search}`)}`,
    );
  }, [location, authRoute, auth.state]);

  useEffect(() => {
    if (!reauthLocation || authRoute) return;
    if (auth.state === "expired") {
      if (
        location.pathname !== reauthLocation.pathname ||
        location.search !== reauthLocation.search
      )
        setReauthLocation(null);
      return;
    }
    if (location.pathname === reauthLocation.pathname) {
      location.replace(
        `${reauthLocation.pathname}${reauthLocation.search}`,
        reauthLocation.overlayFrom,
      );
    }
    setReauthLocation(null);
  }, [location, authRoute, auth.state, reauthLocation]);

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

  const openDrafts = () => {
    setDraftsEditing(false);
    // Keeps whatever is behind the dialog behind it, rather than falling
    // back to the timeline because this entry has no background of its own.
    location.navigateOverlay(COMPOSE_DRAFTS_PATH, location.overlayFrom);
  };

  const closeDrafts = () => {
    setDraftsEditing(false);
    location.goBack(COMPOSE_PATH);
  };

  // The count on the header action, read whenever the dialog opens or the
  // list closes -- the two moments it can have changed.
  useEffect(() => {
    if (!overlayCompose) return;
    setKeptDrafts(listKeptDrafts(draftScope).length);
  }, [draftsOpen, draftsEditing, overlayCompose, draftScope]);

  useEffect(() => {
    if (!draftsOpen) setDraftsEditing(false);
  }, [draftsOpen]);

  // Keep focus in the dialog when switching between the editor and drafts.
  const draftsWereOpen = useRef(false);
  useEffect(() => {
    if (!overlayCompose) {
      draftsWereOpen.current = false;
      return;
    }
    if (draftsOpen === draftsWereOpen.current) return;
    draftsWereOpen.current = draftsOpen;
    composeDialogRef.current?.focus({ preventScroll: true });
  }, [draftsOpen, overlayCompose]);

  useEffect(() => {
    const controller = new AbortController();
    setSiteLoading(true);
    setSiteError(null);
    void api
      .site(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setSite(next);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setSiteError(error);
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
      await auth.logout(site.instanceId);
    } catch {
      auth.forgetSession(site.instanceId);
    }
    location.navigate("/");
  }, [location, auth, site.instanceId]);

  useEffect(() => {
    if (authRoute) return;
    const pathname = displayLocation.pathname;
    if (requestedRoute.id === "profile-edit") return;
    if (pathname !== "/profile" && pathname !== "/profile/") return;
    // Still resolving, or unable to say. Neither is an answer to act on.
    if (auth.state === "checking" || auth.state === "unavailable") return;
    const own = auth.account?.profileUrl;
    // A session handed over by a sibling tab arrives before the account behind
    // it does, so "signed in but nobody yet" is a third state and not a guest.
    // Treating it as one sent a fully signed-in reader to the sign-in page.
    if (own === undefined && auth.state !== "guest") return;
    // In place, not pushed. Pushed, the history read home → /profile →
    // sign-in, and every way back from sign-in re-entered /profile, which
    // pushed sign-in again: the arrow on that page went nowhere, and the
    // shell flashed on each bounce.
    location.replace(own ?? "/login?return=%2Fprofile");
  }, [
    displayLocation.pathname,
    requestedRoute.id,
    location,
    authRoute,
    auth.account,
    auth.state,
  ]);

  // Posting needs a session, and the place to get one is the sign-in page --
  // not a login form folded into the compose dialog, which asked a visitor to
  // sign in inside a surface they had no business seeing yet.
  useEffect(() => {
    if (authRoute || auth.state === "expired") return;
    const composing =
      requestedRoute.id === "compose" ||
      requestedRoute.id === "article-workspace" ||
      requestedRoute.id === "profile-edit";
    if (!composing || publishingAvailable) return;
    // Still resolving, or unable to say. Neither is an answer to act on.
    if (auth.state === "checking" || auth.state === "unavailable") return;
    if (auth.session !== null) return;
    location.replace(`/login?return=${encodeURIComponent(location.pathname)}`);
  }, [
    location,
    authRoute,
    auth.account,
    auth.state,
    publishingAvailable,
    requestedRoute.id,
  ]);

  useEffect(() => {
    const siteTitle = site.title.trim() || "Moment";
    const titleRoute = authRoute
      ? resolveRoute(location.pathname)
      : overlayCompose
        ? requestedRoute
        : route;
    document.title =
      titleRoute.id === "home"
        ? siteTitle
        : `${titleRoute.title} · ${siteTitle}`;
  }, [
    location.pathname,
    authRoute,
    overlayCompose,
    requestedRoute,
    route,
    site.title,
  ]);

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
        !canPublish ||
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
        location.navigateOverlay(COMPOSE_PATH);
      } else {
        location.navigate(COMPOSE_PATH);
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
  }, [location, canPublish, siteIdentityReady]);

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
    route.id !== "comment-detail" &&
    route.id !== "article-focus" &&
    route.id !== "login" &&
    route.id !== "register" &&
    route.id !== "compose" &&
    route.id !== "article-workspace" &&
    route.id !== "edit" &&
    route.id !== "settings" &&
    route.id !== "search" &&
    route.id !== "archive" &&
    route.id !== "profile";
  const requestedProfileUsername = profileUsername(displayLocation.pathname);
  const ownProfile =
    requestedProfileUsername === null ||
    requestedProfileUsername === site.username;
  const profileRedirecting =
    requestedProfileUsername === null && auth.state !== "unavailable";

  // Sign-in is the whole page, not a panel inside the shell. Every hook above
  // has already run, so returning here changes what is rendered without
  // changing what is evaluated.
  if (authRoute && !reauthLocation)
    return authPage === "login" ? <LoginPage site={site} /> : <RegisterPage />;

  const readingSurface = (
    <MentionProvider identities={siteIdentityReady ? [site] : []}>
      <AppShell
        account={auth.account}
        onSignOut={() => void signOutFromRail()}
        accountSettingsAvailable={siteIdentityReady && signedIn}
        profileHref={auth.account?.profileUrl ?? undefined}
        route={route}
        site={site}
        state={state}
      >
        {siteError ? (
          <div className={styles.siteError} role="status">
            <span>
              {siteRecovering
                ? "正在自动重新获取站点资料…"
                : "站点资料暂时无法获取。"}
            </span>
          </div>
        ) : null}
        {hasTimeline ? (
          <div hidden={route.id !== "home"}>
            <SuggestedPeople
              api={api}
              kind="home"
              active={siteIdentityReady && route.id === "home"}
            />
            {siteIdentityReady && publishingAvailable ? (
              <div className={styles.homeComposer} hidden={mobileViewport}>
                <Composer
                  key={auth.account?.userId ?? "guest"}
                  active={route.id === "home"}
                  site={site}
                  variant="inline"
                />
              </div>
            ) : null}
            <TimelinePage
              key={auth.account?.userId ?? "guest"}
              api={api}
              initialSearch={initialHomeSearch.current}
              onSiteChanged={setSite}
              site={site}
              canPublish={siteIdentityReady && canPublish}
            />
          </div>
        ) : null}
        {discussionRouteActive && discussionIdentity.confirming ? (
          <div className={styles.siteError} role="status">
            <span>
              {auth.state === "unavailable"
                ? "暂时无法确认登录身份。"
                : "正在确认登录身份…"}
            </span>
            {auth.state === "unavailable" ? (
              <button type="button" onClick={auth.retryStatus}>
                重试
              </button>
            ) : null}
          </div>
        ) : null}
        {discussionLocation ? (
          <div data-detail-content hidden={!discussionActive}>
            <DetailPage
              key={
                (site.instanceId ?? "") +
                ":" +
                (discussionIdentity.accountKey ?? "guest") +
                ":" +
                (articleFocusIdFromPath(discussionLocation.pathname) ??
                  detailId(discussionLocation.pathname))
              }
              api={api}
              id={
                articleFocusIdFromPath(discussionLocation.pathname) ??
                detailId(discussionLocation.pathname)
              }
              active={discussionActive}
              commentId={
                commentDetailFromPath(discussionLocation.pathname)?.commentId ??
                null
              }

              focusReading={
                resolveRoute(discussionLocation.pathname).id === "article-focus"
              }
              canPublish={siteIdentityReady && canPublish}
              onSiteChanged={setSite}
              site={site}
            />
          </div>
        ) : null}
        {route.id === "search" ? (
          <SearchPage
            api={api}
            recentScope={recentSearchScope(
              site.instanceId,
              auth.account?.userId ?? null,
            )}
            key={`${auth.account?.userId ?? "guest"}:${routeSearchKey(displayLocation.search)}`}
            onSiteChanged={setSite}
            canPublish={siteIdentityReady && canPublish}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {route.id === "archive" ? (
          // No key: the page follows its own search string, so a day picked on
          // the calendar moves the list without throwing the calendar away.
          <ArchivePage
            key={auth.account?.userId ?? "guest"}
            api={api}
            canPublish={siteIdentityReady && canPublish}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {siteIdentityReady &&
        route.id === "profile" &&
        ownProfile &&
        !profileRedirecting ? (
          <ProfilePage api={api} onSiteSaved={setSite} site={site} />
        ) : null}
        {siteIdentityReady &&
        route.id === "profile" &&
        !ownProfile &&
        requestedProfileUsername ? (
          <MemberProfilePage
            api={api}
            username={requestedProfileUsername}
            site={site}
          />
        ) : null}
        {siteIdentityReady && route.id === "compose" ? (
          publishingAvailable ? (
            <Composer
              key={auth.account?.userId ?? "guest"}
              // The compose screen has no second face, so the list address
              // opens its panel instead.
              draftsListStart={isComposeDraftsPath(displayLocation.pathname)}
              quotedPostId={quotedPostId}
              quotedCommentId={quotedCommentId}
              quotedCommentPostId={quotedCommentPostId}
              site={site}
              variant="page"
            />
          ) : (
            <CredentialLogin embedded site={site} />
          )
        ) : null}
        {siteIdentityReady && route.id === "article-workspace" ? (
          auth.account?.status === "active" && signedIn ? (
            <Suspense
              fallback={
                <p className={styles.editorLoading} role="status">
                  正在打开文章…
                </p>
              }
            >
              <ArticleWorkspacePage key={auth.account.userId} site={site} />
            </Suspense>
          ) : (
            <p role="status">
              {auth.account
                ? "账号获准发布后即可管理文章。"
                : "请先登录以管理自己的文章。"}
            </p>
          )
        ) : null}
        {siteIdentityReady && route.id === "edit" ? (
          publishingAvailable ? (
            <EditPage
              key={auth.account?.userId ?? "guest"}
              api={api}
              id={editId(displayLocation.pathname)}
              site={site}
            />
          ) : (
            <CredentialLogin embedded site={site} />
          )
        ) : null}
        {siteIdentityReady && route.id === "settings" ? (
          // Any signed-in account may open this page. What it shows them is the
          // page's own decision, section by section.
          signedIn ? (
            <SettingsPage onSaved={setSite} site={site} />
          ) : (
            <CredentialLogin embedded site={site} />
          )
        ) : null}
        {!siteLoading && placeholderRoute ? (
          <ShellState route={route} state={state} />
        ) : null}
      </AppShell>
      {overlayCompose && siteIdentityReady ? (
        <Dialog.Root
          onOpenChange={(open, details) => {
            if (open) return;
            if (
              details.reason === "escape-key" &&
              details.event.target instanceof Element &&
              details.event.target.closest("[data-map-place][data-dragging]")
            ) {
              details.cancel();
              details.allowPropagation();
              return;
            }
            // Escape and a press on the scrim mean the same thing the back
            // arrow does while the list is up: go back to what was being
            // written, not out of the composer.
            if (draftsOpen) {
              closeDrafts();
              return;
            }
            requestOverlayComposerClose();
          }}
          open={!authRoute}
        >
          <Dialog.Portal keepMounted>
            <Dialog.Backdrop
              className={styles.composeBackdrop}
              style={authRoute ? { display: "none" } : undefined}
            />
            <Dialog.Viewport
              className={styles.composeViewport}
              style={authRoute ? { display: "none" } : undefined}
            >
              <Dialog.Popup
                className={styles.composeDialog}
                onKeyDown={(event) => {
                  // Sorting arrows belong to dnd-kit's document listener.
                  if (
                    (event.key === "ArrowUp" || event.key === "ArrowDown") &&
                    event.target instanceof Element &&
                    event.target.closest("[data-map-place][data-dragging]")
                  )
                    event.preventBaseUIHandler();
                }}
                initialFocus={() =>
                  composeDialogRef.current?.querySelector<HTMLElement>(
                    'textarea, [contenteditable="true"]',
                  ) ?? composeDialogRef.current
                }
                ref={composeDialogRef}
              >
                {publishingAvailable ? (
                  <header className={styles.composeDialogHeader}>
                    {/* Keyed, so turning the dialog over replaces these
                        buttons rather than reusing one node and swapping its
                        class. Reuse let `transition: background-color` run
                        between the two: measured, the pill carried 编辑's
                        white label on 草稿's transparent ground for 200ms on
                        the way in, and 草稿's blue label on 编辑's black pill
                        on the way back. */}
                    {draftsOpen ? (
                      <button
                        aria-label="返回发布"
                        className={styles.composeDialogIcon}
                        key="drafts-back"
                        onClick={closeDrafts}
                        ref={draftsBackRef}
                        type="button"
                      >
                        <Icon name="back" />
                      </button>
                    ) : (
                      <button
                        aria-label="关闭发布"
                        className={styles.composeDialogIcon}
                        key="compose-close"
                        onClick={requestOverlayComposerClose}
                        type="button"
                      >
                        <Icon name="close" />
                      </button>
                    )}
                    {/* Show the title in the drafts list and retain an
                        accessible dialog name while composing. */}
                    <Dialog.Title
                      className={draftsOpen ? undefined : styles.srOnly}
                    >
                      {draftsOpen ? "草稿" : "发布 Moment"}
                    </Dialog.Title>
                    {draftsOpen ? (
                      keptDrafts > 0 ? (
                        <button
                          className={styles.composeDialogEdit}
                          key="drafts-edit"
                          onClick={() => setDraftsEditing((on) => !on)}
                          type="button"
                        >
                          {draftsEditing ? "完成" : "编辑"}
                        </button>
                      ) : null
                    ) : (
                      <button
                        className={styles.composeDialogDrafts}
                        key="compose-drafts"
                        onClick={openDrafts}
                        ref={draftsTriggerRef}
                        type="button"
                      >
                        草稿
                        {keptDrafts > 0 ? <small>{keptDrafts}</small> : null}
                      </button>
                    )}
                  </header>
                ) : (
                  <Dialog.Title className={styles.srOnly}>
                    发布 Moment
                  </Dialog.Title>
                )}
                {!publishingAvailable ? (
                  <CredentialLogin embedded site={site} />
                ) : draftsOpen ? (
                  <ComposeDrafts
                    editing={draftsEditing}
                    instanceId={draftScope}
                    onEditingChange={setDraftsEditing}
                    onOpenDraft={(draft) => {
                      overlayComposer.current?.loadDraft(
                        draft.contentMd,
                        draft.id,
                        draft.mapCard,
                        draft.quote,
                      );
                      closeDrafts();
                    }}
                  />
                ) : null}
                {/* The composer stays mounted behind the list: it holds the
                    text, the media and the upload in flight, and a draft
                    opened from the list is handed to it through its own
                    ref. */}
                <div hidden={draftsOpen}>
                  {publishingAvailable ? (
                    <Composer
                      key={auth.account?.userId ?? "guest"}
                      // The header above already carries it.
                      draftsEntry={false}
                      quotedPostId={quotedPostId}
                      quotedCommentId={quotedCommentId}
                      quotedCommentPostId={quotedCommentPostId}
                      ref={overlayComposer}
                      site={site}
                      variant="page"
                    />
                  ) : null}
                </div>
              </Dialog.Popup>
            </Dialog.Viewport>
          </Dialog.Portal>
        </Dialog.Root>
      ) : null}
      {/* An expired session cannot write a profile, and a prompt whose only
          two buttons are both refused is a trap; it comes back with the next
          session, which is the point at which it can be answered. */}
      {auth.session && auth.account?.usernameChosen === false ? (
        <UsernameSetup
          account={auth.account}
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

  return (
    <>
      {authRoute ? (
        authPage === "login" ? (
          <LoginPage site={site} />
        ) : (
          <RegisterPage />
        )
      ) : null}
      <div
        hidden={authRoute}
        inert={authRoute}
        style={authRoute ? undefined : { display: "contents" }}
      >
        <MediaViewerProvider
          scope={accountCacheScope(
            site.instanceId,
            discussionIdentity.accountKey,
          )}
          api={api}
          site={site}
          enabled={!authRoute && siteIdentityReady}
          onSiteChanged={setSite}
        >
          {readingSurface}
        </MediaViewerProvider>
      </div>
    </>
  );
}

export function App() {
  useEscapeFocus();
  return (
    <ThemeProvider>
      <ReadingLayoutProvider>
        <FeedbackProvider>
          <NavigationProvider>
            <SessionProvider>
              <PublicApp />
            </SessionProvider>
          </NavigationProvider>
        </FeedbackProvider>
      </ReadingLayoutProvider>
    </ThemeProvider>
  );
}
