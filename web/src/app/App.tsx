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
import { ThemeProvider } from "../theme/ThemeProvider";
import { NavigationProvider, useNavigation } from "./navigation";
import { resolveRoute } from "./routes";
import styles from "./App.module.css";

const fallbackSite: SiteIdentity = {
  title: "Moment",
  bio: "",
  timezone: "UTC",
  publicBaseUrl: null,
  avatarMediaId: null,
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

function PublicApp() {
  const location = useNavigation();
  const route = resolveRoute(location.pathname);
  const state = new URLSearchParams(location.search).get("state");
  const api = useMemo(() => new PublicApi(), []);
  const owner = useOwner();
  const [site, setSite] = useState(fallbackSite);
  const [siteLoading, setSiteLoading] = useState(true);
  const [siteError, setSiteError] = useState(false);
  const [siteAttempt, setSiteAttempt] = useState(0);
  const [memoryYears, setMemoryYears] = useState<number[]>([]);
  const [homeVisited, setHomeVisited] = useState(route.id === "home");
  const initialHomeSearch = useRef(route.id === "home" ? location.search : "");

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
      if (!homeVisited) initialHomeSearch.current = location.search;
    }
  }, [homeVisited, location.search, route.id]);

  useEffect(() => {
    const siteTitle = site.title.trim() || "Moment";
    document.title =
      route.id === "home" ? siteTitle : `${route.title} · ${siteTitle}`;
  }, [route.id, route.title, site.title]);

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
        document.querySelector('[role="dialog"]')
      ) {
        return;
      }
      event.preventDefault();
      location.navigate("/owner/compose");
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

  const hasTimeline = route.id === "home" || homeVisited;
  const placeholderRoute =
    route.id !== "home" &&
    route.id !== "detail" &&
    route.id !== "owner-login" &&
    route.id !== "owner-compose" &&
    route.id !== "owner-edit";

  return (
    <AppShell memoryYears={memoryYears} route={route} site={site} state={state}>
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
            site={site}
            owner={owner.state === "owner"}
          />
        </div>
      ) : null}
      {!siteLoading && route.id === "detail" ? (
        <DetailPage
          api={api}
          id={detailId(location.pathname)}
          owner={owner.state === "owner"}
          site={site}
        />
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
          <EditPage api={api} id={editId(location.pathname)} site={site} />
        ) : (
          <OwnerLogin embedded site={site} />
        )
      ) : null}
      {!siteLoading && placeholderRoute ? (
        <ShellState route={route} state={state} />
      ) : null}
    </AppShell>
  );
}

export function App() {
  return (
    <ThemeProvider>
      <NavigationProvider>
        <OwnerProvider>
          <PublicApp />
        </OwnerProvider>
      </NavigationProvider>
    </ThemeProvider>
  );
}
