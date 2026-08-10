import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
  type TimelineRequest,
} from "../../api/public";
import { PostPresentation } from "../posts/PostPresentation";
import { PublicEmpty, PublicError, TimelineLoading } from "./PublicStates";
import styles from "./TimelinePage.module.css";

function requestFromSearch(search: string): TimelineRequest {
  const query = new URLSearchParams(search);
  return {
    cursor: query.get("cursor"),
    from: query.get("from"),
    to: query.get("to"),
  };
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
  const [posts, setPosts] = useState<PublicPost[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [initialError, setInitialError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [loadingInitial, setLoadingInitial] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const initialRequest = useRef(requestFromSearch(initialSearch));
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);

  const loadInitial = useCallback(async () => {
    activeInitial.current?.abort();
    const controller = new AbortController();
    activeInitial.current = controller;
    setLoadingInitial(true);
    setInitialError(null);
    try {
      const page = await api.timeline({
        ...initialRequest.current,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      setPosts(page.items);
      setNextCursor(page.nextCursor);
      onYearsChange?.(yearsFor(page.items, site.timezone));
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

  const loadMore = async () => {
    if (!nextCursor || loadingMoreRef.current) return;
    const controller = new AbortController();
    activeMore.current = controller;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    setMoreError(null);
    try {
      const page = await api.timeline({
        ...initialRequest.current,
        cursor: nextCursor,
        signal: controller.signal,
      });
      const existing = new Set(posts.map((post) => post.id));
      if (page.items.some((post) => existing.has(post.id))) {
        throw new PublicApiError("continued page contains a duplicate post", {
          code: "API_RESPONSE_INVALID",
          status: 0,
        });
      }
      const firstNew = page.items[0]?.id;
      const combined = [...posts, ...page.items];
      setPosts(combined);
      setNextCursor(page.nextCursor);
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
