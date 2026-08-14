import {
  createContext,
  type ReactNode,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";

export type ThemePreference = "light" | "dim" | "lights-out";
export type AccentPreference =
  "blue" | "yellow" | "pink" | "purple" | "orange" | "green";
interface ThemeContextValue {
  preference: ThemePreference;
  accent: AccentPreference;
  resolvedTheme: ThemePreference;
  setPreference: (preference: ThemePreference) => void;
  setAccent: (accent: AccentPreference) => void;
}

const STORAGE_KEY = "moment.theme.v1";
const ACCENT_STORAGE_KEY = "moment.accent.v1";
const ThemeContext = createContext<ThemeContextValue | null>(null);

function isPreference(value: string | null): value is ThemePreference {
  return value === "light" || value === "dim" || value === "lights-out";
}

function readPreference(): ThemePreference {
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (stored === "dark") return "lights-out";
    if (stored === "system") {
      window.localStorage.setItem(STORAGE_KEY, "light");
      return "light";
    }
    return isPreference(stored) ? stored : "light";
  } catch {
    return "light";
  }
}

function isAccent(value: string | null): value is AccentPreference {
  return ["blue", "yellow", "pink", "purple", "orange", "green"].includes(
    value ?? "",
  );
}

function readAccent(): AccentPreference {
  try {
    const stored = window.localStorage.getItem(ACCENT_STORAGE_KEY);
    return isAccent(stored) ? stored : "blue";
  } catch {
    return "blue";
  }
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [preference, setPreferenceState] =
    useState<ThemePreference>(readPreference);
  const [accent, setAccentState] = useState<AccentPreference>(readAccent);
  const resolvedTheme = preference;

  useEffect(() => {
    const root = document.documentElement;
    root.dataset.theme = resolvedTheme;
    root.dataset.accent = accent;
    root.style.colorScheme = resolvedTheme === "light" ? "light" : "dark";
    const themeColor = document.querySelector<HTMLMetaElement>(
      'meta[name="theme-color"]',
    );
    themeColor?.setAttribute(
      "content",
      resolvedTheme === "light"
        ? "#ffffff"
        : resolvedTheme === "dim"
          ? "#15202b"
          : "#000000",
    );
  }, [accent, resolvedTheme]);

  const setPreference = (next: ThemePreference) => {
    setPreferenceState(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // A blocked storage API only disables persistence; theme switching still works.
    }
  };

  const setAccent = (next: AccentPreference) => {
    setAccentState(next);
    try {
      window.localStorage.setItem(ACCENT_STORAGE_KEY, next);
    } catch {
      // A blocked storage API only disables persistence.
    }
  };

  const value = useMemo(
    () => ({ preference, accent, resolvedTheme, setPreference, setAccent }),
    [accent, preference, resolvedTheme],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const context = useContext(ThemeContext);
  if (!context) throw new Error("useTheme must be used within ThemeProvider");
  return context;
}
