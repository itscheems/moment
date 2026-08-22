import { Dialog } from "@base-ui/react/dialog";
import { useEffect, useMemo, useRef, useState } from "react";

import { PublicApi, type SiteIdentity } from "../api/public";
import { AppShell } from "../components/AppShell";
import { ShellState } from "../components/ShellState";
import { DetailPage } from "../features/timeline/DetailPage";
import { TimelinePage } from "../features/timeline/TimelinePage";
import { Composer, type ComposerHandle } from "../features/owner/Composer";
import { EditPage } from "../features/owner/EditPage";
import { OwnerLogin } from "../features/owner/OwnerLogin";
import { OwnerProvider, useOwner } from "../features/owner/OwnerProvider";
import { SettingsPage } from "../features/owner/SettingsPage";
import { ArchivePage } from "../features/memory/ArchivePage";
import { hasValidRawQueryEncoding } from "../features/memory/memoryState";
import { SearchPage } from "../features/memory/SearchPage";
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
  const [memoryYears, setMemoryYears] = useState<number[]>([]);
  const [discoveryYears, setDiscoveryYears] = useState<number[]>([]);
  const [homeVisited, setHomeVisited] = useState(route.id === "home");
  const initialHomeSearch = useRef(
    route.id === "home" ? displayLocation.search : "",
  );
  const overlayComposer = useRef<ComposerHandle>(null);
  const [composeExitOpen, setComposeExitOpen] = useState(false);
  const siteIdentityReady = !siteLoading && !siteError;

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
        owner.state !== "owner" ||
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
  }, [location, owner.state, siteIdentityReady]);

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
    route.id !== "owner-login" &&
    route.id !== "owner-compose" &&
    route.id !== "owner-edit" &&
    route.id !== "owner-settings" &&
    route.id !== "search" &&
    route.id !== "archive" &&
    route.id !== "profile";
  const requestedProfileUsername = profileUsername(displayLocation.pathname);
  const profileExists =
    requestedProfileUsername === null ||
    requestedProfileUsername === site.username;

  const shellYears =
    route.id === "search" || route.id === "archive"
      ? discoveryYears
      : memoryYears;
  return (
    <>
      <AppShell
        memoryYears={shellYears}
        ownerSettingsAvailable={
          siteIdentityReady &&
          (owner.state === "owner" || owner.state === "expired")
        }
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
            {siteIdentityReady &&
            !mobileViewport &&
            (owner.state === "owner" || owner.state === "expired") ? (
              <Composer
                active={route.id === "home"}
                site={site}
                variant="inline"
              />
            ) : null}
            <TimelinePage
              api={api}
              initialSearch={initialHomeSearch.current}
              onYearsChange={siteIdentityReady ? setMemoryYears : undefined}
              onSiteChanged={setSite}
              site={site}
              owner={siteIdentityReady && owner.state === "owner"}
            />
          </div>
        ) : null}
        {route.id === "detail" ? (
          <DetailPage
            api={api}
            id={detailId(displayLocation.pathname)}
            owner={siteIdentityReady && owner.state === "owner"}
            onSiteChanged={setSite}
            site={site}
          />
        ) : null}
        {route.id === "search" ? (
          <SearchPage
            api={api}
            key={routeSearchKey(displayLocation.search)}
            onYearsChange={siteIdentityReady ? setDiscoveryYears : undefined}
            onSiteChanged={setSite}
            owner={siteIdentityReady && owner.state === "owner"}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {route.id === "archive" ? (
          <ArchivePage
            api={api}
            key={routeSearchKey(displayLocation.search)}
            onYearsChange={siteIdentityReady ? setDiscoveryYears : undefined}
            owner={siteIdentityReady && owner.state === "owner"}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {siteIdentityReady && route.id === "profile" && profileExists ? (
          <ProfilePage api={api} onSiteSaved={setSite} site={site} />
        ) : null}
        {siteIdentityReady && route.id === "profile" && !profileExists ? (
          <ShellState
            route={{
              id: "not-found",
              title: "没有这个用户",
              eyebrow: "",
              href: "/",
            }}
            state={null}
          />
        ) : null}
        {siteIdentityReady && route.id === "owner-login" ? (
          <OwnerLogin site={site} />
        ) : null}
        {siteIdentityReady && route.id === "owner-compose" ? (
          owner.state === "owner" || owner.state === "expired" ? (
            <Composer quotedPostId={quotedPostId} site={site} variant="page" />
          ) : (
            <OwnerLogin embedded site={site} />
          )
        ) : null}
        {siteIdentityReady && route.id === "owner-edit" ? (
          owner.state === "owner" || owner.state === "expired" ? (
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
          owner.state === "owner" || owner.state === "expired" ? (
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
                {owner.state === "owner" || owner.state === "expired" ? (
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
    </>
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
