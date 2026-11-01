import {
  createContext,
  type MouseEvent,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type { ProgressName } from "../features/timeline/cursorContinuity";

interface LocationSnapshot {
  pathname: string;
  search: string;
}

interface NavigationLocation extends LocationSnapshot {
  overlayFrom: LocationSnapshot | null;
  panel: {
    key: string;
    from: LocationSnapshot & { overlayFrom: LocationSnapshot | null };
  } | null;
}

interface NavigationContextValue extends NavigationLocation {
  archiveAnchor?: { key: string; top: number };
  navigate: (href: string, options?: { preserveScroll?: boolean }) => void;
  navigateAuth: (href: string) => void;
  /**
   * A redirect: the address corrected in place rather than a page the reader
   * chose. The entry keeps its standing in the history, so one reached from
   * inside the app still goes back inside the app.
   */
  replace: (href: string, overlayFrom?: LocationSnapshot | null) => void;
  /** Synchronizes a position within the mounted page without adding history or scrolling. */
  replaceWithinPage: (href: string) => void;
  /**
   * Opens `href` over what is on screen now, or over `from` when the caller
   * knows better -- a second face of the same overlay keeps the first one's
   * background rather than taking the overlay itself as one.
   */
  navigateOverlay: (href: string, from?: LocationSnapshot | null) => void;
  /** A child editor over the same mounted form, so opening it cannot discard work. */
  openPanel: (href: string, key: string) => void;
  closePanel: (key: string) => void;
  goBack: (fallback: string) => void;
  goBackAuth: (fallback: string) => void;
  replaceTimelineProgress: (cursor: string, name?: ProgressName) => void;
  /**
   * Puts the entry back where the reader left it. A fresh entry -- one just
   * navigated to, or a document opened cold -- has nowhere to go back to, and
   * `placeFresh`, when given, decides where it opens instead of the top.
   */
  restoreCurrentEntry: (placeFresh?: () => void) => boolean;
  setNavigationBlocker: (
    canPublish: symbol,
    blocker: NavigationBlocker | null,
  ) => void;
}

export type NavigationBlocker = (resume: () => void) => boolean;

interface MomentHistoryState {
  momentArchiveAnchor?: { key: string; top: number };
  momentFocusKey?: string;
  /** Navigated to and not yet shown; cleared by the first restoration. */
  momentFresh?: boolean;
  momentInternal?: boolean;
  momentOverlayFrom?: LocationSnapshot;
  momentPanel?: NonNullable<NavigationLocation["panel"]>;
  momentScrollY?: number;
}

const NavigationContext = createContext<NavigationContextValue | null>(null);

function currentLocation(): LocationSnapshot {
  return {
    pathname: window.location.pathname,
    search: window.location.search,
  };
}

function isAuthPagePath(pathname: string): boolean {
  return (
    pathname === "/login" ||
    pathname === "/login/" ||
    pathname === "/register" ||
    pathname === "/register/"
  );
}

function locationSnapshot(
  state: unknown,
  includeOverlay = true,
): NavigationLocation {
  const history =
    typeof state === "object" && state !== null
      ? (state as MomentHistoryState)
      : null;
  const overlay = history?.momentOverlayFrom;
  const panel = history?.momentPanel;
  return {
    ...currentLocation(),
    panel:
      includeOverlay &&
      typeof panel?.key === "string" &&
      typeof panel.from?.pathname === "string" &&
      typeof panel.from.search === "string"
        ? panel
        : null,
    overlayFrom:
      includeOverlay &&
      typeof overlay?.pathname === "string" &&
      typeof overlay.search === "string"
        ? overlay
        : null,
  };
}

function scrollPosition(state: unknown): number {
  if (typeof state !== "object" || state === null) return 0;
  const value = (state as MomentHistoryState).momentScrollY;
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function historyState(): MomentHistoryState {
  return typeof window.history.state === "object" &&
    window.history.state !== null
    ? window.history.state
    : {};
}

function archiveScrollAnchor(): MomentHistoryState["momentArchiveAnchor"] {
  if (window.location.pathname !== "/archive") return undefined;
  const headerBottom =
    document.querySelector("#main-content > header")?.getBoundingClientRect()
      .bottom ?? 0;
  const row = Array.from(
    document.querySelectorAll<HTMLElement>(
      "#main-content [data-timeline-item-key]",
    ),
  ).find((item) => item.getBoundingClientRect().bottom > headerBottom + 1);
  return row
    ? {
        key: row.dataset.timelineItemKey!,
        top: row.getBoundingClientRect().top,
      }
    : undefined;
}

function restoreEntry(state: unknown, restoreFocus = true) {
  window.requestAnimationFrame(() => {
    window.scrollTo({ top: scrollPosition(state), behavior: "auto" });
    const focusKey = (state as MomentHistoryState | null)?.momentFocusKey;
    if (!restoreFocus || !focusKey) return;
    Array.from(document.querySelectorAll<HTMLElement>("[data-focus-key]"))
      .find((element) => element.dataset.focusKey === focusKey)
      ?.focus({ preventScroll: true });
  });
}

export function NavigationProvider({ children }: { children: ReactNode }) {
  const [location, setLocation] = useState(() =>
    locationSnapshot(window.history.state, false),
  );
  const navigationBlockers = useRef(new Map<symbol, NavigationBlocker>());
  const suppressNextPopGuard = useRef(false);

  const canLeave = useCallback((resume: () => void) => {
    const blockers = Array.from(navigationBlockers.current.values()).reverse();
    return blockers.every((blocker) => blocker(resume));
  }, []);

  const setNavigationBlocker = useCallback(
    (canPublish: symbol, blocker: NavigationBlocker | null) => {
      if (blocker) {
        navigationBlockers.current.set(canPublish, blocker);
      } else {
        navigationBlockers.current.delete(canPublish);
      }
    },
    [],
  );

  const commitNavigation = useCallback(
    (
      href: string,
      overlayFrom: LocationSnapshot | null = null,
      preserveScroll = false,
      panel: NavigationLocation["panel"] = null,
    ) => {
      const activeFocusKey =
        document.activeElement instanceof HTMLElement
          ? document.activeElement.closest<HTMLElement>("[data-focus-key]")
              ?.dataset.focusKey
          : undefined;
      window.history.replaceState(
        {
          ...historyState(),
          momentArchiveAnchor: archiveScrollAnchor(),
          momentFocusKey: activeFocusKey,
          momentScrollY: window.scrollY,
        },
        "",
        window.location.href,
      );
      window.history.pushState(
        {
          momentFresh: !preserveScroll,
          momentInternal: true,
          momentOverlayFrom: overlayFrom ?? undefined,
          momentPanel: panel ?? undefined,
          momentScrollY: preserveScroll ? window.scrollY : 0,
        },
        "",
        href,
      );
      setLocation(locationSnapshot(window.history.state));
      if (!overlayFrom && !preserveScroll)
        window.scrollTo({ top: 0, behavior: "auto" });
    },
    [],
  );

  const navigate = useCallback(
    function navigateTo(href: string, options?: { preserveScroll?: boolean }) {
      if (!canLeave(() => navigateTo(href, options))) return;
      commitNavigation(href, null, options?.preserveScroll);
    },
    [canLeave, commitNavigation],
  );

  // The reader has not left the retained editing surface when changing which
  // account entry form is visible. Other destinations still run its blocker.
  const navigateAuth = useCallback(
    (href: string) => {
      const target = new URL(href, window.location.href);
      if (
        isAuthPagePath(window.location.pathname) &&
        target.origin === window.location.origin &&
        isAuthPagePath(target.pathname)
      ) {
        commitNavigation(href);
      } else {
        navigate(href);
      }
    },
    [commitNavigation, navigate],
  );

  // Not guarded by the blockers: a redirect is the app correcting where it
  // is, and nothing on the pages that redirect has work to lose. Pushing
  // here was what made the sign-in page inescapable -- back re-entered
  // /profile, which pushed sign-in again, with the shell flashing in
  // between -- so the entry that asked for the redirect is overwritten and
  // back goes to wherever the reader was before it.
  const replace = useCallback(
    (href: string, overlayFrom?: LocationSnapshot | null) => {
      window.history.replaceState(
        {
          momentFresh: true,
          momentInternal: historyState().momentInternal,
          momentScrollY: 0,
          ...(overlayFrom ? { momentOverlayFrom: overlayFrom } : {}),
        },
        "",
        href,
      );
      setLocation(locationSnapshot(window.history.state));
      window.scrollTo({ top: 0, behavior: "auto" });
    },
    [],
  );

  const replaceWithinPage = useCallback((href: string) => {
    window.history.replaceState(
      { ...historyState(), momentScrollY: window.scrollY },
      "",
      href,
    );
    setLocation(locationSnapshot(window.history.state));
  }, []);

  const navigateOverlay = useCallback(
    function navigateToOverlay(href: string, from?: LocationSnapshot | null) {
      if (!canLeave(() => navigateToOverlay(href, from))) return;
      // `undefined` means "over whatever is on screen"; an explicit `null`
      // means "nothing behind it", and must not fall back to the overlay
      // itself as its own background.
      commitNavigation(href, from === undefined ? currentLocation() : from);
    },
    [canLeave, commitNavigation],
  );

  const openPanel = useCallback(
    (href: string, key: string) => {
      const current = locationSnapshot(window.history.state);
      const from = current.panel?.from ?? {
        pathname: current.pathname,
        search: current.search,
        overlayFrom: current.overlayFrom,
      };
      commitNavigation(href, current.overlayFrom, true, { key, from });
    },
    [commitNavigation],
  );

  const closePanel = useCallback((key: string) => {
    const panel = locationSnapshot(window.history.state).panel;
    if (panel?.key !== key) return;
    suppressNextPopGuard.current = true;
    window.history.back();
  }, []);

  const replaceTimelineProgress = useCallback(
    (cursor: string, name: ProgressName = "loaded_through") => {
      const url = new URL(window.location.href);
      url.searchParams.set(name, cursor);
      window.history.replaceState(
        { ...historyState(), momentScrollY: window.scrollY },
        "",
        `${url.pathname}${url.search}${url.hash}`,
      );
      setLocation(locationSnapshot(window.history.state));
    },
    [],
  );

  const restoreCurrentEntry = useCallback((placeFresh?: () => void) => {
    const state = historyState();
    // A cold document has no state at all; a navigation marks its entry.
    // Either way there is no position to restore, only one to choose.
    const fresh =
      state.momentFresh === true || state.momentScrollY === undefined;
    if (fresh) {
      window.history.replaceState(
        { ...state, momentFresh: false, momentScrollY: 0 },
        "",
        window.location.href,
      );
      if (placeFresh) {
        window.requestAnimationFrame(placeFresh);
        return true;
      }
    }
    // Scroll, but not focus. A load is not a return: no browser puts the
    // ring back on the control you last pressed when you reload, and doing
    // it ourselves painted one on the publish action every refresh
    // (programmatic focus reads as keyboard focus before the document has
    // been pointed at). Going back inside the app still restores it, from
    // the popstate handler.
    restoreEntry(state, false);
    return false;
  }, []);

  const goBack = useCallback(
    function navigateBack(fallback: string) {
      const state = window.history.state as MomentHistoryState | null;
      if (!canLeave(() => navigateBack(fallback))) return;
      if (state?.momentInternal) {
        suppressNextPopGuard.current = true;
        window.history.back();
        return;
      }
      commitNavigation(fallback);
    },
    [canLeave, commitNavigation],
  );

  const goBackAuth = useCallback(
    (fallback: string) => {
      if (!isAuthPagePath(window.location.pathname)) {
        goBack(fallback);
        return;
      }
      // Popstate knows the destination: another auth page keeps the editor,
      // while a real departure still asks its blocker before completing.
      if ((window.history.state as MomentHistoryState | null)?.momentInternal) {
        window.history.back();
      } else {
        goBack(fallback);
      }
    },
    [goBack],
  );

  useEffect(() => {
    let previous = locationSnapshot(window.history.state);
    const onPopState = (event: PopStateEvent) => {
      const next = locationSnapshot(event.state);
      const sameSurface = (a: NavigationLocation, b: NavigationLocation) =>
        a.panel?.from.pathname === b.pathname &&
        a.panel.from.search === b.search;
      const panelTransition =
        sameSurface(previous, next) || sameSurface(next, previous);
      const authTransition =
        isAuthPagePath(previous.pathname) && isAuthPagePath(next.pathname);
      previous = next;
      if (suppressNextPopGuard.current) {
        suppressNextPopGuard.current = false;
        setLocation(locationSnapshot(event.state));
        restoreEntry(event.state);
        return;
      }
      const resume = () => window.history.back();
      if (!panelTransition && !authTransition && !canLeave(resume)) {
        suppressNextPopGuard.current = true;
        window.history.go(1);
        return;
      }
      setLocation(locationSnapshot(event.state));
      restoreEntry(event.state);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [canLeave, location]);

  useEffect(() => {
    const previousScrollRestoration = window.history.scrollRestoration;
    window.history.scrollRestoration = "manual";
    let frame = 0;
    const saveScroll = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(() => {
        window.history.replaceState(
          {
            ...historyState(),
            momentScrollY: window.scrollY,
            momentArchiveAnchor: archiveScrollAnchor(),
          },
          "",
          window.location.href,
        );
      });
    };
    const saveScrollNow = () => {
      window.history.replaceState(
        {
          ...historyState(),
          momentScrollY: window.scrollY,
          momentArchiveAnchor: archiveScrollAnchor(),
        },
        "",
        window.location.href,
      );
    };
    window.addEventListener("scroll", saveScroll, { passive: true });
    window.addEventListener("pagehide", saveScrollNow);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener("scroll", saveScroll);
      window.removeEventListener("pagehide", saveScrollNow);
      window.history.scrollRestoration = previousScrollRestoration;
    };
  }, []);

  const value = useMemo(
    () => ({
      ...location,
      archiveAnchor: historyState().momentArchiveAnchor,
      navigate,
      navigateAuth,
      replace,
      replaceWithinPage,
      navigateOverlay,
      openPanel,
      closePanel,
      goBack,
      goBackAuth,
      replaceTimelineProgress,
      restoreCurrentEntry,
      setNavigationBlocker,
    }),
    [
      location,
      navigate,
      navigateAuth,
      replace,
      replaceWithinPage,
      navigateOverlay,
      openPanel,
      closePanel,
      goBack,
      goBackAuth,
      replaceTimelineProgress,
      restoreCurrentEntry,
      setNavigationBlocker,
    ],
  );
  return (
    <NavigationContext.Provider value={value}>
      {children}
    </NavigationContext.Provider>
  );
}

export function useNavigation(): NavigationContextValue {
  const value = useContext(NavigationContext);
  if (!value) {
    throw new Error("useNavigation must be used within NavigationProvider");
  }
  return value;
}

export function useOptionalNavigation(): NavigationContextValue | null {
  return useContext(NavigationContext);
}

export function InternalLink({
  href,
  children,
  onClick,
  authSwitch = false,
  replace = false,
  ...props
}: Omit<React.AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & {
  href: string;
  authSwitch?: boolean;
  replace?: boolean;
}) {
  const { navigate, navigateAuth, replace: replaceLocation } = useNavigation();
  const follow = (event: MouseEvent<HTMLAnchorElement>) => {
    onClick?.(event);
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey ||
      props.target === "_blank"
    ) {
      return;
    }
    event.preventDefault();
    if (authSwitch) navigateAuth(href);
    else if (replace) replaceLocation(href);
    else navigate(href);
  };
  return (
    <a href={href} onClick={follow} {...props}>
      {children}
    </a>
  );
}
