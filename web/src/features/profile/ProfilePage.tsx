import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import {
  SiteAvatar,
  SiteCover,
  siteHandle,
} from "../../components/SiteIdentity";
import {
  ExactTimeTooltip,
  ShareMenu,
} from "../../components/TimelineInteractions";
import { useOwner } from "../owner/OwnerProvider";
import { PostPresentation } from "../posts/PostPresentation";
import { formatMomentDate } from "../posts/dates";
import { PublicError, TimelineLoading } from "../timeline/PublicStates";
import {
  appendUnique,
  invalidCursorProgress,
  loadedThroughFromSearch,
  validateCursorPage,
  withoutLoadedProgress,
} from "../timeline/cursorContinuity";
import styles from "./ProfilePage.module.css";
import { ProfileEditor } from "./ProfileEditor";

const MAX_REPLAY_PAGES = 1_000;

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

export function ProfilePage({
  api,
  site,
  onSiteSaved,
}: {
  api: PublicApi;
  site: SiteIdentity;
  onSiteSaved: (site: SiteIdentity) => void;
}) {
  const owner = useOwner();
  const navigation = useNavigation();
  const [posts, setPosts] = useState<PublicPost[]>([]);
  const [pinnedPost, setPinnedPost] = useState<PublicPost | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loadingInitial, setLoadingInitial] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [initialError, setInitialError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const activeInitial = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const initialSearch = useRef(window.location.search);
  const entryRestored = useRef(false);
  const visitedCursors = useRef(new Set<string>());
  const title = site.displayName.trim() || site.title.trim() || "Moment";
  const handle = siteHandle(site);
  const initialized = site.instanceInitializedAt
    ? formatMomentDate(site.instanceInitializedAt, site.timezone)
    : null;
  const joined = site.instanceInitializedAt
    ? new Intl.DateTimeFormat("zh-CN", {
        year: "numeric",
        month: "long",
        timeZone: site.timezone,
      }).format(new Date(site.instanceInitializedAt))
    : null;

  const load = useCallback(async () => {
    activeInitial.current?.abort();
    activeMore.current?.abort();
    const controller = new AbortController();
    activeInitial.current = controller;
    setLoadingInitial(true);
    setInitialError(null);
    setMoreError(null);
    try {
      const loadedThrough = loadedThroughFromSearch(initialSearch.current);
      const [initialPage, pinned] = await Promise.all([
        api.timeline({ signal: controller.signal }),
        site.pinnedPostId
          ? api
              .post(site.pinnedPostId, controller.signal)
              .catch((nextError) => {
                if (
                  nextError instanceof PublicApiError &&
                  nextError.code === "POST_NOT_FOUND"
                ) {
                  return null;
                }
                throw nextError;
              })
          : Promise.resolve(null),
      ]);
      if (controller.signal.aborted) return;
      let page = initialPage;
      let combined = page.items;
      let requestedCursor: string | null = null;
      const visited = new Set<string>();
      validateCursorPage(page, requestedCursor, visited, "profile");

      for (
        let replayed = 0;
        loadedThrough !== null && requestedCursor !== loadedThrough;
        replayed += 1
      ) {
        if (replayed >= MAX_REPLAY_PAGES) {
          throw invalidCursorProgress("saved profile position is too deep");
        }
        const continuation = page.nextCursor;
        if (!continuation || visited.has(continuation)) {
          throw invalidCursorProgress("saved profile position is unreachable");
        }
        visited.add(continuation);
        requestedCursor = continuation;
        page = await api.timeline({
          cursor: continuation,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        validateCursorPage(page, requestedCursor, visited, "profile");
        combined = appendUnique(
          combined,
          page.items,
          (post) => post.id,
          "profile",
        );
      }
      visitedCursors.current = visited;
      setPosts(combined);
      setPinnedPost(pinned);
      setNextCursor(page.nextCursor);
    } catch (nextError) {
      if (!controller.signal.aborted) setInitialError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoadingInitial(false);
    }
  }, [api, site.pinnedPostId]);

  useEffect(() => {
    void load();
    return () => {
      activeInitial.current?.abort();
      activeMore.current?.abort();
    };
  }, [load]);

  useEffect(() => {
    if (loadingInitial || initialError || entryRestored.current) return;
    entryRestored.current = true;
    navigation.restoreCurrentEntry();
  }, [initialError, loadingInitial, navigation]);

  const more = async (source: LoadSource = "manual") => {
    if (!nextCursor || loadingMoreRef.current) return;
    const controller = new AbortController();
    activeMore.current = controller;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    setMoreError(null);
    const requestedCursor = nextCursor;
    try {
      if (visitedCursors.current.has(requestedCursor)) {
        throw invalidCursorProgress("profile cursor cycle detected");
      }
      const page = await api.timeline({
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateCursorPage(page, requestedCursor, visited, "profile");
      const firstNew = page.items[0]?.id;
      const combined = appendUnique(
        posts,
        page.items,
        (post) => post.id,
        "profile",
      );
      visitedCursors.current = visited;
      setPosts(combined);
      setNextCursor(page.nextCursor);
      navigation.replaceTimelineProgress(requestedCursor);
      if (source === "manual" && firstNew) {
        window.requestAnimationFrame(() => {
          document
            .querySelector<HTMLElement>(`[data-post-id="${firstNew}"]`)
            ?.focus({ preventScroll: true });
        });
      }
    } catch (nextError) {
      if (!controller.signal.aborted) setMoreError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoadingMore(false);
      loadingMoreRef.current = false;
    }
  };

  const resetFromFirstPage = () => {
    const search = withoutLoadedProgress(initialSearch.current);
    initialSearch.current = search;
    visitedCursors.current = new Set();
    setMoreError(null);
    navigation.navigate(`${navigation.pathname}${search}`);
    void load();
  };

  return (
    <section aria-label={`${title} 的个人资料`} className={styles.profile}>
      <div className={styles.cover}>
        <SiteCover eager site={site} />
      </div>
      <div className={styles.identity}>
        <div className={styles.avatar}>
          <SiteAvatar eager site={site} />
        </div>
        <div className={styles.profileActions}>
          <ShareMenu
            baseUrl={site.publicBaseUrl}
            href={`/@${site.username}`}
            title={`${title} 的个人资料`}
            trigger={
              <button
                aria-label="分享个人资料"
                title="分享个人资料"
                type="button"
              >
                <Icon name="share" />
              </button>
            }
          />
          {owner.state === "owner" ? (
            <div className={styles.profileEditorSlot}>
              <ProfileEditor onSaved={onSiteSaved} site={site} />
            </div>
          ) : null}
        </div>
        <h2>{title}</h2>
        <p className={styles.handle}>{handle}</p>
        {site.bio.trim() ? <p>{site.bio.trim()}</p> : null}
        <div className={styles.profileMeta}>
          {initialized && joined && site.instanceInitializedAt ? (
            <ExactTimeTooltip
              exact={initialized.absolute}
              children={
                <time dateTime={site.instanceInitializedAt}>
                  <Icon name="archive" /> {joined}加入
                </time>
              }
            />
          ) : null}
          <span>{site.timezone}</span>
          <InternalLink href="/archive">查看归档</InternalLink>
        </div>
        <nav aria-label="个人资料内容" className={styles.tabs}>
          <span aria-current="page">Moment</span>
        </nav>
      </div>

      {loadingInitial ? <TimelineLoading /> : null}
      {initialError ? (
        <PublicError
          kind={errorKind(initialError)}
          onResetProgress={
            new URLSearchParams(initialSearch.current).has("loaded_through") &&
            recoverableCursorError(initialError)
              ? resetFromFirstPage
              : undefined
          }
          onRetry={load}
        />
      ) : null}
      {!loadingInitial && !initialError ? (
        posts.length > 0 || pinnedPost ? (
          <div>
            {pinnedPost ? (
              <PostPresentation
                onDeleted={() => setPinnedPost(null)}
                onSiteChanged={onSiteSaved}
                owner={owner.state === "owner"}
                pinned
                post={pinnedPost}
                site={site}
              />
            ) : null}
            {posts
              .filter((post) => post.id !== pinnedPost?.id)
              .map((post) => (
                <PostPresentation
                  key={post.id}
                  onSiteChanged={onSiteSaved}
                  owner={owner.state === "owner"}
                  post={post}
                  site={site}
                />
              ))}
            {moreError ? (
              <div className={styles.moreError} role="alert">
                <span>
                  {errorKind(moreError) === "malformed"
                    ? "下一页数据不完整，已保留当前 Moment。"
                    : "下一页没有加载完成，已保留当前 Moment。"}
                </span>
                {recoverableCursorError(moreError) ? (
                  <button onClick={resetFromFirstPage} type="button">
                    从第一页重新加载
                  </button>
                ) : (
                  <button onClick={() => void more("manual")} type="button">
                    重试加载
                  </button>
                )}
              </div>
            ) : nextCursor ? (
              <LoadMoreSentinel
                className={styles.moreRow}
                cursor={nextCursor}
                label="加载更早的 Moment"
                loading={loadingMore}
                onLoad={(source) => void more(source)}
              />
            ) : null}
          </div>
        ) : (
          <p className={styles.empty}>还没有 Moment</p>
        )
      ) : null}
    </section>
  );
}
