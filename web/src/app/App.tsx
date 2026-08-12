import { Dialog } from "@base-ui/react/dialog";
import { useEffect, useMemo, useRef, useState } from "react";

import { PublicApi, type SiteIdentity } from "../api/public";
import { AppShell } from "../components/AppShell";
import { ShellState } from "../components/ShellState";
import { DetailPage } from "../features/timeline/DetailPage";
import { TimelineLoading } from "../features/timeline/PublicStates";
import { TimelinePage } from "../features/timeline/TimelinePage";
import { Composer } from "../features/owner/Composer";
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
import { NavigationProvider, useNavigation } from "./navigation";
import { resolveRoute } from "./routes";
import styles from "./App.module.css";

const fallbackSite: SiteIdentity = {
  title: "Moment",
  bio: "",
  timezone: "UTC",
  publicBaseUrl: null,
  avatarMediaId: null,
  coverMediaId: null,
  pinnedPostId: null,
  commentsEnabled: true,
  instanceId: null,
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
  const route = resolveRoute(displayLocation.pathname);
  const state =
    route.id === "search" ||
    route.id === "archive" ||
    !hasValidRawQueryEncoding(displayLocation.search)
      ? null
      : new URLSearchParams(displayLocation.search).get("state");
  const api = useMemo(() => new PublicApi(), []);
  const owner = useOwner();
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
  }, [location, owner.state]);

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

  const shellYears =
    route.id === "search" || route.id === "archive"
      ? discoveryYears
      : memoryYears;

  return (
    <>
      <AppShell
        memoryYears={shellYears}
        ownerSettingsAvailable={
          owner.state === "owner" || owner.state === "expired"
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
        {siteLoading ? <TimelineLoading /> : null}
        {!siteLoading && hasTimeline ? (
          <div hidden={route.id !== "home"}>
            {owner.state === "owner" || owner.state === "expired" ? (
              <Composer
                active={route.id === "home"}
                site={site}
                variant="inline"
              />
            ) : null}
            <TimelinePage
              api={api}
              initialSearch={initialHomeSearch.current}
              onYearsChange={setMemoryYears}
              onSiteChanged={setSite}
              site={site}
              owner={owner.state === "owner"}
            />
          </div>
        ) : null}
        {!siteLoading && route.id === "detail" ? (
          <DetailPage
            api={api}
            id={detailId(displayLocation.pathname)}
            owner={owner.state === "owner"}
            onSiteChanged={setSite}
            site={site}
          />
        ) : null}
        {!siteLoading && route.id === "search" ? (
          <SearchPage
            api={api}
            key={routeSearchKey(displayLocation.search)}
            onYearsChange={setDiscoveryYears}
            onSiteChanged={setSite}
            owner={owner.state === "owner"}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {!siteLoading && route.id === "archive" ? (
          <ArchivePage
            api={api}
            key={routeSearchKey(displayLocation.search)}
            onYearsChange={setDiscoveryYears}
            owner={owner.state === "owner"}
            search={displayLocation.search}
            site={site}
          />
        ) : null}
        {!siteLoading && route.id === "profile" ? (
          <ProfilePage api={api} onSiteSaved={setSite} site={site} />
        ) : null}
        {!siteLoading && route.id === "owner-login" ? (
          <OwnerLogin site={site} />
        ) : null}
        {!siteLoading && route.id === "owner-compose" ? (
          owner.state === "owner" || owner.state === "expired" ? (
            <Composer site={site} variant="page" />
          ) : (
            <OwnerLogin embedded site={site} />
          )
        ) : null}
        {!siteLoading && route.id === "owner-edit" ? (
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
        {!siteLoading && route.id === "owner-settings" ? (
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
      {overlayCompose ? (
        <Dialog.Root
          onOpenChange={(open) => {
            if (!open) {
              location.goBack(
                `${displayLocation.pathname}${displayLocation.search}`,
              );
            }
          }}
          open
        >
          <Dialog.Portal>
            <Dialog.Backdrop className={styles.composeBackdrop} />
            <Dialog.Viewport className={styles.composeViewport}>
              <Dialog.Popup className={styles.composeDialog}>
                <header className={styles.composeDialogHeader}>
                  <Dialog.Close aria-label="关闭发布窗口">×</Dialog.Close>
                  <Dialog.Title>发布 Moment</Dialog.Title>
                  <span />
                </header>
                {owner.state === "owner" || owner.state === "expired" ? (
                  <Composer site={site} variant="page" />
                ) : (
                  <OwnerLogin embedded site={site} />
                )}
              </Dialog.Popup>
            </Dialog.Viewport>
          </Dialog.Portal>
        </Dialog.Root>
      ) : null}
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
