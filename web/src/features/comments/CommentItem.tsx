import { AccountIdentityPreview } from "../../components/AccountIdentityPreview";
import { commentsApi } from "../../api/comments";
import { Dialog } from "../../components/Dialog";
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import { motion, useIsPresent, useReducedMotion } from "motion/react";
import { type RefObject, useEffect, useRef, useState } from "react";

import {
  PublicApiError,
  type PublicComment,
  type PublicCommentAuthor,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { Icon } from "../../components/icons";
import { notify } from "../../components/Feedback";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { PostPresentation } from "../posts/PostPresentation";
import { commentAsPost } from "../share/shareCardInput";
import { useSession } from "../auth/SessionProvider";
import { Reauthenticate, type ReplyBlock } from "./CommentComposer";
import styles from "./Comments.module.css";
import postStyles from "../posts/PostPresentation.module.css";
import formStyles from "../../components/Forms.module.css";
import type { ShareCardSource } from "../share/shareCard";

function DeleteCommentDialog({
  postId,
  comment,
  replyCount,
  cascadeAllowed,
  focusTarget,
  open,
  onOpenChange,
  triggerRef,
  onDeleted,
}: {
  postId: string;
  comment: PublicComment;
  replyCount: number;
  cascadeAllowed: boolean;
  focusTarget: RefObject<HTMLElement | null>;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
}) {
  const auth = useSession();
  const { session, expireIfInvalidSession } = auth;
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const pendingRef = useRef(false);
  const deletedRef = useRef(false);
  const root = comment.parentId === null;
  const displayReauth = authExpired || (auth.state === "expired" && !session);
  const displayedError =
    error ??
    (displayReauth ? "登录会话已过期。刷新身份后可在这里重新删除。" : null);

  useDocumentScrollLock(open);

  useEffect(() => {
    if (!authExpired || !session) return;
    setAuthExpired(false);
    setError(null);
  }, [authExpired, session]);

  const remove = async () => {
    if (!session || pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const result = await commentsApi.deleteComment(
        session.csrfToken,
        postId,
        comment.id,
      );
      deletedRef.current = true;
      onDeleted(result.comment, result.repliesAffected);
      onOpenChange(false);
      window.requestAnimationFrame(() => focusTarget.current?.focus());
    } catch (nextError) {
      if (expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("登录会话已过期。刷新身份后可在这里重新删除。");
      } else {
        setError(
          nextError instanceof PublicApiError &&
            nextError.code === "COMMENT_NOT_FOUND"
            ? "这条回复已经不存在。关闭后重新加载列表。"
            : nextError instanceof PublicApiError &&
                nextError.code === "UNAUTHORIZED" &&
                nextError.details.replies_by_others
              ? "这段对话包含他人的回复，需要管理员删除。"
              : "没有删除这条回复。列表保持不变，可以安全重试。",
        );
      }
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        if (pending) return;
        deletedRef.current = false;
        setError(null);
        onOpenChange(nextOpen);
      }}
      open={open}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup
            className={styles.dialogPopup}
            finalFocus={() =>
              deletedRef.current ? focusTarget.current : triggerRef.current
            }
          >
            <Dialog.Title className={styles.dialogTitle}>
              {root ? "删除这条回复？" : "删除这条回复？"}
            </Dialog.Title>
            <Dialog.Description className={styles.dialogDescription}>
              {root && replyCount > 0 && cascadeAllowed
                ? `这条回复和它下面的 ${replyCount} 条回复都会永久删除。`
                : "这条回复会从公开对话中永久删除。"}
            </Dialog.Description>
            {displayedError ? (
              <p className={styles.dialogError} role="alert">
                {displayedError}
              </p>
            ) : null}
            {displayReauth ? (
              <Reauthenticate
                inputId={`delete-${comment.id}-api-token`}
                onReady={() => {
                  setAuthExpired(false);
                  setError(null);
                }}
              />
            ) : null}
            <div className={styles.dialogActions}>
              <Dialog.Close
                className={styles.secondaryAction}
                disabled={pending}
              >
                保留
              </Dialog.Close>
              <button
                aria-busy={pending || undefined}
                className={styles.dangerAction}
                data-pending={pending ? "true" : undefined}
                disabled={pending || !session}
                onClick={() => void remove()}
                type="button"
              >
                {pending
                  ? "正在删除…"
                  : root && replyCount > 0 && cascadeAllowed
                    ? "删除回复及其对话"
                    : root
                      ? "删除回复"
                      : "删除回复"}
              </button>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

export function CommentModerationMenu({
  comment,
  focusTarget,
  pinned,
  replyCount,
  onDeleted,
  onPinChange,
  canModerate,
}: {
  canModerate: boolean;
  comment: PublicComment;
  focusTarget: RefObject<HTMLElement | null>;
  pinned: boolean;
  replyCount: number;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
  onPinChange: (pinnedCommentId: string | null) => void;
}) {
  const auth = useSession();
  const [menuOpen, setMenuOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [pinOpen, setPinOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const pendingRef = useRef(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const root = comment.parentId === null;
  const displayReauth =
    authExpired || (auth.state === "expired" && !auth.session);

  useDocumentScrollLock(pinOpen);

  useEffect(() => {
    if (!authExpired || !auth.session) return;
    setAuthExpired(false);
    setError(null);
  }, [authExpired, auth.session]);

  const changePin = async () => {
    if (!auth.session || pendingRef.current) {
      setAuthExpired(true);
      return;
    }
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const result = pinned
        ? await commentsApi.unpinComment(
            auth.session.csrfToken,
            comment.postId,
            comment.id,
          )
        : await commentsApi.pinComment(
            auth.session.csrfToken,
            comment.postId,
            comment.id,
          );
      onPinChange(result.pinnedCommentId);
      setPinOpen(false);
      notify(pinned ? "已取消置顶回复" : "已置顶回复");
    } catch (nextError) {
      if (auth.expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("登录会话已过期。刷新身份后可在这里重试。");
      } else {
        setError("置顶状态没有改变，可以安全重试。");
      }
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <div className={styles.commentActionsMenu}>
      <Menu.Root onOpenChange={setMenuOpen} open={menuOpen}>
        <Menu.Trigger
          aria-label="回复操作"
          className={formStyles.actionsMenuTrigger}
          ref={triggerRef}
        >
          <Icon name="more" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            align="end"
            className={formStyles.actionsMenuPositioner}
            side="bottom"
            sideOffset={4}
          >
            <Menu.Popup
              aria-label="回复操作"
              className={formStyles.actionsMenuPopup}
            >
              {root && canModerate ? (
                <Menu.Item
                  className={formStyles.actionsMenuItem}
                  onClick={() => {
                    setMenuOpen(false);
                    setError(null);
                    setPinOpen(true);
                  }}
                >
                  <Icon name="pin" />
                  <span>{pinned ? "取消置顶" : "置顶回复"}</span>
                </Menu.Item>
              ) : null}
              <Menu.Item
                className={`${formStyles.actionsMenuItem} ${formStyles.actionsMenuDanger}`}
                onClick={() => {
                  setMenuOpen(false);
                  setDeleteOpen(true);
                }}
              >
                <Icon name="delete" />
                <span>删除</span>
              </Menu.Item>
            </Menu.Popup>
          </Menu.Positioner>
        </Menu.Portal>
      </Menu.Root>

      <DeleteCommentDialog
        cascadeAllowed={canModerate}
        comment={comment}
        focusTarget={focusTarget}
        onDeleted={onDeleted}
        onOpenChange={setDeleteOpen}
        open={deleteOpen}
        postId={comment.postId}
        replyCount={replyCount}
        triggerRef={triggerRef}
      />

      <Dialog.Root
        onOpenChange={(open) => {
          if (pending) return;
          setPinOpen(open);
          if (!open) {
            setError(null);
            window.requestAnimationFrame(() => triggerRef.current?.focus());
          }
        }}
        open={pinOpen}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.dialogBackdrop} />
          <Dialog.Viewport className={styles.dialogViewport}>
            <Dialog.Popup
              className={styles.dialogPopup}
              finalFocus={() => triggerRef.current}
            >
              <Dialog.Title className={styles.dialogTitle}>
                {pinned ? "取消置顶这条回复？" : "置顶这条回复？"}
              </Dialog.Title>
              <Dialog.Description className={styles.dialogDescription}>
                {pinned
                  ? "它会回到这段对话中的原始时间位置。"
                  : "它和下面的直接回复会显示在评论区最前；已有置顶会被替换。"}
              </Dialog.Description>
              {error ? (
                <p className={styles.dialogError} role="alert">
                  {error}
                </p>
              ) : null}
              {displayReauth ? (
                <Reauthenticate
                  inputId={`pin-${comment.id}-api-token`}
                  onReady={() => {
                    setAuthExpired(false);
                    setError(null);
                  }}
                />
              ) : null}
              <div className={styles.dialogActions}>
                <Dialog.Close
                  className={styles.secondaryAction}
                  disabled={pending}
                >
                  取消
                </Dialog.Close>
                <button
                  aria-busy={pending || undefined}
                  className={styles.primaryAction}
                  disabled={pending || !auth.session}
                  onClick={() => void changePin()}
                  type="button"
                >
                  {pending ? "正在处理…" : pinned ? "取消置顶" : "置顶"}
                </button>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}

/**
 * The reply action for a reader who is not signed in.
 *
 * It keeps the affordance and answers on press rather than disappearing.
 * Hiding it would leave a thread full of replies with no visible way to make
 * one, which reads as broken rather than as gated — and the notice above the
 * list is easy to scroll past by the time a reader reaches the comment they
 * actually want to answer.
 *
 * Press, not hover: hovering an action the reader cannot use should not throw
 * a card over the comment they are reading.
 */
function SignInToReplyPopover({
  authorName,
  reason,
  count,
}: {
  authorName: string;
  reason: ReplyBlock;
  count: number;
}) {
  const pending = reason === "pending";
  return (
    <Popover.Root>
      <Popover.Trigger
        data-post-action="reply"
        aria-label={`回复 ${authorName}${count > 0 ? `，${count} 条回复` : ""}`}
        className={`${postStyles.postAction} ${postStyles.replyAction}`}
        onClick={(event) => event.stopPropagation()}
      >
        <Icon name="reply" />
        {count > 0 ? (
          <span className={postStyles.actionCount}>{count}</span>
        ) : null}
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Positioner
          align="start"
          className={styles.signInPositioner}
          collisionAvoidance={{ side: "flip", align: "shift" }}
          collisionPadding={8}
          side="bottom"
          sideOffset={6}
        >
          <Popover.Popup
            className={styles.signInPopup}
            onClick={(event) => event.stopPropagation()}
          >
            <Popover.Title className={styles.signInPopupTitle}>
              {pending ? "正在等待管理员批准" : "登录后回复"}
            </Popover.Title>
            <Popover.Description className={styles.signInPopupBody}>
              {pending
                ? "你的账户正在等待管理员批准，批准后就能留言。"
                : "发表回复需要登录。过往回复继续公开可读。"}
            </Popover.Description>
            {pending ? null : (
              <InternalLink className={styles.signInPopupAction} href="/login">
                登录
              </InternalLink>
            )}
          </Popover.Popup>
        </Popover.Positioner>
      </Popover.Portal>
    </Popover.Root>
  );
}

export function CommentIdentity({ author }: { author: PublicCommentAuthor }) {
  if (!author.username || !author.profileUrl)
    return <span>{author.displayName}</span>;
  return (
    <AccountIdentityPreview author={author}>
      <InternalLink className={styles.commentAuthor} href={author.profileUrl}>
        {author.displayName}
      </InternalLink>
    </AccountIdentityPreview>
  );
}

export function CommentItem({
  active = true,
  comment,
  site,
  postAuthorUserId = null,
  canModerate,
  canDelete = false,
  pinned,
  replyRootId,
  presentation = "list",
  navigable = false,
  canReply = true,
  replyBlockedBy = "signed-out",
  replyCount,
  focusTarget,
  onReply,
  onDeleted,
  onPinChange,
  shareCard,
  repostTarget,
  replyBranch,
}: {
  active?: boolean;
  comment: PublicComment;
  site: SiteIdentity;
  postAuthorUserId?: string | null;
  canModerate: boolean;
  canDelete?: boolean;
  pinned: boolean;
  replyRootId: string | null;
  presentation?: "list" | "direct-reply";
  navigable?: boolean;
  shareCard?: ShareCardSource;
  repostTarget?: PublicPost;
  replyBranch?: import("react").ReactNode;
  /**
   * Whether this reader may actually reply. It is deliberately separate from
   * replyRoot: a null root means there is nothing to reply to and the action
   * has no meaning, while canReply false means the action means something the
   * reader has to sign in to do. The two want different answers on screen.
   */
  canReply?: boolean;
  /**
   * Why replying is unavailable, for when it is. Signed out points at the way
   * in; waiting on approval is already inside and has nothing to press.
   */
  replyBlockedBy?: ReplyBlock;
  replyCount: number;
  focusTarget: RefObject<HTMLElement | null>;
  onReply: (rootId: string, target: PublicComment) => void;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
  onPinChange: (pinnedCommentId: string | null) => void;
}) {
  const root = comment.parentId === null;
  const author = comment.author;
  const authorName = author.displayName;
  const isPostAuthor =
    author.userId !== null && author.userId === postAuthorUserId;
  const isPresent = useIsPresent();
  const reduceMotion = useReducedMotion();
  if (!active) return null;
  const projected = {
    ...(repostTarget ??
      commentAsPost(comment, { id: comment.postId, visibility: "everyone" })),
    commentCount: root ? replyCount : comment.replyCount,
  };
  return (
    <motion.li
      initial={{ height: 0, borderBottomWidth: 0 }}
      animate={{ height: "auto", borderBottomWidth: 1 }}
      exit={{ height: 0, borderBottomWidth: 0 }}
      transition={{ duration: reduceMotion ? 0 : 0.24, ease: [0.2, 0, 0, 1] }}
      style={{ overflow: "hidden" }}
      inert={!isPresent}
      aria-hidden={!isPresent || undefined}
      className={`${styles.commentItem} ${!root && presentation === "list" ? styles.replyComment : ""}`}
      data-comment-id={comment.id}
      data-focus-key={"comment-row-" + comment.id}
      data-depth={presentation === "list" ? comment.depth : 0}
      data-presentation={presentation}
      tabIndex={-1}
    >
      <PostPresentation
        post={projected}
        site={site}
        timelineDensity
        embedded
        navigable={navigable}
        href={commentDetailPath(comment.postId, comment.id)}
        pinned={pinned}
        actions={
          canModerate || canDelete ? (
            <CommentModerationMenu
              canModerate={canModerate}
              comment={comment}
              focusTarget={focusTarget}
              onDeleted={onDeleted}
              onPinChange={onPinChange}
              pinned={pinned}
              replyCount={replyCount}
            />
          ) : undefined
        }
        bylineContext={
          <>
            {isPostAuthor ? (
              <span className={styles.authorBadge}>作者</span>
            ) : null}
            {!root ? (
              comment.replyTo ? (
                <span className={styles.commentReplyTo}>
                  <span>回复</span>
                  <CommentIdentity author={comment.replyTo.author} />
                </span>
              ) : (
                <span className={styles.commentReplyUnknown}>楼内回复</span>
              )
            ) : null}
          </>
        }
        replyControl={
          !replyRootId ? null : !canReply ? (
            <SignInToReplyPopover
              authorName={authorName}
              reason={replyBlockedBy}
              count={projected.commentCount}
            />
          ) : undefined
        }
        onReply={() => {
          if (replyRootId) onReply(replyRootId, comment);
        }}
        shareCard={shareCard}
      />
      {replyBranch}
    </motion.li>
  );
}
