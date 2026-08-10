import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";

import {
  PublicApiError,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { MediaComposer } from "../media/MediaComposer";
import {
  altValue,
  applyUpload,
  fileValidation,
  itemsFromPost,
  mediaInput,
  mediaValidation,
  type ComposerMediaItem,
} from "../media/mediaState";
import { ConfirmDialog } from "./ConfirmDialog";
import {
  COMPOSE_DRAFT_CLEAR_ALL_SCHEMA,
  COMPOSE_DRAFT_CLEARED_EVENT,
  clearComposeDraft,
  composeDraftClearAllKey,
  composeDraftClearanceKey,
  composeDraftKey,
  createComposeDraft,
  loadComposeDraft,
  parseComposeDraftClearEvent,
  parseComposeDraftClearEventValue,
  removeComposeDraftForClearance,
  saveComposeDraft,
  subscribeComposeDraftClearance,
  type ComposeDraft,
  type ComposeDraftClearEvent,
} from "./draft";
import { useOwner } from "./OwnerProvider";
import { OwnerLogin } from "./OwnerLogin";
import { announceOwnerPost } from "./postEvents";
import styles from "./Owner.module.css";

const MAX_CONTENT = 20_000;
const DRAFT_REMOVAL_REPAIR_DELAY_MS = 50;

function createMediaKey(): string {
  if (typeof crypto.randomUUID === "function") return crypto.randomUUID();
  return Array.from(crypto.getRandomValues(new Uint8Array(12)), (value) =>
    value.toString(16).padStart(2, "0"),
  ).join("");
}

function newMediaItem(file: File): ComposerMediaItem {
  return {
    key: `local-${createMediaKey()}`,
    file,
    localPreviewUrl: null,
    mediaId: null,
    previewUrl: "",
    originalUrl: "",
    width: null,
    height: null,
    byteSize: null,
    animatedOriginal: file.type === "image/gif",
    altMode: "missing",
    altText: "",
    state: "queued",
    progress: 0,
    error: null,
  };
}

function readLocalPreview(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.addEventListener("load", () => {
      if (typeof reader.result === "string") resolve(reader.result);
      else reject(new Error("local media preview is not a data URL"));
    });
    reader.addEventListener("error", () =>
      reject(reader.error ?? new Error("local media preview failed")),
    );
    reader.readAsDataURL(file);
  });
}

function uploadMessage(error: unknown): string {
  if (!(error instanceof PublicApiError)) {
    return "图片没有上传完成。文字和已完成的图片都已保留，可以重试。";
  }
  switch (error.code) {
    case "MEDIA_TYPE_UNSUPPORTED":
      return "图片格式不受支持，请选择 JPEG、PNG、WebP 或 GIF。";
    case "MEDIA_TOO_LARGE":
      return "图片超过大小限制，请移除或压缩后重试。";
    case "MEDIA_DIMENSIONS_EXCEEDED":
      return "图片尺寸或 GIF 帧数超过安全限制。";
    case "MEDIA_CORRUPT":
      return "图片内容无效或与文件类型不一致，请重新选择。";
    default:
      return "图片没有上传完成。文字和已完成的图片都已保留，可以重试。";
  }
}

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
  active = true,
}: {
  site: SiteIdentity;
  variant: "inline" | "page" | "edit";
  post?: PublicPost;
  active?: boolean;
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
  const [activeDraft, setActiveDraft] = useState<ComposeDraft | null>(draft);
  const [announcement, setAnnouncement] = useState("");
  const [mediaItems, setMediaItems] = useState<ComposerMediaItem[]>(() =>
    itemsFromPost(post?.media ?? []),
  );
  const pendingRef = useRef(false);
  const allowLeaveRef = useRef(false);
  const dirtyRef = useRef(false);
  const activeDraftRef = useRef(activeDraft);
  const mediaItemsRef = useRef(mediaItems);
  const mediaUploadsRef = useRef(new Map<string, AbortController>());
  const mountedRef = useRef(true);
  const draftRemovalRepairRef = useRef<number | null>(null);
  const navigationBlockerId = useRef(Symbol("composer-navigation-blocker"));
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const idempotencyKey = useRef(createIdempotencyKey());
  const count = useMemo(() => Array.from(content).length, [content]);
  const mediaError = mediaValidation(mediaItems);
  const initialMedia = post?.media ?? [];
  const mediaChanged =
    mediaItems.length !== initialMedia.length ||
    mediaItems.some((item, index) => {
      const initial = initialMedia[index];
      return (
        !initial ||
        item.mediaId !== initial.mediaId ||
        altValue(item) !== initial.altText
      );
    });
  const valid =
    content.trim().length > 0 && count <= MAX_CONTENT && mediaError === null;
  const hasUnpublishedChange =
    dirty &&
    (content !== initialContent || mediaChanged) &&
    !allowLeaveRef.current;

  const replaceMedia = (next: ComposerMediaItem[]) => {
    mediaItemsRef.current = next;
    setMediaItems(next);
  };

  const clearMedia = () => {
    for (const controller of mediaUploadsRef.current.values())
      controller.abort();
    mediaUploadsRef.current.clear();
    mediaItemsRef.current = [];
    setMediaItems([]);
  };

  const markMediaEdited = (message: string) => {
    allowLeaveRef.current = false;
    dirtyRef.current = true;
    setDirty(true);
    setError(null);
    setAnnouncement(message);
    idempotencyKey.current = createIdempotencyKey();
  };

  useEffect(() => {
    if (!session || !authExpired) return;
    setAuthExpired(false);
    setError(null);
    setAnnouncement("会话已刷新，可以重试当前操作。");
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [authExpired, session]);

  useEffect(
    () => () => {
      mountedRef.current = false;
      for (const controller of mediaUploadsRef.current.values())
        controller.abort();
      mediaUploadsRef.current.clear();
      mediaItemsRef.current = [];
    },
    [],
  );

  useEffect(() => {
    if (active || variant !== "inline" || mediaItemsRef.current.length === 0) {
      return;
    }
    clearMedia();
    if (content === initialContent) {
      dirtyRef.current = false;
      setDirty(false);
    }
  }, [active, content, initialContent, variant]);

  useEffect(() => {
    const instanceId = site.instanceId;
    if (variant === "edit" || !instanceId) return;
    const resetDraftSurface = () => {
      allowLeaveRef.current = true;
      activeDraftRef.current = null;
      setActiveDraft(null);
      setDraft(null);
      setContent("");
      dirtyRef.current = false;
      setDirty(false);
      setError(null);
      clearMedia();
    };
    const cancelDraftRemovalRepair = () => {
      if (draftRemovalRepairRef.current !== null) {
        window.clearTimeout(draftRemovalRepairRef.current);
        draftRemovalRepairRef.current = null;
      }
    };
    const handleClearance = (clearance: ComposeDraftClearEvent) => {
      if (clearance.instance_id !== instanceId) return;
      const active = activeDraftRef.current;
      const matches =
        clearance.schema === COMPOSE_DRAFT_CLEAR_ALL_SCHEMA
          ? true
          : active?.fingerprint === clearance.draft_fingerprint &&
            Date.parse(active.savedAt) <= Date.parse(clearance.cleared_at);
      cancelDraftRemovalRepair();
      if (matches) {
        removeComposeDraftForClearance(instanceId, clearance);
        resetDraftSurface();
      } else if (active) {
        saveComposeDraft(instanceId, active);
      }
    };
    const clearedInThisDocument = (event: Event) => {
      const clearance = parseComposeDraftClearEvent(
        (event as CustomEvent<unknown>).detail,
      );
      if (clearance) handleClearance(clearance);
    };
    const clearedInAnotherDocument = (event: StorageEvent) => {
      if (
        event.key === composeDraftKey(instanceId) &&
        event.newValue === null
      ) {
        if (dirtyRef.current && activeDraftRef.current) {
          cancelDraftRemovalRepair();
          draftRemovalRepairRef.current = window.setTimeout(() => {
            draftRemovalRepairRef.current = null;
            if (dirtyRef.current && activeDraftRef.current) {
              saveComposeDraft(instanceId, activeDraftRef.current);
            }
          }, DRAFT_REMOVAL_REPAIR_DELAY_MS);
        }
        return;
      }
      const clearance = parseComposeDraftClearEventValue(event.newValue);
      if (!clearance || clearance.instance_id !== instanceId) return;
      const expectedKey =
        clearance.schema === COMPOSE_DRAFT_CLEAR_ALL_SCHEMA
          ? composeDraftClearAllKey(instanceId)
          : composeDraftClearanceKey(instanceId);
      if (event.key === expectedKey) handleClearance(clearance);
    };
    const unsubscribeClearance =
      subscribeComposeDraftClearance(handleClearance);
    window.addEventListener(COMPOSE_DRAFT_CLEARED_EVENT, clearedInThisDocument);
    window.addEventListener("storage", clearedInAnotherDocument);
    return () => {
      window.removeEventListener(
        COMPOSE_DRAFT_CLEARED_EVENT,
        clearedInThisDocument,
      );
      window.removeEventListener("storage", clearedInAnotherDocument);
      unsubscribeClearance();
      cancelDraftRemovalRepair();
    };
  }, [site.instanceId, variant]);

  useEffect(() => {
    if (
      variant === "edit" ||
      !dirty ||
      draft ||
      !activeDraft ||
      allowLeaveRef.current
    ) {
      return;
    }
    const timer = window.setTimeout(() => {
      if (!saveComposeDraft(site.instanceId, activeDraft)) {
        setDraftUnavailable(true);
      }
    }, 500);
    return () => window.clearTimeout(timer);
  }, [activeDraft, dirty, draft, site.instanceId, variant]);

  useEffect(() => {
    const confirmLeave = () => {
      if (allowLeaveRef.current) return true;
      const confirmed = window.confirm(
        variant === "edit"
          ? "编辑还没有保存。确定离开吗？"
          : "这段内容还没有发布。离开后仍会保留本地草稿。",
      );
      if (confirmed && variant !== "edit") {
        const currentDraft = activeDraftRef.current;
        if (currentDraft) saveComposeDraft(site.instanceId, currentDraft);
      }
      return confirmed;
    };
    setNavigationBlocker(
      navigationBlockerId.current,
      hasUnpublishedChange ? confirmLeave : null,
    );
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!hasUnpublishedChange || allowLeaveRef.current) return;
      if (variant !== "edit") {
        const currentDraft = activeDraftRef.current;
        if (currentDraft) saveComposeDraft(site.instanceId, currentDraft);
      }
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      window.removeEventListener("beforeunload", beforeUnload);
      setNavigationBlocker(navigationBlockerId.current, null);
    };
  }, [
    content,
    hasUnpublishedChange,
    mediaItems,
    setNavigationBlocker,
    site.instanceId,
    variant,
  ]);

  const uploadOne = async (key: string, csrfToken: string) => {
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
      const uploaded = await api.uploadMedia(
        csrfToken,
        current.file,
        (progress) => {
          if (
            !mountedRef.current ||
            mediaUploadsRef.current.get(key) !== controller
          ) {
            return;
          }
          replaceMedia(
            mediaItemsRef.current.map((item) =>
              item.key === key && item.state === "uploading"
                ? { ...item, progress }
                : item,
            ),
          );
        },
        controller.signal,
      );
      if (
        !mountedRef.current ||
        mediaUploadsRef.current.get(key) !== controller ||
        !mediaItemsRef.current.some((item) => item.key === key)
      ) {
        return;
      }
      replaceMedia(
        mediaItemsRef.current.map((item) =>
          item.key === key ? applyUpload(item, uploaded) : item,
        ),
      );
    } catch (nextError) {
      if (
        !mountedRef.current ||
        mediaUploadsRef.current.get(key) !== controller ||
        !mediaItemsRef.current.some((item) => item.key === key)
      ) {
        throw nextError;
      }
      const message = uploadMessage(nextError);
      replaceMedia(
        mediaItemsRef.current.map((item) =>
          item.key === key
            ? { ...item, state: "failed", progress: 0, error: message }
            : item,
        ),
      );
      throw nextError;
    } finally {
      if (mediaUploadsRef.current.get(key) === controller) {
        mediaUploadsRef.current.delete(key);
      }
    }
  };

  const retryUpload = async (key: string) => {
    if (!session || pendingRef.current) return;
    setError(null);
    setAnnouncement("正在重试图片上传…");
    try {
      await uploadOne(key, session.csrfToken);
      setAnnouncement("图片已就绪，可以继续发布。 ");
    } catch (nextError) {
      if (!mountedRef.current) return;
      if (expireIfInvalidSession(nextError)) {
        setAuthExpired(true);
        setError(
          "当前写入会话已失效。文字、图片和上传结果已保留，刷新身份后可以重试。",
        );
      }
      setAnnouncement("");
    }
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!valid || pendingRef.current || !session) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    setAuthExpired(false);
    setAnnouncement(variant === "edit" ? "正在保存…" : "正在发布…");
    let submittedDraft: ComposeDraft | null = null;
    if (!post) {
      const active = activeDraftRef.current;
      submittedDraft =
        active && active.contentMd === content
          ? active
          : createComposeDraft(content, active?.savedAt);
      activeDraftRef.current = submittedDraft;
      setActiveDraft(submittedDraft);
      if (!saveComposeDraft(site.instanceId, submittedDraft)) {
        setDraftUnavailable(true);
      }
    }
    try {
      for (const item of [...mediaItemsRef.current]) {
        if (!item.mediaId) {
          setAnnouncement(
            `正在处理第 ${mediaItemsRef.current.findIndex((candidate) => candidate.key === item.key) + 1} 张图片…`,
          );
          await uploadOne(item.key, session.csrfToken);
        }
      }
      const usages = mediaInput(mediaItemsRef.current);
      setAnnouncement(variant === "edit" ? "正在保存…" : "正在发布…");
      const saved = post
        ? await api.update(session.csrfToken, post.id, content, usages)
        : await api.create(
            session.csrfToken,
            content,
            idempotencyKey.current,
            usages,
          );
      allowLeaveRef.current = true;
      dirtyRef.current = false;
      setDirty(false);
      if (!post && submittedDraft) {
        clearComposeDraft(site.instanceId, {
          reason: "published",
          draft: submittedDraft,
        });
      }
      announceOwnerPost({ kind: post ? "updated" : "created", post: saved });
      setAnnouncement(post ? "已保存" : "已发布");
      clearMedia();
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
        setError(
          "当前写入会话已失效。文字、图片和上传结果已保留，刷新身份后可以重试。",
        );
      } else {
        setError(
          mediaItemsRef.current.some((item) => item.state === "failed")
            ? uploadMessage(nextError)
            : messageFor(nextError),
        );
      }
      setAnnouncement("");
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  const recoverDraft = () => {
    if (!draft) return;
    activeDraftRef.current = draft;
    setActiveDraft(draft);
    setContent(draft.contentMd);
    setDraft(null);
    dirtyRef.current = true;
    setDirty(true);
    setAnnouncement("已恢复未发布草稿；图片需要重新选择。当前版本仅恢复文字。");
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  };

  const discardDraft = () => {
    if (!draft) return;
    clearComposeDraft(site.instanceId, {
      reason: "discarded",
      draft,
    });
    setDraft(null);
    clearMedia();
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
          <span>{variant === "inline" ? "N 新建" : "文字优先 · 可附图"}</span>
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
              allowLeaveRef.current = false;
              if (variant !== "edit") {
                const nextDraft = createComposeDraft(
                  event.target.value,
                  activeDraftRef.current?.savedAt,
                );
                activeDraftRef.current = nextDraft;
                setActiveDraft(nextDraft);
              }
              if (draft) setDraft(null);
              setContent(event.target.value);
              dirtyRef.current = true;
              setDirty(true);
              setError(null);
              idempotencyKey.current = createIdempotencyKey();
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
          <MediaComposer
            disabled={pending}
            items={mediaItems}
            onAdd={(files) => {
              const next = [...mediaItemsRef.current];
              for (const file of files) {
                const invalid = fileValidation(file, next);
                if (invalid) continue;
                const item = newMediaItem(file);
                next.push(item);
                void readLocalPreview(file)
                  .then((localPreviewUrl) => {
                    if (
                      !mountedRef.current ||
                      !mediaItemsRef.current.some(
                        (candidate) =>
                          candidate.key === item.key && candidate.file === file,
                      )
                    ) {
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
                  .catch(() => {
                    if (
                      !mountedRef.current ||
                      !mediaItemsRef.current.some(
                        (candidate) =>
                          candidate.key === item.key && candidate.file === file,
                      )
                    ) {
                      return;
                    }
                    setAnnouncement(
                      "有一张本地缩略图暂时无法显示；原文件仍可继续上传。",
                    );
                  });
              }
              replaceMedia(next);
              markMediaEdited(`已加入 ${files.length} 张图片。`);
            }}
            onChange={(next, message) => {
              replaceMedia(next);
              markMediaEdited(message);
            }}
            onRemove={(key) => {
              const current = mediaItemsRef.current;
              mediaUploadsRef.current.get(key)?.abort();
              mediaUploadsRef.current.delete(key);
              const next = current.filter((item) => item.key !== key);
              replaceMedia(next);
              markMediaEdited("图片已移除，其余图片顺序已更新。");
              if (next.length === 0 && content === initialContent) {
                dirtyRef.current = false;
                setDirty(false);
              }
            }}
            onRetry={(key) => void retryUpload(key)}
          />
          {mediaItems.some((item) => item.altMode === "missing") ? (
            <p className={styles.storageNotice} role="status">
              {mediaItems.filter((item) => item.altMode === "missing").length}{" "}
              张图片尚未描述；这不会阻止发布。
            </p>
          ) : null}
          {mediaError ? (
            <p className={styles.formError} role="alert">
              {mediaError}
            </p>
          ) : null}
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
