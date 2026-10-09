import { Menu } from "@base-ui/react/menu";
import { Tabs } from "@base-ui/react/tabs";
import { motion, useReducedMotion, type HTMLMotionProps } from "motion/react";
import { useCallback, useEffect, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { profileFeedPath, profileFromPath } from "../../app/routes";
import { Icon } from "../../components/icons";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import {
  SiteAvatar,
  SiteCover,
  siteDisplayName,
  siteHandle,
  siteProfileHref,
} from "../../components/SiteIdentity";
import {
  ExactTimeTooltip,
  ShareMenu,
} from "../../components/TimelineInteractions";
import { usePostReplyDialog } from "../comments/usePostReplyDialog";
import { ownsSite, useSession } from "../auth/SessionProvider";
import {
  listenForOwnerPosts,
  listenForPublishedPosts,
  type OwnerPostEvent,
} from "../owner/postEvents";
import { PostPresentation } from "../posts/PostPresentation";
import { formatMomentDate } from "../posts/dates";
import {
  PublicEmpty,
  PublicError,
  TimelineLoading,
} from "../timeline/PublicStates";
import { withoutLoadedProgress } from "../timeline/cursorContinuity";
import { timelineItemKey } from "../timeline/timelineItems";
import {
  matchesFilter,
  profileFilters,
  type ProfileFilter,
} from "./profileTimeline";
import { useProfileTimeline } from "./useProfileTimeline";
import interactionStyles from "../../components/TimelineInteractions.module.css";
import styles from "./ProfilePage.module.css";
import { ProfileEditor } from "./ProfileEditor";

function timezoneLabel(timeZone: string, date: Date): string {
  const part = (style: "longGeneric" | "longOffset") =>
    new Intl.DateTimeFormat("zh-CN", {
      timeZone,
      timeZoneName: style,
    })
      .formatToParts(date)
      .find((candidate) => candidate.type === "timeZoneName")?.value;
  const offset = part("longOffset")?.replace(/^GMT/, "UTC");
  const name = part("longGeneric");
  if (name && name !== timeZone && name !== offset) {
    return offset ? `${name} · ${offset}` : name;
  }
  return offset ?? name ?? timeZone;
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

export function ProfilePage({
  api,
  site,
  onSiteSaved,
}: {
  api: PublicApi;
  site: SiteIdentity;
  onSiteSaved: (site: SiteIdentity) => void;
}) {
  const owner = useSession();
  const { account: viewerAccount, applyAccount } = owner;
  // This is the site owner's own page, so the editor and the per-post actions
  // belong to them alone. A signed-in member reads it like anyone else.
  const ownerView = owner.state === "signed-in" && ownsSite(owner.account);
  const navigation = useNavigation();
  const filter = profileFromPath(navigation.pathname)?.filter ?? "all";
  const [tabState, setTabState] = useState<{
    filter: ProfileFilter;
    leftFilter: "all" | "moment";
    direction: "left" | "right";
    slide: boolean;
  }>(() => ({
    filter,
    leftFilter: filter === "moment" ? "moment" : "all",
    direction: "right",
    slide: false,
  }));
  // Derive the transition before rendering children, including history navigation.
  // The two left-hand choices replace content in place; only crossing columns slides.
  if (tabState.filter !== filter) {
    setTabState({
      filter,
      leftFilter: filter === "article" ? tabState.leftFilter : filter,
      direction: filter === "article" ? "right" : "left",
      slide: filter === "article" || tabState.filter === "article",
    });
  }
  const { leftFilter, direction, slide } = tabState;
  const { store, feeds, initialSearch } = useProfileTimeline(
    api,
    `${site.instanceId ?? site.ownerId}:${ownerView}`,
    filter,
  );
  const [pinnedPost, setPinnedPost] = useState<PublicPost | null>(null);
  const entryRestored = useRef(false);
  const leftTabSlot = useRef<HTMLDivElement>(null);
  const reducedMotion = useReducedMotion();
  const selectFilter = (next: ProfileFilter) => {
    if (next === filter) void store.activate(next);
    const href = profileFeedPath(site.username, next);
    if (navigation.pathname.replace(/\/+$/, "") !== href) {
      navigation.navigate(href + (next === filter ? navigation.search : ""), {
        preserveScroll: true,
      });
    }
  };
  const title = siteDisplayName(site);
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
  const publicTimezone = timezoneLabel(
    site.timezone,
    new Date(site.instanceInitializedAt ?? Date.now()),
  );
  const createdFromProfile = useCallback(
    (comment: PublicComment) => {
      const increment = (post: PublicPost) =>
        post.id === comment.postId
          ? { ...post, commentCount: post.commentCount + 1 }
          : post;
      store.updateComments(comment.postId);
      setPinnedPost((current) => (current ? increment(current) : current));
    },
    [store],
  );
  const { openReply, replyDialog } = usePostReplyDialog({
    api,
    onCreated: createdFromProfile,
    site,
  });

  /**
   * Adopt what the server just saved, and follow the handle if it moved.
   *
   * The owner's handle is the address of this page, so renaming from here left
   * the browser standing at a path that had just stopped belonging to anybody:
   * the site knew the new name, the URL still held the old one, and the app
   * answered by rendering somebody else's profile that no longer exists. The
   * account this editor is editing is also the viewer's own, so its copy in the
   * owner context is restamped too -- the left rail links to it, and the
   * /profile redirect resolves through it.
   *
   * MemberProfilePage.adopt is the same move for a member editing their own
   * page. Both belong on the page that owns the address, not in the dialog.
   */
  const adoptSite = useCallback(
    (saved: SiteIdentity) => {
      onSiteSaved(saved);
      if (viewerAccount) {
        applyAccount({
          ...viewerAccount,
          username: saved.username,
          displayName: saved.displayName,
          bio: saved.bio,
          avatarMediaId: saved.avatarMediaId,
          coverMediaId: saved.coverMediaId,
          profileUrl: siteProfileHref(saved),
        });
      }
      // Only when the browser is standing on the handle that just stopped
      // existing. /profile is an alias a rename cannot break, and moving
      // somebody off it would change their URL for no reason.
      const vacated = siteProfileHref(site);
      if (
        saved.username !== site.username &&
        profileFromPath(navigation.pathname)?.username === site.username
      ) {
        navigation.navigate(
          navigation.pathname.replace(vacated, siteProfileHref(saved)) +
            navigation.search,
          { preserveScroll: true },
        );
      }
    },
    [applyAccount, navigation, onSiteSaved, site, viewerAccount],
  );

  const handleOwnerPostEvent = useCallback(
    (event: OwnerPostEvent) => {
      store.apply(event);
      if (event.kind === "created") store.invalidate();
      setPinnedPost((current) =>
        current?.id === event.post.id
          ? event.kind === "deleted"
            ? null
            : event.post
          : current,
      );
    },
    [store],
  );

  const handleReposted = useCallback(
    (post: PublicPost) => handleOwnerPostEvent({ kind: "reordered", post }),
    [handleOwnerPostEvent],
  );

  useEffect(() => {
    const controller = new AbortController();
    if (site.pinnedPostId) {
      void api
        .post(site.pinnedPostId, controller.signal)
        .then((post) => {
          if (!controller.signal.aborted) setPinnedPost(post);
        })
        .catch(() => {
          if (!controller.signal.aborted) setPinnedPost(null);
        });
    } else setPinnedPost(null);
    return () => controller.abort();
  }, [api, site.pinnedPostId]);
  useEffect(
    () => listenForOwnerPosts(handleOwnerPostEvent),
    [handleOwnerPostEvent],
  );
  useEffect(() => listenForPublishedPosts(() => store.invalidate()), [store]);
  const activeFeed = feeds[filter];
  useEffect(() => {
    if (!activeFeed.initialized || activeFeed.error || entryRestored.current)
      return;
    entryRestored.current = true;
    navigation.restoreCurrentEntry();
  }, [activeFeed.initialized, activeFeed.error, navigation]);

  const more = async (kind: ProfileFilter, source: LoadSource = "manual") => {
    const previousKeys = new Set(feeds[kind].items.map(timelineItemKey));
    const cursor = await store.more(kind);
    if (cursor && kind === "all") navigation.replaceTimelineProgress(cursor);
    if (cursor && source === "manual" && store.active === kind)
      window.requestAnimationFrame(() => {
        if (store.active !== kind) return;
        const items = document.querySelectorAll<HTMLElement>(
          '[role="tabpanel"]:not([inert]) [data-timeline-item-key]',
        );
        Array.from(items)
          .find((row) => !previousKeys.has(row.dataset.timelineItemKey!))
          ?.focus({ preventScroll: true });
      });
  };
  const resetFromFirstPage = (kind: ProfileFilter) => {
    if (kind === "all") {
      const search = withoutLoadedProgress(initialSearch.current);
      initialSearch.current = search;
      navigation.navigate(`${navigation.pathname}${search}`);
    }
    void store.load(kind, true);
  };

  const renderFeed = (kind: ProfileFilter) => {
    const feed = feeds[kind];
    const pinned =
      pinnedPost && matchesFilter(pinnedPost, kind) ? pinnedPost : null;
    return (
      <>
        {feed.loading || (!feed.initialized && !feed.error) ? (
          <TimelineLoading />
        ) : null}
        {feed.error ? (
          <PublicError
            kind={errorKind(feed.error)}
            onRetry={() => void store.load(kind)}
            onResetProgress={
              recoverableCursorError(feed.error)
                ? () => resetFromFirstPage(kind)
                : undefined
            }
          />
        ) : null}
        {feed.initialized && !feed.error ? (
          <>
            {feed.refreshError ? (
              <div className={styles.moreError} role="alert">
                <span>新内容没有加载完成，已保留当前 Moment。</span>
                <button type="button" onClick={() => void store.refresh(kind)}>
                  重试更新
                </button>
              </div>
            ) : null}
            {pinned ? (
              <PostPresentation
                onDeleted={() => setPinnedPost(null)}
                onReply={(event) => openReply(pinned, event)}
                onReposted={handleReposted}
                onSiteChanged={onSiteSaved}
                owner={ownerView}
                pinned
                post={pinned}
                site={site}
                timelineDensity
              />
            ) : null}
            {feed.items
              .filter(
                (post) =>
                  post.id !== pinned?.id || post.timelineItemKind === "repost",
              )
              .map((post) => (
                <PostPresentation
                  key={timelineItemKey(post)}
                  onReply={(event) => openReply(post, event)}
                  onReposted={handleReposted}
                  onSiteChanged={onSiteSaved}
                  owner={ownerView}
                  post={post}
                  site={site}
                  timelineDensity
                />
              ))}
            {feed.items.length === 0 && !pinned ? (
              <PublicEmpty context="profile" owner={ownerView} />
            ) : null}
            {feed.moreError ? (
              <div className={styles.moreError} role="alert">
                <span>
                  {errorKind(feed.moreError) === "malformed"
                    ? "下一页数据不完整，已保留当前 Moment。"
                    : "下一页没有加载完成，已保留当前 Moment。"}
                </span>
                {recoverableCursorError(feed.moreError) ? (
                  <button
                    onClick={() => resetFromFirstPage(kind)}
                    type="button"
                  >
                    从第一页重新加载
                  </button>
                ) : (
                  <button onClick={() => void more(kind)} type="button">
                    重试加载
                  </button>
                )}
              </div>
            ) : feed.nextCursor && kind === filter ? (
              <LoadMoreSentinel
                className={styles.moreRow}
                cursor={feed.nextCursor}
                label="加载更早的 Moment"
                loading={feed.loadingMore}
                onLoad={(source) => void more(kind, source)}
              />
            ) : null}
          </>
        ) : null}
      </>
    );
  };
  const label = leftFilter === "all" ? "全部" : "帖子";
  const labelAnimation = (selected: boolean) => ({
    width: selected ? "auto" : 0,
    opacity: selected ? 1 : 0,
    "--label-fade": selected ? "0px" : "16px",
  });
  const tabTransition = {
    duration: reducedMotion ? 0 : 0.24,
    ease: [0.22, 1, 0.36, 1] as const,
  };
  const labelTransition = {
    ...tabTransition,
    opacity: { duration: tabTransition.duration, ease: "linear" as const },
  };
  const labelContentAnimation = (selected: boolean) => ({
    x: selected ? "0%" : "-100%",
  });

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
            href={siteProfileHref(site)}
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
          {ownsSite(owner.account) &&
          (owner.state === "signed-in" || owner.state === "expired") ? (
            <div className={styles.profileEditorSlot}>
              <ProfileEditor onSaved={adoptSite} site={site} />
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
                  <Icon name="calendar" /> {joined}加入
                </time>
              }
            />
          ) : null}
          <span
            aria-label={`时区：${publicTimezone}（${site.timezone}）`}
            title={site.timezone}
          >
            {publicTimezone}
          </span>
        </div>
      </div>
      <Tabs.Root
        value={filter}
        onValueChange={(value) => selectFilter(value as ProfileFilter)}
        data-feed-direction={direction}
        data-feed-motion={slide ? "slide" : "none"}
        className={styles.contentTabs}
      >
        <Tabs.List
          aria-label="帖子类型"
          className={styles.tabList}
          activateOnFocus={false}
        >
          <div className={styles.tabSlot} ref={leftTabSlot}>
            <Tabs.Tab
              value={leftFilter}
              aria-label={label}
              className={styles.typeTab}
              onClick={() => {
                if (filter === leftFilter) selectFilter(leftFilter);
              }}
            />
            {/* Sibling controls share a full-column hit area without nested buttons. */}
            <div
              className={styles.tabContent}
              data-active={filter !== "article" ? "" : undefined}
            >
              <Icon name="archive" variant="navigation" />
              <motion.span
                className={styles.tabLabel}
                initial={false}
                animate={labelAnimation(filter !== "article")}
                transition={labelTransition}
                aria-hidden={filter === "article"}
                inert={filter === "article"}
              >
                <motion.span
                  aria-hidden="true"
                  className={styles.tabLabelContent}
                  initial={false}
                  animate={labelContentAnimation(filter !== "article")}
                  transition={tabTransition}
                >
                  {label}
                </motion.span>
              </motion.span>
              {/* The dropdown must not lengthen the text's percentage-based slide. */}
              <motion.span
                className={styles.filterSlot}
                initial={false}
                animate={{
                  width: filter !== "article" ? "auto" : 0,
                  opacity: filter !== "article" ? 1 : 0,
                }}
                transition={labelTransition}
                aria-hidden={filter === "article"}
                inert={filter === "article"}
              >
                <Menu.Root>
                  <Menu.Trigger
                    aria-label="选择帖子类型"
                    className={styles.filterTrigger}
                    disabled={filter === "article"}
                  >
                    <Icon name="chevron-down" />
                  </Menu.Trigger>
                  <Menu.Portal>
                    <Menu.Positioner
                      anchor={leftTabSlot}
                      side="bottom"
                      align="center"
                      sideOffset={4}
                      collisionPadding={8}
                      className={interactionStyles.sharePositioner}
                    >
                      <Menu.Popup
                        aria-label="帖子类型筛选"
                        className={interactionStyles.sharePopup}
                      >
                        {(["all", "moment"] as const).map((kind) => (
                          <Menu.Item
                            key={kind}
                            className={interactionStyles.shareItem}
                            onClick={() => {
                              selectFilter(kind);
                            }}
                          >
                            <span>{kind === "all" ? "全部" : "帖子"}</span>
                            {leftFilter === kind ? <Icon name="check" /> : null}
                          </Menu.Item>
                        ))}
                      </Menu.Popup>
                    </Menu.Positioner>
                  </Menu.Portal>
                </Menu.Root>
              </motion.span>
            </div>
          </div>
          <div className={styles.tabSlot}>
            <Tabs.Tab
              value="article"
              aria-label="文章"
              className={styles.typeTab}
              onClick={() => {
                if (filter === "article") selectFilter("article");
              }}
            >
              <svg viewBox="0 0 24 24" aria-hidden="true" fill="currentColor">
                <g>
                  <path
                    clipRule="evenodd"
                    fillRule="evenodd"
                    d="M18.067 12.701c1.169-1.168 3.063-1.168 4.232 0 1.168 1.169 1.168 3.063 0 4.232L17.83 21.4c-.827.827-1.918 1.339-3.083 1.445l-2.851.259.258-2.852c.106-1.165.618-2.256 1.445-3.083l4.468-4.469zm2.818 1.414c-.388-.387-1.016-.387-1.404 0l-4.468 4.469c-.497.496-.803 1.152-.867 1.85l-.042.46.461-.04c.7-.064 1.354-.37 1.85-.867l4.47-4.468c.387-.388.387-1.016 0-1.404z"
                  />
                  <path d="M16 2c.684 0 1.257-.001 1.724.037.478.04.933.125 1.365.345.658.335 1.194.87 1.53 1.53.22.43.305.886.344 1.364C21 5.743 21 6.316 21 7v2h-2V7c0-.717 0-1.194-.03-1.56-.03-.355-.08-.518-.133-.62-.144-.283-.374-.513-.656-.657-.103-.052-.266-.104-.62-.133C17.194 4 16.716 4 16 4H8c-.717 0-1.194 0-1.56.03-.355.03-.518.08-.62.133-.283.144-.513.374-.657.656-.052.103-.104.266-.133.62C5 5.806 5 6.283 5 7v10c0 .716 0 1.194.03 1.56.03.355.08.518.133.62.144.283.374.513.656.657.103.052.266.104.62.133.367.03.844.03 1.561.03h1v2H8c-.684 0-1.257.001-1.724-.037-.478-.04-.933-.125-1.365-.345-.658-.335-1.194-.87-1.53-1.53-.22-.43-.305-.886-.344-1.364C3 18.257 3 17.684 3 17V7c0-.684-.001-1.257.037-1.724.04-.478.125-.933.345-1.365.335-.658.87-1.194 1.53-1.53.43-.22.886-.305 1.364-.344C6.743 2 7.316 2 8 2h8z" />
                  <path d="M13 13H8v-2h5v2zm3-4H8V7h8v2z" />
                </g>
              </svg>
              <motion.span
                aria-hidden="true"
                className={styles.tabLabel}
                initial={false}
                animate={labelAnimation(filter === "article")}
                transition={labelTransition}
              >
                <motion.span
                  className={styles.tabLabelContent}
                  initial={false}
                  animate={labelContentAnimation(filter === "article")}
                  transition={tabTransition}
                >
                  文章
                </motion.span>
              </motion.span>
            </Tabs.Tab>
          </div>
          <Tabs.Indicator
            className={styles.tabIndicator}
            render={
              <motion.span
                initial={false}
                animate={{ x: filter === "article" ? "250%" : "0%" }}
                transition={tabTransition}
              />
            }
          />
        </Tabs.List>
        <div className={styles.feedViewport} aria-busy={activeFeed.loading}>
          {profileFilters.map((kind) => (
            <Tabs.Panel
              key={kind}
              value={kind}
              className={styles.feedPanel}
              render={(props, state) => (
                <motion.div
                  {...(props as HTMLMotionProps<"div">)}
                  initial={
                    slide &&
                    !reducedMotion &&
                    state.transitionStatus === "starting"
                      ? {
                          x: direction === "right" ? "100%" : "-100%",
                        }
                      : false
                  }
                  animate={{
                    x:
                      slide &&
                      !reducedMotion &&
                      state.transitionStatus === "ending"
                        ? direction === "right"
                          ? "-100%"
                          : "100%"
                        : "0%",
                  }}
                  transition={{
                    ...tabTransition,
                    duration: slide ? tabTransition.duration : 0,
                  }}
                />
              )}
            >
              {renderFeed(kind)}
            </Tabs.Panel>
          ))}
        </div>
      </Tabs.Root>
      {replyDialog}
    </section>
  );
}
