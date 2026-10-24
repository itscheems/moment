import { AccountIdentityPreview } from "../../components/AccountIdentityPreview";
import { commentsApi } from "../../api/comments";
import { Dialog } from "../../components/Dialog";
import { Menu } from "@base-ui/react/menu";
import { Popover } from "@base-ui/react/popover";
import {
  type RefObject,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

import {
  PublicApiError,
  type PublicComment,
  type PublicCommentAuthor,
  type SiteIdentity,
} from "../../api/public";
import { InternalLink, useOptionalNavigation } from "../../app/navigation";
import { commentDetailPath } from "../../app/routes";
import { SiteAvatar } from "../../components/SiteIdentity";
import {
  ActionTooltip,
  ExactTimeTooltip,
} from "../../components/TimelineInteractions";
import { Icon } from "../../components/icons";
import { notify } from "../../components/Feedback";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { MarkdownBody } from "../../components/content/MarkdownBody";
import { formatMomentDate } from "../posts/dates";
import { PostLocation } from "../posts/PostPresentation";
import { ImageGrid } from "../media/ImageGrid";
import { useSession } from "../auth/SessionProvider";
import { Reauthenticate, type ReplyBlock } from "./CommentComposer";
import styles from "./Comments.module.css";
import formStyles from "../../components/Forms.module.css";

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
 * Clip the comment body and report whether anything is actually hidden.
 *
 * The clip is unconditional — it lives in CSS and is on from the first paint —
 * so a body that fits is simply never touched by a maximum it does not reach,
 * and nothing on screen ever expands and then snaps shut. All this hook
 * decides is whether to offer the control, and it decides that from the
 * rendered box rather than from the text. Character counts cannot answer it:
 * the same 535 characters of Chinese are 16 lines in the desktop column and 34
 * on a phone, and Latin text of that length is a fraction of either. So it
 * measures, and measures again whenever the box or the font metrics change.
 *
 * Measuring is skipped while expanded, where the clip is off and `scrollHeight`
 * necessarily equals `clientHeight`: believing that reading would decide the
 * body fits and withdraw the control that puts it back.
 */
function useFoldedBody(contentHtml: string, active: boolean) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);

  useEffect(() => {
    const body = bodyRef.current;
    if (!active || !body || expanded) return;
    let live = true;
    const measure = () => {
      if (!live) return;
      setOverflowing(body.scrollHeight > body.clientHeight);
    };
    measure();
    // Web fonts land after first paint and change the line box under the clip.
    void document.fonts?.ready.then(measure);
    if (typeof ResizeObserver === "undefined") {
      return () => {
        live = false;
      };
    }
    const observer = new ResizeObserver(measure);
    observer.observe(body);
    return () => {
      live = false;
      observer.disconnect();
    };
  }, [active, contentHtml, expanded]);

  return { bodyRef, expanded, overflowing, setExpanded };
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
}: {
  authorName: string;
  reason: ReplyBlock;
}) {
  const pending = reason === "pending";
  return (
    <Popover.Root>
      <Popover.Trigger
        aria-label={`回复 ${authorName}`}
        className={styles.commentAction}
        onClick={(event) => event.stopPropagation()}
      >
        <Icon name="reply" />
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
    return <strong>{author.displayName}</strong>;
  return (
    <>
      <AccountIdentityPreview author={author}>
        <InternalLink className={styles.commentAuthor} href={author.profileUrl}>
          {author.displayName}
        </InternalLink>
      </AccountIdentityPreview>
      <InternalLink className={styles.commentHandle} href={author.profileUrl}>
        @{author.username}
      </InternalLink>
    </>
  );
}

export function CommentAuthorAvatar({
  author,
}: {
  author: PublicCommentAuthor;
}) {
  return (
    <AccountIdentityPreview author={author}>
      {author.username && author.profileUrl ? (
        <InternalLink
          aria-label={`查看 ${author.displayName} 的个人资料`}
          className={styles.commentAvatar}
          href={author.profileUrl}
        >
          <SiteAvatar site={{ ...author, coverMediaId: null }} />
        </InternalLink>
      ) : (
        <span aria-hidden="true" className={styles.commentAvatar}>
          {Array.from(author.displayName)[0]?.toLocaleUpperCase() ?? "M"}
        </span>
      )}
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
  const created = formatMomentDate(comment.createdAt, site.timezone);
  const navigation = useOptionalNavigation();
  const detailHref = commentDetailPath(comment.postId, comment.id);
  const root = comment.parentId === null;
  const author = comment.author;
  const authorName = author.displayName;
  const isPostAuthor =
    author.userId !== null && author.userId === postAuthorUserId;
  const headerRef = useRef<HTMLElement>(null);
  const restoreScrollRef = useRef(false);
  const { bodyRef, expanded, overflowing, setExpanded } = useFoldedBody(
    comment.contentHtml,
    active,
  );
  const bodyId = `comment-${comment.id}-body`;

  const replyTo = comment.replyTo;
  const bodyExpanded = expanded;

  // Collapsing pulls everything below it upward. If the comment's own top has
  // already scrolled past, the page jumps and the reader loses the row they
  // were reading; put it back where it was.
  useLayoutEffect(() => {
    if (!restoreScrollRef.current) return;
    restoreScrollRef.current = false;
    const header = headerRef.current;
    if (!header || header.getBoundingClientRect().top >= 0) return;
    header.scrollIntoView?.({ block: "nearest" });
  }, [expanded]);

  // Keep the folded-body state, but release the row's portals and scroll locks
  // while its conversation is suspended for focused article reading.
  if (!active) return null;

  return (
    <li
      className={`${styles.commentItem} ${!root && presentation === "list" ? styles.replyComment : ""}`}
      data-comment-id={comment.id}
      data-focus-key={"comment-row-" + comment.id}
      data-depth={presentation === "list" ? comment.depth : 0}
      data-presentation={presentation}
      tabIndex={-1}
    >
      <article
        aria-label={`${authorName}的回复`}
        onClick={(event) => {
          if (
            !navigable ||
            event.button !== 0 ||
            event.metaKey ||
            event.ctrlKey ||
            event.shiftKey ||
            event.altKey ||
            window.getSelection()?.toString()
          )
            return;
          const target = event.target;
          if (
            target instanceof Element &&
            target.closest(
              "a,button,input,textarea,select,[role='button'],[contenteditable='true']",
            )
          )
            return;
          navigation?.navigate(detailHref);
        }}
      >
        {canModerate || canDelete ? (
          <CommentModerationMenu
            canModerate={canModerate}
            comment={comment}
            focusTarget={focusTarget}
            onDeleted={onDeleted}
            onPinChange={onPinChange}
            pinned={pinned}
            replyCount={replyCount}
          />
        ) : null}
        {}
        <CommentAuthorAvatar author={author} />
        {pinned ? (
          <p className={styles.pinnedCommentMark}>
            <Icon name="pin" />
            <span>已置顶</span>
          </p>
        ) : null}
        <header className={styles.commentByline} ref={headerRef}>
          <CommentIdentity author={author} />
          {!root ? (
            replyTo ? (
              <span className={styles.commentReplyTo}>
                <span>回复</span>
                <CommentIdentity author={replyTo.author} />
              </span>
            ) : (
              <span className={styles.commentReplyUnknown}>楼内回复</span>
            )
          ) : null}
          {isPostAuthor ? (
            <span className={styles.authorBadge}>作者</span>
          ) : null}
          <span aria-hidden="true">·</span>
          <ExactTimeTooltip exact={created.absolute}>
            {navigable ? (
              <InternalLink
                aria-label={"查看" + authorName + "的评论详情"}
                className={styles.commentPermalink}
                data-focus-key={"comment-link-" + comment.id}
                href={detailHref}
              >
                <time dateTime={comment.createdAt}>{created.relative}</time>
              </InternalLink>
            ) : (
              <time dateTime={comment.createdAt}>{created.relative}</time>
            )}
          </ExactTimeTooltip>
        </header>
        {comment.contentHtml ? (
          <div
            className={[
              styles.commentBody,
              bodyExpanded ? "" : styles.commentBodyClipped,
              // Only where the control exists to answer it: a fade with no
              // "展开全文" beside it tells the reader something is missing and
              // then offers them no way to reach it.
              overflowing && !bodyExpanded ? styles.commentBodyFaded : "",
            ]
              .filter(Boolean)
              .join(" ")}
            id={bodyId}
            // A clipped link is still in the tab order — `overflow: hidden` only
            // hides it. Opening the body on focus keeps the reader from tabbing
            // into something they cannot see.
            onFocusCapture={() => setExpanded(true)}
            ref={bodyRef}
          >
            <MarkdownBody
              className={styles.commentMarkdown}
              html={comment.contentHtml}
            />
          </div>
        ) : null}
        {overflowing ? (
          <button
            aria-controls={bodyId}
            aria-expanded={expanded}
            className={styles.commentFoldToggle}
            onClick={() => {
              restoreScrollRef.current = expanded;
              setExpanded(!expanded);
            }}
            type="button"
          >
            {expanded ? "收起" : "展开全文"}
          </button>
        ) : null}
        {/*
         * Media and location stay whole. Folding is about a wall of text; a
         * half-cropped photo grid only reads as broken, and a comment can be
         * nothing but a location or a picture.
         */}
        {comment.location ? (
          <div className={styles.commentAttachment}>
            <PostLocation location={comment.location} />
          </div>
        ) : null}
        {comment.media.length ? (
          <div className={styles.commentAttachment}>
            <ImageGrid media={comment.media} />
          </div>
        ) : null}
        {/*
         * No actions, no footer. The empty <span> this used to fall back to was
         * a spacer for a `space-between` row, and it was almost unreachable
         * while every reader could reply. Now that a signed-out reader has no
         * reply button, keeping it left a 44px band of dead space under every
         * comment in the thread — a gap that reads as something failing to
         * load rather than as nothing being on offer.
         */}
        {/*
         * No root, no footer: there is genuinely nothing to reply to, and an
         * empty action bar still claims its full target height, leaving a band
         * of dead space that reads as something failing to load.
         *
         * Being signed out is a different thing. The action still means
         * something; the reader just has to sign in first. Removing it there
         * would leave them guessing why replies exist but no way to make one,
         * so the button stays and says so when pressed.
         */}
        {replyRootId ? (
          <footer className={styles.commentActions}>
            {canReply ? (
              <ActionTooltip label="回复">
                <button
                  aria-label={`回复 ${authorName}`}
                  className={styles.commentAction}
                  onClick={() => onReply(replyRootId, comment)}
                  type="button"
                >
                  <Icon name="reply" />
                </button>
              </ActionTooltip>
            ) : (
              <SignInToReplyPopover
                authorName={authorName}
                reason={replyBlockedBy}
              />
            )}
          </footer>
        ) : null}
      </article>
    </li>
  );
}
