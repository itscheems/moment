import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
  type TimelinePage as TimelinePageData,
  type TimelineRequest,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import { usePostReplyDialog } from "../comments/usePostReplyDialog";
import { PostPresentation } from "../posts/PostPresentation";
import { PublicEmpty, PublicError, TimelineLoading } from "./PublicStates";
import { listenForPostChanges } from "../posts/postEvents";
import type { PostChangeEvent } from "../posts/postEvents";
import {
  appendUnique,
  invalidCursorProgress,
  loadedThroughFromSearch,
  prependUnique,
  validateCursorPage,
  validateNewerPage,
  withoutLoadedProgress,
} from "./cursorContinuity";
import { reconcileTimelineEvent, timelineItemKey } from "./timelineItems";
import styles from "./TimelinePage.module.css";
import { rowElement, withRowsRendered } from "./scrollAnchor";

const MAX_REPLAY_PAGES = 1_000;

interface TimelineRestoreRequest {
  loadedThrough: string | null;
  loadedNewer: string | null;
  request: TimelineRequest;
}

function requestFromSearch(search: string): TimelineRestoreRequest {
  const query = new URLSearchParams(search);
  return {
    loadedThrough: loadedThroughFromSearch(search),
    loadedNewer: loadedThroughFromSearch(search, "loaded_newer"),
    request: {
      cursor: query.get("cursor"),
      from: query.get("from"),
      to: query.get("to"),
      anchor: query.get("anchor"),
    },
  };
}

function hasLoadedProgress(search: string): boolean {
  const query = new URLSearchParams(search);
  return query.has("loaded_through") || query.has("loaded_newer");
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

/**
 * The timeline, read from wherever it starts.
 *
 * Opened at the head it reads one way, older, the way it always has. Opened
 * at an anchor -- the archive's chosen day -- it reads both ways: the rows on
 * and before the day, then the first page after it, so the day sits with what
 * followed it above and what preceded it below, and either end loads more as
 * the reader nears it. The rules are the same in both directions: one
 * frontier cursor each, the same duplicate and cycle checks, the same
 * progress written to the URL (`loaded_through` older, `loaded_newer` newer)
 * so a back navigation replays exactly the pages that were on screen.
 *
 * What is different about prepending is only the scroll: content inserted
 * above the viewport would push what the reader is looking at down the page,
 * so the row at the top is measured before a page lands and the window is
 * moved by whatever it moved. Browsers with scroll anchoring do the same on
 * their own -- except at the very top of the page, which is exactly where a
 * reader is when they ask for more above -- and the measurement here reads
 * their adjustment back as zero, so the two never add up.
 */
export function TimelinePage({
  api,
  site,
  initialSearch,
  emptyContext = "timeline",
  onSiteChanged,
  canPublish = false,
}: {
  api: PublicApi;
  site: SiteIdentity;
  /**
   * The window, anchor, cursor and progress the list is read with, read once.
   * Not necessarily the page's own URL: the archive hands in an anchor it
   * derived from its position.
   */
  initialSearch: string;
  emptyContext?: "timeline" | "archive";
  onSiteChanged?: (site: SiteIdentity) => void;
  canPublish?: boolean;
}) {
  const {
    navigate,
    pathname,
    search: liveSearch,
    replaceTimelineProgress,
    restoreCurrentEntry,
  } = useNavigation();
  const [posts, setPosts] = useState<PublicPost[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [prevCursor, setPrevCursor] = useState<string | null>(null);
  const [initialError, setInitialError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [newerError, setNewerError] = useState<unknown>(null);
  const [loadingInitial, setLoadingInitial] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [loadingNewer, setLoadingNewer] = useState(false);
  // The first page is on screen and the window is where it should be: only
  // then may the top frontier ask for more, or it would fire at the top of
  // an unplaced page and read the archive forward to its end.
  const [settled, setSettled] = useState(false);
  const [freshId, setFreshId] = useState<string | null>(null);
  const initialSearchRef = useRef(initialSearch);
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const activeNewer = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const loadingNewerRef = useRef(false);
  const initialEntryRestored = useRef(false);
  const postChanges = useRef(new Map<string, PostChangeEvent>());
  const visitedCursors = useRef(new Set<string>());
  /** The row the list opened at: the newest on or before the anchor. */
  const anchorKey = useRef<string | null>(null);
  /** The top row and where it was, measured just before a page is prepended. */
  const compensation = useRef<{ key: string; top: number } | null>(null);

  const reconcilePostChanges = useCallback((items: PublicPost[]) => {
    let reconciled = items;
    for (const event of postChanges.current.values()) {
      reconciled = reconcileTimelineEvent(reconciled, event);
    }
    return reconciled;
  }, []);

  const handlePostChange = useCallback((event: PostChangeEvent) => {
    postChanges.current.set(event.post.id, event);
    setPosts((current) => {
      if (event.kind === "created") {
        setFreshId(event.post.id);
        window.setTimeout(() => setFreshId(null), 700);
      }
      return reconcileTimelineEvent(current, event);
    });
  }, []);

  const handleReposted = useCallback(
    (post: PublicPost) => handlePostChange({ kind: "reordered", post }),
    [handlePostChange],
  );

  const loadInitial = useCallback(async () => {
    activeInitial.current?.abort();
    const controller = new AbortController();
    activeInitial.current = controller;
    setLoadingInitial(true);
    setInitialError(null);
    setMoreError(null);
    setNewerError(null);
    try {
      const { loadedThrough, loadedNewer, request } = requestFromSearch(
        initialSearchRef.current,
      );
      let page = await api.timeline({
        ...request,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const opening = page;
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
          timelineItemKey,
          "timeline",
        );
      }

      // Anchored, the list also reads forward: the first page after the
      // anchor always, and then whatever pages a back navigation had on
      // screen. The head of the timeline has nothing after it worth asking
      // for, so its prev_cursor is left alone.
      let newerCursor: string | null = null;
      if (request.anchor) {
        newerCursor = opening.prevCursor;
        let requestedNewer: string | null = null;
        let newer: PublicPost[] = [];
        for (
          let replayed = 0;
          newerCursor !== null &&
          (requestedNewer === null ||
            (loadedNewer !== null && requestedNewer !== loadedNewer));
          replayed += 1
        ) {
          if (replayed >= MAX_REPLAY_PAGES) {
            throw invalidCursorProgress("loaded timeline cursor is too deep");
          }
          if (visited.has(newerCursor)) {
            throw invalidCursorProgress("timeline cursor cycle detected");
          }
          visited.add(newerCursor);
          requestedNewer = newerCursor;
          const newerPage: TimelinePageData = await api.timeline({
            cursor: newerCursor,
            signal: controller.signal,
          });
          if (controller.signal.aborted) return;
          validateNewerPage(newerPage, visited, "timeline");
          newer = prependUnique(
            newer,
            newerPage.items,
            timelineItemKey,
            "timeline",
          );
          newerCursor = newerPage.prevCursor;
        }
        if (loadedNewer !== null && requestedNewer !== loadedNewer) {
          throw invalidCursorProgress("loaded timeline cursor is unreachable");
        }
        combined = prependUnique(combined, newer, timelineItemKey, "timeline");
      }

      combined = reconcilePostChanges(combined);
      visitedCursors.current = visited;
      anchorKey.current =
        request.anchor && opening.items[0]
          ? timelineItemKey(opening.items[0])
          : null;
      setPosts(combined);
      setNextCursor(page.nextCursor);
      setPrevCursor(newerCursor);
    } catch (error) {
      if (!controller.signal.aborted) setInitialError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingInitial(false);
    }
  }, [api, reconcilePostChanges]);

  useEffect(() => {
    void loadInitial();
    return () => {
      activeInitial.current?.abort();
      activeMore.current?.abort();
      activeNewer.current?.abort();
    };
  }, [loadInitial]);

  useEffect(() => listenForPostChanges(handlePostChange), [handlePostChange]);

  const createdFromTimeline = useCallback((comment: PublicComment) => {
    const increment = (post: PublicPost) =>
      post.id === comment.postId
        ? { ...post, commentCount: post.commentCount + 1 }
        : post;
    setPosts((current) => current.map(increment));
  }, []);
  const { openReply, replyDialog } = usePostReplyDialog({
    api,
    onCreated: createdFromTimeline,
    site,
  });

  // The anchor row goes just under the shell header, with what followed it
  // already above: where a jump to a day lands, and the only way "scroll up
  // for what came after" can be true from the first frame.
  const placeAnchor = useCallback(() => {
    const key = anchorKey.current;
    const row = key ? rowElement(key) : undefined;
    if (row) {
      withRowsRendered(() => {
        const header = document.querySelector<HTMLElement>(
          "#main-content > header",
        );
        window.scrollTo({
          top:
            row.getBoundingClientRect().top +
            window.scrollY -
            (header?.offsetHeight ?? 0),
          behavior: "auto",
        });
      });
    }
    setSettled(true);
  }, []);

  useEffect(() => {
    if (loadingInitial || initialError || initialEntryRestored.current) {
      return;
    }
    initialEntryRestored.current = true;
    const placed = restoreCurrentEntry(
      anchorKey.current ? placeAnchor : undefined,
    );
    // Restoration scrolls on the next frame; the top frontier may look only
    // after that, or it would see itself at the top of an unscrolled page.
    if (!placed) window.requestAnimationFrame(() => setSettled(true));
  }, [initialError, loadingInitial, placeAnchor, restoreCurrentEntry]);

  // A prepended page moved everything below it: move the window by the same
  // amount before the frame is painted, so the row the reader was looking at
  // stays where it was.
  useLayoutEffect(() => {
    const pending = compensation.current;
    if (!pending) return;
    compensation.current = null;
    const row = rowElement(pending.key);
    if (!row) return;
    withRowsRendered(() => {
      const delta = row.getBoundingClientRect().top - pending.top;
      if (delta !== 0) window.scrollBy({ top: delta, behavior: "auto" });
    });
  }, [posts]);

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
        anchor: null,
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateCursorPage(page, requestedCursor, visited, "timeline");
      const firstNew = page.items[0]
        ? timelineItemKey(page.items[0])
        : undefined;
      const combined = appendUnique(
        posts,
        page.items,
        timelineItemKey,
        "timeline",
      );
      visitedCursors.current = visited;
      setPosts(combined);
      setNextCursor(page.nextCursor);
      replaceTimelineProgress(requestedCursor);
      if (source === "manual" && firstNew) {
        window.requestAnimationFrame(() => {
          rowElement(firstNew)?.focus({ preventScroll: true });
        });
      }
    } catch (error) {
      if (!controller.signal.aborted) setMoreError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingMore(false);
      loadingMoreRef.current = false;
    }
  };

  const loadNewer = async (source: LoadSource = "manual") => {
    if (!prevCursor || loadingNewerRef.current) return;
    const controller = new AbortController();
    activeNewer.current = controller;
    loadingNewerRef.current = true;
    setLoadingNewer(true);
    setNewerError(null);
    const requestedCursor = prevCursor;
    try {
      if (visitedCursors.current.has(requestedCursor)) {
        throw invalidCursorProgress("timeline cursor cycle detected");
      }
      const page = await api.timeline({
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateNewerPage(page, visited, "timeline");
      // The oldest of the new rows is the one nearest what was on screen.
      const nearestNew = page.items[page.items.length - 1]
        ? timelineItemKey(page.items[page.items.length - 1])
        : undefined;
      const combined = prependUnique(
        posts,
        page.items,
        timelineItemKey,
        "timeline",
      );
      const top = posts[0] ? rowElement(timelineItemKey(posts[0])) : undefined;
      if (top && page.items.length > 0) {
        compensation.current = {
          key: timelineItemKey(posts[0]),
          top: top.getBoundingClientRect().top,
        };
      }
      visitedCursors.current = visited;
      setPosts(combined);
      setPrevCursor(page.prevCursor);
      replaceTimelineProgress(requestedCursor, "loaded_newer");
      if (source === "manual" && nearestNew) {
        window.requestAnimationFrame(() => {
          rowElement(nearestNew)?.focus({ preventScroll: true });
        });
      }
    } catch (error) {
      if (!controller.signal.aborted) setNewerError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingNewer(false);
      loadingNewerRef.current = false;
    }
  };

  const resetFromFirstPage = () => {
    initialSearchRef.current = withoutLoadedProgress(initialSearchRef.current);
    visitedCursors.current = new Set();
    setMoreError(null);
    setNewerError(null);
    // The page's own address minus the progress, not the window the list was
    // read with: on the archive the two differ, and navigating to the window
    // would be a link to nowhere.
    navigate(`${pathname}${withoutLoadedProgress(liveSearch)}`);
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
  if (posts.length === 0 && !nextCursor && !prevCursor) {
    return <PublicEmpty context={emptyContext} canPublish={canPublish} />;
  }

  return (
    <>
      <section aria-label="公开时间流" className={styles.timelineList}>
        <p aria-live="polite" className={styles.srOnly}>
          已加载 {posts.length} 条 Moment
        </p>
        {newerError ? (
          <div className={styles.moreError} role="alert">
            <span>
              {errorKind(newerError) === "malformed"
                ? "更新的一页数据不完整，没有加入时间流。"
                : "更新的一页没有加载完成。"}
            </span>
            {recoverableCursorError(newerError) ? (
              <button onClick={resetFromFirstPage} type="button">
                从这一天重新加载
              </button>
            ) : (
              <button onClick={() => void loadNewer("manual")} type="button">
                重试加载
              </button>
            )}
          </div>
        ) : null}
        {prevCursor && settled && !newerError ? (
          <LoadMoreSentinel
            className={styles.moreRow}
            cursor={prevCursor}
            label="加载更新的 Moment"
            loading={loadingNewer}
            onLoad={(source) => void loadNewer(source)}
          />
        ) : null}
        {posts.map((post, index) => (
          <PostPresentation
            eagerVideo={index === 0}
            fresh={post.id === freshId}
            key={timelineItemKey(post)}
            canPublish={canPublish}
            onReply={(event) => openReply(post, event)}
            onReposted={handleReposted}
            onSiteChanged={onSiteChanged}
            post={post}
            site={site}
            timelineDensity
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
      {replyDialog}
    </>
  );
}
