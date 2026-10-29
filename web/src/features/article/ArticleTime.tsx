import { useTranslation } from "react-i18next";
import { useSyncExternalStore } from "react";
import { articlePublishedTime, articleSavedAgo } from "./articleDates";

let now = Date.now();
let timer: ReturnType<typeof setInterval> | undefined;
const listeners = new Set<() => void>();
function tick() {
  now = Date.now();
  listeners.forEach((listener) => listener());
}
function visibility() {
  if (timer !== undefined) clearInterval(timer);
  timer = undefined;
  if (document.visibilityState === "visible") {
    tick();
    timer = setInterval(tick, 1000);
  }
}
function subscribe(listener: () => void) {
  listeners.add(listener);
  if (listeners.size === 1) {
    visibility();
    document.addEventListener("visibilitychange", visibility);
  }
  return () => {
    listeners.delete(listener);
    if (!listeners.size) {
      if (timer !== undefined) clearInterval(timer);
      timer = undefined;
      document.removeEventListener("visibilitychange", visibility);
    }
  };
}
const snapshot = () => now;

/** One shared clock updates labels, never their editor or list parent. */
export function ArticleTime({
  value,
  timezone,
  draft = false,
}: {
  value: string;
  timezone: string;
  draft?: boolean;
}) {
  const { t } = useTranslation();
  const now = useSyncExternalStore(subscribe, snapshot, snapshot);
  const savedAgo = draft ? articleSavedAgo(value, now) : "";
  return (
    <time
      dateTime={value}
      aria-live="off"
      data-article-time={draft ? "draft" : "published"}
    >
      {draft ? (
        <>
          {t("Last saved")}{" "}
          <span
            data-relative-time={
              savedAgo === "just now" ? "immediate" : "elapsed"
            }
          >
            {savedAgo}
          </span>
        </>
      ) : (
        articlePublishedTime(value, timezone, now)
      )}
    </time>
  );
}
