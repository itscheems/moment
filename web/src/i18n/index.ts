import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { isUILocale, type UILocale } from "./locale";
import en from "./locales/en.json";
import zhCN from "./locales/zh-CN.json";

export const LANGUAGE_STORAGE_KEY = "moment.ui-locale";

export function browserLocale(languages: readonly string[]): UILocale {
  for (const language of languages) {
    if (/^zh(?:-|$)/i.test(language)) return "zh-CN";
    if (/^en(?:-|$)/i.test(language)) return "en";
  }
  return "zh-CN";
}

export function visitorLocale(): UILocale {
  try {
    const saved = window.localStorage.getItem(LANGUAGE_STORAGE_KEY);
    if (isUILocale(saved)) return saved;
  } catch {
    // Storage can be denied; the current browser language still works.
  }
  return browserLocale(navigator.languages);
}

void i18n.use(initReactI18next).init({
  lng: visitorLocale(),
  fallbackLng: "zh-CN",
  supportedLngs: ["zh-CN", "en"],
  load: "currentOnly",
  resources: { en: { translation: en }, "zh-CN": { translation: zhCN } },
  keySeparator: false,
  nsSeparator: false,
  initAsync: false,
  interpolation: { escapeValue: false },
  react: { useSuspense: false },
});

export const t = i18n.t.bind(i18n);
export function getLocale(): UILocale {
  return i18n.resolvedLanguage === "en" ? "en" : "zh-CN";
}

export default i18n;
