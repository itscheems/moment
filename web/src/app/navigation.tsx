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
}

interface MomentHistoryState {
  momentFocusPostId?: string;
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

export function NavigationProvider({ children }: { children: ReactNode }) {
  const [location, setLocation] = useState(currentLocation);

  const navigate = useCallback((href: string) => {
    const currentState =
      typeof window.history.state === "object" && window.history.state !== null
        ? window.history.state
        : {};
    const activePost =
      document.activeElement instanceof HTMLElement
        ? document.activeElement.closest<HTMLElement>("[data-post-id]")?.dataset
            .postId
        : undefined;
    window.history.replaceState(
      {
        ...currentState,
        momentFocusPostId: activePost,
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
      window.requestAnimationFrame(() => {
        window.scrollTo({ top: scrollPosition(event.state), behavior: "auto" });
        const state = event.state as MomentHistoryState | null;
        if (state?.momentFocusPostId) {
          const post = Array.from(
            document.querySelectorAll<HTMLElement>("[data-post-id]"),
          ).find(
            (element) => element.dataset.postId === state.momentFocusPostId,
          );
          post
            ?.querySelector<HTMLElement>('a[href^="/m/"]')
            ?.focus({ preventScroll: true });
        }
      });
    };
    window.addEventListener("popstate", onPopState);
    return () => window.removeEventListener("popstate", onPopState);
  }, []);

  const value = useMemo(
    () => ({ ...location, navigate, goBack }),
    [location, navigate, goBack],
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
