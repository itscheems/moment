import { getLocale } from "../../i18n";
import { useTranslation } from "react-i18next";
import { AccountName } from "../../components/AccountName";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  PublicApi,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
  type Account,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { profileFeedPath, profileFromPath } from "../../app/routes";
import { Icon } from "../../components/icons";
import {
  canRecoverRead,
  useReadRecovery,
} from "../../components/useReadRecovery";
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
import { useSession } from "../auth/SessionProvider";
import {
  listenForPostChanges,
  listenForPublishedPosts,
  type PostChangeEvent,
} from "../posts/postEvents";
import { PostPresentation } from "../posts/PostPresentation";
import { formatMomentDate } from "../posts/dates";
import {
  PublicEmpty,
  PublicError,
  ReadRecoveryToast,
  TimelineLoading,
} from "../timeline/PublicStates";
import { timelineItemKey } from "../timeline/timelineItems";
import { matchesFilter, type ProfileFilter } from "./profileTimeline";
import { useProfileTimeline } from "./useProfileTimeline";
import styles from "./ProfilePage.module.css";
import { ProfileEditor } from "./ProfileEditor";
import { ProfileHeader } from "./ProfileHeader";
import { ProfileTabs } from "./ProfileTabs";
import { SuggestedPeople } from "../people/SuggestedPeople";

export function ProfilePage({
  api,
  site,
  onSiteSaved,
}: {
  api: PublicApi;
  site: SiteIdentity;
  onSiteSaved: (site: SiteIdentity) => void;
}) {
  const { t } = useTranslation();

  const auth = useSession();
  const { account: viewerAccount, applyAccount } = auth;
  const canPublish =
    auth.state === "signed-in" && auth.account?.status === "active";
  const navigation = useNavigation();
  const filter = profileFromPath(navigation.pathname)?.filter ?? "all";
  const authorApi = useMemo(
    () => api.forAuthor(site.profileUserId),
    [api, site.profileUserId],
  );
  const { store, feeds } = useProfileTimeline(
    authorApi,
    `${site.instanceId}:${site.profileUserId}:${auth.account?.userId ?? "guest"}`,
    filter,
    profileFromPath(navigation.pathname)?.username === site.username,
  );
  const [pinnedPost, setPinnedPost] = useState<PublicPost | null>(null);
  const [pinError, setPinError] = useState<unknown>(null);
  const [pinLoading, setPinLoading] = useState(false);
  const [pinAttempt, setPinAttempt] = useState(0);
  useReadRecovery({
    error: pinError,
    loading: pinLoading,
    onRetry: () => setPinAttempt((value) => value + 1),
    scope: site.pinnedPostId ?? "",
    enabled: profileFromPath(navigation.pathname)?.username === site.username,
  });
  const entryRestored = useRef(false);
  const selectFilter = (next: ProfileFilter) => {
    if (next === filter) void store.activate(next);
    const href = profileFeedPath(site.username, next);
    if (navigation.pathname.replace(/\/+$/, "") !== href) {
      navigation.replaceWithinPage(
        href + (next === filter ? navigation.search : ""),
      );
    }
  };
  const title = siteDisplayName(site);
  const handle = siteHandle(site);
  const initialized = site.instanceInitializedAt
    ? formatMomentDate(site.instanceInitializedAt, site.timezone)
    : null;
  const joined = site.instanceInitializedAt
    ? new Intl.DateTimeFormat(getLocale(), {
        year: "numeric",
        month: "long",
        timeZone: site.timezone,
      }).format(new Date(site.instanceInitializedAt))
    : null;
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

  const adoptSite = useCallback(
    (savedAccount: Account) => {
      if (!applyAccount(savedAccount)) return;
      const saved = {
        ...site,
        username: savedAccount.username,
        displayName: savedAccount.displayName,
        bio: savedAccount.bio,
        avatarMediaId: savedAccount.avatarMediaId,
        coverMediaId: savedAccount.coverMediaId,
      };
      onSiteSaved(saved);
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
    [applyAccount, navigation, onSiteSaved, site],
  );

  const handlePostChange = useCallback(
    (event: PostChangeEvent) => {
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
    (post: PublicPost) => handlePostChange({ kind: "reordered", post }),
    [handlePostChange],
  );

  useEffect(() => {
    const controller = new AbortController();
    setPinError(null);
    setPinLoading(Boolean(site.pinnedPostId));
    if (site.pinnedPostId) {
      void api
        .post(site.pinnedPostId, controller.signal)
        .then((post) => {
          if (!controller.signal.aborted) setPinnedPost(post);
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted) {
            setPinError(error);
            if (!canRecoverRead(error)) setPinnedPost(null);
          }
        })
        .finally(() => {
          if (!controller.signal.aborted) setPinLoading(false);
        });
    } else setPinnedPost(null);
    return () => controller.abort();
  }, [api, site.pinnedPostId, pinAttempt]);
  useEffect(() => listenForPostChanges(handlePostChange), [handlePostChange]);
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
  const renderFeed = (kind: ProfileFilter) => {
    const feed = feeds[kind];
    const pinned =
      pinnedPost?.id === site.pinnedPostId && matchesFilter(pinnedPost, kind)
        ? pinnedPost
        : null;
    return (
      <>
        {(feed.loading && feed.items.length === 0) ||
        (!feed.initialized && !feed.error) ? (
          <TimelineLoading />
        ) : null}
        {feed.error &&
        (!canRecoverRead(feed.error) || feed.items.length === 0) ? (
          <PublicError recovering={canRecoverRead(feed.error)} />
        ) : null}
        {kind === filter &&
        feed.items.length > 0 &&
        (feed.loading || canRecoverRead(feed.error)) ? (
          <ReadRecoveryToast />
        ) : null}
        {feed.initialized &&
        (!feed.error ||
          (canRecoverRead(feed.error) && feed.items.length > 0)) ? (
          <>
            {kind === filter && feed.refreshError ? (
              <ReadRecoveryToast error={feed.refreshError}>
                {t("正在重新获取更新的内容…")}
              </ReadRecoveryToast>
            ) : null}
            {pinned ? (
              <PostPresentation
                onDeleted={() => setPinnedPost(null)}
                onReply={(event) => openReply(pinned, event)}
                onReposted={handleReposted}
                onSiteChanged={onSiteSaved}
                canPublish={canPublish}
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
                  canPublish={canPublish}
                  post={post}
                  site={site}
                  timelineDensity
                />
              ))}
            {feed.items.length === 0 && !pinned ? (
              <PublicEmpty context="profile" canPublish={canPublish} />
            ) : null}
            {kind === filter && feed.moreError ? (
              <ReadRecoveryToast error={feed.moreError}>
                {t("正在重新获取后续内容…")}
              </ReadRecoveryToast>
            ) : feed.nextCursor && kind === filter ? (
              <LoadMoreSentinel
                className={styles.moreRow}
                cursor={feed.nextCursor}
                label={t("加载更早的 Moment")}
                loading={feed.loadingMore || feed.loading}
                onLoad={(source) => void more(kind, source)}
              />
            ) : null}
          </>
        ) : null}
      </>
    );
  };

  return (
    <section aria-label={t("{{value1}} 的个人资料", { value1: title })}>
      <ProfileHeader api={api} userId={site.profileUserId} title={title} />
      <SuggestedPeople
        api={api}
        kind="profile"
        profileUsername={site.username}
      />
      <div className={styles.cover}>
        <SiteCover eager site={site} />
      </div>
      <div className={styles.identity}>
        <div className={styles.profileTop}>
          <div className={styles.avatar}>
            <SiteAvatar eager site={site} />
          </div>
          <div className={styles.profileActions}>
            <ShareMenu
              baseUrl={site.publicBaseUrl}
              href={siteProfileHref(site)}
              title={t("{{value1}} 的个人资料", { value1: title })}
              trigger={
                <button
                  aria-label={t("分享个人资料")}
                  title={t("分享个人资料")}
                  type="button"
                >
                  <Icon name="share" />
                </button>
              }
            />
            {viewerAccount &&
            viewerAccount.userId === site.profileUserId &&
            (auth.state === "signed-in" || auth.state === "expired") ? (
              <div className={styles.profileEditorSlot}>
                <ProfileEditor
                  onSaved={adoptSite}
                  account={viewerAccount}
                  triggerClassName={styles.editProfile}
                />
              </div>
            ) : null}
          </div>
        </div>
        <h2>
          <AccountName identity={site} name={title} prominent />
        </h2>
        <p className={styles.handle}>{handle}</p>
        {site.bio.trim() ? <p>{site.bio.trim()}</p> : null}
        <div className={styles.profileMeta}>
          {initialized && joined && site.instanceInitializedAt ? (
            <ExactTimeTooltip
              exact={initialized.absolute}
              children={
                <time dateTime={site.instanceInitializedAt}>
                  <Icon name="calendar" /> {joined}
                  {t("加入")}
                </time>
              }
            />
          ) : null}
        </div>
      </div>
      <ProfileTabs
        filter={filter}
        onSelect={selectFilter}
        loading={activeFeed.loading}
      >
        {renderFeed}
      </ProfileTabs>
      {replyDialog}
    </section>
  );
}
