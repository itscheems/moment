import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
  type TimelinePage as TimelinePageData,
  type TimelineRequest,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { useReadRecovery } from "../../components/useReadRecovery";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import { usePostReplyDialog } from "../comments/usePostReplyDialog";
import { PostPresentation } from "../posts/PostPresentation";
import {
  PublicEmpty,
  PublicError,
  ReadRecoveryNotice,
  TimelineLoading,
} from "./PublicStates";
import { listenForPostChanges } from "../posts/postEvents";
import type { PostChangeEvent } from "../posts/postEvents";
import {
  appendUnique,
  invalidCursorProgress,
  isCursorProgressError,
  loadedThroughFromSearch,
  prependUnique,
  validateCursorPage,
  validateNewerPage,
  withoutLoadedProgress,
} from "./cursorContinuity";
import { reconcileTimelineEvent, timelineItemKey } from "./timelineItems";
import styles from "./TimelinePage.module.css";
import { rowElement, withRowsRendered } from "./scrollAnchor";
import {
  scrollToArchiveRow,
  type ArchiveJumpDirection,
} from "../memory/archiveMotion";

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
  archiveJumpDirection = null,
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
  archiveJumpDirection?: ArchiveJumpDirection;
}) {
  const {
    pathname,
    replaceTimelineProgress,
    replaceWithinPage,
    restoreCurrentEntry,
    archiveAnchor,
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
  const surfacePath = useRef(pathname);
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const activeNewer = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const loadingNewerRef = useRef(false);
  const initialEntryRestored = useRef(false);
  const postChanges = useRef(new Map<string, PostChangeEvent>());
  const visitedCursors = useRef(new Set<string>());
  const recoveredCursors = useRef(new Set<string>());
  /** The row the list opened at: the newest on or before the anchor. */
  const anchorKey = useRef<string | null>(null);
  const listRef = useRef<HTMLElement | null>(null);
  const savedAnchor = useRef(archiveAnchor);
  const stopArchiveJump = useRef<(() => void) | null>(null);
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

  const forgetProgress = useCallback(
    (dropCursor = false) => {
      const clean = (search: string) => {
        const query = new URLSearchParams(withoutLoadedProgress(search));
        if (dropCursor) query.delete("cursor");
        const value = query.toString();
        return value ? `?${value}` : "";
      };
      initialSearchRef.current = clean(initialSearchRef.current);
      const ownPath = surfacePath.current;
      if (window.location.pathname === ownPath)
        replaceWithinPage(`${ownPath}${clean(window.location.search)}`);
    },
    [replaceWithinPage],
  );

  const loadInitial: () => Promise<void> = useCallback(async () => {
    activeInitial.current?.abort();
    activeMore.current?.abort();
    activeNewer.current?.abort();
    loadingMoreRef.current = false;
    loadingNewerRef.current = false;
    setLoadingMore(false);
    setLoadingNewer(false);
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
      const savedQuery = new URLSearchParams(initialSearchRef.current);
      let discardedProgress =
        (savedQuery.has("loaded_through") && loadedThrough === null) ||
        (savedQuery.has("loaded_newer") &&
          (loadedNewer === null || !request.anchor));
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
          discardedProgress = true;
          break;
        }
        const continuation = page.nextCursor;
        if (!continuation) {
          discardedProgress = true;
          break;
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
        combined = appendUnique(combined, page.items, timelineItemKey);
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
            discardedProgress = true;
            break;
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
          newer = prependUnique(newer, newerPage.items, timelineItemKey);
          newerCursor = newerPage.prevCursor;
        }
        if (loadedNewer !== null && requestedNewer !== loadedNewer) {
          discardedProgress = true;
        }
        combined = prependUnique(combined, newer, timelineItemKey);
      }

      if (discardedProgress) forgetProgress();
      combined = reconcilePostChanges(combined);
      visitedCursors.current = visited;
      anchorKey.current =
        (request.anchor || archiveJumpDirection) && opening.items[0]
          ? timelineItemKey(opening.items[0])
          : null;
      setPosts(combined);
      setNextCursor(page.nextCursor);
      setPrevCursor(newerCursor);
    } catch (error) {
      if (controller.signal.aborted) return;
      const query = new URLSearchParams(initialSearchRef.current);
      if (
        isCursorProgressError(error) &&
        (hasLoadedProgress(initialSearchRef.current) || query.has("cursor"))
      ) {
        forgetProgress(true);
        await loadInitial();
      } else setInitialError(error);
    } finally {
      if (!controller.signal.aborted) setLoadingInitial(false);
    }
  }, [api, archiveJumpDirection, forgetProgress, reconcilePostChanges]);

  useEffect(() => {
    void loadInitial();
    return () => {
      activeInitial.current?.abort();
      activeMore.current?.abort();
      activeNewer.current?.abort();
      stopArchiveJump.current?.();
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
    const list = listRef.current;
    const row = key && list ? rowElement(key, list) : undefined;
    if (row && list) {
      withRowsRendered(() => {
        stopArchiveJump.current = scrollToArchiveRow(
          row,
          list,
          archiveJumpDirection,
          (arrived) => {
            if (arrived) row.dataset.archiveTarget = "true";
            setSettled(true);
          },
        );
      }, list);
    } else {
      setSettled(true);
    }
  }, [archiveJumpDirection]);

  useEffect(() => {
    if (loadingInitial || initialError || initialEntryRestored.current) {
      return;
    }
    initialEntryRestored.current = true;
    let placed = false;
    const restore = () => {
      const saved = savedAnchor.current;
      const row =
        saved && listRef.current
          ? rowElement(saved.key, listRef.current)
          : null;
      if (saved && row) {
        window.scrollTo({
          top: row.getBoundingClientRect().top + window.scrollY - saved.top,
          behavior: "auto",
        });
        return;
      }
      placed = restoreCurrentEntry(anchorKey.current ? placeAnchor : undefined);
    };
    // A saved scroll offset refers to laid-out rows, not their off-screen
    // placeholders. Restore in the same geometry used for a fresh date jump.
    if ((anchorKey.current || savedAnchor.current) && listRef.current) {
      withRowsRendered(restore, listRef.current);
    } else {
      restore();
    }
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
    if (!nextCursor || loadingMoreRef.current || loadingInitial) return;
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
      const combined = appendUnique(posts, page.items, timelineItemKey);
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
      if (activeMore.current === controller) loadingMoreRef.current = false;
    }
  };

  const loadNewer = async (source: LoadSource = "manual") => {
    if (!prevCursor || loadingNewerRef.current || loadingInitial) return;
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
      const combined = prependUnique(posts, page.items, timelineItemKey);
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
      if (activeNewer.current === controller) loadingNewerRef.current = false;
    }
  };

  const recoverContinuation = async (
    error: unknown,
    cursor: string | null,
    newer = false,
  ) => {
    if (
      cursor &&
      isCursorProgressError(error) &&
      !recoveredCursors.current.has(cursor)
    ) {
      recoveredCursors.current.add(cursor);
      const visible = posts.find((post) => {
        const box = rowElement(timelineItemKey(post))?.getBoundingClientRect();
        return box && box.bottom > 0 && box.top < window.innerHeight;
      });
      if (visible) {
        const key = timelineItemKey(visible);
        const row = rowElement(key);
        if (row)
          compensation.current = { key, top: row.getBoundingClientRect().top };
      }
      forgetProgress(true);
      await loadInitial();
    } else if (newer) await loadNewer("automatic");
    else await loadMore("automatic");
  };

  const enabled = pathname === surfacePath.current;
  const recoveringInitial = useReadRecovery({
    error: initialError,
    loading: loadingInitial,
    onRetry: loadInitial,
    enabled,
  });
  useReadRecovery({
    error: moreError,
    loading: loadingMore || loadingInitial,
    onRetry: () => recoverContinuation(moreError, nextCursor),
    enabled,
  });
  useReadRecovery({
    error: newerError,
    loading: loadingNewer || loadingInitial,
    onRetry: () => recoverContinuation(newerError, prevCursor, true),
    enabled,
  });

  if (loadingInitial && posts.length === 0) return <TimelineLoading />;
  if (initialError && (!recoveringInitial || posts.length === 0)) {
    return <PublicError recovering={recoveringInitial} />;
  }
  if (posts.length === 0 && !nextCursor && !prevCursor) {
    return <PublicEmpty context={emptyContext} canPublish={canPublish} />;
  }

  return (
    <>
      <section
        aria-label="公开时间流"
        className={styles.timelineList}
        ref={listRef}
      >
        {posts.length > 0 && (initialError || loadingInitial) ? (
          <ReadRecoveryNotice />
        ) : null}
        <p aria-live="polite" className={styles.srOnly}>
          已加载 {posts.length} 条 Moment
        </p>
        {newerError ? (
          <ReadRecoveryNotice error={newerError}>
            正在重新获取更新的内容…
          </ReadRecoveryNotice>
        ) : null}
        {prevCursor && !newerError ? (
          <LoadMoreSentinel
            className={styles.moreRow}
            cursor={prevCursor}
            enabled={settled}
            label="加载更新的 Moment"
            loading={loadingNewer || loadingInitial}
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
          <ReadRecoveryNotice error={moreError}>
            正在重新获取后续内容…
          </ReadRecoveryNotice>
        ) : null}
        {nextCursor && !moreError ? (
          <LoadMoreSentinel
            className={styles.moreRow}
            cursor={nextCursor}
            enabled={archiveJumpDirection === null || settled}
            label="加载更早的 Moment"
            loading={loadingMore || loadingInitial}
            onLoad={(source) => void loadMore(source)}
          />
        ) : !nextCursor && !moreError ? (
          <p className={styles.endMark}>已经走到这段时间流的起点</p>
        ) : null}
      </section>
      {replyDialog}
    </>
  );
}
