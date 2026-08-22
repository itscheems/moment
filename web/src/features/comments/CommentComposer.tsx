import {
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
  type SiteIdentity,
} from "../../api/public";
import type { OwnerLocation } from "../../api/owner";
import { InternalLink } from "../../app/navigation";
import { SiteAvatar } from "../../components/SiteIdentity";
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
      return "请检查回复内容后重试。";
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

/**
 * Whether this browser may still submit a comment.
 *
 * `expired` and a leftover `session` both count as yes: the composer has to
 * stay mounted in those states so `Reauthenticate` can hand the draft back to
 * the same textarea instead of dropping what the writer already typed. Every
 * other state gets `SignInToReply`.
 *
 * Note that `OwnerState`'s `"guest"` means "no owner session in this browser".
 * It is a session state and it survives the retirement of guest commenting —
 * it has nothing to do with `PublicComment.authorKind === "guest"`.
 */
export function useCanComment(): boolean {
  const owner = useOwner();
  return (
    owner.state === "owner" ||
    owner.state === "expired" ||
    owner.session !== null
  );
}

/**
 * Stands in for the composer when nobody is signed in.
 *
 * Commenting needs an account now, and a thread that simply ends after the
 * last reply reads as broken rather than as closed — the reader cannot tell
 * whether replies are off, still loading, or waiting on them. Say which it is
 * and point at the way in.
 */
export function SignInToReply() {
  return (
    <div className={styles.signInNotice} role="status">
      <strong>登录后回复</strong>
      <span>发表回复需要登录。过往回复继续公开可读。</span>
      <InternalLink href="/owner/login">登录</InternalLink>
    </div>
  );
}

export function Reauthenticate({
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

export function CommentForm({
  api,
  site,
  postId,
  parentId,
  replyTargetId,
  replyTargetAuthor,
  content,
  setContent,
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
  parentId: string | null;
  replyTargetId?: string;
  replyTargetAuthor?: string;
  content: string;
  setContent: (value: string) => void;
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
  const validContent =
    (content.trim().length > 0 || commentLocation !== null) &&
    contentCount <= MAX_COMMENT;
  const mediaError = mediaValidation(mediaItems, 4);
  const formId =
    suppliedFormId ??
    (parentId ? `reply-${replyTargetId ?? parentId}` : "top-level-comment");
  // The composer already knew who it was answering — it just spent that on the
  // 「正在回复 @X」 line and threw it away. A root comment answers nobody, so it
  // never carries one; a reply with no explicit target is answering its root.
  const replyToCommentId =
    parentId === null ? null : (replyTargetId ?? parentId);
  const inlineTopLevel =
    parentId === null && topLevelTargetUsername !== undefined;
  const displayReauth =
    authExpired || (owner.state === "expired" && !owner.session);
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
    !commentsEnabled;
  const layoutRevision = [
    expanded,
    mediaItems.length,
    commentLocation?.name ?? "",
    displayedError !== null,
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
      const uploaded = await owner.api.uploadMedia(
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
      !validContent ||
      !commentsEnabled ||
      mediaError !== null
    ) {
      return;
    }
    if (!owner.session) {
      setAuthExpired(true);
      setError("当前写入会话已失效。文字已保留，刷新身份后可以重试。");
      return;
    }
    const signature = JSON.stringify([
      postId,
      parentId,
      // The reply target belongs in the signature for the same reason the
      // server folds it into its idempotency hash: the same words aimed at two
      // different siblings under one root are two different comments, and
      // reusing the key would replay the first one back as the second.
      replyToCommentId,
      content,
      mediaItems.map((item) => [
        item.key,
        item.altMode,
        item.altText,
        item.mediaId,
      ]),
      commentLocation,
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
      const result = await owner.api.createComment(
        owner.session!.csrfToken,
        postId,
        content,
        parentId,
        idempotencyRef.current,
        mediaInput(mediaItemsRef.current),
        commentLocation,
        replyToCommentId,
      );
      onCreated(result.comment);
      setContent("");
      signatureRef.current = "";
      idempotencyRef.current = "";
      replaceMedia([]);
      setCommentLocation(null);
      setActivated(parentId !== null || initialExpanded);
    } catch (nextError) {
      if (owner.expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("当前写入会话已失效。文字已保留，刷新身份后可以重试。");
      } else {
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
          <SiteAvatar site={site} />
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
            <span className={styles.srOnly}>所有者</span>
          </header>
          {(parentId && replyTargetAuthor) ||
          (!parentId && expanded && topLevelTargetUsername) ? (
            <p className={styles.replyTarget}>
              正在回复{" "}
              {/*
               * The label arrives whole. A handle carries its own @; a legacy
               * row that has no account carries a plain display name, and
               * prefixing that with @ would present a name — spaces and all —
               * as though it were a handle.
               */}
              <span>
                {parentId ? replyTargetAuthor : `@${topLevelTargetUsername}`}
              </span>
            </p>
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
                  validContent &&
                  commentsEnabled &&
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
                      !validContent ||
                      !commentsEnabled ||
                      !owner.session ||
                      mediaError !== null
                    }
                    type="submit"
                  >
                    <span aria-hidden="true">
                      {pending ? "正在发表…" : "回复"}
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
                disabled={pending || !commentsEnabled || !owner.session}
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
