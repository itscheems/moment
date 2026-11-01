import { LikeAction } from "../notifications/LikeAction";
import { useTranslation } from "react-i18next";
import {
  useCallback,
  useEffect,
  useMemo,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import {
  PublicApi,
  PublicApiError,
  type CommentContext,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { MentionProvider } from "../../app/mentions";
import { useNavigation } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { useMediaQuery } from "../../components/useMediaQuery";
import { Icon } from "../../components/icons";
import {
  ActionTooltip,
  ShareMenu,
} from "../../components/TimelineInteractions";
import { useReadRecovery } from "../../components/useReadRecovery";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { CommentThread } from "../comments/CommentThread";
import { CommentModerationMenu } from "../comments/CommentItem";
import {
  listenForDiscussionChanges,
  announceDiscussionChanged,
  type DiscussionChange,
  type CommentMutation,
} from "../comments/discussionChanges";
import {
  commentAsPost,
  loadCommentShareCard,
  loadPostShareCard,
} from "../share/shareCardInput";
import { PostPresentation } from "../posts/PostPresentation";
import { RepostAction } from "../posts/RepostAction";
import { announcePostChanged, listenForPostChanges } from "../posts/postEvents";
import {
  PublicError,
  ReadRecoveryToast,
  TimelineLoading,
} from "../timeline/PublicStates";
import { buildLightboxItems } from "./ImageGrid";
import {
  MediaLightbox,
  viewerTooltipPlacement,
  type MediaLightboxItem,
} from "./MediaLightbox";
import {
  MediaViewerContext,
  mediaSourceKey,
  type MediaSource,
} from "./mediaViewerContext";
import styles from "./Media.module.css";

interface Gallery {
  key: string;
  source: MediaSource;
  items: MediaLightboxItem[];
  selected: number;
}
interface DiscussionData {
  post: PublicPost | null;
  display: PublicPost | null;
  unavailable: boolean;
}
function sourceHref(source: MediaSource) {
  return source.commentId
    ? commentDetailPath(source.postId, source.commentId)
    : "/m/" + encodeURIComponent(source.postId);
}

/** One reader viewer, while each visited discussion keeps its own live form. */
export function MediaViewerProvider({
  children,
  site,
  api,
  enabled = true,
  scope,
  onSiteChanged,
}: {
  children: ReactNode;
  site: SiteIdentity;
  api: PublicApi;
  enabled?: boolean;
  scope: string | null;
  onSiteChanged?: (site: SiteIdentity) => void;
}) {
  const { t } = useTranslation();

  const navigation = useNavigation();
  const wide = useMediaQuery("(min-width: 950px)");
  const [panelShown, setPanelShown] = useState(wide);
  const [galleries, setGalleries] = useState<Gallery[]>([]);
  const [sources, setSources] = useState<MediaSource[]>([]);
  const [data, setData] = useState<Record<string, DiscussionData>>({});
  const [replyRequest, setReplyRequest] = useState<{
    key: string;
    revision: number;
  } | null>(null);
  const returnFocus = useRef<HTMLElement | null>(null);
  const [viewerSession] = useState(
    () => Date.now().toString(36) + "-" + Math.random().toString(36).slice(2),
  );
  const serial = useRef(0);
  const previousScope = useRef(scope);
  useEffect(() => {
    if (previousScope.current === scope) return;
    previousScope.current = scope;
    setGalleries([]);
    setSources([]);
    setData({});
    setReplyRequest(null);
    lastActive.current = null;
    if (navigation.panel?.key.startsWith("photo-viewer:"))
      navigation.closePanel(navigation.panel.key);
  }, [scope, navigation]);
  useEffect(() => {
    const key = navigation.panel?.key;
    if (
      key?.startsWith("photo-viewer:") &&
      !galleries.some((gallery) => gallery.key === key)
    ) {
      navigation.replace(
        navigation.pathname + navigation.search,
        navigation.overlayFrom,
      );
    }
  }, [navigation, galleries]);
  const active = enabled
    ? galleries.find((g) => g.key === navigation.panel?.key)
    : undefined;
  const activeKey = active ? mediaSourceKey(active.source) : null;
  const lastActive = useRef<Gallery | null>(null);
  if (active) lastActive.current = active;
  const displayedGallery = active ?? lastActive.current;
  const currentData = displayedGallery
    ? data[mediaSourceKey(displayedGallery.source)]
    : undefined;
  const open = useCallback(
    (
      source: MediaSource,
      items: MediaLightboxItem[],
      selected: number,
      trigger: HTMLElement,
    ) => {
      if (!items[selected]) return;
      if (!navigation.panel?.key.startsWith("photo-viewer:"))
        returnFocus.current = trigger;
      const gallery = {
        key: "photo-viewer:" + viewerSession + ":" + ++serial.current,
        source,
        items,
        selected,
      };
      setGalleries((current) => [...current, gallery]);
      setSources((current) =>
        current.some((s) => mediaSourceKey(s) === mediaSourceKey(source))
          ? current
          : [...current, source],
      );
      setPanelShown(wide);
      navigation.openPanel(
        navigation.pathname + navigation.search,
        gallery.key,
      );
    },
    [navigation, wide, viewerSession],
  );
  const onData = useCallback((source: MediaSource, value: DiscussionData) => {
    const key = mediaSourceKey(source);
    setData((current) => ({ ...current, [key]: value }));
    if (value.display && !value.unavailable) {
      const items = buildLightboxItems(value.display.media);
      if (!items.length) {
        setData((current) => ({
          ...current,
          [key]: { ...value, unavailable: true },
        }));
      } else {
        setGalleries((current) =>
          current.map((gallery) => {
            if (mediaSourceKey(gallery.source) !== key) return gallery;
            const selectedItem = gallery.items[gallery.selected];
            let index = items.findIndex(
              (item) => item.key === selectedItem?.key,
            );
            if (index < 0 && selectedItem?.originalSrc) {
              index = items.findIndex(
                (item) => item.originalSrc === selectedItem.originalSrc,
              );
            }
            return {
              ...gallery,
              items,
              selected:
                index >= 0
                  ? index
                  : Math.min(gallery.selected, items.length - 1),
            };
          }),
        );
      }
    }
  }, []);
  const context = useMemo(() => ({ open }), [open]);
  const requestReply = () => {
    if (activeKey)
      setReplyRequest({ key: activeKey, revision: ++serial.current });
  };
  const viewPost = () => {
    if (active)
      navigation.navigate(sourceHref(active.source), { preserveScroll: false });
  };
  const shown = currentData?.display;
  const mediaNoun = displayedGallery?.items.some(
    (item) => item.kind === "video",
  )
    ? t("媒体")
    : t("图片");
  return (
    <MediaViewerContext.Provider value={context}>
      {children}
      {displayedGallery ? (
        <MentionProvider identities={[site]}>
          <MediaLightbox
            items={displayedGallery.items}
            playbackScope={displayedGallery.key}
            selected={active?.selected ?? null}
            returnFocus={returnFocus}
            panelShown={panelShown}
            onPanelShownChange={setPanelShown}
            onViewPost={viewPost}
            unavailable={currentData?.unavailable}
            onSelectedChange={(index) => {
              if (!active) return;
              if (index === null) navigation.closePanel(active.key);
              else
                setGalleries((current) =>
                  current.map((g) =>
                    g.key === active.key ? { ...g, selected: index } : g,
                  ),
                );
            }}
            toolbar={
              active ? (
                <>
                  <ActionTooltip
                    label={t("回复")}
                    placement={viewerTooltipPlacement}
                  >
                    <button
                      type="button"
                      aria-label={t("回复{{value1}}所属内容", {
                        value1: mediaNoun,
                      })}
                      data-viewer-action="reply"
                      onClick={requestReply}
                    >
                      <Icon name="reply" />
                      {shown?.commentCount ? (
                        <span>{shown.commentCount}</span>
                      ) : null}
                    </button>
                  </ActionTooltip>
                  {shown && !currentData?.unavailable ? (
                    <>
                      <RepostAction
                        post={shown}
                        tooltipPlacement={viewerTooltipPlacement}
                      />
                      <LikeAction
                        kind={shown.commentPostId ? "comment" : "post"}
                        id={shown.id}
                        tooltipPlacement={viewerTooltipPlacement}
                      />
                      <ShareMenu
                        tooltipPlacement={viewerTooltipPlacement}
                        align="start"
                        baseUrl={site.publicBaseUrl}
                        href={sourceHref(active.source)}
                        title={t("Moment")}
                        card={
                          active.source.commentId
                            ? (signal) =>
                                loadCommentShareCard(
                                  api,
                                  currentData.post!,
                                  active.source.commentId!,
                                  site,
                                  signal,
                                )
                            : (signal) =>
                                loadPostShareCard(api, shown, site, signal)
                        }
                        trigger={
                          <button
                            type="button"
                            aria-label={t("分享{{value1}}所属内容", {
                              value1: mediaNoun,
                            })}
                          >
                            <Icon name="share" />
                          </button>
                        }
                      />
                    </>
                  ) : null}
                </>
              ) : null
            }
            discussion={
              <>
                {sources.map((source) => {
                  const key = mediaSourceKey(source);
                  return (
                    <MediaDiscussion
                      key={key}
                      source={source}
                      api={api}
                      site={site}
                      active={Boolean(active && key === activeKey)}
                      replyRequest={
                        replyRequest?.key === key ? replyRequest.revision : 0
                      }
                      onData={onData}
                      onSiteChanged={onSiteChanged}
                    />
                  );
                })}
              </>
            }
          />
        </MentionProvider>
      ) : null}
    </MediaViewerContext.Provider>
  );
}

function MediaDiscussion({
  source,
  api,
  site,
  active,
  replyRequest,
  onData,
  onSiteChanged,
}: {
  source: MediaSource;
  api: PublicApi;
  site: SiteIdentity;
  active: boolean;
  replyRequest: number;
  onData: (source: MediaSource, value: DiscussionData) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
}) {
  const { t } = useTranslation();

  const auth = useSession();
  const navigation = useNavigation();
  const [post, setPost] = useState<PublicPost | null>(null);
  const [context, setContext] = useState<CommentContext | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [replyOpen, setReplyOpen] = useState(false);
  const scroller = useRef<HTMLElement | null>(null);
  const section = useRef<HTMLDivElement>(null);
  const heading = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const lastReply = useRef(0);
  const rememberedScroll = useRef(0);
  const [change, setChange] = useState<DiscussionChange | null>(null);
  const discussionRevision = useRef(0);
  const instanceId = "media-" + mediaSourceKey(source);
  useEffect(
    () =>
      listenForDiscussionChanges((postId, mutation, origin) => {
        if (postId !== source.postId || origin === instanceId) return;
        setChange({
          ...mutation,
          source: origin,
          revision: ++discussionRevision.current,
        });
        if (active) setAttempt((n) => n + 1);
      }),
    [active, source.postId, instanceId],
  );
  useLayoutEffect(() => {
    scroller.current = section.current;
    if (active && section.current)
      section.current.scrollTop = rememberedScroll.current;
  }, [active]);
  const canModerate =
    (auth.state === "signed-in" || auth.state === "expired") &&
    isAdministrator(auth.account);
  const canDelete =
    auth.account?.status === "active" &&
    auth.account.userId === context?.comment.author.userId;
  const unavailable =
    error instanceof PublicApiError &&
    (error.status === 404 || error.status === 403);
  useEffect(() => {
    if (!active) {
      setReplyOpen(false);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void Promise.all([
      api.post(source.postId, controller.signal),
      source.commentId
        ? api.commentContext(source.postId, source.commentId, controller.signal)
        : Promise.resolve(null),
    ])
      .then(([value, nextContext]) => {
        if (!controller.signal.aborted) {
          setPost(value);
          setContext(nextContext);
        }
      })
      .catch((e: unknown) => {
        if (!controller.signal.aborted) {
          setError(e);
          if (
            e instanceof PublicApiError &&
            (e.status === 404 || e.status === 403)
          ) {
            setPost(null);
            setContext(null);
          }
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [active, api, source.postId, source.commentId, attempt]);

  useEffect(() => {
    if (active && replyRequest && replyRequest !== lastReply.current) {
      lastReply.current = replyRequest;
      setReplyOpen(true);
    }
  }, [active, replyRequest]);
  const retryRead = useCallback(() => setAttempt((n) => n + 1), []);
  const recovering = useReadRecovery({
    error,
    loading,
    onRetry: retryRead,
    enabled: active,
    scope: mediaSourceKey(source),
  });
  const display = useMemo(
    () =>
      post && context
        ? commentAsPost(context.comment, post, context.directReplyCount)
        : post,
    [post, context],
  );
  useEffect(() => {
    if (post && display) onData(source, { post, display, unavailable });
    else if (unavailable)
      onData(source, { post: null, display: null, unavailable: true });
  }, [source, post, display, unavailable, onData]);
  // Keep one immutable display object between state changes.
  const countChanged = (delta: number) => {
    if (!post) return;
    const next = {
      ...post,
      commentCount: Math.max(0, post.commentCount + delta),
    };
    setPost(next);
    announcePostChanged({ kind: "updated", post: next });
  };
  const mutation = (value: CommentMutation) => {
    if (source.commentId || value.kind === "pin") setAttempt((n) => n + 1);
  };
  const moderateMutation = (value: CommentMutation) => {
    mutation(value);
    announceDiscussionChanged(source.postId, value, instanceId);
  };
  useEffect(
    () =>
      listenForPostChanges((event) => {
        if (event.post.id !== source.postId) return;
        if (event.kind === "deleted") {
          setError(
            new PublicApiError(t("内容已不可用"), {
              status: 404,
              code: "POST_NOT_FOUND",
            }),
          );
          setPost(null);
        } else setPost(event.post);
      }),
    [source.postId],
  );
  return (
    <div
      ref={section}
      className={styles.viewerDiscussion}
      hidden={!active}
      inert={!active}
      onScroll={(event) => {
        if (active) rememberedScroll.current = event.currentTarget.scrollTop;
      }}
    >
      {loading && !post ? <TimelineLoading /> : null}
      {error && !post && recovering ? (
        <ReadRecoveryToast active={active} onRetry={retryRead} />
      ) : null}
      {(unavailable || (error && !post)) && !recovering ? (
        <div ref={heading} tabIndex={-1}>
          <PublicError
            active={active}
            kind={unavailable ? "missing" : "detail"}
          />
          <button type="button" onClick={retryRead}>
            {t("重新读取")}
          </button>
        </div>
      ) : null}
      {post && display ? (
        <>
          {error || loading ? <ReadRecoveryToast active={active} /> : null}
          <PostPresentation
            detail
            mediaViewer
            post={display}
            site={site}
            href={sourceHref(source)}
            canPublish={
              auth.state === "signed-in" &&
              auth.account?.status === "active" &&
              !source.commentId
            }
            onDeleted={() => {
              setPost(null);
              setError(
                new PublicApiError(t("内容已不可用"), {
                  status: 404,
                  code: "POST_NOT_FOUND",
                }),
              );
            }}
            onReposted={(next) => {
              setPost(next);
              announcePostChanged({ kind: "reordered", post: next });
            }}
            onSiteChanged={onSiteChanged}
            onReply={(event) => {
              trigger.current = event.currentTarget;
              setReplyOpen(true);
            }}
            shareCard={
              source.commentId
                ? (signal) =>
                    loadCommentShareCard(
                      api,
                      post,
                      source.commentId!,
                      site,
                      signal,
                    )
                : undefined
            }
            commentsId={"comments-media-" + mediaSourceKey(source)}
            actions={
              context && (canDelete || canModerate) ? (
                <CommentModerationMenu
                  comment={context.comment}
                  focusTarget={heading}
                  pinned={context.pinnedCommentId === context.comment.id}
                  replyCount={context.directReplyCount}
                  canModerate={canModerate}
                  onDeleted={(comment, repliesAffected) => {
                    moderateMutation({
                      kind: "deleted",
                      comment,
                      repliesAffected,
                    });
                    countChanged(-(1 + repliesAffected));
                    setPost(null);
                    setError(
                      new PublicApiError(t("内容已不可用"), {
                        status: 404,
                        code: "COMMENT_NOT_FOUND",
                      }),
                    );
                  }}
                  onPinChange={(pinnedCommentId) =>
                    moderateMutation({ kind: "pin", pinnedCommentId })
                  }
                />
              ) : undefined
            }
          />
          <CommentThread
            api={api}
            postId={post.id}
            post={post}
            site={site}
            active={active}
            conversation={context?.comment}
            instanceId={instanceId}
            change={change}
            scrollContainerRef={scroller}
            onCountChange={countChanged}
            onMutation={mutation}
            replyDialogOpen={replyOpen}
            onReplyDialogOpenChange={setReplyOpen}
            replyDialogTriggerRef={trigger}
          />
        </>
      ) : null}
      <button
        className={styles.srOnly}
        type="button"
        onClick={() => navigation.navigate(sourceHref(source))}
      >
        {t("查看所属内容详情")}
      </button>
    </div>
  );
}
