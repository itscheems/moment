import {
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useSyncExternalStore,
} from "react";
import type { PublicApi } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { withoutLoadedProgress } from "../timeline/cursorContinuity";
import { rowElement, withRowsRendered } from "../timeline/scrollAnchor";
import { ProfileTimeline, type ProfileFilter } from "./profileTimeline";

export function useProfileTimeline(
  api: PublicApi,
  scope: string,
  filter: ProfileFilter,
) {
  const { replaceWithinPage } = useNavigation();
  const path = useRef(window.location.pathname);
  const initialSearch = useRef(window.location.search);
  const store = useMemo(
    () => new ProfileTimeline(api, initialSearch.current),
    [api, scope],
  );
  const feeds = useSyncExternalStore(store.subscribe, store.snapshot);
  const compensation = useRef<{ key: string; top: number } | null>(null);
  useEffect(() => {
    store.onProgressDiscarded = () => {
      initialSearch.current = withoutLoadedProgress(initialSearch.current);
      if (window.location.pathname === path.current)
        replaceWithinPage(
          `${path.current}${withoutLoadedProgress(window.location.search)}`,
        );
    };
    store.beforeMerge = () => {
      const rows = Array.from(
        document.querySelectorAll<HTMLElement>(
          '[role="tabpanel"]:not([inert]) [data-timeline-item-key]',
        ),
      );
      const visible =
        rows.find(
          (row) =>
            row.getBoundingClientRect().top < 0 &&
            row.getBoundingClientRect().bottom > 0,
        ) ?? rows.find((row) => row.getBoundingClientRect().top >= 0);
      // No adjustment at the top: new publications should be visible there.
      if (visible && window.scrollY > 0)
        compensation.current = {
          key: visible.dataset.timelineItemKey!,
          top: visible.getBoundingClientRect().top,
        };
    };
    return () => {
      store.onProgressDiscarded = undefined;
      queueMicrotask(() => store.disposeIfUnused());
    };
  }, [store, replaceWithinPage]);
  useEffect(() => {
    void store.activate(filter);
  }, [filter, store]);
  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState !== "hidden") store.invalidate();
    };
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refresh);
    return () => {
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refresh);
    };
  }, [store]);
  useLayoutEffect(() => {
    const anchor = compensation.current;
    compensation.current = null;
    if (!anchor) return;
    // During an exit transition the same item can occur in two panels.
    // Only the active panel can be the reader's current scroll anchor.
    const panel = document.querySelector<HTMLElement>(
      '[role="tabpanel"]:not([inert]):not([hidden])',
    );
    const row = panel ? rowElement(anchor.key, panel) : undefined;
    if (!row || !panel) return;
    withRowsRendered(() => {
      const delta = row.getBoundingClientRect().top - anchor.top;
      if (delta !== 0) window.scrollBy({ top: delta, behavior: "auto" });
    }, panel);
  }, [feeds]);
  return { store, feeds, initialSearch };
}
