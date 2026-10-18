import {
  type FormEvent,
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
import { Icon } from "../../components/icons";
import { useReadRecovery } from "../../components/useReadRecovery";
import { ReadRecoveryNotice } from "../timeline/PublicStates";
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
import { DatePicker } from "./DatePicker";
import {
  activeSearchYear,
  nextDate,
  parseSearchLocation,
  previousDate,
  searchHref,
} from "./memoryState";
import styles from "./Memory.module.css";

const MAX_REPLAY_PAGES = 1_000;

function naturalDate(value: string | null, fallback: string): string {
  if (!value) return fallback;
  const [year, month, day] = value.split("-").map(Number);
  return `${year}年${month}月${day}日`;
}

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

export function SearchPage({
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
}) {
  const location = useNavigation();
  const stateRef = useRef(parseSearchLocation(search));
  const state = stateRef.current;
  const savedProgress = useRef(state.loadedThrough);
  const { replaceWithinPage } = location;
  const [queryInput, setQueryInput] = useState(state.query ?? "");
  const [fromInput, setFromInput] = useState(state.from ?? "");
  const [toInput, setToInput] = useState(
    state.to ? previousDate(state.to) : "",
  );
  const [filtersOpen, setFiltersOpen] = useState(
    Boolean(state.from || state.to),
  );
  const [formError, setFormError] = useState<string | null>(null);
  const [hits, setHits] = useState<SearchHit[]>([]);
  const [yearCounts, setYearCounts] = useState<
    Array<{ year: number; count: number }>
  >([]);
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
      setYearCounts(page.yearCounts);
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

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const query = queryInput.trim();
    if (!query) {
      setFormError("输入要找的文字。一个字也可以搜索。");
      return;
    }
    if (Array.from(query).length > 1_000) {
      setFormError("搜索词不能超过 1,000 个字符。");
      return;
    }
    if (fromInput && toInput && fromInput > toInput) {
      setFormError("结束日期不能早于开始日期。");
      return;
    }
    setFormError(null);
    location.navigate(
      searchHref({
        query,
        from: fromInput || null,
        to: toInput ? nextDate(toInput) : null,
      }),
    );
  };

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
      setYearCounts(page.yearCounts);
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

  const activeYear = activeSearchYear(state.from, state.to);
  const total = yearCounts.reduce((sum, item) => sum + item.count, 0);
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
  const filterLabel =
    fromInput || toInput
      ? `${naturalDate(fromInput || null, "最早")} 至 ${naturalDate(toInput || null, "现在")}`
      : "日期";
  const clearQuery = () => {
    setQueryInput("");
    setFormError(null);
    if (state.query || state.from || state.to || state.loadedThrough) {
      location.navigate("/search");
      return;
    }
    window.requestAnimationFrame(() => {
      document.querySelector<HTMLInputElement>("#moment-search-input")?.focus();
    });
  };

  return (
    <div className={styles.memoryPage}>
      <form className={styles.searchForm} onSubmit={submit}>
        <div className={styles.searchPrimary}>
          <div className={styles.queryField}>
            <label htmlFor="moment-search-input">搜索 Moment</label>
            <div className={styles.queryControl} data-search-control>
              <Icon name="search" />
              <input
                aria-describedby={
                  formError
                    ? "search-shortcut search-form-error"
                    : "search-shortcut"
                }
                aria-keyshortcuts="/"
                aria-label="搜索 Moment"
                autoComplete="off"
                autoFocus={!state.query}
                id="moment-search-input"
                maxLength={1_000}
                onChange={(event) => setQueryInput(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key !== "Escape") return;
                  event.preventDefault();
                  if (queryInput) {
                    clearQuery();
                  } else if (filtersOpen) {
                    setFiltersOpen(false);
                  } else {
                    event.currentTarget.blur();
                  }
                }}
                placeholder="搜索 Moment"
                type="search"
                value={queryInput}
              />
              {queryInput ? (
                <button
                  aria-label="清除搜索内容"
                  className={styles.searchClear}
                  onClick={clearQuery}
                  type="button"
                >
                  <Icon name="close" />
                </button>
              ) : null}
              <button className={styles.searchSubmit} type="submit">
                搜索
              </button>
            </div>
            <small id="search-shortcut">按 / 聚焦搜索</small>
          </div>
        </div>
        <div className={styles.filterBar}>
          <button
            aria-controls="search-date-filters"
            aria-expanded={filtersOpen}
            className={styles.filterToggle}
            onClick={() => setFiltersOpen((current) => !current)}
            type="button"
          >
            <Icon name="archive" />
            <span>{filterLabel}</span>
          </button>
          {fromInput || toInput ? (
            <button
              className={styles.clearFilter}
              onClick={() => {
                setFromInput("");
                setToInput("");
                if (state.query) {
                  location.navigate(searchHref({ query: state.query }));
                }
              }}
              type="button"
            >
              清除日期
            </button>
          ) : null}
        </div>
        <div
          className={styles.dateFields}
          hidden={!filtersOpen}
          id="search-date-filters"
        >
          <DatePicker
            id="moment-search-from"
            label="开始日期"
            onChange={setFromInput}
            value={fromInput}
          />
          <DatePicker
            align="end"
            id="moment-search-to"
            label="结束日期"
            onChange={setToInput}
            value={toInput}
          />
          <button type="submit">应用</button>
        </div>
        {formError ? (
          <p className={styles.formError} id="search-form-error" role="alert">
            {formError}
          </p>
        ) : null}
      </form>

      {state.error ? (
        <SearchMessage
          action={<InternalLink href="/search">重新设置搜索</InternalLink>}
          detail="请重新输入搜索条件。"
          title="搜索链接无效"
        />
      ) : null}
      {loading && hits.length === 0 ? (
        <section
          aria-busy="true"
          aria-label="正在搜索"
          className={styles.loading}
        >
          <span>正在搜索…</span>
        </section>
      ) : null}
      {!loading && error && recovering ? (
        <ReadRecoveryNotice>正在重新获取搜索结果…</ReadRecoveryNotice>
      ) : null}
      {loading && hits.length > 0 ? (
        <ReadRecoveryNotice>正在更新搜索结果…</ReadRecoveryNotice>
      ) : null}
      {!loading && error && !recovering ? (
        <SearchMessage
          detail="请检查搜索词和日期后调整条件。"
          title="无法使用这些搜索条件"
        />
      ) : null}

      {!state.query && !state.error ? (
        <p className={styles.searchHint}>按文字或日期查找 Moment</p>
      ) : null}

      {!state.error &&
      state.query &&
      (hits.length > 0 || (!loading && !error)) ? (
        <>
          <section aria-label="搜索概览" className={styles.searchSummary}>
            <div>
              <p aria-live="polite" role="status">
                找到 <strong>{total}</strong> 条与“{state.query}”有关的 Moment
              </p>
            </div>
            {state.from || state.to ? (
              <InternalLink href={searchHref({ query: state.query })}>
                清除日期
              </InternalLink>
            ) : null}
          </section>
          {yearCounts.length > 0 ? (
            <nav
              aria-label="按年份筛选搜索结果"
              className={styles.yearTrack}
              data-compact={yearCounts.length <= 2 ? "true" : undefined}
            >
              <InternalLink
                aria-current={
                  activeYear === null && !state.from && !state.to
                    ? "page"
                    : undefined
                }
                data-focus-key="search-year-all"
                href={searchHref({ query: state.query })}
              >
                <strong>全部</strong>
                <span>{total}</span>
              </InternalLink>
              {yearCounts.map((item) => (
                <InternalLink
                  aria-current={activeYear === item.year ? "page" : undefined}
                  data-focus-key={`search-year-${item.year}`}
                  href={searchHref({
                    query: state.query!,
                    from: `${item.year}-01-01`,
                    to: `${item.year + 1}-01-01`,
                  })}
                  key={item.year}
                >
                  <strong>{item.year}</strong>
                  <span>{item.count}</span>
                </InternalLink>
              ))}
            </nav>
          ) : null}
          {hits.length === 0 ? (
            <SearchMessage
              action={
                <div className={styles.messageActions}>
                  <button
                    onClick={() => {
                      setFromInput("");
                      setToInput("");
                      document
                        .querySelector<HTMLInputElement>("#moment-search-input")
                        ?.focus();
                    }}
                    type="button"
                  >
                    修改搜索
                  </button>
                  <InternalLink href="/archive">按年月浏览</InternalLink>
                </div>
              }
              detail="换一个词，或清除日期后再试。"
              title="没有找到 Moment"
            />
          ) : (
            <section aria-label="搜索结果" className={styles.resultList}>
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
                <ReadRecoveryNotice error={moreError}>
                  正在重新获取后续结果…
                </ReadRecoveryNotice>
              ) : null}
              {nextCursor && !moreError ? (
                <LoadMoreSentinel
                  className={styles.moreRow}
                  cursor={nextCursor}
                  label="加载更早的结果"
                  loading={loadingMore || loading}
                  onLoad={(source) => void loadMore(source)}
                />
              ) : !nextCursor && !moreError && hits.length > 0 ? (
                <p className={styles.endMark}>已显示全部 Moment</p>
              ) : null}
            </section>
          )}
        </>
      ) : null}
      {replyDialog}
    </div>
  );
}
