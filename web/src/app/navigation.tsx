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

interface LocationSnapshot {
  pathname: string;
  search: string;
}

interface NavigationLocation extends LocationSnapshot {
  overlayFrom: LocationSnapshot | null;
}

interface NavigationContextValue extends NavigationLocation {
  navigate: (href: string) => void;
  navigateOverlay: (href: string) => void;
  goBack: (fallback: string) => void;
  replaceTimelineProgress: (cursor: string) => void;
  restoreCurrentEntry: () => void;
  setNavigationBlocker: (
    owner: symbol,
    blocker: NavigationBlocker | null,
  ) => void;
}

export type NavigationBlocker = (resume: () => void) => boolean;

interface MomentHistoryState {
  momentFocusKey?: string;
  momentInternal?: boolean;
  momentOverlayFrom?: LocationSnapshot;
  momentScrollY?: number;
}

const NavigationContext = createContext<NavigationContextValue | null>(null);

function currentLocation(): LocationSnapshot {
  return {
    pathname: window.location.pathname,
    search: window.location.search,
  };
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
  return {
    ...currentLocation(),
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

function restoreEntry(state: unknown) {
  window.requestAnimationFrame(() => {
    window.scrollTo({ top: scrollPosition(state), behavior: "auto" });
    const focusKey = (state as MomentHistoryState | null)?.momentFocusKey;
    if (!focusKey) return;
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
    (owner: symbol, blocker: NavigationBlocker | null) => {
      if (blocker) {
        navigationBlockers.current.set(owner, blocker);
      } else {
        navigationBlockers.current.delete(owner);
      }
    },
    [],
  );

  const commitNavigation = useCallback(
    (href: string, overlayFrom: LocationSnapshot | null = null) => {
      const activeFocusKey =
        document.activeElement instanceof HTMLElement
          ? document.activeElement.closest<HTMLElement>("[data-focus-key]")
              ?.dataset.focusKey
          : undefined;
      window.history.replaceState(
        {
          ...historyState(),
          momentFocusKey: activeFocusKey,
          momentScrollY: window.scrollY,
        },
        "",
        window.location.href,
      );
      window.history.pushState(
        {
          momentInternal: true,
          momentOverlayFrom: overlayFrom ?? undefined,
          momentScrollY: 0,
        },
        "",
        href,
      );
      setLocation(locationSnapshot(window.history.state));
      if (!overlayFrom) window.scrollTo({ top: 0, behavior: "auto" });
    },
    [],
  );

  const navigate = useCallback(
    function navigateTo(href: string) {
      if (!canLeave(() => navigateTo(href))) return;
      commitNavigation(href);
    },
    [canLeave, commitNavigation],
  );

  const navigateOverlay = useCallback(
    function navigateToOverlay(href: string) {
      if (!canLeave(() => navigateToOverlay(href))) return;
      commitNavigation(href, currentLocation());
    },
    [canLeave, commitNavigation],
  );

  const replaceTimelineProgress = useCallback((cursor: string) => {
    const url = new URL(window.location.href);
    url.searchParams.set("loaded_through", cursor);
    window.history.replaceState(
      { ...historyState(), momentScrollY: window.scrollY },
      "",
      `${url.pathname}${url.search}${url.hash}`,
    );
    setLocation(locationSnapshot(window.history.state));
  }, []);

  const restoreCurrentEntry = useCallback(() => {
    restoreEntry(window.history.state);
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

  useEffect(() => {
    const onPopState = (event: PopStateEvent) => {
      if (suppressNextPopGuard.current) {
        suppressNextPopGuard.current = false;
        setLocation(locationSnapshot(event.state));
        restoreEntry(event.state);
        return;
      }
      const resume = () => window.history.back();
      if (!canLeave(resume)) {
        suppressNextPopGuard.current = true;
        window.history.go(1);
        return;
      }
      setLocation(locationSnapshot(event.state));
      restoreEntry(event.state);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, [canLeave]);

  useEffect(() => {
    const previousScrollRestoration = window.history.scrollRestoration;
    window.history.scrollRestoration = "manual";
    let frame = 0;
    const saveScroll = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(() => {
        window.history.replaceState(
          { ...historyState(), momentScrollY: window.scrollY },
          "",
          window.location.href,
        );
      });
    };
    const saveScrollNow = () => {
      window.history.replaceState(
        { ...historyState(), momentScrollY: window.scrollY },
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
      navigate,
      navigateOverlay,
      goBack,
      replaceTimelineProgress,
      restoreCurrentEntry,
      setNavigationBlocker,
    }),
    [
      location,
      navigate,
      navigateOverlay,
      goBack,
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

export function InternalLink({
  href,
  children,
  onClick,
  ...props
}: Omit<React.AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & {
  href: string;
}) {
  const { navigate } = useNavigation();
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
    navigate(href);
  };
  return (
    <a href={href} onClick={follow} {...props}>
      {children}
    </a>
  );
}
