import {
  createContext,
  type MouseEvent,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

interface LocationSnapshot {
  pathname: string;
  search: string;
}

interface NavigationContextValue extends LocationSnapshot {
  navigate: (href: string) => void;
  goBack: (fallback: string) => void;
  replaceTimelineProgress: (cursor: string) => void;
  restoreCurrentEntry: () => void;
}

interface MomentHistoryState {
  momentFocusKey?: string;
  momentInternal?: boolean;
  momentScrollY?: number;
}

const NavigationContext = createContext<NavigationContextValue | null>(null);

function currentLocation(): LocationSnapshot {
  return {
    pathname: window.location.pathname,
    search: window.location.search,
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
  const [location, setLocation] = useState(currentLocation);

  const navigate = useCallback((href: string) => {
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
      { momentInternal: true, momentScrollY: 0 },
      "",
      href,
    );
    setLocation(currentLocation());
    window.scrollTo({ top: 0, behavior: "auto" });
  }, []);

  const replaceTimelineProgress = useCallback((cursor: string) => {
    const url = new URL(window.location.href);
    url.searchParams.set("loaded_through", cursor);
    window.history.replaceState(
      { ...historyState(), momentScrollY: window.scrollY },
      "",
      `${url.pathname}${url.search}${url.hash}`,
    );
    setLocation(currentLocation());
  }, []);

  const restoreCurrentEntry = useCallback(() => {
    restoreEntry(window.history.state);
  }, []);

  const goBack = useCallback(
    (fallback: string) => {
      const state = window.history.state as MomentHistoryState | null;
      if (state?.momentInternal) {
        window.history.back();
        return;
      }
      navigate(fallback);
    },
    [navigate],
  );

  useEffect(() => {
    const onPopState = (event: PopStateEvent) => {
      setLocation(currentLocation());
      restoreEntry(event.state);
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

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
      goBack,
      replaceTimelineProgress,
      restoreCurrentEntry,
    }),
    [location, navigate, goBack, replaceTimelineProgress, restoreCurrentEntry],
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
