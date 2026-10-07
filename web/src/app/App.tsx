import { Dialog } from "@base-ui/react/dialog";
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
import { Icon } from "../components/icons";
import { ShellState } from "../components/ShellState";
import { ArticleWorkspacePage } from "../features/article/editor";
import { DetailPage } from "../features/timeline/DetailPage";
import { TimelinePage } from "../features/timeline/TimelinePage";
import { Composer, type ComposerHandle } from "../features/owner/Composer";
import { ComposeDrafts } from "../features/owner/ComposeDrafts";
import { listKeptDrafts } from "../features/owner/drafts";
import { EditPage } from "../features/owner/EditPage";
import { LoginPage } from "../features/owner/LoginPage";
import { MentionProvider } from "./mentions";
import { OwnerLogin } from "../features/owner/OwnerLogin";
import {
  SessionProvider,
  ownsSite,
  useSession,
} from "../features/auth/SessionProvider";
import { SettingsPage } from "../features/owner/SettingsPage";
import { UsernameSetup } from "../features/owner/UsernameSetup";
import { ArchivePage } from "../features/memory/ArchivePage";
import { hasValidRawQueryEncoding } from "../features/memory/memoryState";
import { SearchPage } from "../features/memory/SearchPage";
import { MemberProfilePage } from "../features/profile/MemberProfilePage";
import { ProfilePage } from "../features/profile/ProfilePage";
import { ThemeProvider } from "../theme/ThemeProvider";
import { ReadingLayoutProvider } from "../features/article/ReadingLayout";
import { FeedbackProvider } from "../components/Feedback";
import { useDocumentScrollLock } from "../components/useDocumentScrollLock";
import {
  COMPOSE_DIALOG_VIEWPORT_QUERY,
  MOBILE_VIEWPORT_QUERY,
  useMediaQuery,
} from "../components/useMediaQuery";
import { NavigationProvider, useNavigation } from "./navigation";
import {
  COMPOSE_DRAFTS_PATH,
  COMPOSE_PATH,
  movedArticleComposePath,
  articleFocusIdFromPath,
  isComposeDraftsPath,
  movedComposePath,
  profileFromPath,
  resolveRoute,
} from "./routes";
import styles from "./App.module.css";

/** What sits behind the compose dialog when its address was loaded cold. */
const HOME_LOCATION = { pathname: "/", search: "" };

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
    Array.from(params.entries()).filter(([name]) => name !== "loaded_through"),
  );
}

function PublicApp() {
  const location = useNavigation();
  const requestedRoute = resolveRoute(location.pathname);
  // Composing is a dialog wherever the page has room for one, whether it was
  // opened from the timeline or the address was loaded cold. Reloading it used
  // to drop to a bare compose screen with nothing behind it; the reference
  // keeps the dialog over the timeline, and so does this.
  const composeDialogViewport = useMediaQuery(COMPOSE_DIALOG_VIEWPORT_QUERY);
  const overlayCompose =
    requestedRoute.id === "owner-compose" && composeDialogViewport;
  const displayLocation = overlayCompose
    ? (location.overlayFrom ?? HOME_LOCATION)
    : location;
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
  const owner = useSession();
  const mobileViewport = useMediaQuery(MOBILE_VIEWPORT_QUERY);
  const [site, setSite] = useState(fallbackSite);
  const [siteLoading, setSiteLoading] = useState(true);
  const [siteError, setSiteError] = useState(false);
  const [siteAttempt, setSiteAttempt] = useState(0);
  const [homeVisited, setHomeVisited] = useState(route.id === "home");
  const initialHomeSearch = useRef(
    route.id === "home" ? displayLocation.search : "",
  );
  // The drafts list lives in the address, so a reload comes back to it.
  const draftsOpen = overlayCompose && isComposeDraftsPath(location.pathname);
  const overlayComposer = useRef<ComposerHandle>(null);
  const composeDialogRef = useRef<HTMLDivElement>(null);
  const draftsBackRef = useRef<HTMLButtonElement>(null);
  const draftsTriggerRef = useRef<HTMLButtonElement>(null);
  const [composeExitOpen, setComposeExitOpen] = useState(false);
  // Selection is a mode inside the drafts list, so it stays out of the URL.
  const [draftsEditing, setDraftsEditing] = useState(false);
  const [keptDrafts, setKeptDrafts] = useState(0);
  const siteIdentityReady = !siteLoading && !siteError;
  // Accounts arrived, so being signed in stopped meaning owning the site.
  // Every owner-only surface asks the account instead of the session state. A
  // session with no account behind it -- an instance that cannot answer for
  // accounts, or a lookup that failed -- keeps the pre-accounts answer, so the
  // single owner such an instance has is never locked out of it.
  const siteOwner = ownsSite(owner.account);
  const ownerSession = owner.state === "signed-in" && siteOwner;
  // An expired session still renders the pages behind it; they carry their own
  // inline re-login rather than throwing the reader back to a login screen.
  const signedIn = owner.state === "signed-in" || owner.state === "expired";
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
    setKeptDrafts(listKeptDrafts(site.instanceId).length);
  }, [draftsOpen, draftsEditing, overlayCompose, site.instanceId]);

  useEffect(() => {
    if (!draftsOpen) setDraftsEditing(false);
  }, [draftsOpen]);

  // Turning the dialog over takes the pressed control with it, and focus
  // with nowhere to go falls back to the popup -- which then wears a ring
  // around the whole dialog. It lands on the other face's way back instead.
  const draftsWereOpen = useRef(false);
  useEffect(() => {
    if (!overlayCompose) {
      draftsWereOpen.current = false;
      return;
    }
    if (draftsOpen === draftsWereOpen.current) return;
    draftsWereOpen.current = draftsOpen;
    (draftsOpen ? draftsBackRef : draftsTriggerRef).current?.focus();
  }, [draftsOpen, overlayCompose]);

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
    // In place, not pushed. Pushed, the history read home → /profile →
    // sign-in, and every way back from sign-in re-entered /profile, which
    // pushed sign-in again: the arrow on that page went nowhere, and the
    // shell flashed on each bounce.
    location.replace(own ?? "/login?return=%2Fprofile");
  }, [displayLocation.pathname, location, owner.account, owner.state]);

  // Composing moved out from under /owner. An entry already in somebody's
  // history renders as it did and has its address corrected in place, rather
  // than turning into a not-found page.
  useEffect(() => {
    const moved =
      movedArticleComposePath(location.pathname) ??
      movedComposePath(location.pathname);
    if (moved) location.replace(`${moved}${location.search}`);
  }, [location]);

  // Posting needs a session, and the place to get one is the sign-in page --
  // not a login form folded into the compose dialog, which asked a visitor to
  // sign in inside a surface they had no business seeing yet.
  useEffect(() => {
    const composing =
      requestedRoute.id === "owner-compose" ||
      requestedRoute.id === "article-workspace";
    if (!composing || ownerCapable) return;
    // Still resolving, or unable to say. Neither is an answer to act on.
    if (owner.state === "checking" || owner.state === "unavailable") return;
    // A session that exists but has not been answered for yet is not a
    // guest. Signing in sets it a tick before the account arrives, and
    // treating that tick as "no session" sent the owner straight back to
    // the page they had just signed in on.
    if (owner.session !== null) return;
    location.replace(`/login?return=${encodeURIComponent(location.pathname)}`);
  }, [location, owner.account, owner.state, ownerCapable, requestedRoute.id]);

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
    route.id !== "article-focus" &&
    route.id !== "login" &&
    route.id !== "owner-compose" &&
    route.id !== "article-workspace" &&
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
  // Bare `/profile` is only ever passed through -- to the account's own
  // handle or to sign-in, once the session is known -- so nothing is drawn
  // there in the meantime: the site owner's page appearing for a frame before
  // the redirect was the flash a visitor saw. The one instance that cannot
  // answer for accounts keeps the pre-accounts page.
  const profileRedirecting =
    requestedProfileUsername === null && owner.state !== "unavailable";

  // Sign-in is the whole page, not a panel inside the shell. Every hook above
  // has already run, so returning here changes what is rendered without
  // changing what is evaluated.
  if (route.id === "login") return <LoginPage site={site} />;

  return (
    // The owner's handle is the one this app knows without asking. Anything
    // else a body mentions is resolved against the server before it becomes a
    // link, because a mention is text somebody typed and not a reference
    // anything checked.
    <MentionProvider identities={siteIdentityReady ? [site] : []}>
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
        {route.id === "detail" || route.id === "article-focus" ? (
          <div data-detail-content>
            <DetailPage
              api={api}
              id={
                articleFocusIdFromPath(displayLocation.pathname) ??
                detailId(displayLocation.pathname)
              }
              focusReading={route.id === "article-focus"}
              owner={siteIdentityReady && ownerSession}
              onSiteChanged={setSite}
              site={site}
            />
          </div>
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
          <MemberProfilePage api={api} username={requestedProfileUsername} />
        ) : null}
        {siteIdentityReady && route.id === "owner-compose" ? (
          ownerCapable ? (
            <Composer
              // The compose screen has no second face, so the list address
              // opens its panel instead.
              draftsListStart={isComposeDraftsPath(displayLocation.pathname)}
              quotedPostId={quotedPostId}
              site={site}
              variant="page"
            />
          ) : (
            <OwnerLogin embedded site={site} />
          )
        ) : null}
        {siteIdentityReady && route.id === "article-workspace" ? (
          owner.account?.status === "active" && owner.state === "signed-in" ? (
            <Suspense
              fallback={
                <p className={styles.editorLoading} role="status">
                  正在打开文章…
                </p>
              }
            >
              <ArticleWorkspacePage key={owner.account.userId} site={site} />
            </Suspense>
          ) : (
            <p role="status">
              {owner.account
                ? "账号获准发布后即可管理文章。"
                : "请先登录以管理自己的文章。"}
            </p>
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
            if (open) return;
            // Escape and a press on the scrim mean the same thing the back
            // arrow does while the list is up: go back to what was being
            // written, not out of the composer.
            if (draftsOpen) {
              closeDrafts();
              return;
            }
            requestOverlayComposerClose();
          }}
          open
        >
          <Dialog.Portal>
            <Dialog.Backdrop className={styles.composeBackdrop} />
            <Dialog.Viewport className={styles.composeViewport}>
              <Dialog.Popup
                className={styles.composeDialog}
                // Focus the textarea so the owner can write immediately.
                // Base UI otherwise focuses the first tabbable control.
                initialFocus={() =>
                  composeDialogRef.current?.querySelector("textarea") ??
                  composeDialogRef.current
                }
                ref={composeDialogRef}
              >
                {ownerCapable ? (
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
                {!ownerCapable ? (
                  <OwnerLogin embedded site={site} />
                ) : draftsOpen ? (
                  <ComposeDrafts
                    editing={draftsEditing}
                    instanceId={site.instanceId}
                    onEditingChange={setDraftsEditing}
                    onOpenDraft={(draft) => {
                      overlayComposer.current?.loadDraft(
                        draft.contentMd,
                        draft.id,
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
                  {ownerCapable ? (
                    <Composer
                      // The header above already carries it.
                      draftsEntry={false}
                      quotedPostId={quotedPostId}
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
