import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
  type TimelineRequest,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { PostPresentation } from "../posts/PostPresentation";
import { PublicEmpty, PublicError, TimelineLoading } from "./PublicStates";
import styles from "./TimelinePage.module.css";

const MAX_REPLAY_PAGES = 1_000;

interface TimelineRestoreRequest {
  loadedThrough: string | null;
  request: TimelineRequest;
}

function invalidTimelineProgress(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

function requestFromSearch(search: string): TimelineRestoreRequest {
  const query = new URLSearchParams(search);
  const progress = query.getAll("loaded_through");
  if (
    progress.length > 1 ||
    (progress.length === 1 &&
      (progress[0].length === 0 || progress[0].length > 4_096))
  ) {
    throw invalidTimelineProgress("loaded timeline cursor is invalid");
  }
  return {
    loadedThrough: progress[0] ?? null,
    request: {
      cursor: query.get("cursor"),
      from: query.get("from"),
      to: query.get("to"),
    },
  };
}

function appendPage(current: PublicPost[], incoming: PublicPost[]) {
  const existing = new Set(current.map((post) => post.id));
  if (incoming.some((post) => existing.has(post.id))) {
    throw invalidTimelineProgress("continued page contains a duplicate post");
  }
  return [...current, ...incoming];
}

function errorKind(error: unknown): "timeline" | "malformed" {
  return error instanceof PublicApiError &&
    error.code === "API_RESPONSE_INVALID"
    ? "malformed"
    : "timeline";
}

function yearsFor(posts: PublicPost[], timezone: string): number[] {
  const formatter = new Intl.DateTimeFormat("en", {
    timeZone: timezone,
    year: "numeric",
  });
  return Array.from(
    new Set(
      posts.map((post) => Number(formatter.format(new Date(post.publishedAt)))),
    ),
  ).filter(Number.isFinite);
}

export function TimelinePage({
  api,
  site,
  initialSearch,
  onYearsChange,
}: {
  api: PublicApi;
  site: SiteIdentity;
  initialSearch: string;
  onYearsChange?: (years: number[]) => void;
}) {
  const { replaceTimelineProgress, restoreCurrentEntry } = useNavigation();
  const [posts, setPosts] = useState<PublicPost[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [initialError, setInitialError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [loadingInitial, setLoadingInitial] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const initialSearchRef = useRef(initialSearch);
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const initialEntryRestored = useRef(false);

  const loadInitial = useCallback(async () => {
    activeInitial.current?.abort();
    const controller = new AbortController();
    activeInitial.current = controller;
    setLoadingInitial(true);
    setInitialError(null);
    try {
      const { loadedThrough, request } = requestFromSearch(
        initialSearchRef.current,
      );
      let page = await api.timeline({
        ...request,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      let combined = page.items;
      const initialCursor = request.cursor || null;
      let requestedCursor = initialCursor;
      const visited = new Set<string>();
      if (requestedCursor) visited.add(requestedCursor);

      for (
        let replayed = 0;
        loadedThrough !== null && requestedCursor !== loadedThrough;
        replayed += 1
      ) {
        if (replayed >= MAX_REPLAY_PAGES) {
          throw invalidTimelineProgress("loaded timeline cursor is too deep");
        }
        const continuation = page.nextCursor;
        if (!continuation) {
          throw invalidTimelineProgress(
            "loaded timeline cursor is unreachable",
          );
        }
        if (visited.has(continuation)) {
          throw invalidTimelineProgress("timeline cursor cycle detected");
        }
        visited.add(continuation);
        requestedCursor = continuation;
        page = await api.timeline({
          ...request,
          cursor: continuation,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        combined = appendPage(combined, page.items);
      }

      setPosts(combined);
      setNextCursor(page.nextCursor);
      onYearsChange?.(yearsFor(combined, site.timezone));
    } catch (error) {
      if (!controller.signal.aborted) setInitialError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingInitial(false);
    }
  }, [api, onYearsChange, site.timezone]);

  useEffect(() => {
    void loadInitial();
    return () => {
      activeInitial.current?.abort();
      activeMore.current?.abort();
    };
  }, [loadInitial]);

  useEffect(() => {
    if (loadingInitial || initialError || initialEntryRestored.current) {
      return;
    }
    initialEntryRestored.current = true;
    restoreCurrentEntry();
  }, [initialError, loadingInitial, restoreCurrentEntry]);

  const loadMore = async () => {
    if (!nextCursor || loadingMoreRef.current) return;
    const controller = new AbortController();
    activeMore.current = controller;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    setMoreError(null);
    const requestedCursor = nextCursor;
    try {
      const page = await api.timeline({
        ...requestFromSearch(initialSearchRef.current).request,
        cursor: requestedCursor,
        signal: controller.signal,
      });
      const firstNew = page.items[0]?.id;
      const combined = appendPage(posts, page.items);
      setPosts(combined);
      setNextCursor(page.nextCursor);
      replaceTimelineProgress(requestedCursor);
      onYearsChange?.(yearsFor(combined, site.timezone));
      if (firstNew) {
        window.requestAnimationFrame(() => {
          Array.from(document.querySelectorAll<HTMLElement>("[data-post-id]"))
            .find((element) => element.dataset.postId === firstNew)
            ?.focus({ preventScroll: true });
        });
      }
    } catch (error) {
      if (!controller.signal.aborted) setMoreError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingMore(false);
      loadingMoreRef.current = false;
    }
  };

  if (loadingInitial) return <TimelineLoading />;
  if (initialError) {
    return <PublicError kind={errorKind(initialError)} onRetry={loadInitial} />;
  }
  if (posts.length === 0) return <PublicEmpty />;

  return (
    <section aria-label="公开时间流" className={styles.timelineList}>
      <p aria-live="polite" className={styles.srOnly}>
        已加载 {posts.length} 条 Moment
      </p>
      {posts.map((post) => (
        <PostPresentation key={post.id} post={post} site={site} />
      ))}
      {moreError ? (
        <div className={styles.moreError} role="alert">
          <span>
            {errorKind(moreError) === "malformed"
              ? "下一页数据不完整，没有追加到时间流。"
              : "下一页没有加载完成。"}
          </span>
          <button onClick={loadMore} type="button">
            重试加载
          </button>
        </div>
      ) : null}
      {nextCursor && !moreError ? (
        <div className={styles.moreRow}>
          <button disabled={loadingMore} onClick={loadMore} type="button">
            {loadingMore ? "正在加载…" : "加载更早的 Moment"}
          </button>
        </div>
      ) : (
        <p className={styles.endMark}>已经走到这段时间流的起点</p>
      )}
    </section>
  );
}
