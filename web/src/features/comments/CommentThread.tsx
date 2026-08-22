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
import { useOwner } from "../owner/OwnerProvider";
import { CommentForm, SignInToReply, useCanComment } from "./CommentComposer";
import { CommentItem } from "./CommentItem";
import { PostReplyDialog } from "./PostReplyDialog";
import {
  appendRepliesToRoot,
  appendRoots,
  countReplies,
  mergePinnedConversation,
  orderPinnedConversation,
  reconcileCreatedComment,
  reconcileDeletedComment,
} from "./commentOrdering";
import { LoadMoreSentinel } from "../../components/LoadMoreSentinel";
import { validateCursorPage } from "../timeline/cursorContinuity";
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
  rootId: string;
  targetId: string;
  targetAuthor: string;
  content: string;
}

export function CommentThread({
  api,
  postId,
  post,
  site,
  onCountChange,
  replyDialogOpen = false,
  onReplyDialogOpenChange,
  replyDialogTriggerRef,
}: {
  api: PublicApi;
  postId: string;
  post?: PublicPost;
  site: SiteIdentity;
  /**
   * Reports how the post's comment total moved, not what it now is. A thread
   * that only ever holds one page cannot count the whole thing, and the server
   * already sends the total on the post itself.
   */
  onCountChange?: (delta: number) => void;
  replyDialogOpen?: boolean;
  onReplyDialogOpenChange?: (open: boolean) => void;
  replyDialogTriggerRef?: RefObject<HTMLButtonElement | null>;
}) {
  const owner = useOwner();
  const [items, setItems] = useState<PublicComment[] | null>(null);
  const [pinnedCommentId, setPinnedCommentId] = useState<string | null>(null);
  const [nextCursor, setNextCursor] = useState<string | null>(null);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState<unknown>(null);
  const [replyPaging, setReplyPaging] = useState<Map<string, ReplyPaging>>(
    () => new Map(),
  );
  const visitedCursors = useRef(new Set<string>());
  const [loadError, setLoadError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [topContent, setTopContent] = useState("");
  const [dialogContent, setDialogContent] = useState("");
  const [reply, setReply] = useState<ReplyDraft | null>(null);
  const [closedByServer, setClosedByServer] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusCommentRef = useRef<string | null>(null);
  const commentsEnabled = site.commentsEnabled && !closedByServer;
  const canCompose = useCanComment();

  const load = useCallback(() => setAttempt((value) => value + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    setLoadError(null);
    setMoreError(null);
    visitedCursors.current = new Set();
    void api
      .comments(postId, { signal: controller.signal })
      .then((thread) => {
        if (controller.signal.aborted) return;
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
      });
    return () => controller.abort();
  }, [api, attempt, postId]);

  const loadMoreRoots = useCallback(
    (source: "automatic" | "manual") => {
      const cursor = nextCursor;
      if (!cursor || loadingMore) return;
      // A cursor counts as spent only once a page actually comes back for it.
      // Marking it before the request would turn an ordinary network failure
      // into a permanent "cycle detected", with the retry button unable to do
      // the one thing it offers.
      const visited = new Set(visitedCursors.current);
      visited.add(cursor);
      setLoadingMore(true);
      setMoreError(null);
      void api
        .comments(postId, { cursor })
        .then((page) => {
          validateCursorPage(page, cursor, visited, "comment");
          visitedCursors.current = visited;
          const firstNewId = page.items[0]?.id;
          setItems((current) => appendRoots(current ?? [], page.items));
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
        .catch((error: unknown) => setMoreError(error))
        .finally(() => setLoadingMore(false));
    },
    [api, loadingMore, nextCursor, postId],
  );

  const loadMoreReplies = useCallback(
    (rootId: string) => {
      const paging = replyPaging.get(rootId);
      if (!paging || paging.loading || !paging.cursor) return;
      setReplyPaging((current) =>
        withReplyPaging(current, rootId, { loading: true, error: null }),
      );
      void api
        .commentReplies(postId, rootId, { cursor: paging.cursor })
        .then((page) => {
          setItems((current) =>
            current
              ? appendRepliesToRoot(current, rootId, page.items)
              : current,
          );
          setReplyPaging((current) =>
            withReplyPaging(current, rootId, {
              cursor: page.nextCursor,
              loading: false,
            }),
          );
        })
        .catch((error: unknown) =>
          setReplyPaging((current) =>
            withReplyPaging(current, rootId, { loading: false, error }),
          ),
        );
    },
    [api, postId, replyPaging],
  );

  useEffect(() => {
    const id = focusCommentRef.current;
    if (!id || !items?.some((comment) => comment.id === id)) return;
    focusCommentRef.current = null;
    document.querySelector<HTMLElement>(`[data-comment-id="${id}"]`)?.focus();
  }, [items]);

  const created = (comment: PublicComment) => {
    focusCommentRef.current = comment.id;
    setItems((current) => {
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
    setReply(null);
    setAnnouncement("回复已发表");
  };

  const deleted = (comment: PublicComment, repliesAffected: number) => {
    setItems((current) =>
      current ? reconcileDeletedComment(current, comment) : current,
    );
    if (reply?.rootId === comment.id || reply?.targetId === comment.id)
      setReply(null);
    if (pinnedCommentId === comment.id) setPinnedCommentId(null);
    onCountChange?.(-(1 + repliesAffected));
    setAnnouncement(
      repliesAffected > 0
        ? `回复和 ${repliesAffected} 条后续回复已删除`
        : "回复已删除",
    );
  };

  const replyCounts = useMemo(() => countReplies(items ?? []), [items]);

  const rootsById = useMemo(
    () =>
      new Map<string, PublicComment>(
        (items ?? [])
          .filter((comment) => comment.parentId === null)
          .map((comment) => [comment.id, comment]),
      ),
    [items],
  );

  const displayedItems = useMemo(
    () => orderPinnedConversation(items ?? [], pinnedCommentId),
    [items, pinnedCommentId],
  );

  // A root's branch ends where the next root begins. Marking the last row of
  // each run is what lets the "more replies" control sit at the bottom of the
  // branch it belongs to instead of being attached to the root itself, which
  // would put it above the replies it continues.
  const hiddenReplies = useMemo(() => {
    const loaded = countReplies(displayedItems);
    const hidden = new Map<string, number>();
    for (const comment of displayedItems) {
      if (comment.parentId !== null) continue;
      const remaining = comment.replyCount - (loaded.get(comment.id) ?? 0);
      // The count is a snapshot and the cursor is the truth about what is left
      // to fetch. A reply deleted after the page was built makes the count
      // read high; without this the branch would keep offering replies that a
      // press could never produce.
      if (remaining > 0 && replyPaging.get(comment.id)?.cursor) {
        hidden.set(comment.id, remaining);
      }
    }
    return hidden;
  }, [displayedItems, replyPaging]);

  const branchEnds = useMemo(() => {
    const ends = new Map<string, string>();
    displayedItems.forEach((comment, index) => {
      const rootId = comment.parentId ?? comment.id;
      const next = displayedItems[index + 1];
      const continues = next && (next.parentId ?? next.id) === rootId;
      if (!continues) ends.set(comment.id, rootId);
    });
    return ends;
  }, [displayedItems]);

  return (
    <section
      aria-labelledby="comments-heading"
      className={styles.thread}
      id="comments"
    >
      <h2
        className={styles.srOnly}
        id="comments-heading"
        ref={headingRef}
        tabIndex={-1}
      >
        回复
      </h2>

      <p aria-live="polite" className={styles.srOnly} role="status">
        {announcement}
      </p>

      {post ? (
        <PostReplyDialog
          api={api}
          commentsEnabled={commentsEnabled}
          content={dialogContent}
          onClosed={() => setClosedByServer(true)}
          onContentChange={setDialogContent}
          onCreated={created}
          onOpenChange={(open) => onReplyDialogOpenChange?.(open)}
          open={replyDialogOpen}
          post={post}
          site={site}
          triggerRef={replyDialogTriggerRef}
        />
      ) : null}

      {items === null && !loadError ? (
        <div aria-busy="true" className={styles.threadState}>
          <span className={styles.loadingDot} />
          <span>正在读取回复…</span>
        </div>
      ) : null}

      {loadError ? (
        <div className={styles.threadError} role="alert">
          <strong>
            {loadError instanceof PublicApiError &&
            loadError.code === "API_RESPONSE_INVALID"
              ? "回复数据无法安全显示"
              : "这次没有取到回复"}
          </strong>
          <span>Moment 正文仍可阅读；重试不会重复发表任何内容。</span>
          <button onClick={load} type="button">
            重新加载回复
          </button>
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
              api={api}
              commentsEnabled={commentsEnabled}
              content={topContent}
              onClosed={() => setClosedByServer(true)}
              onCreated={created}
              parentId={null}
              postId={postId}
              setContent={setTopContent}
              site={site}
              topLevelTargetUsername={site.username}
            />
          ) : (
            <SignInToReply />
          )}

          {items.length > 0 ? (
            <ol className={styles.commentList}>
              {displayedItems.map((comment) => (
                <Fragment key={comment.id}>
                  <CommentItem
                    comment={comment}
                    focusTarget={headingRef}
                    onDeleted={deleted}
                    onPinChange={(nextPinnedCommentId) => {
                      setPinnedCommentId(nextPinnedCommentId);
                      setAnnouncement(
                        nextPinnedCommentId ? "回复已置顶" : "已取消置顶回复",
                      );
                    }}
                    onReply={(root, target) => {
                      const targetAuthor =
                        target.authorKind === "owner"
                          ? site.displayName.trim() ||
                            site.title.trim() ||
                            target.authorName
                          : target.authorName;
                      setReply({
                        rootId: root.id,
                        targetId: target.id,
                        targetAuthor,
                        content: "",
                      });
                      window.requestAnimationFrame(() =>
                        document
                          .getElementById(`reply-${target.id}-content`)
                          ?.focus(),
                      );
                    }}
                    owner={owner.state === "owner" || owner.state === "expired"}
                    pinned={comment.id === pinnedCommentId}
                    // A null root is the existing "there is nothing to reply
                    // to here" signal, and it is the right one for a signed-out
                    // reader too: offering a reply button that can only open a
                    // sign-in prompt would repeat that prompt on every row,
                    // when the one above the list already says it once.
                    replyRoot={
                      !canCompose
                        ? null
                        : comment.parentId === null
                          ? comment
                          : (rootsById.get(comment.parentId) ?? null)
                    }
                    replyCount={replyCounts.get(comment.id) ?? 0}
                    site={site}
                  />
                  {reply?.targetId === comment.id ? (
                    <li className={styles.replyComposerRow}>
                      <CommentForm
                        api={api}
                        commentsEnabled={commentsEnabled}
                        content={reply.content}
                        onCancel={() => setReply(null)}
                        onClosed={() => setClosedByServer(true)}
                        onCreated={created}
                        onPromote={() => {
                          setTopContent(reply.content);
                          setReply(null);
                          window.requestAnimationFrame(() =>
                            document
                              .getElementById("top-level-comment-content")
                              ?.focus(),
                          );
                        }}
                        parentId={reply.rootId}
                        postId={postId}
                        replyTargetAuthor={reply.targetAuthor}
                        replyTargetId={reply.targetId}
                        setContent={(content) =>
                          setReply((current) =>
                            current ? { ...current, content } : current,
                          )
                        }
                        site={site}
                      />
                    </li>
                  ) : null}
                  {branchEnds.has(comment.id) &&
                  hiddenReplies.has(branchEnds.get(comment.id) as string) ? (
                    <ReplyBranch
                      hidden={
                        hiddenReplies.get(
                          branchEnds.get(comment.id) as string,
                        ) as number
                      }
                      onLoad={() =>
                        loadMoreReplies(branchEnds.get(comment.id) as string)
                      }
                      paging={replyPaging.get(
                        branchEnds.get(comment.id) as string,
                      )}
                    />
                  ) : null}
                </Fragment>
              ))}
            </ol>
          ) : null}

          {moreError ? (
            <div className={styles.threadError} role="alert">
              <strong>
                {moreError instanceof PublicApiError &&
                moreError.code === "API_RESPONSE_INVALID"
                  ? "后续回复无法安全显示"
                  : "这次没有取到更多回复"}
              </strong>
              <span>已经读到的回复仍在，重试不会重复发表任何内容。</span>
              <button
                onClick={() =>
                  moreError instanceof PublicApiError &&
                  moreError.code === "API_RESPONSE_INVALID"
                    ? load()
                    : loadMoreRoots("manual")
                }
                type="button"
              >
                {moreError instanceof PublicApiError &&
                moreError.code === "API_RESPONSE_INVALID"
                  ? "从头重新加载"
                  : "重试"}
              </button>
            </div>
          ) : null}

          {nextCursor && !moreError ? (
            <LoadMoreSentinel
              cursor={nextCursor}
              label="加载更多回复"
              loading={loadingMore}
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
  hidden,
  onLoad,
  paging,
}: {
  hidden: number;
  onLoad: () => void;
  paging?: ReplyPaging;
}) {
  return (
    <li className={styles.replyBranch}>
      <button disabled={paging?.loading} onClick={onLoad} type="button">
        {paging?.loading ? "正在加载…" : `展开其余 ${hidden} 条回复`}
      </button>
      {paging?.error ? (
        <span role="alert">没有取到更多回复，可以再试一次。</span>
      ) : null}
    </li>
  );
}
