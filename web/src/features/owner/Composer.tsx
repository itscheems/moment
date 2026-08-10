import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";

import {
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { ConfirmDialog } from "./ConfirmDialog";
import {
  COMPOSE_DRAFT_CLEARED_EVENT,
  clearComposeDraft,
  composeDraftKey,
  loadComposeDraft,
  saveComposeDraft,
  type ComposeDraft,
} from "./draft";
import { useOwner } from "./OwnerProvider";
import { OwnerLogin } from "./OwnerLogin";
import { announceOwnerPost } from "./postEvents";
import styles from "./Owner.module.css";

const MAX_CONTENT = 20_000;

function createIdempotencyKey(): string {
  if (typeof crypto.randomUUID === "function") {
    return `web-${crypto.randomUUID()}`;
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return `web-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

function messageFor(error: unknown): string {
  if (!(error instanceof PublicApiError)) {
    return "发布没有完成，文字已保留。检查连接后可以安全重试。";
  }
  switch (error.code) {
    case "CONTENT_EMPTY":
      return "写下一些内容后再发布。";
    case "CONTENT_TOO_LARGE":
      return "正文超过 20,000 个字符，请缩短后重试。";
    case "EXTERNAL_IMAGE_NOT_ALLOWED":
      return "Markdown 不能引用外部图片；请移除图片语法后重试。";
    case "POST_NOT_FOUND":
      return "这个 Moment 已不存在，无法继续编辑。";
    case "IDEMPOTENCY_KEY_REUSED":
      return "这次重试与最初内容不一致，请刷新后重新发布。";
    default:
      return "操作没有完成，文字已保留。可以安全重试。";
  }
}

export function Composer({
  site,
  variant,
  post,
}: {
  site: SiteIdentity;
  variant: "inline" | "page" | "edit";
  post?: PublicPost;
}) {
  const { navigate, setNavigationBlocker } = useNavigation();
  const { api, session, expireIfInvalidSession } = useOwner();
  const initialContent = post?.contentMd ?? "";
  const [content, setContent] = useState(initialContent);
  const [draft, setDraft] = useState<ComposeDraft | null>(() =>
    variant === "edit" ? null : loadComposeDraft(site.instanceId),
  );
  const [dirty, setDirty] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [authExpired, setAuthExpired] = useState(false);
  const [draftUnavailable, setDraftUnavailable] = useState(
    variant !== "edit" && !site.instanceId,
  );
  const [announcement, setAnnouncement] = useState("");
  const pendingRef = useRef(false);
  const allowLeaveRef = useRef(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const idempotencyKey = useRef(createIdempotencyKey());
  const count = useMemo(() => Array.from(content).length, [content]);
  const valid = content.trim().length > 0 && count <= MAX_CONTENT;
  const hasUnpublishedChange =
    dirty && content !== initialContent && !allowLeaveRef.current;

  useEffect(() => {
    if (!session || !authExpired) return;
    setAuthExpired(false);
    setError(null);
    setAnnouncement("会话已刷新，可以重试当前操作。");
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [authExpired, session]);

  useEffect(() => {
    const instanceId = site.instanceId;
    if (variant === "edit" || !instanceId) return;
    const resetDraftSurface = () => {
      allowLeaveRef.current = true;
      setDraft(null);
      setContent("");
      setDirty(false);
      setError(null);
    };
    const clearedInThisDocument = (event: Event) => {
      const detail = (event as CustomEvent<unknown>).detail;
      if (
        typeof detail === "object" &&
        detail !== null &&
        (detail as { instanceId?: unknown }).instanceId === instanceId
      ) {
        resetDraftSurface();
      }
    };
    const clearedInAnotherDocument = (event: StorageEvent) => {
      if (
        event.key === composeDraftKey(instanceId) &&
        event.newValue === null
      ) {
        resetDraftSurface();
      }
    };
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, clearedInThisDocument);
    window.addEventListener("storage", clearedInAnotherDocument);
    return () => {
      window.removeEventListener(
        COMPOSE_DRAFT_CLEARED_EVENT,
        clearedInThisDocument,
      );
      window.removeEventListener("storage", clearedInAnotherDocument);
    };
  }, [site.instanceId, variant]);

  useEffect(() => {
    if (variant === "edit" || !dirty || draft || allowLeaveRef.current) return;
    const timer = window.setTimeout(() => {
      if (!saveComposeDraft(site.instanceId, content)) {
        setDraftUnavailable(true);
      }
    }, 500);
    return () => window.clearTimeout(timer);
  }, [content, dirty, draft, site.instanceId, variant]);

  useEffect(() => {
    const confirmLeave = () => {
      if (allowLeaveRef.current) return true;
      const confirmed = window.confirm(
        variant === "edit"
          ? "编辑还没有保存。确定离开吗？"
          : "这段内容还没有发布。离开后仍会保留本地草稿。",
      );
      if (confirmed && variant !== "edit") {
        saveComposeDraft(site.instanceId, content);
      }
      return confirmed;
    };
    setNavigationBlocker(hasUnpublishedChange ? confirmLeave : null);
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!hasUnpublishedChange || allowLeaveRef.current) return;
      if (variant !== "edit") saveComposeDraft(site.instanceId, content);
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      window.removeEventListener("beforeunload", beforeUnload);
      setNavigationBlocker(null);
    };
  }, [
    content,
    hasUnpublishedChange,
    setNavigationBlocker,
    site.instanceId,
    variant,
  ]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!valid || pendingRef.current || !session) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    setAuthExpired(false);
    setAnnouncement(variant === "edit" ? "正在保存…" : "正在发布…");
    if (!post && !saveComposeDraft(site.instanceId, content)) {
      setDraftUnavailable(true);
    }
    try {
      const saved = post
        ? await api.update(session.csrfToken, post.id, content)
        : await api.create(session.csrfToken, content, idempotencyKey.current);
      allowLeaveRef.current = true;
      setDirty(false);
      if (!post) clearComposeDraft(site.instanceId);
      announceOwnerPost({ kind: post ? "updated" : "created", post: saved });
      setAnnouncement(post ? "已保存" : "已发布");
      if (variant === "inline") {
        setContent("");
        idempotencyKey.current = createIdempotencyKey();
        window.setTimeout(() => {
          allowLeaveRef.current = false;
          setAnnouncement("");
        }, 1_000);
      } else {
        navigate(post ? `/m/${encodeURIComponent(saved.id)}` : "/");
      }
    } catch (nextError) {
      if (expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError("当前写入会话已失效。文字已保留，刷新身份后可以重试。");
      } else {
        setError(messageFor(nextError));
      }
      setAnnouncement("");
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  const recoverDraft = () => {
    if (!draft) return;
    setContent(draft.contentMd);
    setDraft(null);
    setDirty(true);
    setAnnouncement("已恢复未发布草稿；图片需要重新选择。当前版本仅恢复文字。");
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  };

  const discardDraft = () => {
    clearComposeDraft(site.instanceId);
    setDraft(null);
    setAnnouncement("本地草稿已丢弃");
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  };

  return (
    <section
      aria-label={post ? "编辑 Moment" : "发布 Moment"}
      className={`${styles.composer} ${variant === "inline" ? styles.inlineComposer : styles.pageComposer}`}
    >
      <div aria-hidden="true" className={styles.newTick}>
        <span />
      </div>
      <div className={styles.composerMain}>
        <header className={styles.composerHeader}>
          <div>
            <p className={styles.eyebrow}>
              {post ? "补充这段记忆" : "新的时间刻度"}
            </p>
            <h2>{post ? "编辑文字" : "现在，写下来"}</h2>
          </div>
          <span>{variant === "inline" ? "N 新建" : "纯文本"}</span>
        </header>

        {draft ? (
          <div className={styles.draftRecovery} role="status">
            <div>
              <strong>发现一段未发布草稿</strong>
              <span>
                保存于 {new Date(draft.savedAt).toLocaleString("zh-CN")}
              </span>
            </div>
            <div>
              <button onClick={recoverDraft} type="button">
                恢复
              </button>
              <ConfirmDialog
                confirmLabel="丢弃草稿"
                danger
                description="这只会移除当前实例里这段未发布的本地文字，不能撤销。"
                onConfirm={discardDraft}
                title="丢弃本地草稿？"
                trigger={<button type="button">丢弃</button>}
              />
            </div>
          </div>
        ) : null}

        <form
          className={styles.composerForm}
          onSubmit={(event) => void submit(event)}
        >
          <label htmlFor={`${variant}-composer-content`}>
            {post ? "Moment 正文" : "此刻想到什么？"}
          </label>
          <textarea
            aria-describedby={`${variant}-composer-guidance ${variant}-composer-error`}
            disabled={pending}
            id={`${variant}-composer-content`}
            onChange={(event) => {
              if (draft) setDraft(null);
              setContent(event.target.value);
              setDirty(true);
              setError(null);
            }}
            onCompositionEnd={() => undefined}
            onKeyDown={(event) => {
              if (
                event.key === "Enter" &&
                (event.metaKey || event.ctrlKey) &&
                !event.nativeEvent.isComposing &&
                valid &&
                !pendingRef.current
              ) {
                event.preventDefault();
                event.currentTarget.form?.requestSubmit();
              }
            }}
            placeholder="一句话也可以。支持 Markdown。"
            ref={textareaRef}
            rows={variant === "inline" ? 4 : 10}
            value={content}
          />
          <div className={styles.composerMeta}>
            <p id={`${variant}-composer-guidance`}>
              <span>Markdown</span>
              <span>⌘/Ctrl + Enter 发布</span>
            </p>
            <span className={count > MAX_CONTENT ? styles.countInvalid : ""}>
              {count.toLocaleString("zh-CN")} /{" "}
              {MAX_CONTENT.toLocaleString("zh-CN")}
            </span>
          </div>
          {draftUnavailable && variant !== "edit" ? (
            <p className={styles.storageNotice}>
              本地草稿恢复不可用；仍可正常发布。
            </p>
          ) : null}
          {error ? (
            <p
              className={styles.formError}
              id={`${variant}-composer-error`}
              role="alert"
            >
              {error} {authExpired ? <span>请在下方刷新身份。</span> : null}
            </p>
          ) : (
            <span id={`${variant}-composer-error`} />
          )}
          <footer className={styles.composerFooter}>
            <p aria-live="polite">{announcement}</p>
            <button
              className={styles.primaryButton}
              disabled={!valid || pending || !session}
              type="submit"
            >
              {pending
                ? post
                  ? "正在保存…"
                  : "正在发布…"
                : post
                  ? "保存更改"
                  : "发布"}
            </button>
          </footer>
        </form>
        {!session ? <OwnerLogin embedded site={site} /> : null}
      </div>
    </section>
  );
}
