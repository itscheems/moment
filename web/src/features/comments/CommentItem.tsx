import { Dialog } from "@base-ui/react/dialog";
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
import { MarkdownBody } from "../../components/content/MarkdownBody";
import { formatMomentDate } from "../posts/dates";
import { PostLocation } from "../posts/PostPresentation";
import { ImageGrid } from "../media/ImageGrid";
import { useOwner } from "../owner/OwnerProvider";
import { Reauthenticate, type ReplyBlock } from "./CommentComposer";
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

/**
 * How long the reply-target highlight stays on after the prefix is followed.
 *
 * Long enough to find the row with your eyes, short enough that it is gone
 * before it starts reading as a permanent state of that comment.
 */
const REPLY_TARGET_FLASH_MS = 1400;

/**
 * Whether a comment was written from the site owner's account.
 *
 * `author_kind` used to answer this. It is the axis being retired: its one
 * remaining job is marking rows a legacy guest wrote, and its value domain
 * stops meaning anything the day a second account exists. The resolved user id
 * is the stable answer, and today it gives exactly the same verdict.
 */
function ownedBySite(author: PublicCommentAuthor, site: SiteIdentity): boolean {
  return author.userId !== null && author.userId === site.ownerId;
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
function useFoldedBody(contentHtml: string) {
  const bodyRef = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflowing, setOverflowing] = useState(false);

  useEffect(() => {
    const body = bodyRef.current;
    if (!body || expanded) return;
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
  }, [contentHtml, expanded]);

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
              {pending ? "正在等待站长批准" : "登录后回复"}
            </Popover.Title>
            <Popover.Description className={styles.signInPopupBody}>
              {pending
                ? "你的账户正在等待站长批准，批准后就能留言。"
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

export function CommentItem({
  comment,
  site,
  owner,
  pinned,
  replyRoot,
  canReply = true,
  replyBlockedBy = "signed-out",
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
  onReply: (root: PublicComment, target: PublicComment) => void;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
  onPinChange: (pinnedCommentId: string | null) => void;
}) {
  const created = formatMomentDate(comment.createdAt, site.timezone);
  const root = comment.parentId === null;
  // Everything the byline shows comes from this one projection, which the
  // server resolved. Reading the site identity for owner rows and the stored
  // string for the rest is what let the CLI and the browser print two
  // different author names for the same comment.
  const author = comment.author;
  const authorName = author.displayName;
  const ownerAccount = ownedBySite(author, site);
  // A legacy guest row carries a free-text name and nothing else — no profile
  // to open, so no links and no preview card. The parser guarantees the three
  // reference fields arrive together; this narrows them for the renderer.
  const account =
    author.username !== null && author.profileUrl !== null
      ? { profileUrl: author.profileUrl, username: author.username }
      : null;
  // The face is read from the same projection as the name, for every account
  // and not just the owner's. It used to come from the site identity, which is
  // the owner's own picture — site.avatar_media_id reads that account's row —
  // so every member fell through to a letter while the server was already
  // shipping their avatar_media_id. A comment author has no cover of its own;
  // SiteAvatar does not read one.
  const authorFace = {
    displayName: authorName,
    avatarMediaId: author.avatarMediaId,
    coverMediaId: null,
  };
  const avatar = account ? (
    <InternalLink
      aria-label={`查看 ${authorName} 的个人资料`}
      className={styles.commentAvatar}
      href={account.profileUrl}
    >
      <SiteAvatar site={authorFace} />
    </InternalLink>
  ) : (
    // A legacy guest row has no account behind it and never had a picture, so
    // the name the visitor typed is the only thing left to draw with.
    <span aria-hidden="true" className={styles.commentAvatar}>
      {Array.from(authorName)[0]?.toLocaleUpperCase() ?? "M"}
    </span>
  );
  const headerRef = useRef<HTMLElement>(null);
  const restoreScrollRef = useRef(false);
  const flashTimerRef = useRef<number | null>(null);
  const { bodyRef, expanded, overflowing, setExpanded } = useFoldedBody(
    comment.contentHtml,
  );
  const bodyId = `comment-${comment.id}-body`;

  const replyTo = comment.replyTo;
  // Answering the root is what the reply's position already says. Repeat it and
  // the prefix becomes noise on every single reply; judge by target comment
  // rather than target author, because when the root's author has written
  // several replies their name is precisely what stops telling you which one.
  const namesReplyTarget =
    replyTo !== null && replyTo.commentId !== comment.parentId;
  const [replyTargetOnPage, setReplyTargetOnPage] = useState(false);

  useEffect(() => {
    if (!namesReplyTarget || replyTo === null) return;
    // Deliberately no dependency list. Whether the target is mounted changes
    // when the thread pages more replies in, and that reaches this item as a
    // re-render rather than as a change to any prop it is given.
    const present =
      document.querySelector(`[data-comment-id="${replyTo.commentId}"]`) !==
      null;
    setReplyTargetOnPage((current) =>
      current === present ? current : present,
    );
  });

  useEffect(
    () => () => {
      if (flashTimerRef.current !== null)
        window.clearTimeout(flashTimerRef.current);
    },
    [],
  );

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

  const revealReplyTarget = () => {
    if (replyTo === null) return;
    const target = document.querySelector<HTMLElement>(
      `[data-comment-id="${replyTo.commentId}"]`,
    );
    if (!target) return;
    target.focus();
    target.dataset.commentFlash = "true";
    if (flashTimerRef.current !== null)
      window.clearTimeout(flashTimerRef.current);
    flashTimerRef.current = window.setTimeout(() => {
      flashTimerRef.current = null;
      delete target.dataset.commentFlash;
    }, REPLY_TARGET_FLASH_MS);
  };

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
        {/*
         * Only the owner gets the hover card, for the reason the byline below
         * gives: the card wants a whole SiteIdentity, and this site's is the
         * owner's. The picture itself needs no such lookup, so it is drawn for
         * everybody.
         */}
        {ownerAccount && account ? (
          <SiteIdentityPreview site={site}>{avatar}</SiteIdentityPreview>
        ) : (
          avatar
        )}
        {pinned ? (
          <p className={styles.pinnedCommentMark}>
            <Icon name="pin" />
            <span>已置顶</span>
          </p>
        ) : null}
        <header className={styles.commentByline} ref={headerRef}>
          {account ? (
            <>
              {/*
               * `SiteIdentityPreview` wants a whole `SiteIdentity` — cover and
               * bio included — and the comment author projection has neither.
               * The owner is the only account that can comment today, so the
               * site identity is the author's identity and passing it is
               * correct rather than convenient. Do not synthesise one for any
               * other user id: it would quietly show this site's cover and bio
               * under somebody else's name the day accounts arrive. That day
               * this needs a real lookup by username.
               */}
              {ownerAccount ? (
                <SiteIdentityPreview site={site}>
                  <InternalLink
                    className={styles.commentAuthor}
                    href={account.profileUrl}
                  >
                    {authorName}
                  </InternalLink>
                </SiteIdentityPreview>
              ) : (
                <InternalLink
                  className={styles.commentAuthor}
                  href={account.profileUrl}
                >
                  {authorName}
                </InternalLink>
              )}
              <InternalLink
                className={styles.commentHandle}
                href={account.profileUrl}
              >
                @{account.username}
              </InternalLink>
            </>
          ) : (
            <strong>{authorName}</strong>
          )}
          {ownerAccount ? (
            <span className={styles.ownerBadge}>所有者</span>
          ) : null}
          <span aria-hidden="true">·</span>
          <ExactTimeTooltip exact={created.absolute}>
            <time dateTime={comment.createdAt}>{created.relative}</time>
          </ExactTimeTooltip>
        </header>
        {namesReplyTarget && replyTo ? (
          <p className={styles.commentReplyTo}>
            <span>回复</span>{" "}
            {replyTo.author.username !== null && replyTargetOnPage ? (
              // A button, not an anchor: this moves focus inside the page and
              // has no address of its own to offer. Cross-page targets are a
              // later lane, so an off-page target stays plain text rather than
              // becoming a control that does nothing.
              <button
                className={styles.commentReplyToLink}
                onClick={revealReplyTarget}
                type="button"
              >
                @{replyTo.author.username}
              </button>
            ) : (
              // Display names are not unique and a legacy guest target has no
              // username at all, so the handle is the identifying form wherever
              // there is one.
              <span>
                {replyTo.author.username !== null
                  ? `@${replyTo.author.username}`
                  : replyTo.author.displayName}
              </span>
            )}
          </p>
        ) : null}
        {comment.contentHtml ? (
          <div
            className={[
              styles.commentBody,
              expanded ? "" : styles.commentBodyClipped,
              // Only where the control exists to answer it: a fade with no
              // "展开全文" beside it tells the reader something is missing and
              // then offers them no way to reach it.
              overflowing && !expanded ? styles.commentBodyFaded : "",
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
        {comment.location ? <PostLocation location={comment.location} /> : null}
        <ImageGrid media={comment.media} />
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
        {replyRoot ? (
          <footer className={styles.commentActions}>
            {canReply ? (
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
