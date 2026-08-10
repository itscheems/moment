import { Dialog } from "@base-ui/react/dialog";
import {
  Fragment,
  type FormEvent,
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
  type SiteIdentity,
} from "../../api/public";
import { formatMomentDate } from "../posts/dates";
import { useOwner } from "../owner/OwnerProvider";
import styles from "./Comments.module.css";

const MAX_AUTHOR_NAME = 40;
const MAX_COMMENT = 2_000;

type CommentMode = "guest" | "owner";

interface ReplyDraft {
  rootId: string;
  rootAuthor: string;
  content: string;
}

function createIdempotencyKey(): string {
  if (typeof crypto.randomUUID === "function") {
    return `web-comment-${crypto.randomUUID()}`;
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return `web-comment-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

function orderComments(left: PublicComment, right: PublicComment): number {
  const time = Date.parse(left.createdAt) - Date.parse(right.createdAt);
  return time || left.id.localeCompare(right.id);
}

export function reconcileCreatedComment(
  current: PublicComment[],
  created: PublicComment,
): PublicComment[] {
  if (current.some((comment) => comment.id === created.id)) return current;
  if (created.parentId === null) {
    const index = current.findIndex(
      (comment) =>
        comment.parentId === null && orderComments(created, comment) < 0,
    );
    return index < 0
      ? [...current, created]
      : [...current.slice(0, index), created, ...current.slice(index)];
  }
  const rootIndex = current.findIndex(
    (comment) => comment.id === created.parentId && comment.parentId === null,
  );
  if (rootIndex < 0) {
    throw new Error("created reply has no visible top-level parent");
  }
  let index = rootIndex + 1;
  while (
    index < current.length &&
    current[index].parentId === created.parentId &&
    orderComments(current[index], created) < 0
  ) {
    index += 1;
  }
  return [...current.slice(0, index), created, ...current.slice(index)];
}

export function reconcileDeletedComment(
  current: PublicComment[],
  deleted: PublicComment,
): PublicComment[] {
  if (deleted.parentId !== null) {
    return current.filter((comment) => comment.id !== deleted.id);
  }
  const rootIndex = current.findIndex((comment) => comment.id === deleted.id);
  if (rootIndex < 0) return current;
  let end = rootIndex + 1;
  while (end < current.length && current[end].parentId === deleted.id) end += 1;
  return [...current.slice(0, rootIndex), ...current.slice(end)];
}

function messageFor(error: unknown, replying: boolean): string {
  if (!(error instanceof PublicApiError)) {
    return "回应没有发出，文字已保留。检查连接后可以安全重试。";
  }
  switch (error.code) {
    case "CONTENT_EMPTY":
      return "写下一些内容后再发表。";
    case "CONTENT_TOO_LARGE":
      return "回应超过 2,000 个字符，请缩短后重试。";
    case "EXTERNAL_IMAGE_NOT_ALLOWED":
      return "回应不能引用 Markdown 图片；普通链接仍然可以使用。";
    case "INVALID_ARGUMENT":
      return "请检查显示名和回应内容后重试。";
    case "COMMENT_NOT_FOUND":
      return replying
        ? "上级回应已被删除。这段文字仍保留，可以改为顶级回应。"
        : "这条回应已不存在。文字仍保留，可以重新加载后再试。";
    case "COMMENTS_DISABLED":
      return "回应刚刚被关闭；这段文字仍保留在当前页面。";
    case "COMMENT_REJECTED":
      return "这次回应没有通过防滥用检查，文字仍保留。";
    case "IDEMPOTENCY_KEY_REUSED":
      return "这次重试与最初内容不一致。再编辑一个字符即可重新发表。";
    case "API_RESPONSE_INVALID":
      return "服务器返回了不完整的回应数据。文字已保留，请稍后重试。";
    default:
      return "回应没有发出，文字已保留。可以安全重试。";
  }
}

function Reauthenticate({ onReady }: { onReady: () => void }) {
  const { login } = useOwner();
  const [token, setToken] = useState("");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pendingRef = useRef(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!token || pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      await login(token);
      setToken("");
      onReady();
    } catch {
      setError("身份刷新没有完成。凭据仍只在当前输入框中，可以重试。");
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <form className={styles.reauth} onSubmit={(event) => void submit(event)}>
      <div>
        <strong>重新确认所有者身份</strong>
        <span>回应文字不会离开当前页面。</span>
      </div>
      <label htmlFor="comment-owner-token">所有者凭据</label>
      <input
        autoCapitalize="none"
        autoComplete="off"
        id="comment-owner-token"
        onChange={(event) => setToken(event.target.value)}
        required
        spellCheck={false}
        type="password"
        value={token}
      />
      {error ? <p role="alert">{error}</p> : null}
      <button disabled={!token || pending} type="submit">
        {pending ? "正在确认…" : "刷新身份"}
      </button>
    </form>
  );
}

function CommentForm({
  api,
  postId,
  mode,
  parentId,
  rootAuthor,
  content,
  setContent,
  guestName,
  setGuestName,
  commentsEnabled,
  onCreated,
  onClosed,
  onPromote,
  onCancel,
}: {
  api: PublicApi;
  postId: string;
  mode: CommentMode;
  parentId: string | null;
  rootAuthor?: string;
  content: string;
  setContent: (value: string) => void;
  guestName: string;
  setGuestName: (value: string) => void;
  commentsEnabled: boolean;
  onCreated: (comment: PublicComment) => void;
  onClosed: () => void;
  onPromote?: () => void;
  onCancel?: () => void;
}) {
  const owner = useOwner();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const [website, setWebsite] = useState("");
  const [retryUntil, setRetryUntil] = useState(0);
  const [remaining, setRemaining] = useState(0);
  const pendingRef = useRef(false);
  const idempotencyRef = useRef("");
  const signatureRef = useRef("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const contentCount = useMemo(() => Array.from(content).length, [content]);
  const nameCount = useMemo(
    () => Array.from(guestName.trim()).length,
    [guestName],
  );
  const validName =
    mode === "owner" || (nameCount > 0 && nameCount <= MAX_AUTHOR_NAME);
  const validContent = content.trim().length > 0 && contentCount <= MAX_COMMENT;
  const rateLimited = remaining > 0;
  const formId = parentId ? `reply-${parentId}` : "top-level-comment";

  useEffect(() => {
    if (!retryUntil) {
      setRemaining(0);
      return;
    }
    const update = () =>
      setRemaining(Math.max(0, Math.ceil((retryUntil - Date.now()) / 1_000)));
    update();
    const timer = window.setInterval(update, 250);
    return () => window.clearInterval(timer);
  }, [retryUntil]);

  useEffect(() => {
    if (!authExpired || !owner.session) return;
    setAuthExpired(false);
    setError(null);
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [authExpired, owner.session]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (
      pendingRef.current ||
      !validName ||
      !validContent ||
      !commentsEnabled ||
      rateLimited
    ) {
      return;
    }
    if (mode === "owner" && !owner.session) {
      setAuthExpired(true);
      setError("当前写入会话已失效。文字已保留，刷新身份后可以重试。");
      return;
    }
    const authorName = guestName.trim();
    const signature = JSON.stringify([
      mode,
      postId,
      parentId,
      mode === "guest" ? authorName : null,
      content,
      mode === "guest" ? website : null,
    ]);
    if (signatureRef.current !== signature) {
      signatureRef.current = signature;
      idempotencyRef.current = createIdempotencyKey();
    }
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      const result =
        mode === "owner"
          ? await owner.api.createComment(
              owner.session!.csrfToken,
              postId,
              content,
              parentId,
              idempotencyRef.current,
            )
          : await api.createGuestComment(
              postId,
              { authorName, contentMd: content, parentId, website },
              idempotencyRef.current,
            );
      onCreated(result.comment);
      setContent("");
      setWebsite("");
      signatureRef.current = "";
      idempotencyRef.current = "";
      setRetryUntil(0);
    } catch (nextError) {
      if (mode === "owner" && owner.expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("当前写入会话已失效。文字已保留，刷新身份后可以重试。");
      } else {
        if (
          nextError instanceof PublicApiError &&
          nextError.code === "RATE_LIMITED" &&
          nextError.retryAfterSeconds
        ) {
          setRetryUntil(Date.now() + nextError.retryAfterSeconds * 1_000);
        }
        if (
          nextError instanceof PublicApiError &&
          nextError.code === "COMMENTS_DISABLED"
        ) {
          onClosed();
        }
        setError(messageFor(nextError, parentId !== null));
      }
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <div
      className={`${styles.formSurface} ${parentId ? styles.replyForm : ""}`}
    >
      <form
        aria-label={
          parentId ? `回复 ${rootAuthor ?? "顶级回应"}` : "发表顶级回应"
        }
        className={styles.commentForm}
        onSubmit={(event) => void submit(event)}
      >
        <header>
          <div>
            <p>{parentId ? "一级回复" : "加入这段对话"}</p>
            <h3>
              {parentId ? `回复 ${rootAuthor ?? "这条回应"}` : "写下回应"}
            </h3>
          </div>
          <span>{mode === "owner" ? "Owner 身份" : "访客"}</span>
        </header>
        {mode === "guest" ? (
          <div className={styles.nameField}>
            <label htmlFor={`${formId}-name`}>显示名</label>
            <input
              autoComplete="name"
              disabled={pending}
              id={`${formId}-name`}
              maxLength={MAX_AUTHOR_NAME}
              onChange={(event) => {
                setGuestName(event.target.value);
                setError(null);
              }}
              placeholder="怎么称呼你"
              required
              value={guestName}
            />
            <span aria-label={`显示名 ${nameCount} / ${MAX_AUTHOR_NAME} 字`}>
              {nameCount}/{MAX_AUTHOR_NAME}
            </span>
          </div>
        ) : null}
        <label className={styles.srOnly} htmlFor={`${formId}-content`}>
          {parentId ? "回复内容" : "回应内容"}
        </label>
        <textarea
          aria-describedby={`${formId}-guidance ${formId}-error`}
          disabled={pending}
          id={`${formId}-content`}
          onChange={(event) => {
            setContent(event.target.value);
            setError(null);
          }}
          onKeyDown={(event) => {
            if (
              event.key === "Enter" &&
              (event.metaKey || event.ctrlKey) &&
              !event.nativeEvent.isComposing &&
              validName &&
              validContent &&
              commentsEnabled &&
              !rateLimited
            ) {
              event.preventDefault();
              event.currentTarget.form?.requestSubmit();
            }
          }}
          placeholder="支持 Markdown；请勿粘贴图片语法。"
          ref={textareaRef}
          required
          rows={parentId ? 4 : 5}
          value={content}
        />
        <input
          aria-hidden="true"
          autoComplete="off"
          className={styles.honeypot}
          name="website"
          onChange={(event) => setWebsite(event.target.value)}
          tabIndex={-1}
          type="text"
          value={website}
        />
        <div className={styles.formMeta} id={`${formId}-guidance`}>
          <span>Markdown · Ctrl/⌘ + Enter 发表</span>
          <span
            className={contentCount > MAX_COMMENT ? styles.countInvalid : ""}
            aria-label={`回应 ${contentCount} / ${MAX_COMMENT} 字`}
          >
            {contentCount}/{MAX_COMMENT}
          </span>
        </div>
        {error ? (
          <div className={styles.formError} id={`${formId}-error`} role="alert">
            <span>{error}</span>
            {parentId && error.includes("改为顶级回应") && onPromote ? (
              <button onClick={onPromote} type="button">
                改为顶级回应
              </button>
            ) : null}
          </div>
        ) : null}
        {rateLimited ? (
          <p className={styles.rateNotice} role="status">
            发表得有点快，请在 {remaining}{" "}
            秒后重试。文字和本次重试凭据都已保留。
          </p>
        ) : null}
        {!commentsEnabled ? (
          <p className={styles.closedInline} role="status">
            回应已关闭。文字仍保留在当前页面，便于复制。
          </p>
        ) : null}
        <footer>
          {onCancel ? (
            <button
              className={styles.secondaryAction}
              onClick={onCancel}
              type="button"
            >
              取消回复
            </button>
          ) : (
            <span />
          )}
          <button
            className={styles.primaryAction}
            disabled={
              pending ||
              !validName ||
              !validContent ||
              !commentsEnabled ||
              rateLimited ||
              (mode === "owner" && !owner.session)
            }
            type="submit"
          >
            {pending
              ? "正在发表…"
              : rateLimited
                ? `${remaining} 秒后重试`
                : parentId
                  ? "发表回复"
                  : "发表回应"}
          </button>
        </footer>
      </form>
      {authExpired ? (
        <Reauthenticate
          onReady={() => {
            setAuthExpired(false);
            setError(null);
          }}
        />
      ) : null}
    </div>
  );
}

function DeleteCommentDialog({
  postId,
  comment,
  replyCount,
  focusTarget,
  onDeleted,
}: {
  postId: string;
  comment: PublicComment;
  replyCount: number;
  focusTarget: RefObject<HTMLElement | null>;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
}) {
  const { api, session, expireIfInvalidSession } = useOwner();
  const [open, setOpen] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const pendingRef = useRef(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const deletedRef = useRef(false);
  const root = comment.parentId === null;

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
      setOpen(false);
      window.requestAnimationFrame(() => focusTarget.current?.focus());
    } catch (nextError) {
      if (expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("所有者会话已过期。刷新身份后可在这里重新删除。");
      } else {
        setError(
          nextError instanceof PublicApiError &&
            nextError.code === "COMMENT_NOT_FOUND"
            ? "这条回应已经不存在。关闭后重新加载列表。"
            : "没有删除这条回应。列表保持不变，可以安全重试。",
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
        setOpen(nextOpen);
      }}
      open={open}
    >
      <Dialog.Trigger className={styles.commentAction} ref={triggerRef}>
        删除
      </Dialog.Trigger>
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
              {root ? "删除这条顶级回应？" : "删除这条回复？"}
            </Dialog.Title>
            <Dialog.Description className={styles.dialogDescription}>
              {root && replyCount > 0
                ? `这条回应和它下面的 ${replyCount} 条一级回复都会永久删除。`
                : "这条回应会从公开对话中永久删除。"}
            </Dialog.Description>
            {error ? (
              <p className={styles.dialogError} role="alert">
                {error}
              </p>
            ) : null}
            {authExpired ? (
              <Reauthenticate
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
                className={styles.dangerAction}
                disabled={pending || !session}
                onClick={() => void remove()}
                type="button"
              >
                {pending
                  ? "正在删除…"
                  : root && replyCount > 0
                    ? "删除回应和回复"
                    : root
                      ? "删除回应"
                      : "删除回复"}
              </button>
            </div>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

function CommentItem({
  comment,
  site,
  owner,
  replyCount,
  focusTarget,
  onReply,
  onDeleted,
}: {
  comment: PublicComment;
  site: SiteIdentity;
  owner: boolean;
  replyCount: number;
  focusTarget: RefObject<HTMLElement | null>;
  onReply: (comment: PublicComment) => void;
  onDeleted: (deleted: PublicComment, repliesAffected: number) => void;
}) {
  const created = formatMomentDate(comment.createdAt, site.timezone);
  const root = comment.parentId === null;
  const authorName =
    comment.authorKind === "owner"
      ? site.title.trim() || comment.authorName
      : comment.authorName;
  return (
    <li
      className={`${styles.commentItem} ${root ? "" : styles.replyComment}`}
      data-comment-id={comment.id}
      data-depth={comment.depth}
      tabIndex={-1}
    >
      <article aria-label={`${authorName}的${root ? "回应" : "一级回复"}`}>
        <header className={styles.commentByline}>
          <strong>{authorName}</strong>
          {comment.authorKind === "owner" ? (
            <span className={styles.ownerBadge}>Owner</span>
          ) : null}
          <span aria-hidden="true">·</span>
          <time dateTime={comment.createdAt} title={created.absolute}>
            {created.relative}
          </time>
        </header>
        <div
          className={styles.commentMarkdown}
          dangerouslySetInnerHTML={{ __html: comment.contentHtml }}
        />
        <footer className={styles.commentActions}>
          {root ? (
            <button
              className={styles.commentAction}
              onClick={() => onReply(comment)}
              type="button"
            >
              回复
            </button>
          ) : (
            <span />
          )}
          {owner ? (
            <DeleteCommentDialog
              comment={comment}
              focusTarget={focusTarget}
              onDeleted={onDeleted}
              postId={comment.postId}
              replyCount={replyCount}
            />
          ) : null}
        </footer>
      </article>
    </li>
  );
}

export function CommentThread({
  api,
  postId,
  site,
}: {
  api: PublicApi;
  postId: string;
  site: SiteIdentity;
}) {
  const owner = useOwner();
  const [items, setItems] = useState<PublicComment[] | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [attempt, setAttempt] = useState(0);
  const [guestName, setGuestName] = useState("");
  const [topContent, setTopContent] = useState("");
  const [reply, setReply] = useState<ReplyDraft | null>(null);
  const [closedByServer, setClosedByServer] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  const headingRef = useRef<HTMLHeadingElement>(null);
  const focusCommentRef = useRef<string | null>(null);
  const commentsEnabled = site.commentsEnabled && !closedByServer;
  const mode: CommentMode =
    owner.state === "owner" || owner.state === "expired" || owner.session
      ? "owner"
      : "guest";

  const load = useCallback(() => setAttempt((value) => value + 1), []);

  useEffect(() => {
    const controller = new AbortController();
    setLoadError(null);
    void api
      .comments(postId, controller.signal)
      .then((thread) => {
        if (!controller.signal.aborted) setItems(thread.items);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) setLoadError(error);
      });
    return () => controller.abort();
  }, [api, attempt, postId]);

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
    setAnnouncement(comment.parentId ? "回复已发表" : "回应已发表");
  };

  const deleted = (comment: PublicComment, repliesAffected: number) => {
    setItems((current) =>
      current ? reconcileDeletedComment(current, comment) : current,
    );
    if (reply?.rootId === comment.id) setReply(null);
    setAnnouncement(
      repliesAffected > 0
        ? `回应和 ${repliesAffected} 条一级回复已删除`
        : "回应已删除",
    );
  };

  const replyCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const comment of items ?? []) {
      if (comment.parentId) {
        counts.set(comment.parentId, (counts.get(comment.parentId) ?? 0) + 1);
      }
    }
    return counts;
  }, [items]);

  return (
    <section aria-labelledby="comments-heading" className={styles.thread}>
      <header className={styles.threadHeader}>
        <span aria-hidden="true" className={styles.threadMark}>
          回应
        </span>
        <div>
          <p>沿着这段 Moment 继续</p>
          <h2 id="comments-heading" ref={headingRef} tabIndex={-1}>
            公开回应
          </h2>
          <span>
            {items ? `${items.length} 条，最多一级回复` : "最多一级回复"}
          </span>
        </div>
      </header>

      <p aria-live="polite" className={styles.srOnly} role="status">
        {announcement}
      </p>

      {items === null && !loadError ? (
        <div aria-busy="true" className={styles.threadState}>
          <span className={styles.loadingDot} />
          <span>正在读取回应…</span>
        </div>
      ) : null}

      {loadError ? (
        <div className={styles.threadError} role="alert">
          <strong>
            {loadError instanceof PublicApiError &&
            loadError.code === "API_RESPONSE_INVALID"
              ? "回应数据无法安全显示"
              : "这次没有取回应答"}
          </strong>
          <span>Moment 正文仍可阅读；重试不会重复发表任何内容。</span>
          <button onClick={load} type="button">
            重新加载回应
          </button>
        </div>
      ) : null}

      {items ? (
        <>
          {!site.commentsEnabled && !closedByServer ? (
            <div className={styles.closedNotice} role="status">
              <strong>回应已关闭</strong>
              <span>过往回应继续公开可读，现在不能发表新内容。</span>
            </div>
          ) : (
            <CommentForm
              api={api}
              commentsEnabled={commentsEnabled}
              content={topContent}
              guestName={guestName}
              mode={mode}
              onClosed={() => setClosedByServer(true)}
              onCreated={created}
              parentId={null}
              postId={postId}
              setContent={setTopContent}
              setGuestName={setGuestName}
            />
          )}

          {items.length === 0 ? (
            <div className={styles.emptyThread}>
              <strong>还没有公开回应</strong>
              <span>
                {commentsEnabled
                  ? "可以从上面的表单留下第一条。"
                  : "回应关闭期间，仍可以安静阅读这段 Moment。"}
              </span>
            </div>
          ) : (
            <ol className={styles.commentList}>
              {items.map((comment) => (
                <Fragment key={comment.id}>
                  <CommentItem
                    comment={comment}
                    focusTarget={headingRef}
                    onDeleted={deleted}
                    onReply={(root) => {
                      setReply({
                        rootId: root.id,
                        rootAuthor:
                          root.authorKind === "owner"
                            ? site.title.trim() || root.authorName
                            : root.authorName,
                        content: "",
                      });
                      window.requestAnimationFrame(() =>
                        document
                          .getElementById(`reply-${root.id}-content`)
                          ?.focus(),
                      );
                    }}
                    owner={owner.state === "owner" || owner.state === "expired"}
                    replyCount={replyCounts.get(comment.id) ?? 0}
                    site={site}
                  />
                  {reply?.rootId === comment.id ? (
                    <li className={styles.replyComposerRow}>
                      <CommentForm
                        api={api}
                        commentsEnabled={commentsEnabled}
                        content={reply.content}
                        guestName={guestName}
                        mode={mode}
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
                        rootAuthor={reply.rootAuthor}
                        setContent={(content) =>
                          setReply((current) =>
                            current ? { ...current, content } : current,
                          )
                        }
                        setGuestName={setGuestName}
                      />
                    </li>
                  ) : null}
                </Fragment>
              ))}
            </ol>
          )}
        </>
      ) : null}
    </section>
  );
}
