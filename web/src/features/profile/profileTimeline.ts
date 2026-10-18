import { PublicApi, type PostKind, type PublicPost } from "../../api/public";
import {
  appendUnique,
  invalidCursorProgress,
  loadedThroughFromSearch,
  withoutLoadedProgress,
  isCursorProgressError,
  validateCursorPage,
  validateNewerPage,
} from "../timeline/cursorContinuity";
import {
  compareTimelineItems,
  reconcileTimelineEvent,
  timelineItemKey,
} from "../timeline/timelineItems";
import type { PostChangeEvent } from "../posts/postEvents";

function pageVisible(): boolean {
  return document.visibilityState !== "hidden";
}

export type ProfileFilter = "all" | PostKind;
export const profileFilters: ProfileFilter[] = ["all", "moment", "article"];
export function matchesFilter(
  post: PublicPost,
  filter: ProfileFilter,
): boolean {
  return filter === "all" || post.kind === filter;
}
export function mergeProfileItems(
  current: PublicPost[],
  incoming: PublicPost[],
): PublicPost[] {
  const items = new Map(current.map((post) => [timelineItemKey(post), post]));
  for (const post of incoming) items.set(timelineItemKey(post), post);
  return [...items.values()].sort(compareTimelineItems);
}
export interface ProfileFeed {
  items: PublicPost[];
  initialized: boolean;
  loading: boolean;
  loadingMore: boolean;
  nextCursor: string | null;
  refreshCursor: string | null;
  continuation: string | null;
  error: unknown;
  moreError: unknown;
  refreshError: unknown;
}
function emptyFeed(): ProfileFeed {
  return {
    items: [],
    initialized: false,
    loading: false,
    loadingMore: false,
    nextCursor: null,
    refreshCursor: null,
    continuation: null,
    error: null,
    moreError: null,
    refreshError: null,
  };
}

/** One profile visit, with independent older and newer frontiers per filter. */
export class ProfileTimeline {
  private feeds: Record<ProfileFilter, ProfileFeed> = {
    all: emptyFeed(),
    moment: emptyFeed(),
    article: emptyFeed(),
  };
  private listeners = new Set<() => void>();
  private requests = new Map<string, AbortController>();
  private generations: Record<ProfileFilter, number> = {
    all: 0,
    moment: 0,
    article: 0,
  };
  private visited: Record<ProfileFilter, Set<string>> = {
    all: new Set(),
    moment: new Set(),
    article: new Set(),
  };
  private events = new Map<string, PostChangeEvent>();
  private disposed = false;
  active: ProfileFilter = "all";
  beforeMerge?: () => void;
  onProgressDiscarded?: () => void;
  constructor(
    private api: PublicApi,
    private initialSearch: string,
  ) {}
  snapshot = () => this.feeds;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private set(filter: ProfileFilter, patch: Partial<ProfileFeed>) {
    if (this.disposed) return;
    this.feeds = {
      ...this.feeds,
      [filter]: { ...this.feeds[filter], ...patch },
    };
    for (const listener of this.listeners) listener();
  }
  disposeIfUnused() {
    if (this.listeners.size === 0) this.dispose();
  }
  dispose() {
    this.disposed = true;
    for (const controller of this.requests.values()) controller.abort();
    this.requests.clear();
  }
  private reconcile(items: PublicPost[], filter: ProfileFilter) {
    let next = items;
    for (const event of this.events.values()) {
      if (matchesFilter(event.post, filter) || event.kind === "deleted")
        next = reconcileTimelineEvent(next, event);
    }
    return next;
  }
  apply(event: PostChangeEvent) {
    this.events.set(event.post.id, event);
    this.beforeMerge?.();
    for (const filter of profileFilters) {
      if (matchesFilter(event.post, filter) || event.kind === "deleted")
        this.set(filter, {
          items: reconcileTimelineEvent(this.feeds[filter].items, event),
        });
    }
  }
  updateComments(id: string) {
    for (const filter of profileFilters)
      this.set(filter, {
        items: this.feeds[filter].items.map((post) =>
          post.id === id
            ? { ...post, commentCount: post.commentCount + 1 }
            : post,
        ),
      });
  }
  activate(filter: ProfileFilter) {
    this.active = filter;
    if (!this.feeds[filter].initialized) return this.load(filter);
    return this.refresh(filter);
  }
  invalidate() {
    for (const filter of profileFilters) this.generations[filter] += 1;
    if (pageVisible()) void this.activate(this.active);
  }
  async load(filter: ProfileFilter, reset = false): Promise<void> {
    const key = `initial:${filter}`;
    if (this.requests.has(key) || this.disposed) return;
    const controller = new AbortController();
    this.requests.set(key, controller);
    const generation = this.generations[filter];
    this.set(filter, { loading: true, error: null, moreError: null });
    try {
      let page = await this.api.timeline({
        kind: filter === "all" ? null : filter,
        signal: controller.signal,
      });
      const refreshCursor = page.refreshCursor ?? null;
      let items = page.items;
      const loadedThrough =
        filter === "all" && !reset
          ? loadedThroughFromSearch(this.initialSearch)
          : null;
      if (
        !reset &&
        filter === "all" &&
        loadedThrough === null &&
        new URLSearchParams(this.initialSearch).has("loaded_through")
      ) {
        this.initialSearch = withoutLoadedProgress(this.initialSearch);
        this.onProgressDiscarded?.();
      }
      let cursor: string | null = null;
      const visited = new Set<string>();
      validateCursorPage(page, cursor, visited, "profile");
      for (
        let count = 0;
        loadedThrough !== null && cursor !== loadedThrough;
        count++
      ) {
        if (count >= 1000 || !page.nextCursor) {
          this.initialSearch = withoutLoadedProgress(this.initialSearch);
          this.onProgressDiscarded?.();
          break;
        }
        if (visited.has(page.nextCursor))
          throw invalidCursorProgress("profile cursor cycle detected");
        cursor = page.nextCursor;
        visited.add(cursor);
        page = await this.api.timeline({ cursor, signal: controller.signal });
        validateCursorPage(page, cursor, visited, "profile");
        items = appendUnique(items, page.items, timelineItemKey);
      }
      if (controller.signal.aborted) return;
      this.visited[filter] = visited;
      this.set(filter, {
        items: this.reconcile(items, filter),
        initialized: true,
        nextCursor: page.nextCursor,
        refreshCursor,
        continuation: null,
        refreshError: null,
      });
    } catch (error) {
      if (!controller.signal.aborted) {
        if (
          !reset &&
          new URLSearchParams(this.initialSearch).has("loaded_through") &&
          isCursorProgressError(error)
        ) {
          this.initialSearch = withoutLoadedProgress(this.initialSearch);
          this.onProgressDiscarded?.();
          this.requests.delete(key);
          await this.load(filter, true);
        } else this.set(filter, { error });
      }
    } finally {
      if (this.requests.get(key) === controller) {
        this.requests.delete(key);
        if (!controller.signal.aborted) {
          this.set(filter, { loading: false });
          if (
            generation !== this.generations[filter] &&
            this.feeds[filter].initialized &&
            pageVisible()
          )
            void this.refresh(filter);
        }
      }
    }
  }
  async more(filter: ProfileFilter): Promise<string | null> {
    const feed = this.feeds[filter],
      cursor = feed.nextCursor,
      key = `older:${filter}`;
    if (!cursor || this.requests.has(key) || this.disposed) return null;
    const controller = new AbortController();
    this.requests.set(key, controller);
    this.set(filter, { loadingMore: true, moreError: null });
    try {
      if (this.visited[filter].has(cursor))
        throw invalidCursorProgress("profile cursor cycle detected");
      const page = await this.api.timeline({
        kind: filter === "all" ? null : filter,
        cursor,
        signal: controller.signal,
      });
      if (controller.signal.aborted) return null;
      const visited = new Set(this.visited[filter]);
      visited.add(cursor);
      validateCursorPage(page, cursor, visited, "profile");
      // The inclusive refresh millisecond may already have brought in rows
      // beyond the independent older cursor. That overlap is legitimate.
      const combined = mergeProfileItems(this.feeds[filter].items, page.items);
      this.visited[filter] = visited;
      this.set(filter, {
        items: this.reconcile(combined, filter),
        nextCursor: page.nextCursor,
      });
      return cursor;
    } catch (moreError) {
      if (!controller.signal.aborted) this.set(filter, { moreError });
      return null;
    } finally {
      this.requests.delete(key);
      if (!controller.signal.aborted) this.set(filter, { loadingMore: false });
    }
  }
  async refresh(filter: ProfileFilter): Promise<void> {
    const feed = this.feeds[filter],
      key = `newer:${filter}`;
    if (
      !feed.initialized ||
      !feed.refreshCursor ||
      this.requests.has(key) ||
      this.disposed ||
      !pageVisible()
    )
      return;
    const controller = new AbortController();
    this.requests.set(key, controller);
    const generation = this.generations[filter];
    this.set(filter, { refreshError: null });
    let cursor = feed.continuation ?? feed.refreshCursor;
    const visited = new Set<string>();
    try {
      for (let count = 0; ; count++) {
        visited.add(cursor);
        const page = await this.api.timeline({
          kind: filter === "all" ? null : filter,
          cursor,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        validateNewerPage(page, visited, "profile refresh");
        // A response can also add cards to the active sibling cache.
        // Capture its scroll anchor before either cache is updated.
        if (
          filter === this.active ||
          (this.feeds[this.active].initialized &&
            page.items.some((post) => matchesFilter(post, this.active)))
        )
          this.beforeMerge?.();
        this.set(filter, {
          items: this.reconcile(
            mergeProfileItems(this.feeds[filter].items, page.items),
            filter,
          ),
          refreshCursor: page.refreshCursor ?? this.feeds[filter].refreshCursor,
          continuation: page.prevCursor,
        });
        // Sharing cards does not advance another filter's coverage watermark.
        for (const other of profileFilters) {
          if (other === filter || !this.feeds[other].initialized) continue;
          this.set(other, {
            items: this.reconcile(
              mergeProfileItems(
                this.feeds[other].items,
                page.items.filter((post) => matchesFilter(post, other)),
              ),
              other,
            ),
          });
        }
        if (!page.prevCursor || !pageVisible()) break;
        cursor = page.prevCursor;
        if ((count + 1) % 3 === 0)
          await new Promise<void>((resolve) => setTimeout(resolve, 0));
      }
    } catch (refreshError) {
      if (!controller.signal.aborted) this.set(filter, { refreshError });
    } finally {
      this.requests.delete(key);
      if (
        !controller.signal.aborted &&
        generation !== this.generations[filter] &&
        pageVisible()
      )
        void this.refresh(filter);
    }
  }
}
