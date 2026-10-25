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
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { useReadRecovery } from "../../components/useReadRecovery";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { ReadRecoveryNotice } from "../timeline/PublicStates";
import { PostPresentation } from "../posts/PostPresentation";
import { CommentModerationMenu } from "./CommentItem";
import {
  commentAsPost,
  commentShareCard,
  loadCommentShareCard,
} from "../share/shareCardInput";
import { CommentThread } from "./CommentThread";
import type { CommentMutation, DiscussionChange } from "./discussionChanges";
import styles from "./Comments.module.css";

export function CommentDetailPage({
  api,
  post,
  site,
  commentId,
  active = true,
  change,
  onMutation,
  onCountChange,
  onPostChanged,
  onSiteChanged,
}: {
  api: PublicApi;
  post: PublicPost;
  site: SiteIdentity;
  commentId: string;
  active?: boolean;
  change?: DiscussionChange | null;
  onMutation: (mutation: CommentMutation) => void;
  onCountChange: (delta: number) => void;
  onPostChanged?: (post: PublicPost) => void;
  onSiteChanged?: (site: SiteIdentity) => void;
}) {
  const navigation = useNavigation();
  const auth = useSession();
  const [context, setContext] = useState<CommentContext | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [replyOpen, setReplyOpen] = useState(false);
  const [dialogTarget, setDialogTarget] = useState<
    import("../../api/public").PublicComment | null | undefined
  >(undefined);
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
  const selected = context?.comment;
  const replyTarget = context?.replyTarget;
  const selectedPost = selected
    ? commentAsPost(selected, post, context.directReplyCount)
    : null;
  const canCompose = auth.account?.status === "active";
  const canModerate =
    (auth.state === "signed-in" || auth.state === "expired") &&
    isAdministrator(auth.account);
  const actionsFor = (comment: import("../../api/public").PublicComment) =>
    canModerate ||
    (auth.account?.status === "active" &&
      auth.account.userId === comment.author.userId) ? (
      <CommentModerationMenu
        comment={comment}
        focusTarget={missingRef}
        pinned={context?.pinnedCommentId === comment.id}
        replyCount={comment.replyCount}
        canModerate={canModerate}
        onDeleted={(deleted, repliesAffected) => {
          mutation({ kind: "deleted", comment: deleted, repliesAffected });
          onCountChange(-(1 + repliesAffected));
        }}
        onPinChange={(pinnedCommentId) =>
          mutation({ kind: "pin", pinnedCommentId })
        }
      />
    ) : undefined;
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
          <section aria-label="原帖引用">
            <PostPresentation
              post={post}
              site={site}
              conversation
              connected={!!selected && !missing}
              canPublish={canCompose}
              onReposted={onPostChanged}
              onSiteChanged={onSiteChanged}
              onReply={(event) => {
                triggerRef.current = event.currentTarget;
                setDialogTarget(null);
                setReplyOpen(true);
              }}
              onDeleted={() => navigation.navigate("/")}
            />
          </section>
          {selected && !missing && replyTarget ? (
            <section aria-label="被回复评论引用">
              <PostPresentation
                post={commentAsPost(replyTarget, post)}
                site={site}
                conversation
                connected
                actions={actionsFor(replyTarget)}
                href={commentDetailPath(post.id, replyTarget.id)}
                shareCard={(signal) =>
                  loadCommentShareCard(api, post, replyTarget.id, site, signal)
                }
                onReply={(event) => {
                  triggerRef.current = event.currentTarget;
                  setDialogTarget(replyTarget);
                  setReplyOpen(true);
                }}
              />
            </section>
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
              conversation
              shareCard={commentShareCard(context, post, site)}
              commentsId={"comments-" + selected.id}
              post={selectedPost}
              site={site}
              href={commentDetailPath(post.id, selected.id)}
              onReply={(event: MouseEvent<HTMLButtonElement>) => {
                triggerRef.current = event.currentTarget;
                setDialogTarget(selected);
                setReplyOpen(true);
              }}
              actions={actionsFor(selected)}
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
            replyDialogComment={dialogTarget}
            onReplyDialogOpenChange={setReplyOpen}
            replyDialogTriggerRef={triggerRef}
          />
          {loading && context ? <ReadRecoveryNotice /> : null}
        </>
      ) : null}
    </section>
  );
}
