import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { type RefObject, useEffect, useRef, useState } from "react";

import {
  PublicApiError,
  type PublicComment,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { SiteAvatar } from "../../components/SiteIdentity";
import {
  ActionTooltip,
  ExactTimeTooltip,
  SiteIdentityPreview,
} from "../../components/TimelineInteractions";
import { Icon } from "../../components/icons";
import { notify } from "../../components/Feedback";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { MarkdownContent } from "../../components/MarkdownContent";
import { formatMomentDate } from "../posts/dates";
import { PostLocation } from "../posts/PostPresentation";
import { ImageGrid } from "../media/ImageGrid";
import { useOwner } from "../owner/OwnerProvider";
import { Reauthenticate } from "./CommentComposer";
import styles from "./Comments.module.css";
import ownerStyles from "../owner/Owner.module.css";

function DeleteCommentDialog({
  postId,
  comment,
  replyCount,
  focusTarget,
  open,
  onOpenChange,
  triggerRef,
  onDeleted,
}: {
  postId: string;
  comment: PublicComment;
  replyCount: number;
  focusTarget: RefObject<HTMLElement | null>;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  triggerRef: RefObject<HTMLButtonElement | null>;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
}) {
  const owner = useOwner();
  const { api, session, expireIfInvalidSession } = owner;
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const pendingRef = useRef(false);
  const deletedRef = useRef(false);
  const root = comment.parentId === null;
  const displayReauth = authExpired || (owner.state === "expired" && !session);
  const displayedError =
    error ??
    (displayReauth ? "所有者会话已过期。刷新身份后可在这里重新删除。" : null);

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
      const result = await api.deleteComment(
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
        setError("所有者会话已过期。刷新身份后可在这里重新删除。");
      } else {
        setError(
          nextError instanceof PublicApiError &&
            nextError.code === "COMMENT_NOT_FOUND"
            ? "这条回复已经不存在。关闭后重新加载列表。"
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
              {root && replyCount > 0
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
                inputId={`delete-${comment.id}-owner-token`}
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
                  : root && replyCount > 0
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

function CommentOwnerMenu({
  comment,
  focusTarget,
  pinned,
  replyCount,
  onDeleted,
  onPinChange,
}: {
  comment: PublicComment;
  focusTarget: RefObject<HTMLElement | null>;
  pinned: boolean;
  replyCount: number;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
  onPinChange: (pinnedCommentId: string | null) => void;
}) {
  const owner = useOwner();
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
    authExpired || (owner.state === "expired" && !owner.session);

  useDocumentScrollLock(pinOpen);

  useEffect(() => {
    if (!authExpired || !owner.session) return;
    setAuthExpired(false);
    setError(null);
  }, [authExpired, owner.session]);

  const changePin = async () => {
    if (!owner.session || pendingRef.current) {
      setAuthExpired(true);
      return;
    }
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const result = pinned
        ? await owner.api.unpinComment(
            owner.session.csrfToken,
            comment.postId,
            comment.id,
          )
        : await owner.api.pinComment(
            owner.session.csrfToken,
            comment.postId,
            comment.id,
          );
      onPinChange(result.pinnedCommentId);
      setPinOpen(false);
      notify(pinned ? "已取消置顶回复" : "已置顶回复");
    } catch (nextError) {
      if (owner.expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("所有者会话已过期。刷新身份后可在这里重试。");
      } else {
        setError("置顶状态没有改变，可以安全重试。");
      }
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <div className={styles.commentOwnerMenu}>
      <Menu.Root onOpenChange={setMenuOpen} open={menuOpen}>
        <Menu.Trigger
          aria-label="回复操作"
          className={ownerStyles.ownerMenuTrigger}
          ref={triggerRef}
        >
          <Icon name="more" />
        </Menu.Trigger>
        <Menu.Portal>
          <Menu.Positioner
            align="end"
            className={ownerStyles.ownerMenuPositioner}
            side="bottom"
            sideOffset={4}
          >
            <Menu.Popup
              aria-label="回复操作"
              className={ownerStyles.ownerMenuPopup}
            >
              {root ? (
                <Menu.Item
                  className={ownerStyles.ownerMenuItem}
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
                className={`${ownerStyles.ownerMenuItem} ${ownerStyles.ownerMenuDanger}`}
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
                  inputId={`pin-${comment.id}-owner-token`}
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
                  disabled={pending || !owner.session}
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

export function CommentItem({
  comment,
  site,
  owner,
  pinned,
  replyRoot,
  replyCount,
  focusTarget,
  onReply,
  onDeleted,
  onPinChange,
}: {
  comment: PublicComment;
  site: SiteIdentity;
  owner: boolean;
  pinned: boolean;
  replyRoot: PublicComment | null;
  replyCount: number;
  focusTarget: RefObject<HTMLElement | null>;
  onReply: (root: PublicComment, target: PublicComment) => void;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
  onPinChange: (pinnedCommentId: string | null) => void;
}) {
  const created = formatMomentDate(comment.createdAt, site.timezone);
  const root = comment.parentId === null;
  // The non-owner branch below is the legacy read-only `guest` kind: rows that
  // predate the account requirement, or that an old archive brought back. They
  // carry only a free-text name, so they render as plain text — no `@username`,
  // no profile link, no preview card, no badge. Keep it: dropping it would
  // strand comments the owner still has in their own backups.
  const authorName =
    comment.authorKind === "owner"
      ? site.displayName.trim() || site.title.trim() || comment.authorName
      : comment.authorName;
  return (
    <li
      className={`${styles.commentItem} ${root ? "" : styles.replyComment}`}
      data-comment-id={comment.id}
      data-depth={comment.depth}
      tabIndex={-1}
    >
      <article aria-label={`${authorName}的回复`}>
        {owner ? (
          <CommentOwnerMenu
            comment={comment}
            focusTarget={focusTarget}
            onDeleted={onDeleted}
            onPinChange={onPinChange}
            pinned={pinned}
            replyCount={replyCount}
          />
        ) : null}
        {comment.authorKind === "owner" ? (
          <SiteIdentityPreview site={site}>
            <InternalLink
              aria-label={`查看 ${authorName} 的个人资料`}
              className={styles.commentAvatar}
              href={`/@${site.username}`}
            >
              <SiteAvatar site={site} />
            </InternalLink>
          </SiteIdentityPreview>
        ) : (
          <span aria-hidden="true" className={styles.commentAvatar}>
            {Array.from(authorName)[0]?.toLocaleUpperCase() ?? "M"}
          </span>
        )}
        {pinned ? (
          <p className={styles.pinnedCommentMark}>
            <Icon name="pin" />
            <span>已置顶</span>
          </p>
        ) : null}
        <header className={styles.commentByline}>
          {comment.authorKind === "owner" ? (
            <SiteIdentityPreview site={site}>
              <InternalLink
                className={styles.commentAuthor}
                href={`/@${site.username}`}
              >
                {authorName}
              </InternalLink>
            </SiteIdentityPreview>
          ) : (
            <strong>{authorName}</strong>
          )}
          {comment.authorKind === "owner" ? (
            <span className={styles.ownerBadge}>所有者</span>
          ) : null}
          <span aria-hidden="true">·</span>
          <ExactTimeTooltip exact={created.absolute}>
            <time dateTime={comment.createdAt}>{created.relative}</time>
          </ExactTimeTooltip>
        </header>
        {comment.contentHtml ? (
          <MarkdownContent
            className={styles.commentMarkdown}
            html={comment.contentHtml}
            mentionUsername={site.username}
          />
        ) : null}
        {comment.location ? <PostLocation location={comment.location} /> : null}
        <ImageGrid media={comment.media} />
        <footer className={styles.commentActions}>
          {replyRoot ? (
            <ActionTooltip label="回复">
              <button
                aria-label={`回复 ${authorName}`}
                className={styles.commentAction}
                onClick={() => onReply(replyRoot, comment)}
                type="button"
              >
                <Icon name="reply" />
              </button>
            </ActionTooltip>
          ) : (
            <span />
          )}
        </footer>
      </article>
    </li>
  );
}
