import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
  type TimelineRequest,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import { PostPresentation } from "../posts/PostPresentation";
import { PostReplyDialog } from "../comments/CommentThread";
import { PublicEmpty, PublicError, TimelineLoading } from "./PublicStates";
import { listenForOwnerPosts } from "../owner/postEvents";
import type { OwnerPostEvent } from "../owner/postEvents";
import {
  appendUnique,
  invalidCursorProgress,
  loadedThroughFromSearch,
  validateCursorPage,
  withoutLoadedProgress,
} from "./cursorContinuity";
import styles from "./TimelinePage.module.css";

const MAX_REPLAY_PAGES = 1_000;

interface TimelineRestoreRequest {
  loadedThrough: string | null;
  request: TimelineRequest;
}

function requestFromSearch(search: string): TimelineRestoreRequest {
  const query = new URLSearchParams(search);
  return {
    loadedThrough: loadedThroughFromSearch(search),
    request: {
      cursor: query.get("cursor"),
      from: query.get("from"),
      to: query.get("to"),
    },
  };
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
  const [replyDialogOpen, setReplyDialogOpen] = useState(false);
  const [replyDialogPost, setReplyDialogPost] = useState<PublicPost | null>(
    null,
  );
  const [replyDialogContent, setReplyDialogContent] = useState("");
  const [replyDialogGuestName, setReplyDialogGuestName] = useState("");
  const initialSearchRef = useRef(initialSearch);
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const initialEntryRestored = useRef(false);
  const ownerEvents = useRef(new Map<string, OwnerPostEvent>());
  const visitedCursors = useRef(new Set<string>());
  const replyDialogTriggerRef = useRef<HTMLButtonElement>(null);

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
      validateCursorPage(page, requestedCursor, visited, "timeline");

      for (
        let replayed = 0;
        loadedThrough !== null && requestedCursor !== loadedThrough;
        replayed += 1
      ) {
        if (replayed >= MAX_REPLAY_PAGES) {
          throw invalidCursorProgress("loaded timeline cursor is too deep");
        }
        const continuation = page.nextCursor;
        if (!continuation) {
          throw invalidCursorProgress("loaded timeline cursor is unreachable");
        }
        if (visited.has(continuation)) {
          throw invalidCursorProgress("timeline cursor cycle detected");
        }
        visited.add(continuation);
        requestedCursor = continuation;
        page = await api.timeline({
          ...request,
          cursor: continuation,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        validateCursorPage(page, requestedCursor, visited, "timeline");
        combined = appendUnique(
          combined,
          page.items,
          (post) => post.id,
          "timeline",
        );
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

  const createdFromTimeline = useCallback((comment: PublicComment) => {
    const increment = (post: PublicPost) =>
      post.id === comment.postId
        ? { ...post, commentCount: post.commentCount + 1 }
        : post;
    setPosts((current) => current.map(increment));
    setReplyDialogPost((current) => (current ? increment(current) : current));
  }, []);

  useEffect(() => {
    if (loadingInitial || initialError || initialEntryRestored.current) {
      return;
    }
    initialEntryRestored.current = true;
    restoreCurrentEntry();
  }, [initialError, loadingInitial, restoreCurrentEntry]);

  const loadMore = async (source: LoadSource = "manual") => {
    if (!nextCursor || loadingMoreRef.current) return;
    const controller = new AbortController();
    activeMore.current = controller;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    setMoreError(null);
    const requestedCursor = nextCursor;
    try {
      if (visitedCursors.current.has(requestedCursor)) {
        throw invalidCursorProgress("timeline cursor cycle detected");
      }
      const page = await api.timeline({
        ...requestFromSearch(initialSearchRef.current).request,
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateCursorPage(page, requestedCursor, visited, "timeline");
      const firstNew = page.items[0]?.id;
      const combined = appendUnique(
        posts,
        page.items,
        (post) => post.id,
        "timeline",
      );
      visitedCursors.current = visited;
      setPosts(combined);
      setNextCursor(page.nextCursor);
      replaceTimelineProgress(requestedCursor);
      if (source === "manual" && firstNew) {
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
  if (posts.length === 0) return <PublicEmpty owner={owner} />;

  return (
    <>
      <section aria-label="公开时间流" className={styles.timelineList}>
        <p aria-live="polite" className={styles.srOnly}>
          已加载 {posts.length} 条 Moment
        </p>
        {posts.map((post) => (
          <PostPresentation
            fresh={post.id === freshId}
            key={post.id}
            owner={owner}
            onReply={(event) => {
              replyDialogTriggerRef.current = event.currentTarget;
              if (replyDialogPost?.id !== post.id) setReplyDialogContent("");
              setReplyDialogPost(post);
              setReplyDialogOpen(true);
            }}
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
              <button onClick={() => void loadMore("manual")} type="button">
                重试加载
              </button>
            )}
          </div>
        ) : null}
        {nextCursor && !moreError ? (
          <LoadMoreSentinel
            className={styles.moreRow}
            cursor={nextCursor}
            label="加载更早的 Moment"
            loading={loadingMore}
            onLoad={(source) => void loadMore(source)}
          />
        ) : (
          <p className={styles.endMark}>已经走到这段时间流的起点</p>
        )}
      </section>
      {replyDialogPost ? (
        <PostReplyDialog
          api={api}
          commentsEnabled={site.commentsEnabled}
          content={replyDialogContent}
          guestName={replyDialogGuestName}
          onContentChange={setReplyDialogContent}
          onCreated={createdFromTimeline}
          onGuestNameChange={setReplyDialogGuestName}
          onOpenChange={setReplyDialogOpen}
          open={replyDialogOpen}
          post={replyDialogPost}
          site={site}
          triggerRef={replyDialogTriggerRef}
        />
      ) : null}
    </>
  );
}
