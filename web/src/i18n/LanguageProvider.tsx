import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useLayoutEffect,
  useState,
} from "react";
import { useTranslation } from "react-i18next";
import { type UILocale, preferencesApi } from "../api/preferences";
import { useSession } from "../features/auth/SessionProvider";
import i18n, { LANGUAGE_STORAGE_KEY, visitorLocale } from "./index";

interface LanguageContextValue {
  locale: UILocale;
  saveLanguage: (locale: UILocale, signal?: AbortSignal) => Promise<boolean>;
}

const LanguageContext = createContext<LanguageContextValue | null>(null);

export function LanguageProvider({ children }: { children: ReactNode }) {
  const { account, session, preferences, applyPreferences } = useSession();
  const [visitor, setVisitor] = useState(visitorLocale);
  // Keep the known language while a cookie transition resolves its new account.
  // Guest transitions explicitly clear preferences in the session provider.
  const locale = preferences?.uiLocale ?? visitor;

  useLayoutEffect(() => {
    void i18n.changeLanguage(locale);
    document.documentElement.lang = locale;
    const manifest = document.querySelector<HTMLLinkElement>(
      'link[rel="manifest"]',
    );
    if (manifest)
      manifest.href =
        locale === "en"
          ? "/manifest.en.webmanifest?v=3"
          : "/manifest.webmanifest?v=3";
  }, [locale]);

  const saveLanguage = useCallback(
    async (next: UILocale, signal?: AbortSignal) => {
      if (account && session) {
        const saved = await preferencesApi.update(
          session.csrfToken,
          next,
          signal,
        );
        if (signal?.aborted) return false;
        return applyPreferences(account.userId, session.csrfToken, saved);
      }
      setVisitor(next);
      try {
        window.localStorage.setItem(LANGUAGE_STORAGE_KEY, next);
      } catch {
        // The selection still applies for this visit when storage is unavailable.
      }
      return true;
    },
    [account, session, applyPreferences],
  );

  return (
    <LanguageContext.Provider value={{ locale, saveLanguage }}>
      {children}
    </LanguageContext.Provider>
  );
}

export function useLanguage(): LanguageContextValue {
  const context = useContext(LanguageContext);
  if (!context)
    throw new Error("useLanguage must be used within LanguageProvider");
  useTranslation();
  return context;
}
