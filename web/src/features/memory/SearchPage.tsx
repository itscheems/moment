import { useTranslation } from "react-i18next";
import { BackControl } from "../../components/BackControl";
import {
  type ReactNode,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  type PublicComment,
  type SearchHit,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { useReadRecovery } from "../../components/useReadRecovery";
import { ReadRecoveryToast } from "../timeline/PublicStates";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import { usePostReplyDialog } from "../comments/usePostReplyDialog";
import { PostPresentation } from "../posts/PostPresentation";
import {
  appendUnique,
  invalidCursorProgress,
  isCursorProgressError,
  validateCursorPage,
} from "../timeline/cursorContinuity";
import { parseSearchLocation, searchHref } from "./memoryState";
import { SearchBox } from "./RailSearch";
import { PeopleSearchResults } from "./PeopleSearchResults";
import styles from "./Memory.module.css";

const MAX_REPLAY_PAGES = 1_000;

function SearchMessage({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: ReactNode;
}) {
  return (
    <section className={styles.message} role="status">
      <div>
        <h2>{title}</h2>
        <p>{detail}</p>
        {action}
      </div>
    </section>
  );
}

function ContentSearchResults({
  api,
  canPublish,
  search,
  site,
  onSiteChanged,
}: {
  api: PublicApi;
  canPublish: boolean;
  search: string;
  site: SiteIdentity;
  onSiteChanged?: (site: SiteIdentity) => void;
  recentScope?: string | null;
}) {
  const { t } = useTranslation();

  const location = useNavigation();
  const stateRef = useRef(parseSearchLocation(search));
  const state = stateRef.current;
  const savedProgress = useRef(state.loadedThrough);
  const { replaceWithinPage } = location;
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(Boolean(state.query && !state.error));
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [moreError, setMoreError] = useState<unknown>(null);
  const activeRequest = useRef<AbortController | null>(null);
  const activeMore = useRef<AbortController | null>(null);
  const loadingMoreRef = useRef(false);
  const entryRestored = useRef(false);
  const visitedCursors = useRef(new Set<string>());
  const recoveredCursors = useRef(new Set<string>());
  const createdFromSearch = useCallback((comment: PublicComment) => {
    setHits((current) =>
      current.map((hit) =>
        hit.post.id === comment.postId
          ? {
              ...hit,
              post: {
                ...hit.post,
                commentCount: hit.post.commentCount + 1,
              },
            }
          : hit,
      ),
    );
  }, []);
  const { openReply, replyDialog } = usePostReplyDialog({
    api,
    onCreated: createdFromSearch,
    site,
  });

  const forgetProgress = useCallback(() => {
    savedProgress.current = null;
    if (window.location.pathname === "/search")
      replaceWithinPage(
        searchHref({
          query: state.query ?? "",
          from: state.from,
          to: state.to,
        }),
      );
  }, [replaceWithinPage, state]);

  const load: () => Promise<void> = useCallback(async () => {
    if (!state.query || state.error) return;
    activeRequest.current?.abort();
    activeMore.current?.abort();
    loadingMoreRef.current = false;
    setLoadingMore(false);
    const controller = new AbortController();
    activeRequest.current = controller;
    setLoading(true);
    setError(null);
    setMoreError(null);
    try {
      let page = await api.search({
        sort: "latest",
        query: state.query,
        from: state.from,
        to: state.to,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      let combined = page.items;
      let requestedCursor: string | null = null;
      const visited = new Set<string>();
      validateCursorPage(page, requestedCursor, visited, "search");
      for (
        let replayed = 0;
        savedProgress.current !== null &&
        requestedCursor !== savedProgress.current;
        replayed += 1
      ) {
        if (replayed >= MAX_REPLAY_PAGES) {
          forgetProgress();
          break;
        }
        const continuation = page.nextCursor;
        if (!continuation) {
          forgetProgress();
          break;
        }
        if (visited.has(continuation)) {
          throw invalidCursorProgress("search cursor cycle detected");
        }
        visited.add(continuation);
        requestedCursor = continuation;
        page = await api.search({
          sort: "latest",
          query: state.query,
          from: state.from,
          to: state.to,
          cursor: continuation,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        validateCursorPage(page, requestedCursor, visited, "search");
        combined = appendUnique(combined, page.items, (item) => item.post.id);
      }
      visitedCursors.current = visited;
      setHits(combined);

      setNextCursor(page.nextCursor);
    } catch (requestError) {
      if (controller.signal.aborted) return;
      if (
        savedProgress.current !== null &&
        isCursorProgressError(requestError)
      ) {
        forgetProgress();
        await load();
      } else setError(requestError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, state, forgetProgress]);

  useEffect(() => {
    void load();
    return () => {
      activeRequest.current?.abort();
      activeMore.current?.abort();
    };
  }, [load]);

  useEffect(() => {
    if (!state.query || loading || error || entryRestored.current) {
      return;
    }
    entryRestored.current = true;
    location.restoreCurrentEntry();
  }, [error, loading, location, state.query]);

  const loadMore = async (source: LoadSource = "manual") => {
    if (!state.query || !nextCursor || loadingMoreRef.current || loading)
      return;
    const controller = new AbortController();
    activeMore.current = controller;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    setMoreError(null);
    const requestedCursor = nextCursor;
    try {
      if (visitedCursors.current.has(requestedCursor)) {
        throw invalidCursorProgress("search cursor cycle detected");
      }
      const page = await api.search({
        sort: "latest",
        query: state.query,
        from: state.from,
        to: state.to,
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateCursorPage(page, requestedCursor, visited, "search");
      const firstNew = page.items[0]?.post.id;
      const combined = appendUnique(hits, page.items, (item) => item.post.id);
      visitedCursors.current = visited;
      setHits(combined);

      setNextCursor(page.nextCursor);
      location.replaceTimelineProgress(requestedCursor);
      if (source === "manual" && firstNew) {
        window.requestAnimationFrame(() => {
          document
            .querySelector<HTMLElement>(`[data-post-id="${firstNew}"]`)
            ?.focus({ preventScroll: true });
        });
      }
    } catch (requestError) {
      if (!controller.signal.aborted) setMoreError(requestError);
    } finally {
      if (!controller.signal.aborted) setLoadingMore(false);
      if (activeMore.current === controller) loadingMoreRef.current = false;
    }
  };

  const recoverMore = async () => {
    if (
      nextCursor &&
      isCursorProgressError(moreError) &&
      !recoveredCursors.current.has(nextCursor)
    ) {
      recoveredCursors.current.add(nextCursor);
      forgetProgress();
      await load();
    } else await loadMore("automatic");
  };
  const enabled = location.pathname === "/search";
  const recovering = useReadRecovery({
    error,
    loading,
    onRetry: load,
    enabled,
  });
  useReadRecovery({
    error: moreError,
    loading: loadingMore || loading,
    onRetry: recoverMore,
    enabled,
  });
  return (
    <div className={styles.memoryPage}>
      {state.error ? (
        <SearchMessage
          action={
            <InternalLink href="/search">{t("重新设置搜索")}</InternalLink>
          }
          detail={t("请重新输入搜索条件。")}
          title={t("搜索链接无效")}
        />
      ) : null}
      {loading && hits.length === 0 ? (
        <section
          aria-busy="true"
          aria-label={t("正在搜索")}
          className={styles.loading}
        >
          <span>{t("正在搜索…")}</span>
        </section>
      ) : null}
      {!loading && error && recovering ? (
        <ReadRecoveryToast active={enabled}>
          {t("正在重新获取搜索结果…")}
        </ReadRecoveryToast>
      ) : null}
      {loading && hits.length > 0 ? (
        <ReadRecoveryToast active={enabled}>
          {t("正在更新搜索结果…")}
        </ReadRecoveryToast>
      ) : null}
      {!loading && error && !recovering ? (
        <SearchMessage
          detail={t("请调整搜索词后重试。")}
          title={t("无法使用这些搜索条件")}
        />
      ) : null}

      {!state.error &&
      state.query &&
      (hits.length > 0 || (!loading && !error)) ? (
        <>
          {hits.length === 0 ? (
            <SearchMessage
              action={
                <div className={styles.messageActions}>
                  <button
                    onClick={() => {
                      document
                        .querySelector<HTMLInputElement>("#moment-search-input")
                        ?.focus();
                    }}
                    type="button"
                  >
                    {t("修改搜索")}
                  </button>
                </div>
              }
              detail={t("换一个关键词再试。")}
              title={t("没有找到 Moment")}
            />
          ) : (
            <section aria-label={t("搜索结果")} className={styles.resultList}>
              {hits.map((hit) => (
                <PostPresentation
                  searchQuery={state.query!}
                  onReply={(event) => openReply(hit.post, event)}
                  onSiteChanged={onSiteChanged}
                  key={hit.post.id}
                  canPublish={canPublish}
                  post={hit.post}
                  site={site}
                />
              ))}
              {moreError ? (
                <ReadRecoveryToast active={enabled} error={moreError}>
                  {t("正在重新获取后续结果…")}
                </ReadRecoveryToast>
              ) : null}
              {nextCursor && !moreError ? (
                <LoadMoreSentinel
                  className={styles.moreRow}
                  cursor={nextCursor}
                  label={t("加载更早的结果")}
                  loading={loadingMore || loading}
                  onLoad={(source) => void loadMore(source)}
                />
              ) : !nextCursor && !moreError && hits.length > 0 ? (
                <p className={styles.endMark}>{t("已显示全部 Moment")}</p>
              ) : null}
            </section>
          )}
        </>
      ) : null}
      {replyDialog}
    </div>
  );
}

export function SearchPage(props: {
  api: PublicApi;
  canPublish: boolean;
  search: string;
  site: SiteIdentity;
  onSiteChanged?: (site: SiteIdentity) => void;
  recentScope?: string | null;
}) {
  const { t } = useTranslation();
  const location = useNavigation();
  const state = parseSearchLocation(props.search);
  const query = state.query ?? "";
  return (
    <>
      <header className={styles.searchTop}>
        <h1 className={styles.searchTitle}>{t("搜索")}</h1>
        <div className={styles.searchHeading}>
          <BackControl>
            <button
              aria-label={t("返回时间流")}
              className={styles.searchBack}
              type="button"
              onClick={() => location.goBack("/")}
            />
          </BackControl>
          <SearchBox
            api={props.api}
            scope={props.recentScope ?? null}
            initialValue={query}
            tab={state.tab}
            main
            autoFocus={!state.error && !query}
          />
        </div>
        <nav className={styles.searchTabs} aria-label={t("搜索类型")}>
          <InternalLink
            replace
            href={query ? searchHref({ query }) : "/search"}
            aria-current={state.tab === "latest" ? "page" : undefined}
          >
            <span className={styles.searchTabLabel}>{t("最新")}</span>
          </InternalLink>
          <InternalLink
            replace
            href={
              query ? searchHref({ query, tab: "people" }) : "/search?f=user"
            }
            aria-current={state.tab === "people" ? "page" : undefined}
          >
            <span className={styles.searchTabLabel}>{t("用户")}</span>
          </InternalLink>
        </nav>
      </header>
      {state.tab === "people" && !state.error ? (
        <PeopleSearchResults
          api={props.api}
          query={query}
          scope={props.recentScope ?? null}
        />
      ) : (
        <ContentSearchResults {...props} />
      )}
    </>
  );
}
