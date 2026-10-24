import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type MouseEvent,
} from "react";
import {
  PublicApi,
  PublicApiError,
  type CommentContext,
  type PublicCommentAuthor,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { useReadRecovery } from "../../components/useReadRecovery";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { ReadRecoveryNotice } from "../timeline/PublicStates";
import { PostPresentation } from "../posts/PostPresentation";
import {
  CommentAuthorAvatar,
  CommentIdentity,
  CommentModerationMenu,
} from "./CommentItem";
import { CommentThread } from "./CommentThread";
import type { CommentMutation, DiscussionChange } from "./discussionChanges";
import styles from "./Comments.module.css";

function plainSummary(html: string): string {
  return (
    new DOMParser()
      .parseFromString(html, "text/html")
      .body.textContent?.trim() ?? ""
  );
}

function ContextQuote({
  label,
  author,
  href,
  summary,
  connected,
}: {
  label: string;
  author: PublicCommentAuthor;
  href: string;
  summary: string;
  connected: boolean;
}) {
  return (
    <section
      aria-label={label + "引用"}
      className={styles.contextQuote}
      data-connected={connected || undefined}
    >
      <CommentAuthorAvatar author={author} />
      <div className={styles.contextByline}>
        <span>{label === "被回复评论" ? "回复的评论" : label}</span>
        <CommentIdentity author={author} />
      </div>
      <InternalLink href={href} className={styles.contextExcerpt}>
        {summary}
      </InternalLink>
    </section>
  );
}

export function CommentDetailPage({
  api,
  post,
  site,
  commentId,
  active = true,
  change,
  onMutation,
  onCountChange,
}: {
  api: PublicApi;
  post: PublicPost;
  site: SiteIdentity;
  commentId: string;
  active?: boolean;
  change?: DiscussionChange | null;
  onMutation: (mutation: CommentMutation) => void;
  onCountChange: (delta: number) => void;
}) {
  const navigation = useNavigation();
  const auth = useSession();
  const [context, setContext] = useState<CommentContext | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [replyOpen, setReplyOpen] = useState(false);
  const sectionRef = useRef<HTMLElement>(null);
  const missingRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const request = useRef<AbortController | null>(null);
  const loaded = useRef(false);
  const revision = useRef(0);
  const load = useCallback(async () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError(null);
    try {
      const value = await api.commentContext(
        post.id,
        commentId,
        controller.signal,
      );
      if (!controller.signal.aborted) {
        setContext(value);
        loaded.current = true;
      }
    } catch (nextError) {
      if (!controller.signal.aborted) setError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, post.id, commentId]);
  useEffect(() => {
    if (active && !loaded.current) void load();
    if (!active) {
      request.current?.abort();
      setReplyOpen(false);
    }
    return () => request.current?.abort();
  }, [active, load]);
  useEffect(() => {
    if (!change || change.revision === revision.current) return;
    revision.current = change.revision;
    loaded.current = false;
    if (active) void load();
  }, [active, change, load]);
  const recovering = useReadRecovery({
    error,
    loading,
    onRetry: load,
    enabled: active,
    scope: commentId,
  });
  const missing = error instanceof PublicApiError && error.status === 404;
  useLayoutEffect(() => {
    if (active && context && !loading)
      navigation.restoreCurrentEntry(() =>
        sectionRef.current?.scrollIntoView?.({ block: "start" }),
      );
  }, [active, context, loading, navigation]);
  useEffect(() => {
    if (active && missing) missingRef.current?.focus({ preventScroll: true });
  }, [active, missing]);
  const mutation = (value: CommentMutation) => {
    onMutation(value);
    loaded.current = false;
    if (active) void load();
  };
  const postAuthor = post.author ?? {
    userId: null,
    username: null,
    displayName: site.displayName,
    avatarMediaId: null,
    profileUrl: null,
  };
  const postSummary = post.article
    ? post.article.title +
      (post.article.excerpt ? " — " + post.article.excerpt : "")
    : plainSummary(post.contentHtml) || post.location?.displayName || "图片";
  const selected = context?.comment;
  const selectedPost: PublicPost | null = selected
    ? {
        id: selected.id,
        author: selected.author,
        kind: "moment",
        article: null,
        contentMd: selected.contentMd,
        contentHtml: selected.contentHtml,
        createdAt: selected.createdAt,
        updatedAt: selected.createdAt,
        publishedAt: selected.createdAt,
        repostedAt: null,
        timelineItemKind: "original",
        visibility: post.visibility,
        commentCount: context.directReplyCount,
        media: selected.media,
        linkCards: [],
        location: selected.location,
      }
    : null;
  const canModerate =
    (auth.state === "signed-in" || auth.state === "expired") &&
    isAdministrator(auth.account);
  const canDelete =
    auth.account?.status === "active" &&
    auth.account.userId === selected?.author.userId;
  return (
    <section
      className={styles.discussionDetail}
      ref={sectionRef}
      hidden={!active}
      inert={!active}
      aria-label="评论详情"
    >
      {active ? (
        <div className={styles.discussionContext}>
          <ContextQuote
            label="原帖"
            author={postAuthor}
            href={"/m/" + post.id}
            summary={postSummary}
            connected={!!selected && !missing}
          />
          {selected && !missing && context.replyTarget ? (
            <ContextQuote
              label="被回复评论"
              author={context.replyTarget.author}
              href={commentDetailPath(post.id, context.replyTarget.id)}
              summary={
                plainSummary(context.replyTarget.contentHtml) ||
                context.replyTarget.location?.displayName ||
                "图片"
              }
              connected
            />
          ) : null}
        </div>
      ) : null}
      {missing ? (
        <div
          className={styles.threadState}
          ref={missingRef}
          role="status"
          tabIndex={-1}
        >
          <strong>这条评论已不存在或不可访问。</strong>
          <InternalLink href={"/m/" + post.id}>返回原帖</InternalLink>
        </div>
      ) : null}
      {!context && (loading || recovering) && !missing ? (
        <div className={styles.threadState} aria-busy="true">
          正在读取评论…
        </div>
      ) : null}
      {error && !recovering && !missing ? (
        <div className={styles.threadError} role="alert">
          <span>这次没有取到评论。</span>
          <button type="button" onClick={() => void load()}>
            重试
          </button>
        </div>
      ) : null}
      {selected && selectedPost && !missing ? (
        <>
          {active ? (
            <PostPresentation
              detail
              commentsId={"comments-" + selected.id}
              post={selectedPost}
              site={site}
              href={commentDetailPath(post.id, selected.id)}
              onReply={(event: MouseEvent<HTMLButtonElement>) => {
                triggerRef.current = event.currentTarget;
                setReplyOpen(true);
              }}
              actions={
                canModerate || canDelete ? (
                  <CommentModerationMenu
                    comment={selected}
                    focusTarget={missingRef}
                    pinned={context.pinnedCommentId === selected.id}
                    replyCount={selected.replyCount}
                    canModerate={canModerate}
                    onDeleted={(comment, repliesAffected) => {
                      mutation({ kind: "deleted", comment, repliesAffected });
                      onCountChange(-(1 + repliesAffected));
                    }}
                    onPinChange={(pinnedCommentId) =>
                      mutation({ kind: "pin", pinnedCommentId })
                    }
                  />
                ) : undefined
              }
            />
          ) : null}
          <CommentThread
            api={api}
            postId={post.id}
            post={post}
            conversation={selected}
            site={site}
            active={active}
            change={change}
            onMutation={mutation}
            onCountChange={onCountChange}
            replyDialogOpen={replyOpen}
            onReplyDialogOpenChange={setReplyOpen}
            replyDialogTriggerRef={triggerRef}
          />
          {loading && context ? <ReadRecoveryNotice /> : null}
        </>
      ) : null}
    </section>
  );
}
