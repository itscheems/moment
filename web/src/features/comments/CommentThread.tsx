import postStyles from "../posts/PostPresentation.module.css";
import {
  Fragment,
  type RefObject,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicComment,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { CommentForm, SignInToReply, useReplyBlock } from "./CommentComposer";
import { CommentItem } from "./CommentItem";
import {
  clearReplyTarget,
  isDirectReply,
  mergeCommentReplies,
  type CommentMutation,
  type DiscussionChange,
} from "./discussionChanges";
import { PostReplyDialog } from "./PostReplyDialog";
import { commentAsPost, loadCommentShareCard } from "../share/shareCardInput";
import {
  appendRepliesToRoot,
  appendRoots,
  countReplies,
  mergePinnedConversation,
  orderPinnedConversation,
  reconcileCreatedComment,
  reconcileDeletedComment,
} from "./commentOrdering";
import {
  LoadMoreSentinel,
  type LoadSource,
} from "../../components/LoadMoreSentinel";
import { useReadRecovery } from "../../components/useReadRecovery";
import { ReadRecoveryNotice } from "../timeline/PublicStates";
import {
  isCursorProgressError,
  validateCursorPage,
} from "../timeline/cursorContinuity";
import styles from "./Comments.module.css";

/**
 * How far a single root's replies have been read.
 *
 * Roots and replies page independently, so each root carries its own cursor.
 * The cursor on the comment itself only describes the page it arrived with and
 * goes stale as soon as one more is fetched, which is why this is tracked
 * separately rather than read back off the row.
 */
interface ReplyPaging {
  cursor: string | null;
  loading: boolean;
  error: unknown;
  /**
   * Cursors this root has already been answered for. Reply pages get the same
   * cycle guard the roots, timeline, profile and search pages get: without it
   * a cursor that repeats itself leaves a control that refetches the identical
   * page forever and never reports anything wrong.
   */
  visited: string[];
}

function replyPagingFromComments(
  comments: PublicComment[],
): Map<string, ReplyPaging> {
  const paging = new Map<string, ReplyPaging>();
  for (const comment of comments) {
    if (comment.parentId !== null) continue;
    paging.set(comment.id, {
      cursor: comment.repliesNextCursor,
      loading: false,
      error: null,
      visited: [],
    });
  }
  return paging;
}

function mergeReplyPaging(
  current: Map<string, ReplyPaging>,
  comments: PublicComment[],
): Map<string, ReplyPaging> {
  const merged = new Map(current);
  for (const [id, paging] of replyPagingFromComments(comments)) {
    if (!merged.has(id)) merged.set(id, paging);
  }
  return merged;
}

function withReplyPaging(
  current: Map<string, ReplyPaging>,
  rootId: string,
  patch: Partial<ReplyPaging>,
): Map<string, ReplyPaging> {
  const existing = current.get(rootId);
  if (!existing) return current;
  const merged = new Map(current);
  merged.set(rootId, { ...existing, ...patch });
  return merged;
}

interface ReplyDraft {
  comment: PublicComment;
  content: string;
}

export function CommentThread({
  api,
  postId,
  post,
  site,
  active = true,
  onCountChange,
  replyDialogOpen = false,
  onReplyDialogOpenChange,
  replyDialogTriggerRef,
  change,
  onMutation,
  conversation,
  replyDialogComment,
}: {
  api: PublicApi;
  postId: string;
  post: PublicPost;
  site: SiteIdentity;
  /** Pause fetching and interaction while preserving the conversation drafts. */
  active?: boolean;
  /**
   * Reports how the post's comment total moved, not what it now is. A thread
   * that only ever holds one page cannot count the whole thing, and the server
   * already sends the total on the post itself.
   */
  onCountChange?: (delta: number) => void;
  replyDialogOpen?: boolean;
  onReplyDialogOpenChange?: (open: boolean) => void;
  replyDialogTriggerRef?: RefObject<HTMLButtonElement | null>;
  change?: DiscussionChange | null;
  onMutation?: (mutation: CommentMutation) => void;
  conversation?: PublicComment;
  replyDialogComment?: PublicComment | null;
}) {
  const auth = useSession();
  const canModerate =
    (auth.state === "signed-in" || auth.state === "expired") &&
    isAdministrator(auth.account);
  const [items, setItems] = useState<PublicComment[] | null>(null);
  const [expandedRoots, setExpandedRoots] = useState<Set<string>>(
    () => new Set(),
  );
  const [pinnedCommentId, setPinnedCommentId] = useState<string | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [replyPaging, setReplyPaging] = useState<Map<string, ReplyPaging>>(
    () => new Map(),
  );
  const visitedCursors = useRef(new Set<string>());
  const loadedThread = useRef<{
    api: PublicApi;
    postId: string;
    attempt: number;
  } | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [threadLoading, setThreadLoading] = useState(true);
  const continuations = useRef(new Map<string, AbortController>());
  const recoveredCursors = useRef(new Set<string>());
  const [attempt, setAttempt] = useState(0);
  const [topContent, setTopContent] = useState("");
  const [dialogDrafts, setDialogDrafts] = useState<Record<string, string>>({});
  const [reply, setReply] = useState<ReplyDraft | null>(null);
  const [replyOpen, setReplyOpen] = useState(false);
  const [closedByServer, setClosedByServer] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusCommentRef = useRef<string | null>(null);
  const commentsEnabled = site.commentsEnabled && !closedByServer;
  const replyBlock = useReplyBlock();
  const canCompose = replyBlock === null;
  const conversationId = conversation?.id;
  const dialogComment =
    replyDialogComment === null
      ? undefined
      : (replyDialogComment ?? conversation);
  const dialogKey = dialogComment?.id ?? post.id;
  const sourceKey = conversationId ? conversationId + ":direct" : "post";
  const commentsId = conversationId ? "comments-" + conversationId : "comments";
  const headingId = commentsId + "-heading";
  const readPage = useCallback(
    async (options: { cursor?: string; signal?: AbortSignal }) => {
      if (!conversationId) return api.comments(postId, options);
      const page = await api.commentReplies(postId, conversationId, {
        ...options,
        scope: "direct",
      });
      return { ...page, pinned: [], pinnedCommentId: null };
    },
    [api, postId, conversationId],
  );

  const load = useCallback(() => setAttempt((value) => value + 1), []);
  const recoveringThread = useReadRecovery({
    error: loadError,
    loading: threadLoading,
    onRetry: load,
    enabled: active,
    scope: postId,
  });
  const recoverRoots = () => {
    const key = `roots:${nextCursor}`;
    if (
      nextCursor &&
      isCursorProgressError(moreError) &&
      !recoveredCursors.current.has(key)
    ) {
      recoveredCursors.current.add(key);
      load();
    } else loadMoreRoots("automatic");
  };
  useReadRecovery({
    error: moreError,
    loading: loadingMore || threadLoading,
    onRetry: recoverRoots,
    enabled: active,
    scope: postId,
  });
  useEffect(
    () => () => {
      for (const controller of continuations.current.values())
        controller.abort();
      continuations.current.clear();
    },
    [],
  );

  useEffect(() => {
    if (!active) return;
    const loaded = loadedThread.current;
    if (
      loaded?.api === api &&
      loaded.postId === postId &&
      loaded.attempt === attempt
    )
      return;
    const controller = new AbortController();
    for (const pending of continuations.current.values()) pending.abort();
    continuations.current.clear();
    setLoadingMore(false);
    setThreadLoading(true);
    setLoadError(null);
    setMoreError(null);
    visitedCursors.current = new Set();
    void readPage({ signal: controller.signal })
      .then((thread) => {
        if (controller.signal.aborted) return;
        loadedThread.current = { api, postId, attempt };
        // The pinned conversation is delivered beside the page so that pinning
        // never reorders what a cursor addresses. Folding it back into the
        // stream at its own point in time keeps one array to reason about, and
        // keeps that array in the creation order every other helper walks.
        setItems(mergePinnedConversation(thread.items, thread.pinned));
        setPinnedCommentId(thread.pinnedCommentId);
        setNextCursor(thread.nextCursor);
        setReplyPaging(
          replyPagingFromComments([...thread.pinned, ...thread.items]),
        );
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setLoadError(error);
      })
      .finally(() => {
        if (!controller.signal.aborted) setThreadLoading(false);
      });
    return () => controller.abort();
  }, [active, api, attempt, postId, readPage]);

  const loadMoreRoots = useCallback(
    (source: "automatic" | "manual") => {
      const cursor = nextCursor;
      if (
        !active ||
        !cursor ||
        loadingMore ||
        threadLoading ||
        continuations.current.has("roots")
      )
        return;
      const controller = new AbortController();
      continuations.current.set("roots", controller);
      // A cursor counts as spent only once a page actually comes back for it.
      // Marking it before the request would turn an ordinary network failure
      // into a permanent "cycle detected", with the retry button unable to do
      // the one thing it offers.
      const visited = new Set(visitedCursors.current);
      visited.add(cursor);
      setLoadingMore(true);
      setMoreError(null);
      void readPage({ cursor, signal: controller.signal })
        .then((page) => {
          if (controller.signal.aborted) return;
          validateCursorPage(page, cursor, visited, "comment");
          visitedCursors.current = visited;
          const firstNewId = page.items[0]?.id;
          setItems((current) =>
            conversationId
              ? mergeCommentReplies(current ?? [], page.items)
              : appendRoots(current ?? [], page.items),
          );
          setReplyPaging((current) => mergeReplyPaging(current, page.items));
          setNextCursor(page.nextCursor);
          // Only a deliberate press moves focus. An observer-triggered load is
          // something the reader did not ask for, and yanking focus mid-scroll
          // would lose their place.
          if (source === "manual" && firstNewId) {
            window.requestAnimationFrame(() =>
              document
                .querySelector<HTMLElement>(`[data-comment-id="${firstNewId}"]`)
                ?.focus({ preventScroll: true }),
            );
          }
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted) setMoreError(error);
        })
        .finally(() => {
          if (continuations.current.get("roots") === controller) {
            continuations.current.delete("roots");
            if (!controller.signal.aborted) setLoadingMore(false);
          }
        });
    },
    [active, loadingMore, nextCursor, readPage, conversationId, threadLoading],
  );

  const loadMoreReplies = useCallback(
    (rootId: string, source: LoadSource = "manual") => {
      const paging = replyPaging.get(rootId);
      if (
        !active ||
        !paging ||
        paging.loading ||
        !paging.cursor ||
        threadLoading ||
        continuations.current.has(rootId)
      )
        return;
      const controller = new AbortController();
      continuations.current.set(rootId, controller);
      const cursor = paging.cursor;
      const visited = new Set(paging.visited);
      visited.add(cursor);
      setReplyPaging((current) =>
        withReplyPaging(current, rootId, { loading: true, error: null }),
      );
      void api
        .commentReplies(postId, rootId, { cursor, signal: controller.signal })
        .then((page) => {
          if (controller.signal.aborted) return;
          validateCursorPage(page, cursor, visited, "comment reply");
          const firstNewId = page.items[0]?.id;
          setItems((current) =>
            current
              ? appendRepliesToRoot(current, rootId, page.items)
              : current,
          );
          setReplyPaging((current) =>
            withReplyPaging(current, rootId, {
              cursor: page.nextCursor,
              loading: false,
              visited: [...visited],
            }),
          );
          // The pressed control is inside the run that just grew, so it is
          // unmounted and remounted further down and focus would otherwise
          // fall to the document body. Land on the first reply that arrived —
          // the thing the reader asked for — matching how the root sentinel
          // treats a deliberate press.
          if (source === "manual" && firstNewId) {
            window.requestAnimationFrame(() =>
              document
                .querySelector<HTMLElement>(`[data-comment-id="${firstNewId}"]`)
                ?.focus({ preventScroll: true }),
            );
          }
        })
        .catch((error: unknown) => {
          if (!controller.signal.aborted)
            setReplyPaging((current) =>
              withReplyPaging(current, rootId, { loading: false, error }),
            );
        })
        .finally(() => {
          if (continuations.current.get(rootId) === controller)
            continuations.current.delete(rootId);
        });
    },
    [active, api, postId, replyPaging, threadLoading],
  );

  const recoverReplies = (rootId: string, source: LoadSource) => {
    const paging = replyPaging.get(rootId);
    const key = `replies:${rootId}:${paging?.cursor}`;
    if (
      isCursorProgressError(paging?.error) &&
      !recoveredCursors.current.has(key)
    ) {
      recoveredCursors.current.add(key);
      load();
    } else loadMoreReplies(rootId, source);
  };

  useEffect(() => {
    if (!active) {
      focusCommentRef.current = null;
      return;
    }
    const id = focusCommentRef.current;
    if (!id || !items?.some((comment) => comment.id === id)) return;
    focusCommentRef.current = null;
    document.querySelector<HTMLElement>(`[data-comment-id="${id}"]`)?.focus();
  }, [active, items]);

  const created = (comment: PublicComment) => {
    if (comment.parentId)
      setExpandedRoots((current) =>
        new Set(current).add(comment.parentId as string),
      );
    focusCommentRef.current = comment.id;
    setItems((current) => {
      if (conversationId)
        return isDirectReply(comment, conversationId)
          ? mergeCommentReplies(current ?? [], [comment])
          : current;
      if (!current) return [comment];
      try {
        return reconcileCreatedComment(current, comment);
      } catch {
        load();
        return current;
      }
    });
    if (comment.parentId === null) {
      setReplyPaging((current) => mergeReplyPaging(current, [comment]));
    }
    onCountChange?.(1);
    onMutation?.({ kind: "created", comment });
    setReply(null);
    setReplyOpen(false);
    setAnnouncement("回复已发表");
  };

  const deleted = (comment: PublicComment, repliesAffected: number) => {
    setItems((current) =>
      current ? reconcileDeletedComment(current, comment) : current,
    );
    if (
      reply?.comment.parentId === comment.id ||
      reply?.comment.id === comment.id
    )
      setReply(null);
    if (pinnedCommentId === comment.id) setPinnedCommentId(null);
    onCountChange?.(-(1 + repliesAffected));
    onMutation?.({ kind: "deleted", comment, repliesAffected });
    setAnnouncement(
      repliesAffected > 0
        ? `回复和 ${repliesAffected} 条后续回复已删除`
        : "回复已删除",
    );
    if (conversationId && comment.parentId === conversationId) load();
  };

  useEffect(() => {
    if (!change || change.source === sourceKey) return;
    if (change.kind === "pin") {
      setPinnedCommentId(change.pinnedCommentId);
      return;
    }
    const comment = change.comment;
    if (comment.postId !== postId) return;
    setItems((current) => {
      if (!current) return current;
      if (conversationId)
        return change.kind === "created"
          ? isDirectReply(comment, conversationId)
            ? mergeCommentReplies(current, [comment])
            : current
          : current
              .filter((item) => item.id !== comment.id)
              .map((item) => clearReplyTarget(item, comment.id));
      if (change.kind === "created") {
        if (
          comment.parentId &&
          !current.some((item) => item.id === comment.parentId)
        )
          return current;
        return reconcileCreatedComment(current, comment);
      }
      const absent = !current.some((item) => item.id === comment.id);
      const next = reconcileDeletedComment(current, comment).map((item) =>
        clearReplyTarget(item, comment.id),
      );
      return absent && comment.parentId
        ? next.map((item) =>
            item.id === comment.parentId
              ? { ...item, replyCount: Math.max(0, item.replyCount - 1) }
              : item,
          )
        : next;
    });
    if (change.kind === "deleted") {
      setReply((current) =>
        current?.comment.parentId === comment.id ||
        current?.comment.id === comment.id
          ? null
          : current,
      );
      setPinnedCommentId((current) =>
        current === comment.id ? null : current,
      );
    }
    if (
      conversationId &&
      change.kind === "deleted" &&
      comment.parentId === conversationId
    )
      load();
  }, [change, postId, sourceKey, conversationId, load]);

  const replyCounts = useMemo(() => countReplies(items ?? []), [items]);

  const displayedItems = useMemo(
    () =>
      conversationId
        ? (items ?? [])
        : orderPinnedConversation(items ?? [], pinnedCommentId),
    [items, pinnedCommentId, conversationId],
  );

  const loadedReplies = useMemo(
    () => countReplies(displayedItems),
    [displayedItems],
  );
  const visibleItems = useMemo(() => {
    const first = new Set<string>();
    return displayedItems.filter((comment) => {
      if (conversationId) return true;
      if (!comment.parentId || expandedRoots.has(comment.parentId)) return true;
      if (first.has(comment.parentId)) return false;
      first.add(comment.parentId);
      return true;
    });
  }, [displayedItems, expandedRoots, conversationId]);
  const visibleIds = useMemo(
    () => new Set(visibleItems.map((comment) => comment.id)),
    [visibleItems],
  );
  const branchTotals = useMemo(
    () =>
      new Map(
        displayedItems
          .filter((comment) => comment.parentId === null)
          .map((comment) => [
            comment.id,
            Math.max(comment.replyCount, loadedReplies.get(comment.id) ?? 0),
          ]),
      ),
    [displayedItems, loadedReplies],
  );

  const branchEnds = useMemo(() => {
    const ends = new Map<string, string>();
    visibleItems.forEach((comment, index) => {
      const rootId = comment.parentId ?? comment.id;
      const next = visibleItems[index + 1];
      const continues = next && (next.parentId ?? next.id) === rootId;
      if (!continues) ends.set(comment.id, rootId);
    });
    return ends;
  }, [visibleItems]);

  return (
    <section
      aria-labelledby={headingId}
      className={styles.thread}
      hidden={!active}
      id={commentsId}
      inert={!active}
    >
      <h2
        className={styles.srOnly}
        id={headingId}
        ref={headingRef}
        tabIndex={-1}
      >
        回复
      </h2>

      <p aria-live="polite" className={styles.srOnly} role="status">
        {announcement}
      </p>

      {post && active ? (
        <PostReplyDialog
          api={api}
          commentsEnabled={commentsEnabled}
          content={dialogDrafts[dialogKey] ?? ""}
          onClosed={() => setClosedByServer(true)}
          onContentChange={(content) =>
            setDialogDrafts((current) => ({ ...current, [dialogKey]: content }))
          }
          onCreated={created}
          onOpenChange={(open) => onReplyDialogOpenChange?.(open)}
          open={replyDialogOpen}
          post={post}
          comment={dialogComment}
          site={site}
          triggerRef={replyDialogTriggerRef}
        />
      ) : null}
      {post && reply && active ? (
        <PostReplyDialog
          api={api}
          commentsEnabled={commentsEnabled}
          content={reply.content}
          onClosed={() => setClosedByServer(true)}
          onContentChange={(content) =>
            setReply((current) => (current ? { ...current, content } : current))
          }
          onCreated={created}
          onOpenChange={setReplyOpen}
          open={replyOpen}
          post={post}
          comment={reply.comment}
          site={site}
        />
      ) : null}

      {items === null && (!loadError || recoveringThread) ? (
        <div aria-busy="true" className={styles.threadState}>
          <span className={styles.loadingDot} />
          <span>{loadError ? "正在重新读取回复…" : "正在读取回复…"}</span>
        </div>
      ) : null}

      {items !== null && loadError && recoveringThread ? (
        <ReadRecoveryNotice>正在重新读取回复…</ReadRecoveryNotice>
      ) : null}
      {loadError && !recoveringThread ? (
        <div className={styles.threadError} role="alert">
          <strong>
            {loadError instanceof PublicApiError &&
            loadError.code === "API_RESPONSE_INVALID"
              ? "回复数据无法安全显示"
              : "这次没有取到回复"}
          </strong>
          <span>Moment 正文仍可阅读，请确认登录状态和访问链接。</span>
        </div>
      ) : null}

      {items ? (
        <>
          {!site.commentsEnabled && !closedByServer ? (
            <div className={styles.closedNotice} role="status">
              <strong>回复已关闭</strong>
              <span>过往回复继续公开可读，现在不能发表新内容。</span>
            </div>
          ) : canCompose ? (
            <CommentForm
              active={active}
              api={api}
              commentsEnabled={commentsEnabled}
              content={topContent}
              onClosed={() => setClosedByServer(true)}
              onCreated={created}
              parentId={
                conversation ? (conversation.parentId ?? conversation.id) : null
              }
              replyTargetId={conversation?.id}
              replyTargetAuthor={
                conversation?.author.username
                  ? "@" + conversation.author.username
                  : conversation?.author.displayName
              }
              formId={conversationId ? "comment-" + conversationId : undefined}
              postId={postId}
              setContent={setTopContent}
              site={site}
              topLevelTargetUsername={
                (conversation?.author ?? post?.author)?.username ?? ""
              }
            />
          ) : (
            <SignInToReply />
          )}

          {items.length > 0 ? (
            <ol className={styles.commentList}>
              {displayedItems.map((comment) => (
                <Fragment key={comment.id}>
                  <CommentItem
                    postAuthorUserId={
                      (conversation?.author ?? post.author)?.userId ?? null
                    }
                    active={active && visibleIds.has(comment.id)}
                    comment={comment}
                    navigable
                    repostTarget={commentAsPost(comment, post)}
                    shareCard={(signal) =>
                      loadCommentShareCard(api, post, comment.id, site, signal)
                    }
                    presentation={conversationId ? "direct-reply" : "list"}
                    focusTarget={headingRef}
                    onDeleted={deleted}
                    onPinChange={(nextPinnedCommentId) => {
                      setPinnedCommentId(nextPinnedCommentId);
                      onMutation?.({
                        kind: "pin",
                        pinnedCommentId: nextPinnedCommentId,
                      });
                      setAnnouncement(
                        nextPinnedCommentId ? "回复已置顶" : "已取消置顶回复",
                      );
                    }}
                    onReply={(_rootId, target) => {
                      setReply((current) =>
                        current?.comment.id === target.id
                          ? current
                          : { comment: target, content: "" },
                      );
                      setReplyOpen(true);
                    }}
                    canModerate={canModerate}
                    canDelete={
                      auth.account?.status === "active" &&
                      auth.account.userId === comment.author.userId
                    }
                    pinned={comment.id === pinnedCommentId}
                    // A null root means there is nothing to reply to, which is
                    // a different thing from not being allowed to. Being
                    // signed out travels as canReply instead, so the action
                    // keeps its place and explains itself when pressed rather
                    // than vanishing and leaving a thread full of replies with
                    // no visible way to make one.
                    canReply={canCompose}
                    replyBlockedBy={replyBlock ?? "signed-out"}
                    replyRootId={comment.parentId ?? comment.id}
                    // The root's own total, not the rows on screen: the
                    // delete confirmation is describing an irreversible
                    // cascade, and a page holds only part of a conversation.
                    // The loaded count is kept as a floor so a reply that has
                    // been optimistically inserted still counts.
                    replyCount={Math.max(
                      comment.replyCount,
                      replyCounts.get(comment.id) ?? 0,
                    )}
                    site={site}
                  />
                  {branchEnds.has(comment.id) &&
                  (branchTotals.get(branchEnds.get(comment.id)!) ?? 0) > 1
                    ? (() => {
                        const rootId = branchEnds.get(comment.id)!;
                        const expanded = expandedRoots.has(rootId);
                        return (
                          <ReplyBranch
                            active={active}
                            expanded={expanded}
                            hidden={Math.max(
                              0,
                              (branchTotals.get(rootId) ?? 0) -
                                (expanded
                                  ? (loadedReplies.get(rootId) ?? 0)
                                  : 1),
                            )}
                            onExpand={() => {
                              setExpandedRoots((current) =>
                                new Set(current).add(rootId),
                              );
                              if (
                                (replyPaging.get(rootId)?.visited.length ??
                                  0) === 0
                              )
                                recoverReplies(rootId, "manual");
                            }}
                            onCollapse={() => {
                              setExpandedRoots((current) => {
                                const next = new Set(current);
                                next.delete(rootId);
                                return next;
                              });
                              window.requestAnimationFrame(() => {
                                const row = document.querySelector<HTMLElement>(
                                  `[data-comment-id="${rootId}"]`,
                                );
                                row?.focus({ preventScroll: true });
                                if (row && row.getBoundingClientRect().top < 0)
                                  row.scrollIntoView?.({ block: "nearest" });
                              });
                            }}
                            onLoad={(source) => recoverReplies(rootId, source)}
                            paging={replyPaging.get(rootId)}
                          />
                        );
                      })()
                    : null}
                </Fragment>
              ))}
            </ol>
          ) : null}

          {moreError ? (
            <ReadRecoveryNotice error={moreError}>
              正在重新获取更多回复…
            </ReadRecoveryNotice>
          ) : null}

          {nextCursor && !moreError ? (
            <LoadMoreSentinel
              cursor={nextCursor}
              label="加载更多"
              loading={loadingMore || threadLoading}
              onLoad={loadMoreRoots}
            />
          ) : null}
        </>
      ) : null}
    </section>
  );
}

/**
 * The tail of one root's replies, offering the ones that did not travel.
 *
 * It counts down rather than announcing a total: the replies already on screen
 * are visible, so what the reader needs to know is how many they have not seen.
 */
function ReplyBranch({
  active,
  hidden,
  expanded,
  onExpand,
  onCollapse,
  onLoad,
  paging,
}: {
  active: boolean;
  hidden: number;
  expanded: boolean;
  onExpand: () => void;
  onCollapse: () => void;
  onLoad: (source: LoadSource) => void;
  paging?: ReplyPaging;
}) {
  useReadRecovery({
    error: paging?.error,
    loading: Boolean(paging?.loading),
    onRetry: () => onLoad("automatic"),
    enabled: active,
  });
  return (
    <li className={styles.replyBranch}>
      {!paging?.error && (!expanded || paging?.cursor) ? (
        <button
          className={postStyles.readMore}
          disabled={paging?.loading}
          aria-expanded={expanded}
          onClick={() => (expanded ? onLoad("manual") : onExpand())}
          type="button"
        >
          {paging?.loading
            ? "正在加载…"
            : expanded
              ? "加载更多"
              : `展开其余 ${hidden} 条回复`}
        </button>
      ) : null}
      {expanded ? (
        <button
          className={postStyles.readMore}
          type="button"
          onClick={onCollapse}
        >
          收起
        </button>
      ) : null}
      {paging?.error ? (
        <ReadRecoveryNotice error={paging.error}>
          正在重新获取回复…
        </ReadRecoveryNotice>
      ) : null}
    </li>
  );
}
