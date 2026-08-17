import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import {
  Fragment,
  type FormEvent,
  type RefObject,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicComment,
  type MentionSuggestion,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import type { OwnerLocation } from "../../api/owner";
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
import { MediaComposer } from "../media/MediaComposer";
import {
  applyUpload,
  fileValidation,
  MAX_TOTAL_BYTES,
  mediaInput,
  mediaValidation,
  newMediaItem,
  readLocalPreview,
  releaseDiscardedLocalPreviews,
  releaseLocalPreview,
  uploadMessage,
  type ComposerMediaItem,
} from "../media/mediaState";
import { useOwner } from "../owner/OwnerProvider";
import { LocationPicker, SelectedLocationChip } from "../owner/LocationPicker";
import { MentionSuggestions } from "../owner/MentionSuggestions";
import { activeMention, type ActiveMention } from "../owner/mentions";
import styles from "./Comments.module.css";
import ownerStyles from "../owner/Owner.module.css";

const MAX_AUTHOR_NAME = 40;
const MAX_COMMENT = 2_000;

function CommentProgress({ count }: { count: number }) {
  const radius = 9;
  const circumference = 2 * Math.PI * radius;
  const ratio = Math.min(count / MAX_COMMENT, 1);
  return (
    <span
      aria-hidden="true"
      className={styles.commentProgress}
      data-invalid={count > MAX_COMMENT ? "true" : undefined}
    >
      <svg viewBox="0 0 24 24">
        <circle className={styles.progressTrack} cx="12" cy="12" r={radius} />
        <circle
          className={styles.progressValue}
          cx="12"
          cy="12"
          r={radius}
          strokeDasharray={circumference}
          strokeDashoffset={circumference * (1 - ratio)}
        />
      </svg>
    </span>
  );
}

type CommentMode = "guest" | "owner";

interface ReplyDraft {
  rootId: string;
  targetId: string;
  targetAuthor: string;
  content: string;
}

function useMeasuredBlockSize(layoutRevision: string) {
  const frameRef = useRef<HTMLDivElement>(null);
  const contentRef = useRef<HTMLDivElement>(null);
  const measuredHeightRef = useRef<number | null>(null);

  const measure = useCallback(() => {
    const frame = frameRef.current;
    const measuredContent = contentRef.current;
    if (!frame || !measuredContent) return;

    const nextHeight = measuredContent.getBoundingClientRect().height;
    if (!Number.isFinite(nextHeight) || nextHeight <= 0) return;
    if (
      measuredHeightRef.current !== null &&
      Math.abs(measuredHeightRef.current - nextHeight) < 0.5
    ) {
      return;
    }

    measuredHeightRef.current = nextHeight;
    frame.style.blockSize = `${nextHeight}px`;
  }, []);

  useLayoutEffect(() => {
    measure();
  }, [layoutRevision, measure]);

  useLayoutEffect(() => {
    const measuredContent = contentRef.current;
    if (!measuredContent || typeof ResizeObserver === "undefined") return;

    const observer = new ResizeObserver(measure);
    observer.observe(measuredContent);
    return () => observer.disconnect();
  }, [measure]);

  return { contentRef, frameRef };
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

export function orderPinnedConversation(
  current: PublicComment[],
  pinnedCommentId: string | null,
): PublicComment[] {
  if (!pinnedCommentId) return current;
  const start = current.findIndex(
    (comment) => comment.id === pinnedCommentId && comment.parentId === null,
  );
  if (start <= 0) return current;
  let end = start + 1;
  while (end < current.length && current[end].parentId === pinnedCommentId) {
    end += 1;
  }
  return [
    ...current.slice(start, end),
    ...current.slice(0, start),
    ...current.slice(end),
  ];
}

function messageFor(error: unknown, replying: boolean): string {
  if (!(error instanceof PublicApiError)) {
    return "回复没有发出，文字已保留。检查连接后可以安全重试。";
  }
  switch (error.code) {
    case "CONTENT_EMPTY":
      return "写下一些内容后再发表。";
    case "CONTENT_TOO_LARGE":
      return "回复超过 2,000 个字符，请缩短后重试。";
    case "EXTERNAL_IMAGE_NOT_ALLOWED":
      return "回复不能引用 Markdown 图片；普通链接仍然可以使用。";
    case "INVALID_ARGUMENT":
      return "请检查显示名和回复内容后重试。";
    case "COMMENT_NOT_FOUND":
      return replying
        ? "上级回复已被删除。这段文字仍保留，可以改为直接回复 Moment。"
        : "这条回复已不存在。文字仍保留，可以重新加载后再试。";
    case "COMMENTS_DISABLED":
      return "回复刚刚被关闭；这段文字仍保留在当前页面。";
    case "COMMENT_REJECTED":
      return "这次回复没有通过防滥用检查，文字仍保留。";
    case "IDEMPOTENCY_KEY_REUSED":
      return "这次重试与最初内容不一致。再编辑一个字符即可重新发表。";
    case "API_RESPONSE_INVALID":
      return "服务器返回了不完整的回复数据。文字已保留，请稍后重试。";
    default:
      return "回复没有发出，文字已保留。可以安全重试。";
  }
}

function Reauthenticate({
  inputId,
  onReady,
}: {
  inputId: string;
  onReady: () => void;
}) {
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
      setError("身份刷新没有完成。令牌仍只在当前输入框中，可以重试。");
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  return (
    <form className={styles.reauth} onSubmit={(event) => void submit(event)}>
      <div>
        <strong>重新确认所有者身份</strong>
        <span>回复文字不会离开当前页面。</span>
      </div>
      <label htmlFor={inputId}>所有者令牌</label>
      <input
        autoCapitalize="none"
        autoComplete="off"
        id={inputId}
        onChange={(event) => setToken(event.target.value)}
        required
        spellCheck={false}
        type="password"
        value={token}
      />
      {error ? <p role="alert">{error}</p> : null}
      <button
        aria-busy={pending || undefined}
        data-pending={pending ? "true" : undefined}
        disabled={!token || pending}
        type="submit"
      >
        {pending ? "正在确认…" : "刷新身份"}
      </button>
    </form>
  );
}

function CommentForm({
  api,
  site,
  postId,
  mode,
  parentId,
  replyTargetId,
  replyTargetAuthor,
  content,
  setContent,
  guestName,
  setGuestName,
  commentsEnabled,
  onCreated,
  onClosed,
  onPromote,
  onCancel,
  formId: suppliedFormId,
  initialExpanded = false,
  textareaRef: suppliedTextareaRef,
  topLevelTargetUsername,
}: {
  api: PublicApi;
  site: SiteIdentity;
  postId: string;
  mode: CommentMode;
  parentId: string | null;
  replyTargetId?: string;
  replyTargetAuthor?: string;
  content: string;
  setContent: (value: string) => void;
  guestName: string;
  setGuestName: (value: string) => void;
  commentsEnabled: boolean;
  onCreated: (comment: PublicComment) => void;
  onClosed: () => void;
  onPromote?: () => void;
  onCancel?: () => void;
  formId?: string;
  initialExpanded?: boolean;
  textareaRef?: RefObject<HTMLTextAreaElement | null>;
  topLevelTargetUsername?: string;
}) {
  const owner = useOwner();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const [website, setWebsite] = useState("");
  const [retryUntil, setRetryUntil] = useState(0);
  const [remaining, setRemaining] = useState(0);
  const [activated, setActivated] = useState(
    parentId !== null || initialExpanded,
  );
  const [mediaItems, setMediaItems] = useState<ComposerMediaItem[]>([]);
  const [commentLocation, setCommentLocation] = useState<OwnerLocation | null>(
    null,
  );
  const [mention, setMention] = useState<ActiveMention | null>(null);
  const [mentionItems, setMentionItems] = useState<MentionSuggestion[]>([]);
  const [mentionActive, setMentionActive] = useState(0);
  const mediaItemsRef = useRef(mediaItems);
  const mediaUploadsRef = useRef(new Map<string, AbortController>());
  const mountedRef = useRef(true);
  const pendingRef = useRef(false);
  const idempotencyRef = useRef("");
  const signatureRef = useRef("");
  const localTextareaRef = useRef<HTMLTextAreaElement>(null);
  const textareaRef = suppliedTextareaRef ?? localTextareaRef;
  const contentCount = useMemo(() => Array.from(content).length, [content]);
  const nameCount = useMemo(
    () => Array.from(guestName.trim()).length,
    [guestName],
  );
  const validName =
    mode === "owner" || (nameCount > 0 && nameCount <= MAX_AUTHOR_NAME);
  const validContent =
    (content.trim().length > 0 || commentLocation !== null) &&
    contentCount <= MAX_COMMENT;
  const mediaError = mediaValidation(mediaItems, 4);
  const rateLimited = remaining > 0;
  const formId =
    suppliedFormId ??
    (parentId ? `reply-${replyTargetId ?? parentId}` : "top-level-comment");
  const inlineTopLevel =
    parentId === null && topLevelTargetUsername !== undefined;
  const displayReauth =
    mode === "owner" &&
    (authExpired || (owner.state === "expired" && !owner.session));
  const displayedError =
    error ??
    (displayReauth
      ? "当前写入会话已失效。文字已保留，刷新身份后可以重试。"
      : null);
  const expanded =
    parentId !== null ||
    activated ||
    content.trim().length > 0 ||
    mediaItems.length > 0 ||
    commentLocation !== null ||
    pending ||
    displayedError !== null ||
    rateLimited ||
    !commentsEnabled;
  const layoutRevision = [
    mode,
    expanded,
    mediaItems.length,
    commentLocation?.name ?? "",
    displayedError !== null,
    rateLimited,
    displayReauth,
    commentsEnabled,
    mentionItems.length,
  ].join(":");
  const { contentRef: layoutContentRef, frameRef: layoutFrameRef } =
    useMeasuredBlockSize(layoutRevision);

  const replaceMedia = (next: ComposerMediaItem[]) => {
    releaseDiscardedLocalPreviews(mediaItemsRef.current, next);
    mediaItemsRef.current = next;
    setMediaItems(next);
  };

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      for (const controller of mediaUploadsRef.current.values())
        controller.abort();
      releaseDiscardedLocalPreviews(mediaItemsRef.current, []);
    };
  }, []);

  const uploadOne = async (key: string) => {
    const current = mediaItemsRef.current.find((item) => item.key === key);
    if (!current || current.mediaId || !current.file) return;
    const controller = new AbortController();
    mediaUploadsRef.current.get(key)?.abort();
    mediaUploadsRef.current.set(key, controller);
    replaceMedia(
      mediaItemsRef.current.map((item) =>
        item.key === key
          ? { ...item, state: "uploading", progress: 0, error: null }
          : item,
      ),
    );
    try {
      const progress = (value: number) => {
        if (
          !mountedRef.current ||
          mediaUploadsRef.current.get(key) !== controller
        )
          return;
        replaceMedia(
          mediaItemsRef.current.map((item) =>
            item.key === key ? { ...item, progress: value } : item,
          ),
        );
      };
      const uploaded =
        mode === "owner"
          ? await owner.api.uploadMedia(
              owner.session!.csrfToken,
              current.file,
              progress,
              controller.signal,
              (processingPosterUrl) => {
                if (
                  !mountedRef.current ||
                  mediaUploadsRef.current.get(key) !== controller
                ) {
                  URL.revokeObjectURL(processingPosterUrl);
                  return;
                }
                replaceMedia(
                  mediaItemsRef.current.map((item) =>
                    item.key === key && item.state === "uploading"
                      ? { ...item, processingPosterUrl }
                      : item,
                  ),
                );
              },
            )
          : await api.uploadCommentMedia(
              postId,
              current.file,
              progress,
              controller.signal,
            );
      if (
        !mountedRef.current ||
        mediaUploadsRef.current.get(key) !== controller
      )
        return;
      replaceMedia(
        mediaItemsRef.current.map((item) =>
          item.key === key ? applyUpload(item, uploaded) : item,
        ),
      );
    } catch (nextError) {
      if (
        mountedRef.current &&
        mediaUploadsRef.current.get(key) === controller
      ) {
        const message = uploadMessage(nextError);
        replaceMedia(
          mediaItemsRef.current.map((item) =>
            item.key === key
              ? { ...item, state: "failed", progress: 0, error: message }
              : item,
          ),
        );
        setError(message);
      }
      throw nextError;
    } finally {
      if (mediaUploadsRef.current.get(key) === controller)
        mediaUploadsRef.current.delete(key);
    }
  };

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

  useEffect(() => {
    if (!mention) {
      setMentionItems([]);
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void api
        .mentionSuggestions(mention.query, controller.signal)
        .then((items) => {
          if (!controller.signal.aborted) {
            setMentionItems(items);
            setMentionActive(0);
          }
        })
        .catch(() => {
          if (!controller.signal.aborted) setMentionItems([]);
        });
    }, 120);
    return () => {
      window.clearTimeout(timer);
      controller.abort();
    };
  }, [api, mention?.query, mention?.start]);

  const pickMention = (item: MentionSuggestion) => {
    if (!mention) return;
    const next = `${content.slice(0, mention.start)}@${item.username} ${content.slice(mention.end)}`;
    const caret = mention.start + item.username.length + 2;
    setContent(next);
    setMention(null);
    setMentionItems([]);
    setError(null);
    window.requestAnimationFrame(() => {
      textareaRef.current?.focus();
      textareaRef.current?.setSelectionRange(caret, caret);
    });
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (
      pendingRef.current ||
      !validName ||
      !validContent ||
      !commentsEnabled ||
      rateLimited ||
      mediaError !== null
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
      mediaItems.map((item) => [
        item.key,
        item.altMode,
        item.altText,
        item.mediaId,
      ]),
      commentLocation,
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
      for (const item of [...mediaItemsRef.current]) {
        if (!item.mediaId) await uploadOne(item.key);
      }
      const usages = mediaInput(mediaItemsRef.current);
      const result =
        mode === "owner"
          ? await owner.api.createComment(
              owner.session!.csrfToken,
              postId,
              content,
              parentId,
              idempotencyRef.current,
              usages,
              commentLocation,
            )
          : await api.createGuestComment(
              postId,
              {
                authorName,
                contentMd: content,
                parentId,
                website,
                media: usages,
              },
              idempotencyRef.current,
            );
      onCreated(result.comment);
      setContent("");
      setWebsite("");
      signatureRef.current = "";
      idempotencyRef.current = "";
      setRetryUntil(0);
      replaceMedia([]);
      setCommentLocation(null);
      setActivated(parentId !== null || initialExpanded);
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
      className={`${styles.formSurface} ${styles.commentLayoutFrame} ${inlineTopLevel ? styles.inlineTopLevelForm : ""} ${parentId ? styles.replyForm : ""} ${expanded ? styles.commentExpanded : styles.commentCollapsed}`}
      data-comment-layout-frame="true"
      data-comment-layout-state={expanded ? "expanded" : "collapsed"}
      data-comment-top-level={inlineTopLevel ? "true" : undefined}
      onFocusCapture={() => setActivated(true)}
      ref={layoutFrameRef}
    >
      <div
        className={styles.commentComposerLayout}
        data-comment-layout-content="true"
        ref={layoutContentRef}
      >
        <span aria-hidden="true" className={styles.composerAvatar}>
          {mode === "owner" ? (
            <SiteAvatar site={site} />
          ) : (
            (Array.from(guestName.trim())[0]?.toLocaleUpperCase() ?? "?")
          )}
        </span>
        <form
          aria-label={
            parentId ? `回复 ${replyTargetAuthor ?? "这条回复"}` : "发表回复"
          }
          className={styles.commentForm}
          onSubmit={(event) => void submit(event)}
        >
          <header className={styles.commentComposerHeader}>
            <div className={styles.srOnly}>
              <p>{parentId ? "一级回复" : "加入这段对话"}</p>
              <h3>
                {parentId
                  ? `回复 ${replyTargetAuthor ?? "这条回复"}`
                  : "写下回复"}
              </h3>
            </div>
            <span className={styles.srOnly}>
              {mode === "owner" ? "所有者" : "访客"}
            </span>
          </header>
          {(parentId && replyTargetAuthor) ||
          (!parentId && expanded && topLevelTargetUsername) ? (
            <p className={styles.replyTarget}>
              正在回复{" "}
              <span>
                @{parentId ? replyTargetAuthor : topLevelTargetUsername}
              </span>
            </p>
          ) : null}
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
            回复内容
          </label>
          <div className={styles.commentInputRow}>
            <textarea
              aria-describedby={`${formId}-guidance ${formId}-error`}
              disabled={pending}
              id={`${formId}-content`}
              onChange={(event) => {
                setContent(event.target.value);
                setMention(
                  activeMention(
                    event.target.value,
                    event.target.selectionStart ?? event.target.value.length,
                  ),
                );
                setError(null);
              }}
              onKeyDown={(event) => {
                if (mentionItems.length > 0 && mention) {
                  if (event.key === "ArrowDown" || event.key === "ArrowUp") {
                    event.preventDefault();
                    setMentionActive(
                      (current) =>
                        (current +
                          (event.key === "ArrowDown" ? 1 : -1) +
                          mentionItems.length) %
                        mentionItems.length,
                    );
                    return;
                  }
                  if (event.key === "Escape") {
                    event.preventDefault();
                    setMention(null);
                    setMentionItems([]);
                    return;
                  }
                  if (
                    (event.key === "Enter" || event.key === "Tab") &&
                    !event.metaKey &&
                    !event.ctrlKey
                  ) {
                    event.preventDefault();
                    pickMention(mentionItems[mentionActive]);
                    return;
                  }
                }
                if (
                  event.key === "Enter" &&
                  (event.metaKey || event.ctrlKey) &&
                  !event.nativeEvent.isComposing &&
                  validName &&
                  validContent &&
                  commentsEnabled &&
                  !rateLimited &&
                  mediaError === null
                ) {
                  event.preventDefault();
                  event.currentTarget.form?.requestSubmit();
                }
              }}
              placeholder="发布你的回复"
              ref={textareaRef}
              required={commentLocation === null}
              rows={parentId ? 1 : 2}
              value={content}
            />
            {!expanded ? (
              <button
                aria-label="回复"
                className={styles.collapsedReplyAction}
                disabled
                type="button"
              >
                回复
              </button>
            ) : null}
          </div>
          <MentionSuggestions
            active={mentionActive}
            items={mentionItems}
            onPick={pickMention}
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
            <span className={styles.srOnly}>
              支持 Markdown；请勿粘贴图片语法。Ctrl/⌘ + Enter 发表
            </span>
            <span
              className={
                contentCount > MAX_COMMENT
                  ? styles.countInvalid
                  : contentCount > MAX_COMMENT * 0.9
                    ? ""
                    : styles.srOnly
              }
              aria-label={`回复 ${contentCount} / ${MAX_COMMENT} 字`}
            >
              {contentCount}/{MAX_COMMENT}
            </span>
          </div>
          {commentLocation ? (
            <SelectedLocationChip
              disabled={pending}
              location={commentLocation}
              onRemove={() => {
                setCommentLocation(null);
                setActivated(true);
                setError(null);
              }}
            />
          ) : null}
          <MediaComposer
            className={styles.commentMediaComposer}
            disabled={pending || !commentsEnabled}
            footerAction={
              <div className={styles.commentSubmitCluster}>
                <CommentProgress count={contentCount} />
                <div className={styles.commentSubmitActions}>
                  {onCancel ? (
                    <button
                      className={styles.secondaryAction}
                      onClick={onCancel}
                      type="button"
                    >
                      取消
                    </button>
                  ) : null}
                  <button
                    aria-busy={pending || undefined}
                    aria-label="发表回复"
                    className={styles.primaryAction}
                    data-pending={pending ? "true" : undefined}
                    disabled={
                      pending ||
                      !validName ||
                      !validContent ||
                      !commentsEnabled ||
                      rateLimited ||
                      (mode === "owner" && !owner.session) ||
                      mediaError !== null
                    }
                    type="submit"
                  >
                    <span aria-hidden="true">
                      {pending
                        ? "正在发表…"
                        : rateLimited
                          ? `${remaining} 秒后重试`
                          : "回复"}
                    </span>
                  </button>
                </div>
              </div>
            }
            items={mediaItems}
            maximum={4}
            maximumTotalBytes={MAX_TOTAL_BYTES}
            pickerTooltip={inlineTopLevel ? "媒体" : undefined}
            subject="回复"
            toolbarAction={
              <LocationPicker
                api={owner.api}
                disabled={
                  pending ||
                  !commentsEnabled ||
                  mode !== "owner" ||
                  !owner.session
                }
                onChange={(nextLocation) => {
                  setCommentLocation(nextLocation);
                  setActivated(true);
                  setError(null);
                }}
                pickerTooltip="位置"
                value={commentLocation}
              />
            }
            onAdd={(files) => {
              const next = [...mediaItemsRef.current];
              for (const file of files) {
                if (fileValidation(file, next, MAX_TOTAL_BYTES)) continue;
                const item = newMediaItem(file);
                next.push(item);
                void readLocalPreview(file)
                  .then((localPreviewUrl) => {
                    if (
                      !mountedRef.current ||
                      !mediaItemsRef.current.some(
                        (candidate) => candidate.key === item.key,
                      )
                    ) {
                      releaseLocalPreview(localPreviewUrl);
                      return;
                    }
                    replaceMedia(
                      mediaItemsRef.current.map((candidate) =>
                        candidate.key === item.key
                          ? { ...candidate, localPreviewUrl }
                          : candidate,
                      ),
                    );
                  })
                  .catch(() => undefined);
              }
              replaceMedia(next);
              setActivated(true);
            }}
            onChange={(next) => replaceMedia(next)}
            onRemove={(key) => {
              mediaUploadsRef.current.get(key)?.abort();
              mediaUploadsRef.current.delete(key);
              replaceMedia(
                mediaItemsRef.current.filter((item) => item.key !== key),
              );
            }}
            onRetry={(key) => void uploadOne(key).catch(() => undefined)}
          />
          {mediaError ? (
            <p className={styles.formError} role="alert">
              {mediaError}
            </p>
          ) : null}
          {displayedError ? (
            <div
              className={styles.formError}
              id={`${formId}-error`}
              role="alert"
            >
              <span>{displayedError}</span>
              {parentId &&
              displayedError.includes("改为直接回复 Moment") &&
              onPromote ? (
                <button onClick={onPromote} type="button">
                  直接回复 Moment
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
              回复已关闭。文字仍保留在当前页面，便于复制。
            </p>
          ) : null}
        </form>
        {displayReauth ? (
          <Reauthenticate
            inputId={`${formId}-owner-token`}
            onReady={() => {
              setAuthExpired(false);
              setError(null);
              window.requestAnimationFrame(() => textareaRef.current?.focus());
            }}
          />
        ) : null}
      </div>
    </div>
  );
}

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

function CommentItem({
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

export function PostReplyDialog({
  api,
  commentsEnabled,
  content,
  guestName,
  onClosed,
  onContentChange,
  onCreated,
  onGuestNameChange,
  onOpenChange,
  open,
  post,
  site,
  triggerRef,
}: {
  api: PublicApi;
  commentsEnabled: boolean;
  content: string;
  guestName: string;
  onClosed?: () => void;
  onContentChange: (value: string) => void;
  onCreated: (comment: PublicComment) => void;
  onGuestNameChange: (value: string) => void;
  onOpenChange: (open: boolean) => void;
  open: boolean;
  post: PublicPost;
  site: SiteIdentity;
  triggerRef?: RefObject<HTMLButtonElement | null>;
}) {
  const owner = useOwner();
  const [closedByServer, setClosedByServer] = useState(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const mode: CommentMode =
    owner.state === "owner" || owner.state === "expired" || owner.session
      ? "owner"
      : "guest";
  const canComment = commentsEnabled && !closedByServer;

  useDocumentScrollLock(open);

  useEffect(() => setClosedByServer(false), [post.id, commentsEnabled]);

  const initialFocus = useCallback(() => {
    const textarea = textareaRef.current;
    if (!textarea) return null;
    const caret = textarea.value.length;
    textarea.setSelectionRange(caret, caret);
    return textarea;
  }, []);

  return (
    <Dialog.Root onOpenChange={onOpenChange} open={open}>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup
            className={styles.replyDialogPopup}
            finalFocus={() => triggerRef?.current ?? true}
            initialFocus={initialFocus}
          >
            <header className={styles.replyDialogHeader}>
              <Dialog.Close
                aria-label="关闭回复窗口"
                className={styles.replyDialogClose}
              >
                <Icon name="close" />
              </Dialog.Close>
              <Dialog.Title className={styles.srOnly}>
                回复这条 Moment
              </Dialog.Title>
            </header>
            <article
              aria-label={`${site.displayName}发布的 Moment`}
              className={styles.replyContext}
            >
              <span aria-hidden="true" className={styles.replyContextAvatar}>
                <SiteAvatar site={site} />
              </span>
              <div>
                <p className={styles.replyContextByline}>
                  <strong>{site.displayName}</strong>
                  <span>@{site.username}</span>
                </p>
                <MarkdownContent
                  className={styles.replyContextContent}
                  html={post.contentHtml}
                  mentionUsername={site.username}
                />
                <p className={styles.replyContextTarget}>
                  正在回复 <span>@{site.username}</span>
                </p>
              </div>
            </article>
            <CommentForm
              api={api}
              commentsEnabled={canComment}
              content={content}
              formId="dialog-top-level-comment"
              guestName={guestName}
              initialExpanded
              mode={mode}
              onClosed={() => {
                setClosedByServer(true);
                onClosed?.();
              }}
              onCreated={(comment) => {
                onCreated(comment);
                onOpenChange(false);
              }}
              parentId={null}
              postId={post.id}
              setContent={onContentChange}
              setGuestName={onGuestNameChange}
              site={site}
              textareaRef={textareaRef}
            />
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
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
  const [guestName, setGuestName] = useState("");
  const [topContent, setTopContent] = useState("");
  const [dialogContent, setDialogContent] = useState("");
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

  const replyCounts = useMemo(() => {
    const counts = new Map<string, number>();
    for (const comment of items ?? []) {
      if (comment.parentId) {
        counts.set(comment.parentId, (counts.get(comment.parentId) ?? 0) + 1);
      }
    }
    return counts;
  }, [items]);

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
          guestName={guestName}
          onClosed={() => setClosedByServer(true)}
          onContentChange={setDialogContent}
          onCreated={created}
          onGuestNameChange={setGuestName}
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
              site={site}
              topLevelTargetUsername={site.username}
            />
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
                    replyRoot={
                      comment.parentId === null
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
                        replyTargetAuthor={reply.targetAuthor}
                        replyTargetId={reply.targetId}
                        setContent={(content) =>
                          setReply((current) =>
                            current ? { ...current, content } : current,
                          )
                        }
                        setGuestName={setGuestName}
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
