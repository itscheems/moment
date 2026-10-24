import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import {
  PublicApi,
  PublicApiError,
  type CommentContext,
  type CommentReplyScope,
  type PublicComment,
  type PublicCommentAuthor,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { useReadRecovery } from "../../components/useReadRecovery";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { ReadRecoveryNotice } from "../timeline/PublicStates";
import {
  isCursorProgressError,
  validateCursorPage,
} from "../timeline/cursorContinuity";
import { CommentForm, SignInToReply, useReplyBlock } from "./CommentComposer";
import {
  CommentAuthorAvatar,
  CommentIdentity,
  CommentItem,
} from "./CommentItem";
import {
  clearReplyTarget,
  isDirectReply,
  mergeCommentReplies,
  type CommentMutation,
  type DiscussionChange,
} from "./discussionChanges";
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
  scope = "direct",
  active = true,
  change,
  onMutation,
  onCountChange,
}: {
  api: PublicApi;
  post: PublicPost;
  site: SiteIdentity;
  commentId: string;
  scope?: CommentReplyScope;
  active?: boolean;
  change?: DiscussionChange | null;
  onMutation: (mutation: CommentMutation) => void;
  onCountChange: (delta: number) => void;
}) {
  const navigation = useNavigation();
  const auth = useSession();
  const replyBlock = useReplyBlock();
  const [context, setContext] = useState<CommentContext | null>(null);
  const [items, setItems] = useState<PublicComment[]>([]);
  const [cursor, setCursor] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [missing, setMissing] = useState(false);
  const [content, setContent] = useState("");
  const [target, setTarget] = useState<PublicComment | null>(null);
  const [closed, setClosed] = useState(false);
  const [expanded, setExpanded] = useState(true);
  const [announcement, setAnnouncement] = useState("");
  const [recentReply, setRecentReply] = useState<string | null>(null);
  const [targetNotice, setTargetNotice] = useState<string | null>(null);
  const sectionRef = useRef<HTMLElement>(null);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const missingRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const loaded = useRef(false);
  const request = useRef<AbortController | null>(null);
  const continuation = useRef<AbortController | null>(null);
  const visited = useRef(new Set<string>());
  const recovered = useRef(new Set<string>());
  const appliedRevision = useRef(0);
  const focusReply = useRef<string | null>(null);
  const viewKey = commentId + ":" + scope;

  const load = useCallback(async () => {
    request.current?.abort();
    continuation.current?.abort();
    continuation.current = null;
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setLoadingMore(false);
    setError(null);
    setMoreError(null);
    try {
      const [nextContext, page] = await Promise.all([
        api.commentContext(post.id, commentId, controller.signal),
        api.commentReplies(post.id, commentId, {
          scope,
          signal: controller.signal,
        }),
      ]);
      if (controller.signal.aborted) return;
      setContext(nextContext);
      setItems(page.items);
      setCursor(page.nextCursor);
      visited.current = new Set();
      loaded.current = true;
      setMissing(false);
    } catch (nextError) {
      if (!controller.signal.aborted) setError(nextError);
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }, [api, post.id, commentId, scope]);

  useEffect(() => {
    if (active && !loaded.current) void load();
    if (!active) {
      request.current?.abort();
      continuation.current?.abort();
      continuation.current = null;
      setLoadingMore(false);
    }
    return () => {
      request.current?.abort();
      continuation.current?.abort();
      continuation.current = null;
    };
  }, [active, load]);

  const recovering = useReadRecovery({
    error,
    loading,
    onRetry: load,
    enabled: active && !missing,
    scope: viewKey,
  });

  const loadMore = useCallback(
    async (manual = true) => {
      if (!active || !cursor || continuation.current || loading) return;
      const controller = new AbortController();
      continuation.current = controller;
      const nextVisited = new Set(visited.current);
      nextVisited.add(cursor);
      setLoadingMore(true);
      setMoreError(null);
      try {
        const page = await api.commentReplies(post.id, commentId, {
          scope,
          cursor,
          signal: controller.signal,
        });
        if (controller.signal.aborted) return;
        validateCursorPage(page, cursor, nextVisited, "comment reply");
        visited.current = nextVisited;
        setItems((current) => mergeCommentReplies(current, page.items));
        setCursor(page.nextCursor);
        if (manual && page.items[0]) focusReply.current = page.items[0].id;
      } catch (nextError) {
        if (!controller.signal.aborted) setMoreError(nextError);
      } finally {
        if (continuation.current === controller) {
          continuation.current = null;
          if (!controller.signal.aborted) setLoadingMore(false);
        }
      }
    },
    [active, api, commentId, cursor, loading, post.id, scope],
  );

  const retryMore = () => {
    if (
      cursor &&
      isCursorProgressError(moreError) &&
      !recovered.current.has(cursor)
    ) {
      recovered.current.add(cursor);
      void load();
    } else void loadMore(false);
  };
  useReadRecovery({
    error: moreError,
    loading: loadingMore || loading,
    onRetry: retryMore,
    enabled: active && !missing,
    scope: viewKey,
  });

  useLayoutEffect(() => {
    if (!active || !context || loading) return;
    navigation.restoreCurrentEntry(() => {
      sectionRef.current?.scrollIntoView?.({ block: "start" });
    });
  }, [active, context, loading, navigation]);

  useEffect(() => {
    if (!active || !focusReply.current) return;
    const element = sectionRef.current?.querySelector<HTMLElement>(
      '[data-comment-id="' + focusReply.current + '"]',
    );
    if (element) {
      focusReply.current = null;
      element.focus({ preventScroll: true });
    }
  }, [active, items]);

  const applyMutation = useCallback(
    (mutation: CommentMutation) => {
      if (mutation.kind === "pin") {
        setContext((current) =>
          current
            ? { ...current, pinnedCommentId: mutation.pinnedCommentId }
            : current,
        );
        return;
      }
      const comment = mutation.comment;
      if (comment.postId !== post.id) return;
      if (mutation.kind === "created") {
        setContext((current) =>
          current &&
          current.comment.id === comment.parentId &&
          current.comment.parentId === null
            ? {
                ...current,
                comment: {
                  ...current.comment,
                  replyCount: current.comment.replyCount + 1,
                },
              }
            : current,
        );
        const belongs =
          scope === "thread"
            ? comment.parentId === context?.rootCommentId
            : isDirectReply(comment, commentId);
        if (belongs)
          setItems((current) => mergeCommentReplies(current, [comment]));
        return;
      }
      setRecentReply((current) => (current === comment.id ? null : current));
      if (comment.id === commentId || comment.id === context?.rootCommentId) {
        request.current?.abort();
        continuation.current?.abort();
        setMissing(true);
      }
      setItems((current) =>
        current
          .filter(
            (item) => item.id !== comment.id && item.parentId !== comment.id,
          )
          .map((item) => clearReplyTarget(item, comment.id)),
      );
      setContext((current) =>
        current
          ? {
              ...current,
              comment: {
                ...clearReplyTarget(current.comment, comment.id),
                replyCount:
                  current.comment.id === comment.parentId
                    ? Math.max(0, current.comment.replyCount - 1)
                    : current.comment.replyCount,
              },
              replyTarget:
                current.replyTarget?.id === comment.id
                  ? null
                  : current.replyTarget
                    ? clearReplyTarget(current.replyTarget, comment.id)
                    : null,
              pinnedCommentId:
                current.pinnedCommentId === comment.id
                  ? null
                  : current.pinnedCommentId,
            }
          : current,
      );
      if (target?.id === comment.id) {
        setTarget(null);
        setTargetNotice("回复对象已删除，已改为回复当前评论。草稿仍然保留。");
      }
      // A deleted target's surviving answers now belong to the root's unaddressed set.
      if (scope === "direct" && comment.parentId === commentId) {
        loaded.current = false;
        if (active) void load();
      }
    },
    [
      active,
      commentId,
      context?.rootCommentId,
      load,
      post.id,
      scope,
      target?.id,
    ],
  );

  useEffect(() => {
    if (
      !change ||
      change.source === viewKey ||
      change.revision === appliedRevision.current
    )
      return;
    appliedRevision.current = change.revision;
    applyMutation(change);
  }, [change, viewKey, applyMutation]);

  const created = (comment: PublicComment) => {
    applyMutation({ kind: "created", comment });
    onMutation({ kind: "created", comment });
    onCountChange(1);
    setAnnouncement("回复已发表");
    const visibleHere = scope === "thread" || isDirectReply(comment, commentId);
    setRecentReply(visibleHere ? null : comment.id);
    setTarget(null);
    setTargetNotice(null);
    setExpanded(true);
    if (visibleHere) focusReply.current = comment.id;
  };
  const deleted = (comment: PublicComment, repliesAffected: number) => {
    applyMutation({ kind: "deleted", comment, repliesAffected });
    onMutation({ kind: "deleted", comment, repliesAffected });
    onCountChange(-(1 + repliesAffected));
    setAnnouncement("回复已删除");
  };
  const pinChanged = (pinnedCommentId: string | null) => {
    applyMutation({ kind: "pin", pinnedCommentId });
    onMutation({ kind: "pin", pinnedCommentId });
  };
  const replyTo = (rootId: string, nextTarget: PublicComment) => {
    if (context && rootId !== context.rootCommentId) return;
    setTarget(nextTarget);
    setTargetNotice(null);
    window.requestAnimationFrame(() => {
      textareaRef.current?.focus();
      textareaRef.current?.scrollIntoView?.({ block: "center" });
    });
  };
  const canModerate =
    (auth.state === "signed-in" || auth.state === "expired") &&
    isAdministrator(auth.account);
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
    : plainSummary(post.contentHtml) ||
      post.location?.displayName ||
      (post.media.length ? "图片" : "原帖");
  const currentTarget = target ?? context?.comment;
  const missingRead =
    missing ||
    (error instanceof PublicApiError &&
      (error.status === 404 ||
        error.code === "COMMENT_NOT_FOUND" ||
        error.code === "POST_NOT_FOUND"));
  const isReady = context !== null && !missingRead;
  useEffect(() => {
    if (active && missingRead)
      missingRef.current?.focus({ preventScroll: true });
  }, [active, missingRead]);

  return (
    <section
      className={styles.discussionDetail}
      ref={sectionRef}
      hidden={!active}
      inert={!active}
      aria-label={scope === "thread" ? "整层讨论" : "评论详情"}
    >
      <p aria-live="polite" role="status" className={styles.srOnly}>
        {announcement}
      </p>
      {active ? (
        <div className={styles.discussionContext}>
          <ContextQuote
            label="原帖"
            author={postAuthor}
            href={"/m/" + post.id}
            summary={postSummary}
            connected={isReady}
          />
          {isReady && context.replyTarget ? (
            <ContextQuote
              label="被回复评论"
              author={context.replyTarget.author}
              href={commentDetailPath(post.id, context.replyTarget.id)}
              summary={
                plainSummary(context.replyTarget.contentHtml) ||
                context.replyTarget.location?.displayName ||
                "图片回复"
              }
              connected
            />
          ) : null}
        </div>
      ) : null}
      {missingRead ? (
        <div
          className={styles.threadState}
          role="status"
          ref={missingRef}
          tabIndex={-1}
        >
          <strong>这条评论已不存在或不可访问。</strong>
          <InternalLink href={"/m/" + post.id}>返回原帖</InternalLink>
        </div>
      ) : null}
      {!context && (loading || recovering) && !missingRead ? (
        <div className={styles.threadState} aria-busy="true">
          正在读取评论…
        </div>
      ) : null}
      {error && !recovering && !missingRead ? (
        <div className={styles.threadError} role="alert">
          <span>这次没有取到评论。</span>
          <button type="button" onClick={() => void load()}>
            重试
          </button>
        </div>
      ) : null}
      {isReady ? (
        <>
          <ol className={styles.commentList}>
            <CommentItem
              active={active}
              comment={context.comment}
              site={site}
              postAuthorUserId={post.author?.userId ?? null}
              presentation="focused"
              focusTarget={missingRef}
              canModerate={canModerate}
              canDelete={
                auth.account?.status === "active" &&
                auth.account.userId === context.comment.author.userId
              }
              pinned={context.pinnedCommentId === commentId}
              replyRootId={context.rootCommentId}
              canReply={replyBlock === null}
              replyBlockedBy={replyBlock ?? "signed-out"}
              replyCount={context.comment.replyCount}
              onReply={replyTo}
              onDeleted={deleted}
              onPinChange={pinChanged}
            />
          </ol>
          {targetNotice ? (
            <p className={styles.targetNotice} role="status">
              {targetNotice}
            </p>
          ) : null}
          {!site.commentsEnabled || closed ? (
            <p className={styles.closedNotice}>
              回复已关闭，过往回复继续可读。
            </p>
          ) : replyBlock === null && currentTarget ? (
            <CommentForm
              active={active}
              api={api}
              postId={post.id}
              parentId={context.rootCommentId}
              replyTargetId={currentTarget.id}
              replyTargetAuthor={
                currentTarget.author.username
                  ? "@" + currentTarget.author.username
                  : currentTarget.author.displayName
              }
              content={content}
              setContent={setContent}
              commentsEnabled={!closed && site.commentsEnabled}
              site={site}
              onCreated={created}
              onClosed={() => setClosed(true)}
              textareaRef={textareaRef}
              formId={"discussion-" + commentId + "-" + scope}
            />
          ) : (
            <SignInToReply />
          )}
          {recentReply ? (
            <p className={styles.recentReply}>
              <InternalLink href={commentDetailPath(post.id, recentReply)}>
                查看刚发布的回复
              </InternalLink>
            </p>
          ) : null}
          <h2
            className={styles.discussionHeading}
            ref={headingRef}
            tabIndex={-1}
          >
            {scope === "thread" ? "整层回复" : "直接回复"}
            <InternalLink
              href={commentDetailPath(
                post.id,
                context.rootCommentId,
                scope !== "thread",
              )}
            >
              {scope === "thread" ? "只看直接回复" : "查看整层讨论"}
            </InternalLink>
          </h2>
          {items.length === 0 && !loading ? (
            <p className={styles.threadState}>还没有回复。</p>
          ) : null}
          <ol
            className={styles.commentList}
            aria-label={scope === "thread" ? "整层回复列表" : "直接回复列表"}
          >
            {items.map((comment, index) => (
              <CommentItem
                key={comment.id}
                active={active && (expanded || index === 0)}
                navigable
                presentation={scope === "direct" ? "direct-reply" : "list"}
                comment={comment}
                site={site}
                postAuthorUserId={post.author?.userId ?? null}
                focusTarget={headingRef}
                canModerate={canModerate}
                canDelete={
                  auth.account?.status === "active" &&
                  auth.account.userId === comment.author.userId
                }
                pinned={false}
                replyRootId={context.rootCommentId}
                canReply={replyBlock === null}
                replyBlockedBy={replyBlock ?? "signed-out"}
                replyCount={0}
                onReply={replyTo}
                onDeleted={deleted}
                onPinChange={pinChanged}
              />
            ))}
          </ol>
          {items.length > 1 || cursor ? (
            <div
              className={`${styles.replyBranch} ${scope === "direct" ? styles.directReplyPaging : ""}`}
            >
              {expanded && cursor ? (
                <button
                  type="button"
                  disabled={loadingMore || loading}
                  onClick={() => void loadMore()}
                >
                  {loadingMore ? "正在加载…" : "加载更多回复"}
                </button>
              ) : null}
              {expanded && items.length > 1 ? (
                <button
                  type="button"
                  onClick={() => {
                    setExpanded(false);
                    headingRef.current?.focus({ preventScroll: true });
                    headingRef.current?.scrollIntoView?.({ block: "nearest" });
                  }}
                >
                  收起回复
                </button>
              ) : null}
              {!expanded ? (
                <button
                  type="button"
                  aria-expanded={false}
                  onClick={() => setExpanded(true)}
                >
                  展开已读取的回复
                </button>
              ) : null}
            </div>
          ) : null}
          {moreError ? (
            <ReadRecoveryNotice error={moreError}>
              正在重新读取更多回复…
            </ReadRecoveryNotice>
          ) : null}
          {loading && context ? (
            <ReadRecoveryNotice>正在重新读取评论…</ReadRecoveryNotice>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
