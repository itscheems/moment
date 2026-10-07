import {
  createContext,
  useCallback,
  useContext,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from "react";

export interface ReadingLayoutPreference {
  width: "standard" | "wide" | "wider";
  fontSize: number;
  lineHeight: "normal" | "compact" | "relaxed";
}

export const READING_LAYOUT_STORAGE_KEY = "moment.readingLayout.v1";
export const DEFAULT_READING_LAYOUT: ReadingLayoutPreference = {
  width: "standard",
  fontSize: 16,
  lineHeight: "normal",
};
const WIDTHS = { standard: "44.25rem", wide: "56.25rem", wider: "68.75rem" };

export function readReadingLayout(): ReadingLayoutPreference {
  try {
    const value = JSON.parse(
      window.localStorage.getItem(READING_LAYOUT_STORAGE_KEY) ?? "null",
    );
    if (
      value &&
      Object.hasOwn(WIDTHS, value.width) &&
      Number.isInteger(value.fontSize) &&
      value.fontSize >= 14 &&
      value.fontSize <= 24 &&
      value.fontSize % 2 === 0 &&
      ["normal", "compact", "relaxed"].includes(value.lineHeight)
    ) {
      return {
        width: value.width,
        fontSize: value.fontSize,
        lineHeight: value.lineHeight,
      };
    }
  } catch {
    // Reading remains usable when preferences or browser storage are unavailable.
  }
  return { ...DEFAULT_READING_LAYOUT };
}

export function readingLayoutStyle(
  value: ReadingLayoutPreference,
): CSSProperties {
  return {
    "--reading-preferred-width": WIDTHS[value.width],
    "--reading-font-size": `${value.fontSize / 16}rem`,
    "--reading-line-height":
      value.lineHeight === "normal"
        ? undefined
        : value.lineHeight === "compact"
          ? 1.4
          : 1.8,
  } as CSSProperties;
}

interface ReadingLayoutState {
  preference: ReadingLayoutPreference;
  setPreference: (patch: Partial<ReadingLayoutPreference>) => void;
  reset: () => void;
}

// Noninteractive/static consumers retain the original geometry without a provider.
const ReadingLayoutContext = createContext<ReadingLayoutState>({
  preference: DEFAULT_READING_LAYOUT,
  setPreference: () => {},
  reset: () => {},
});

function captureReadingAnchor() {
  if (window.scrollY === 0) return null;
  const body = document.querySelector(
    "[data-reading-layout] [data-content-renderer]",
  );
  const header = document.querySelector("header");
  const top = (header?.getBoundingClientRect().bottom ?? 0) + 12;
  for (const element of body?.querySelectorAll<HTMLElement>(
    "h1,h2,h3,h4,h5,h6,p,pre,figcaption",
  ) ?? []) {
    const rect = element.getBoundingClientRect();
    if (rect.bottom > top && rect.top < window.innerHeight) {
      return { element, top: rect.top };
    }
  }
  return null;
}

export function ReadingLayoutProvider({ children }: { children: ReactNode }) {
  const [preference, setState] = useState(readReadingLayout);
  const anchor = useRef<ReturnType<typeof captureReadingAnchor>>(null);
  const update = useCallback(
    (next: ReadingLayoutPreference, reset = false) => {
      if (
        next.width !== preference.width ||
        next.fontSize !== preference.fontSize ||
        next.lineHeight !== preference.lineHeight
      ) {
        anchor.current = captureReadingAnchor();
        setState(next);
      }
      try {
        if (reset) window.localStorage.removeItem(READING_LAYOUT_STORAGE_KEY);
        else
          window.localStorage.setItem(
            READING_LAYOUT_STORAGE_KEY,
            JSON.stringify(next),
          );
      } catch {
        // Blocked storage only disables persistence, never the current adjustment.
      }
    },
    [preference],
  );
  useLayoutEffect(() => {
    const pending = anchor.current;
    anchor.current = null;
    if (!pending?.element.isConnected) return;
    // As with timeline prepends, account for native scroll anchoring before compensating.
    const delta = pending.element.getBoundingClientRect().top - pending.top;
    if (delta) window.scrollBy({ top: delta, behavior: "auto" });
  }, [preference]);

  return (
    <ReadingLayoutContext.Provider
      value={{
        preference,
        setPreference: (patch) => update({ ...preference, ...patch }),
        reset: () => update({ ...DEFAULT_READING_LAYOUT }, true),
      }}
    >
      {children}
    </ReadingLayoutContext.Provider>
  );
}

export function useReadingLayout() {
  return useContext(ReadingLayoutContext);
}
