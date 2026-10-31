import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { locationsApi } from "../../api/locations";
import { useEditHistory } from "../../components/useEditHistory";
import { EditMediaHistory } from "../media/editMediaHistory";

import { mediaApi } from "../../api/media";
import { commentsApi } from "../../api/comments";
import { type LocationInput } from "../../api/client";
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

import { LoginLink } from "../auth/loginNavigation";
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
  releaseLocalPreview,
  uploadMessage,
  type ComposerMediaItem,
} from "../media/mediaState";
import { useSession } from "../auth/SessionProvider";
import {
  LocationPicker,
  SelectedLocationChip,
} from "../compose/LocationPicker";
import { MentionSuggestions } from "../compose/MentionSuggestions";
import { activeMention, type ActiveMention } from "../compose/mentions";
import { ReplyParticipants } from "./ReplyParticipants";
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

/**
 * Marks the frame for as long as a fold or unfold is running inside it, so
 * the frame follows the content instead of easing after it (see the
 * `data-comment-layout-motion` rule). The mark goes on before the first
 * frame of the motion is painted, and the window is the content motion's
 * own duration, read from the stylesheet so the two cannot drift apart.
 */
function useFoldMotion(
  frameRef: RefObject<HTMLDivElement | null>,
  expanded: boolean,
) {
  const previousRef = useRef(expanded);
  useLayoutEffect(() => {
    if (previousRef.current === expanded) return;
    previousRef.current = expanded;
    const frame = frameRef.current;
    if (!frame) return;
    frame.dataset.commentLayoutMotion = "fold";
    const timer = window.setTimeout(
      () => {
        delete frame.dataset.commentLayoutMotion;
      },
      motionDuration(frame, "--motion-content"),
    );
    return () => window.clearTimeout(timer);
  }, [expanded, frameRef]);
}

function motionDuration(element: Element, token: string): number {
  const raw = getComputedStyle(element).getPropertyValue(token).trim();
  const value = Number.parseFloat(raw);
  if (!Number.isFinite(value)) return 0;
  return raw.endsWith("ms") ? value : value * 1000;
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
    return t("回复没有发出，文字已保留。检查连接后可以安全重试。");
  }
  switch (error.code) {
    case "CONTENT_EMPTY":
      return t("写下一些内容后再发表。");
    case "CONTENT_TOO_LARGE":
      return t("回复超过 2,000 个字符，请缩短后重试。");
    case "EXTERNAL_IMAGE_NOT_ALLOWED":
      return t("回复不能引用 Markdown 图片；普通链接仍然可以使用。");
    case "INVALID_ARGUMENT":
      return t("请检查回复内容后重试。");
    case "COMMENT_NOT_FOUND":
      return replying
        ? t("上级回复已被删除。这段文字仍保留，可以改为直接回复 Moment。")
        : t("这条回复已不存在。文字仍保留，可以重新加载后再试。");
    case "COMMENTS_DISABLED":
      return t("回复刚刚被关闭；这段文字仍保留在当前页面。");
    // Reachable when approval is withdrawn, or granted and then withdrawn,
    // while a composer is already open. Retrying will not help, so this is one
    // of the few failures that does not offer it.
    case "ACCOUNT_NOT_APPROVED":
      return t("你的账户正在等待管理员批准，暂时还不能留言。文字已保留。");
    case "COMMENT_REJECTED":
      return t("这次回复没有通过防滥用检查，文字仍保留。");
    case "IDEMPOTENCY_KEY_REUSED":
      return t("这次重试与最初内容不一致。再编辑一个字符即可重新发表。");
    case "API_RESPONSE_INVALID":
      return t("服务器返回了不完整的回复数据。文字已保留，请稍后重试。");
    default:
      return t("回复没有发出，文字已保留。可以安全重试。");
  }
}

export function useCanComment(): boolean {
  return useReplyBlock() === null;
}

export type ReplyBlock = "signed-out" | "pending";

/**
 * Why this reader cannot reply, or null when they can.
 *
 * The same rule as `useCanComment`, read for its reason: the two answers point
 * at different next steps, and a reader who is already signed in should never
 * be handed a login link.
 */
export function useReplyBlock(): ReplyBlock | null {
  const auth = useSession();
  if (auth.account?.status === "pending") return "pending";
  return auth.state === "signed-in" ||
    auth.state === "expired" ||
    auth.session !== null
    ? null
    : "signed-out";
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
  const { t } = useTranslation();

  const block = useReplyBlock();
  // Signed in already, just not cleared to write yet. Offering them a login
  // link would send them round a loop they have already been through.
  if (block === "pending") {
    return (
      <div className={styles.signInNotice} role="status">
        <strong>{t("正在等待管理员批准")}</strong>
        <span>{t("你的账户正在等待管理员批准，批准后就能留言。")}</span>
      </div>
    );
  }
  return (
    <div className={styles.signInNotice} role="status">
      <strong>{t("登录后回复")}</strong>
      <span>{t("发表回复需要登录。过往回复继续公开可读。")}</span>
      <LoginLink>{t("登录")}</LoginLink>
    </div>
  );
}

export function CommentForm({
  active = true,
  api,
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
  replyParticipantLabel,
}: {
  active?: boolean;
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
  replyParticipantLabel?: string;
}) {
  const { t } = useTranslation();

  const auth = useSession();
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const [activated, setActivated] = useState(
    (parentId !== null && topLevelTargetUsername === undefined) ||
      initialExpanded,
  );
  const [mediaItems, setMediaItems] = useState<ComposerMediaItem[]>([]);
  const [commentLocation, setCommentLocation] = useState<LocationInput | null>(
    null,
  );
  const [mention, setMention] = useState<ActiveMention | null>(null);
  const [mentionItems, setMentionItems] = useState<MentionSuggestion[]>([]);
  const [mentionActive, setMentionActive] = useState(0);
  const mediaItemsRef = useRef(mediaItems);
  const [mediaHistory] = useState(() => new EditMediaHistory());
  const resetEditHistory = useRef(() => {});
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
  // Nested replies address the selected comment, independently of their flat
  // presentation under the root comment. Top-level replies address the post.
  const replyToCommentId =
    parentId === null ? null : (replyTargetId ?? parentId);
  const replyLabel =
    replyTargetAuthor ??
    replyParticipantLabel ??
    (topLevelTargetUsername ? "@" + topLevelTargetUsername : "");
  const inlineTopLevel = topLevelTargetUsername !== undefined;
  const displayReauth =
    authExpired || (auth.state === "expired" && !auth.session);
  const displayedError =
    error ??
    (displayReauth
      ? t("当前写入会话已失效。文字已保留，重新登录后可以重试。")
      : null);
  const expanded =
    (parentId !== null && !inlineTopLevel) ||
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
  useFoldMotion(layoutFrameRef, expanded);

  const replaceMedia = (next: ComposerMediaItem[]) => {
    mediaHistory.remember(mediaItemsRef.current);
    mediaHistory.remember(next, mediaItemsRef.current);
    mediaItemsRef.current = next;
    setMediaItems(next);
  };

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      for (const controller of mediaUploadsRef.current.values())
        controller.abort();
      mediaHistory.remember(mediaItemsRef.current);
      mediaHistory.clear();
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
      const uploaded = await mediaApi.uploadMedia(
        auth.session!.csrfToken,
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

  const recoveryPending = useRef(false);
  useEffect(() => {
    if (displayReauth) {
      recoveryPending.current = true;
      return;
    }
    if (!recoveryPending.current || !auth.session) return;
    recoveryPending.current = false;
    setAuthExpired(false);
    setError(null);
    if (active && (content.trim() || mediaItems.length))
      window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [displayReauth, auth.session, active, content, mediaItems.length]);

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
    if (!auth.session) {
      setAuthExpired(true);
      setError(t("当前写入会话已失效。文字已保留，重新登录后可以重试。"));
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
      const result = await commentsApi.createComment(
        auth.session!.csrfToken,
        postId,
        content,
        parentId,
        idempotencyRef.current,
        mediaInput(mediaItemsRef.current),
        commentLocation,
        replyToCommentId,
      );
      onCreated(result.comment);
      resetEditHistory.current();
      setContent("");
      signatureRef.current = "";
      idempotencyRef.current = "";
      replaceMedia([]);
      mediaHistory.clear();
      setCommentLocation(null);
      setActivated((parentId !== null && !inlineTopLevel) || initialExpanded);
    } catch (nextError) {
      if (auth.expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError(t("当前写入会话已失效。文字已保留，重新登录后可以重试。"));
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

  const historyGroup = JSON.stringify([
    mediaHistory.signature(mediaItems),
    commentLocation,
  ]);
  const { reset: resetHistory, ...historyEvents } = useEditHistory({
    value: { content, mediaItems, commentLocation },
    signature: JSON.stringify([content, historyGroup]),
    group: historyGroup,
    input: textareaRef,
    enabled: !pending && commentsEnabled,
    restore: (next) => {
      setContent(next.content);
      replaceMedia(mediaHistory.restore(next.mediaItems));
      setCommentLocation(next.commentLocation);
      setMention(null);
      setMentionItems([]);
      setError(null);
    },
  });
  resetEditHistory.current = resetHistory;

  return (
    <div
      className={`${styles.formSurface} ${styles.commentLayoutFrame} ${inlineTopLevel ? styles.inlineTopLevelForm : ""} ${parentId && !inlineTopLevel ? styles.replyForm : ""} ${expanded ? styles.commentExpanded : styles.commentCollapsed}`}
      data-comment-layout-frame="true"
      data-comment-layout-state={expanded ? "expanded" : "collapsed"}
      data-comment-top-level={inlineTopLevel ? "true" : undefined}
      {...historyEvents}
      onFocusCapture={() => setActivated(true)}
      ref={layoutFrameRef}
    >
      <div
        className={styles.commentComposerLayout}
        data-comment-layout-content="true"
        ref={layoutContentRef}
      >
        <span aria-hidden="true" className={styles.composerAvatar}>
          <SiteAvatar
            site={
              auth.account ?? {
                displayName: "",
                avatarMediaId: null,
                coverMediaId: null,
              }
            }
          />
        </span>
        <form
          data-writing-surface
          aria-label={
            parentId
              ? t("回复 {{value1}}", {
                  value1: replyTargetAuthor ?? t("这条回复"),
                })
              : t("发表回复")
          }
          className={styles.commentForm}
          onSubmit={(event) => void submit(event)}
        >
          <header className={styles.commentComposerHeader}>
            <div className={styles.srOnly}>
              <p>{parentId ? t("一级回复") : t("加入这段对话")}</p>
              <h3>
                {parentId
                  ? t("回复 {{value1}}", {
                      value1: replyTargetAuthor ?? t("这条回复"),
                    })
                  : t("写下回复")}
              </h3>
            </div>
          </header>
          {(expanded || inlineTopLevel) && auth.account?.status === "active" ? (
            <div
              className={`${styles.replyTargetRow} ${expanded ? "" : styles.replyTargetRowCollapsed}`}
              aria-hidden={expanded ? undefined : true}
            >
              <ReplyParticipants
                key={`${postId}:${replyToCommentId ?? "post"}:${auth.account.userId}`}
                kind={replyToCommentId ? "comment" : "post"}
                id={replyToCommentId ?? postId}
                enabled={active && expanded && auth.state === "signed-in"}
                fallback={replyLabel}
              />
            </div>
          ) : null}
          <label className={styles.srOnly} htmlFor={`${formId}-content`}>
            {t("回复内容")}
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
              placeholder={t("发布你的回复")}
              ref={textareaRef}
              required={commentLocation === null}
              rows={inlineTopLevel ? 2 : parentId ? 1 : 2}
              value={content}
            />
            {!expanded ? (
              <button
                aria-label={t("回复")}
                className={styles.collapsedReplyAction}
                disabled
                type="button"
              >
                {t("回复")}
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
              {t("支持 Markdown；请勿粘贴图片语法。Ctrl/⌘ + Enter 发表")}
            </span>
            <span
              className={
                contentCount > MAX_COMMENT
                  ? styles.countInvalid
                  : contentCount > MAX_COMMENT * 0.9
                    ? ""
                    : styles.srOnly
              }
              aria-label={t("回复 {{value1}} / {{value2}} 字", {
                value1: contentCount,
                value2: MAX_COMMENT,
              })}
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
                window.requestAnimationFrame(() =>
                  textareaRef.current?.focus(),
                );
              }}
            />
          ) : null}
          {/* Folded away rather than unmounted, so the toolbar row can grow in
              with the composer. Folded, it is hidden from assistive tech and
              unreachable by keyboard or pointer. */}
          <div
            aria-hidden={expanded ? undefined : true}
            className={`${styles.toolbarRow} ${expanded ? "" : styles.toolbarRowCollapsed}`}
            inert={!expanded}
          >
            {active ? (
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
                          {t("取消")}
                        </button>
                      ) : null}
                      <button
                        aria-busy={pending || undefined}
                        aria-label={t("发表回复")}
                        className={styles.primaryAction}
                        data-pending={pending ? "true" : undefined}
                        disabled={
                          pending ||
                          !validContent ||
                          !commentsEnabled ||
                          !auth.session ||
                          mediaError !== null
                        }
                        type="submit"
                      >
                        <span aria-hidden="true">
                          {pending ? t("正在发表…") : t("回复")}
                        </span>
                      </button>
                    </div>
                  </div>
                }
                items={mediaItems}
                maximum={4}
                maximumTotalBytes={MAX_TOTAL_BYTES}
                pickerTooltip={inlineTopLevel ? t("媒体") : undefined}
                subject={t("回复")}
                toolbarAction={
                  <LocationPicker
                    api={locationsApi}
                    disabled={pending || !commentsEnabled || !auth.session}
                    onChange={(nextLocation) => {
                      setCommentLocation(nextLocation);
                      setActivated(true);
                      setError(null);
                    }}
                    pickerTooltip={t("位置")}
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
                  window.requestAnimationFrame(() =>
                    textareaRef.current?.focus(),
                  );
                }}
                onChange={(next) => {
                  const replacements = next.filter(
                    (item) =>
                      item.file &&
                      item.file !==
                        mediaItemsRef.current.find(
                          (current) => current.key === item.key,
                        )?.file,
                  );
                  for (const item of replacements) {
                    mediaUploadsRef.current.get(item.key)?.abort();
                    mediaUploadsRef.current.delete(item.key);
                  }
                  replaceMedia(next);
                }}
                onRemove={(key) => {
                  mediaUploadsRef.current.get(key)?.abort();
                  mediaUploadsRef.current.delete(key);
                  replaceMedia(
                    mediaItemsRef.current.filter((item) => item.key !== key),
                  );
                }}
                onRetry={(key) => void uploadOne(key).catch(() => undefined)}
              />
            ) : null}
          </div>
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
              displayedError.includes(t("改为直接回复 Moment")) &&
              onPromote ? (
                <button onClick={onPromote} type="button">
                  {t("直接回复 Moment")}
                </button>
              ) : null}
            </div>
          ) : null}
          {!commentsEnabled ? (
            <p className={styles.closedInline} role="status">
              {t("回复已关闭。文字仍保留在当前页面，便于复制。")}
            </p>
          ) : null}
        </form>
        {displayReauth ? <LoginLink>{t("重新登录")}</LoginLink> : null}
      </div>
    </div>
  );
}
