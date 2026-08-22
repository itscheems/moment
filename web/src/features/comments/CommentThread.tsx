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
  countReplies,
  orderPinnedConversation,
  reconcileCreatedComment,
  reconcileDeletedComment,
} from "./commentOrdering";
import styles from "./Comments.module.css";

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
  onCountChange?: (count: number) => void;
  replyDialogOpen?: boolean;
  onReplyDialogOpenChange?: (open: boolean) => void;
  replyDialogTriggerRef?: RefObject<HTMLButtonElement | null>;
}) {
  const owner = useOwner();
  const [items, setItems] = useState<PublicComment[] | null>(null);
  const [pinnedCommentId, setPinnedCommentId] = useState<string | null>(null);
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
    void api
      .comments(postId, { signal: controller.signal })
      .then((thread) => {
        if (!controller.signal.aborted) {
          setItems(thread.items);
          setPinnedCommentId(thread.pinnedCommentId);
        }
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setLoadError(error);
      });
    return () => controller.abort();
  }, [api, attempt, postId]);

  useEffect(() => {
    if (items) onCountChange?.(items.length);
  }, [items, onCountChange]);

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
                </Fragment>
              ))}
            </ol>
          ) : null}
        </>
      ) : null}
    </section>
  );
}
