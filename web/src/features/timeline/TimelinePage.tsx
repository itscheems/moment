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
import { listenForOwnerPosts } from "../owner/postEvents";
import type { OwnerPostEvent } from "../owner/postEvents";
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

type TimelinePageResult = Awaited<ReturnType<PublicApi["timeline"]>>;

function validateCursorPage(
  page: TimelinePageResult,
  requestedCursor: string | null,
  visited: ReadonlySet<string>,
): void {
  if (page.items.length === 0 && page.nextCursor !== null) {
    throw invalidTimelineProgress("empty timeline page advances the cursor");
  }
  if (page.nextCursor !== null && visited.has(page.nextCursor)) {
    throw invalidTimelineProgress(
      page.nextCursor === requestedCursor
        ? "timeline cursor points to itself"
        : "timeline cursor cycle detected",
    );
  }
}

function withoutLoadedProgress(search: string): string {
  const query = new URLSearchParams(search);
  query.delete("loaded_through");
  const value = query.toString();
  return value ? `?${value}` : "";
}

function hasLoadedProgress(search: string): boolean {
  return new URLSearchParams(search).has("loaded_through");
}

function recoverableCursorError(error: unknown): boolean {
  return (
    error instanceof PublicApiError &&
    (error.code === "INVALID_ARGUMENT" || error.code === "API_RESPONSE_INVALID")
  );
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
  onSiteChanged,
  owner = false,
}: {
  api: PublicApi;
  site: SiteIdentity;
  initialSearch: string;
  onYearsChange?: (years: number[]) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
  owner?: boolean;
}) {
  const { navigate, pathname, replaceTimelineProgress, restoreCurrentEntry } =
    useNavigation();
  const [posts, setPosts] = useState<PublicPost[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [initialError, setInitialError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [loadingInitial, setLoadingInitial] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [freshId, setFreshId] = useState<string | null>(null);
  const initialSearchRef = useRef(initialSearch);
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const initialEntryRestored = useRef(false);
  const ownerEvents = useRef(new Map<string, OwnerPostEvent>());
  const visitedCursors = useRef(new Set<string>());

  const reconcileOwnerEvents = useCallback((items: PublicPost[]) => {
    let reconciled = items;
    for (const event of ownerEvents.current.values()) {
      if (event.kind === "created") {
        reconciled = [
          event.post,
          ...reconciled.filter((post) => post.id !== event.post.id),
        ];
      } else if (event.kind === "updated") {
        reconciled = reconciled.map((post) =>
          post.id === event.post.id ? event.post : post,
        );
      } else {
        reconciled = reconciled.filter((post) => post.id !== event.post.id);
      }
    }
    return reconciled;
  }, []);

  const loadInitial = useCallback(async () => {
    activeInitial.current?.abort();
    const controller = new AbortController();
    activeInitial.current = controller;
    setLoadingInitial(true);
    setInitialError(null);
    setMoreError(null);
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
      validateCursorPage(page, requestedCursor, visited);

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
        validateCursorPage(page, requestedCursor, visited);
        combined = appendPage(combined, page.items);
      }

      combined = reconcileOwnerEvents(combined);
      visitedCursors.current = visited;
      setPosts(combined);
      setNextCursor(page.nextCursor);
    } catch (error) {
      if (!controller.signal.aborted) setInitialError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingInitial(false);
    }
  }, [api, reconcileOwnerEvents]);

  useEffect(() => {
    void loadInitial();
    return () => {
      activeInitial.current?.abort();
      activeMore.current?.abort();
    };
  }, [loadInitial]);

  useEffect(
    () =>
      listenForOwnerPosts((event) => {
        ownerEvents.current.set(event.post.id, event);
        setPosts((current) => {
          let next: PublicPost[];
          if (event.kind === "created") {
            next = [
              event.post,
              ...current.filter((post) => post.id !== event.post.id),
            ];
            setFreshId(event.post.id);
            window.setTimeout(() => setFreshId(null), 700);
          } else if (event.kind === "updated") {
            next = current.map((post) =>
              post.id === event.post.id ? event.post : post,
            );
          } else {
            next = current.filter((post) => post.id !== event.post.id);
          }
          return next;
        });
      }),
    [],
  );

  useEffect(() => {
    onYearsChange?.(yearsFor(posts, site.timezone));
  }, [onYearsChange, posts, site.timezone]);

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
      if (visitedCursors.current.has(requestedCursor)) {
        throw invalidTimelineProgress("timeline cursor cycle detected");
      }
      const page = await api.timeline({
        ...requestFromSearch(initialSearchRef.current).request,
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateCursorPage(page, requestedCursor, visited);
      const firstNew = page.items[0]?.id;
      const combined = appendPage(posts, page.items);
      visitedCursors.current = visited;
      setPosts(combined);
      setNextCursor(page.nextCursor);
      replaceTimelineProgress(requestedCursor);
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

  const resetFromFirstPage = () => {
    const search = withoutLoadedProgress(initialSearchRef.current);
    initialSearchRef.current = search;
    visitedCursors.current = new Set();
    setMoreError(null);
    navigate(`${pathname}${search}`);
    void loadInitial();
  };

  if (loadingInitial) return <TimelineLoading />;
  if (initialError) {
    return (
      <PublicError
        kind={errorKind(initialError)}
        onResetProgress={
          hasLoadedProgress(initialSearchRef.current) &&
          recoverableCursorError(initialError)
            ? resetFromFirstPage
            : undefined
        }
        onRetry={loadInitial}
      />
    );
  }
  if (posts.length === 0) return <PublicEmpty />;

  return (
    <section aria-label="公开时间流" className={styles.timelineList}>
      <p aria-live="polite" className={styles.srOnly}>
        已加载 {posts.length} 条 Moment
      </p>
      {posts.map((post) => (
        <PostPresentation
          fresh={post.id === freshId}
          key={post.id}
          owner={owner}
          onSiteChanged={onSiteChanged}
          post={post}
          site={site}
        />
      ))}
      {moreError ? (
        <div className={styles.moreError} role="alert">
          <span>
            {errorKind(moreError) === "malformed"
              ? "下一页数据不完整，没有追加到时间流。"
              : "下一页没有加载完成。"}
          </span>
          {recoverableCursorError(moreError) ? (
            <button onClick={resetFromFirstPage} type="button">
              从第一页重新加载
            </button>
          ) : (
            <button onClick={loadMore} type="button">
              重试加载
            </button>
          )}
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
