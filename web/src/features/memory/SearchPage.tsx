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
  PublicApiError,
  type SearchHit,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { formatMomentDate } from "../posts/dates";
import { PostPresentation } from "../posts/PostPresentation";
import { DatePicker } from "./DatePicker";
import {
  activeSearchYear,
  parseSearchLocation,
  searchHref,
} from "./memoryState";
import styles from "./Memory.module.css";

const MAX_REPLAY_PAGES = 1_000;

function malformed(message: string): PublicApiError {
  return new PublicApiError(message, {
    code: "API_RESPONSE_INVALID",
    status: 0,
  });
}

function sameYearCounts(
  left: Array<{ year: number; count: number }>,
  right: Array<{ year: number; count: number }>,
): boolean {
  return (
    left.length === right.length &&
    left.every(
      (item, index) =>
        item.year === right[index]?.year && item.count === right[index]?.count,
    )
  );
}

function appendHits(current: SearchHit[], incoming: SearchHit[]): SearchHit[] {
  const ids = new Set(current.map((item) => item.post.id));
  if (incoming.some((item) => ids.has(item.post.id))) {
    throw malformed("continued search page contains a duplicate post");
  }
  return [...current, ...incoming];
}

function validateCursorPage(
  page: { items: SearchHit[]; nextCursor: string | null },
  requestedCursor: string | null,
  visited: ReadonlySet<string>,
): void {
  if (page.items.length === 0 && page.nextCursor !== null) {
    throw malformed("empty search page advances the cursor");
  }
  if (page.nextCursor !== null && visited.has(page.nextCursor)) {
    throw malformed(
      page.nextCursor === requestedCursor
        ? "search cursor points to itself"
        : "search cursor cycle detected",
    );
  }
}

function highlightedSnippet(snippet: string, query: string): ReactNode {
  const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  if (!escaped) return snippet;
  const matcher = new RegExp(escaped, "giu");
  const parts: ReactNode[] = [];
  let cursor = 0;
  for (const match of snippet.matchAll(matcher)) {
    const index = match.index ?? 0;
    if (index > cursor) parts.push(snippet.slice(cursor, index));
    parts.push(<mark key={`${index}:${match[0]}`}>{match[0]}</mark>);
    cursor = index + match[0].length;
  }
  if (cursor === 0) return snippet;
  if (cursor < snippet.length) parts.push(snippet.slice(cursor));
  return parts;
}

function SearchMessage({
  marker,
  title,
  detail,
  action,
}: {
  marker: string;
  title: string;
  detail: string;
  action?: ReactNode;
}) {
  return (
    <section className={styles.message} role="status">
      <span>{marker}</span>
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
  owner,
  search,
  site,
  onYearsChange,
}: {
  api: PublicApi;
  owner: boolean;
  search: string;
  site: SiteIdentity;
  onYearsChange?: (years: number[]) => void;
}) {
  const location = useNavigation();
  const stateRef = useRef(parseSearchLocation(search));
  const state = stateRef.current;
  const [queryInput, setQueryInput] = useState(state.query ?? "");
  const [fromInput, setFromInput] = useState(state.from ?? "");
  const [toInput, setToInput] = useState(state.to ?? "");
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

  const load = useCallback(async () => {
    if (!state.query || state.error) return;
    activeRequest.current?.abort();
    activeMore.current?.abort();
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
      const aggregate = page.yearCounts;
      let requestedCursor: string | null = null;
      const visited = new Set<string>();
      validateCursorPage(page, requestedCursor, visited);
      for (
        let replayed = 0;
        state.loadedThrough !== null && requestedCursor !== state.loadedThrough;
        replayed += 1
      ) {
        if (replayed >= MAX_REPLAY_PAGES) {
          throw malformed("saved search position is too deep");
        }
        const continuation = page.nextCursor;
        if (!continuation || visited.has(continuation)) {
          throw malformed("saved search position is unreachable");
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
        if (!sameYearCounts(aggregate, page.yearCounts)) {
          throw malformed("search aggregate changed between cursor pages");
        }
        validateCursorPage(page, requestedCursor, visited);
        combined = appendHits(combined, page.items);
      }
      visitedCursors.current = visited;
      setHits(combined);
      setYearCounts(aggregate);
      setNextCursor(page.nextCursor);
    } catch (requestError) {
      if (!controller.signal.aborted) setError(requestError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, state]);

  useEffect(() => {
    void load();
    return () => {
      activeRequest.current?.abort();
      activeMore.current?.abort();
    };
  }, [load]);

  useEffect(() => {
    onYearsChange?.(yearCounts.map((item) => item.year));
  }, [onYearsChange, yearCounts]);

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
    if (fromInput && toInput && fromInput >= toInput) {
      setFormError("结束日需要晚于起始日。");
      return;
    }
    setFormError(null);
    location.navigate(
      searchHref({ query, from: fromInput || null, to: toInput || null }),
    );
  };

  const loadMore = async () => {
    if (!state.query || !nextCursor || loadingMoreRef.current) return;
    const controller = new AbortController();
    activeMore.current = controller;
    loadingMoreRef.current = true;
    setLoadingMore(true);
    setMoreError(null);
    const requestedCursor = nextCursor;
    try {
      if (visitedCursors.current.has(requestedCursor)) {
        throw malformed("search cursor cycle detected");
      }
      const page = await api.search({
        query: state.query,
        from: state.from,
        to: state.to,
        cursor: requestedCursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return;
      if (!sameYearCounts(yearCounts, page.yearCounts)) {
        throw malformed("search aggregate changed between cursor pages");
      }
      const visited = new Set(visitedCursors.current);
      visited.add(requestedCursor);
      validateCursorPage(page, requestedCursor, visited);
      const firstNew = page.items[0]?.post.id;
      const combined = appendHits(hits, page.items);
      visitedCursors.current = visited;
      setHits(combined);
      setNextCursor(page.nextCursor);
      location.replaceTimelineProgress(requestedCursor);
      if (firstNew) {
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
      loadingMoreRef.current = false;
    }
  };

  const activeYear = activeSearchYear(state.from, state.to);
  const total = yearCounts.reduce((sum, item) => sum + item.count, 0);
  const clearProgressHref = state.query
    ? searchHref({ query: state.query, from: state.from, to: state.to })
    : "/search";
  const resetFromFirstPage = () => {
    stateRef.current = { ...state, loadedThrough: null, error: null };
    visitedCursors.current = new Set();
    location.navigate(clearProgressHref);
  };
  const continuedPageInvalid =
    moreError instanceof PublicApiError &&
    (moreError.code === "INVALID_ARGUMENT" ||
      moreError.code === "API_RESPONSE_INVALID");

  return (
    <div className={styles.memoryPage}>
      <form className={styles.searchForm} onSubmit={submit}>
        <label className={styles.queryField} htmlFor="moment-search-input">
          <span>搜索 Moment</span>
          <div className={styles.queryControl}>
            <Icon name="search" />
            <input
              aria-describedby="search-shortcut search-form-error"
              aria-keyshortcuts="/"
              aria-label="搜索 Moment"
              autoComplete="off"
              autoFocus={!state.query}
              id="moment-search-input"
              maxLength={1_000}
              onChange={(event) => setQueryInput(event.target.value)}
              placeholder="搜索"
              type="search"
              value={queryInput}
            />
          </div>
          <small id="search-shortcut">在页面空白处按 / 可立即搜索</small>
        </label>
        <div className={styles.dateFields}>
          <DatePicker
            id="moment-search-from"
            label="起始日（含）"
            onChange={setFromInput}
            value={fromInput}
          />
          <DatePicker
            align="end"
            id="moment-search-to"
            label="结束日（不含）"
            onChange={setToInput}
            value={toInput}
          />
          <button type="submit">搜索</button>
        </div>
        <p className={styles.formError} id="search-form-error" role="alert">
          {formError}
        </p>
      </form>

      {state.error ? (
        <SearchMessage
          action={<InternalLink href="/search">重新设置搜索</InternalLink>}
          detail={state.error}
          marker="链接无效"
          title="这些搜索条件无法还原"
        />
      ) : null}
      {loading ? (
        <section
          aria-busy="true"
          aria-label="正在搜索"
          className={styles.loading}
        >
          <span>正在沿时间流查找“{state.query}”…</span>
        </section>
      ) : null}
      {!loading && error ? (
        <SearchMessage
          action={
            <div className={styles.messageActions}>
              <button onClick={() => void load()} type="button">
                重试搜索
              </button>
              {state.loadedThrough ? (
                <button onClick={resetFromFirstPage} type="button">
                  从第一页重新加载
                </button>
              ) : null}
            </div>
          }
          detail={
            error instanceof PublicApiError &&
            error.code === "INVALID_ARGUMENT" &&
            state.loadedThrough
              ? "保存的分页位置已经失效；搜索词和日期仍保留，可以从第一页重新加载。"
              : error instanceof PublicApiError &&
                  error.code === "INVALID_ARGUMENT"
                ? "搜索词或日期范围不被服务器接受；条件仍保留在上方，可以直接修改。"
                : error instanceof PublicApiError &&
                    error.code === "API_RESPONSE_INVALID"
                  ? "服务器返回的数据不完整，没有显示可能错序的结果。"
                  : "检查网络后重试；搜索词和日期仍保留在当前链接中。"
          }
          marker="没有完成"
          title="没有找到结果"
        />
      ) : null}

      {!loading && !error && state.query ? (
        <>
          <section aria-label="搜索概览" className={styles.searchSummary}>
            <div>
              <p aria-live="polite" role="status">
                找到 <strong>{total}</strong> 条与“{state.query}”有关的 Moment
              </p>
              {state.from || state.to ? (
                <p className={styles.activeContext}>
                  日期：{state.from ?? "最早"} 至 {state.to ?? "现在"}
                  （结束日不含）
                </p>
              ) : null}
            </div>
            {state.from || state.to ? (
              <InternalLink href={searchHref({ query: state.query })}>
                清除日期
              </InternalLink>
            ) : null}
          </section>
          {yearCounts.length > 0 ? (
            <nav aria-label="按年份筛选搜索结果" className={styles.yearTrack}>
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
              detail="换一个更短的词，或清除日期范围后再试。当前条件不会被静默删除。"
              marker="0 条"
              title="这段时间里没有匹配"
            />
          ) : (
            <section aria-label="搜索结果" className={styles.resultList}>
              {hits.map((hit) => (
                <PostPresentation
                  context={
                    <p className={styles.snippet}>
                      <span>
                        匹配片段 ·{" "}
                        <time dateTime={hit.post.publishedAt}>
                          {
                            formatMomentDate(
                              hit.post.publishedAt,
                              site.timezone,
                            ).absolute
                          }
                        </time>
                      </span>
                      {highlightedSnippet(hit.snippet, state.query!)}
                    </p>
                  }
                  key={hit.post.id}
                  owner={owner}
                  post={hit.post}
                  site={site}
                />
              ))}
              {moreError ? (
                <div className={styles.moreError} role="alert">
                  <span>更早的结果没有加载完成，已显示的内容保持不变。</span>
                  {continuedPageInvalid ? (
                    <button onClick={resetFromFirstPage} type="button">
                      从第一页重新加载
                    </button>
                  ) : (
                    <button onClick={() => void loadMore()} type="button">
                      重试加载
                    </button>
                  )}
                </div>
              ) : null}
              {nextCursor && !moreError ? (
                <div className={styles.moreRow}>
                  <button
                    disabled={loadingMore}
                    onClick={() => void loadMore()}
                    type="button"
                  >
                    {loadingMore ? "正在加载…" : "加载更早的结果"}
                  </button>
                </div>
              ) : hits.length > 0 ? (
                <p className={styles.endMark}>已经看完这些搜索结果</p>
              ) : null}
            </section>
          )}
        </>
      ) : null}
    </div>
  );
}
