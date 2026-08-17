import {
  forwardRef,
  type ClipboardEvent as ReactClipboardEvent,
  type FormEvent,
  useCallback,
  useEffect,
  useImperativeHandle,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Dialog } from "@base-ui/react/dialog";

import {
  PublicApi,
  PublicApiError,
  type MentionSuggestion,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import type { OwnerLocation } from "../../api/owner";
import { useNavigation } from "../../app/navigation";
import { notify } from "../../components/Feedback";
import { MarkdownContent } from "../../components/MarkdownContent";
import { MediaComposer } from "../media/MediaComposer";
import { ImageGrid } from "../media/ImageGrid";
import { SiteAvatar } from "../../components/SiteIdentity";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  altValue,
  applyUpload,
  itemsFromPost,
  mediaInput,
  mediaValidation,
  newMediaItem,
  readLocalPreview,
  releaseDiscardedLocalPreviews,
  releaseLocalPreview,
  selectMediaFiles,
  uploadMessage,
  type ComposerMediaItem,
} from "../media/mediaState";
import { ConfirmDialog } from "./ConfirmDialog";
import { LocationPicker, SelectedLocationChip } from "./LocationPicker";
import { MentionSuggestions } from "./MentionSuggestions";
import {
  activeMention,
  mentionPanelPosition,
  mentionSegments,
} from "./mentions";
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

function initialPostLocation(post?: PublicPost): OwnerLocation | null {
  if (!post?.location) return null;
  return {
    name: post.location.name,
    displayName: post.location.displayName,
    countryCode: "",
    country: "",
    region: "",
    city: "",
    district: "",
    category: "",
    provider: "manual",
    providerPlaceId: "",
    latitude: null,
    longitude: null,
    coordinateSystem: null,
  };
}

export interface ComposerHandle {
  hasUnpublishedChanges: () => boolean;
  leaveAndKeepDraft: () => void;
  leaveAndDiscard: () => void;
}

interface ComposerProps {
  site: SiteIdentity;
  variant: "inline" | "page" | "edit";
  post?: PublicPost;
  active?: boolean;
  quotedPostId?: string;
}

export const Composer = forwardRef<ComposerHandle, ComposerProps>(
  function Composer({ site, variant, post, active = true, quotedPostId }, ref) {
    const { navigate, setNavigationBlocker } = useNavigation();
    const { api, session, expireIfInvalidSession } = useOwner();
    const publicApi = useMemo(() => new PublicApi(), []);
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
    const [quotedPost, setQuotedPost] = useState<PublicPost | null>(null);
    const [quoteError, setQuoteError] = useState(false);
    const [mediaItems, setMediaItems] = useState<ComposerMediaItem[]>(() =>
      itemsFromPost(post?.media ?? []),
    );
    const [mediaSelectionError, setMediaSelectionError] = useState<
      string | null
    >(null);
    const initialLocation = useMemo(() => initialPostLocation(post), [post]);
    const [location, setLocation] = useState<OwnerLocation | null>(
      initialLocation,
    );
    const [locationChanged, setLocationChanged] = useState(false);
    const [mention, setMention] = useState<{
      start: number;
      end: number;
      query: string;
    } | null>(null);
    const [mentionItems, setMentionItems] = useState<MentionSuggestion[]>([]);
    const [mentionActive, setMentionActive] = useState(0);
    const [leavePromptOpen, setLeavePromptOpen] = useState(false);
    const pendingRef = useRef(false);
    const pendingNavigationRef = useRef<(() => void) | null>(null);
    const allowLeaveRef = useRef(false);
    const dirtyRef = useRef(false);
    const activeDraftRef = useRef(activeDraft);
    const mediaItemsRef = useRef(mediaItems);
    const mediaUploadsRef = useRef(new Map<string, AbortController>());
    const mountedRef = useRef(true);
    const draftRemovalRepairRef = useRef<number | null>(null);
    const navigationBlockerId = useRef(Symbol("composer-navigation-blocker"));
    const textareaRef = useRef<HTMLTextAreaElement>(null);
    const highlightRef = useRef<HTMLDivElement>(null);
    const mentionAnchorRef = useRef<HTMLSpanElement>(null);
    const editorRef = useRef<HTMLDivElement>(null);
    const [mentionPosition, setMentionPosition] = useState({
      left: 0,
      top: 48,
      width: 320,
    });

    useDocumentScrollLock(leavePromptOpen);
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
    const repostOnly =
      quotedPost !== null &&
      content.trim().length === 0 &&
      mediaItems.length === 0;
    const valid =
      (content.trim().length > 0 ||
        mediaItems.length > 0 ||
        quotedPost !== null) &&
      count <= MAX_CONTENT &&
      mediaError === null &&
      mediaItems.every((item) => item.state === "ready");
    const hasUnpublishedChange =
      dirty &&
      (content !== initialContent || mediaChanged || locationChanged) &&
      !allowLeaveRef.current;

    useEffect(() => {
      if (!quotedPostId || variant === "edit") {
        setQuotedPost(null);
        setQuoteError(false);
        return;
      }
      const controller = new AbortController();
      setQuoteError(false);
      void publicApi
        .post(quotedPostId, controller.signal)
        .then(setQuotedPost)
        .catch(() => {
          if (!controller.signal.aborted) setQuoteError(true);
        });
      return () => controller.abort();
    }, [publicApi, quotedPostId, variant]);

    useEffect(() => {
      if (!mention) {
        setMentionItems([]);
        return;
      }
      const controller = new AbortController();
      const timer = window.setTimeout(() => {
        void publicApi
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
    }, [mention?.query, mention?.start, publicApi]);

    const updateMentionPosition = useCallback(() => {
      const anchor = mentionAnchorRef.current;
      const editor = editorRef.current;
      const textarea = textareaRef.current;
      if (!anchor || !editor || !textarea) return;

      const anchorRect = anchor.getBoundingClientRect();
      const editorRect = editor.getBoundingClientRect();
      if (editorRect.width <= 0) return;
      const lineHeight = Number.parseFloat(
        window.getComputedStyle(textarea).lineHeight,
      );
      const effectiveLineHeight = Number.isFinite(lineHeight) ? lineHeight : 28;
      const next = mentionPanelPosition({
        anchorLeft: anchorRect.left,
        anchorTop: anchorRect.top,
        editorLeft: editorRect.left,
        editorTop: editorRect.top,
        editorWidth: editorRect.width,
        itemCount: mentionItems.length,
        lineHeight: effectiveLineHeight,
        scrollLeft: textarea.scrollLeft,
        scrollTop: textarea.scrollTop,
        viewportHeight: window.innerHeight,
      });
      setMentionPosition((current) =>
        current.left === next.left &&
        current.top === next.top &&
        current.width === next.width
          ? current
          : next,
      );
    }, [mentionItems.length]);

    useLayoutEffect(() => {
      if (!mention || mentionItems.length === 0) return;
      updateMentionPosition();
      const editor = editorRef.current;
      if (!editor || typeof ResizeObserver === "undefined") return;
      const observer = new ResizeObserver(updateMentionPosition);
      observer.observe(editor);
      return () => observer.disconnect();
    }, [content, mention, mentionItems.length, updateMentionPosition]);

    const pickMention = (item: MentionSuggestion) => {
      if (!mention) return;
      const next = `${content.slice(0, mention.start)}@${item.username} ${content.slice(mention.end)}`;
      const caret = mention.start + item.username.length + 2;
      setContent(next);
      if (variant !== "edit") {
        const nextDraft = createComposeDraft(
          next,
          activeDraftRef.current?.savedAt,
        );
        activeDraftRef.current = nextDraft;
        setActiveDraft(nextDraft);
      }
      setMention(null);
      setMentionItems([]);
      allowLeaveRef.current = false;
      dirtyRef.current = true;
      setDirty(true);
      setError(null);
      idempotencyKey.current = createIdempotencyKey();
      window.requestAnimationFrame(() => {
        textareaRef.current?.focus();
        textareaRef.current?.setSelectionRange(caret, caret);
      });
    };

    const replaceMedia = (next: ComposerMediaItem[]) => {
      releaseDiscardedLocalPreviews(mediaItemsRef.current, next);
      mediaItemsRef.current = next;
      setMediaItems(next);
    };

    const clearMedia = useCallback(() => {
      for (const controller of mediaUploadsRef.current.values())
        controller.abort();
      mediaUploadsRef.current.clear();
      releaseDiscardedLocalPreviews(mediaItemsRef.current, []);
      mediaItemsRef.current = [];
      setMediaItems([]);
    }, []);

    useImperativeHandle(
      ref,
      () => ({
        hasUnpublishedChanges: () => hasUnpublishedChange,
        leaveAndKeepDraft: () => {
          allowLeaveRef.current = true;
          if (variant === "edit" || content.trim().length === 0) return;
          const current =
            activeDraftRef.current?.contentMd === content
              ? activeDraftRef.current
              : createComposeDraft(content, activeDraftRef.current?.savedAt);
          activeDraftRef.current = current;
          saveComposeDraft(site.instanceId, current);
        },
        leaveAndDiscard: () => {
          allowLeaveRef.current = true;
          const current = activeDraftRef.current;
          if (variant !== "edit" && current) {
            clearComposeDraft(site.instanceId, {
              reason: "discarded",
              draft: current,
            });
          }
          clearMedia();
          setLocation(initialLocation);
          setLocationChanged(false);
        },
      }),
      [
        clearMedia,
        content,
        hasUnpublishedChange,
        initialLocation,
        site.instanceId,
        variant,
      ],
    );

    const markMediaEdited = (message: string) => {
      allowLeaveRef.current = false;
      dirtyRef.current = true;
      setDirty(true);
      setError(null);
      setAnnouncement(message);
      idempotencyKey.current = createIdempotencyKey();
    };

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

    const addMedia = (files: File[], source: "picker" | "paste" = "picker") => {
      const next = [...mediaItemsRef.current];
      const addedKeys: string[] = [];
      for (const file of files) {
        const item = newMediaItem(file);
        next.push(item);
        addedKeys.push(item.key);
        void readLocalPreview(file)
          .then((localPreviewUrl) => {
            if (
              !mountedRef.current ||
              !mediaItemsRef.current.some(
                (candidate) =>
                  candidate.key === item.key && candidate.file === file,
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
      markMediaEdited(
        source === "paste"
          ? `已从剪贴板加入 ${files.length} 张图片。`
          : `已加入 ${files.length} 张图片。`,
      );
      if (session) {
        for (const key of addedKeys) {
          void uploadOne(key, session.csrfToken).catch((nextError) => {
            if (!mountedRef.current) return;
            if (expireIfInvalidSession(nextError)) {
              setAuthExpired(true);
              setError(
                "当前写入会话已失效。文字、媒体和上传结果已保留，刷新身份后可以重试。",
              );
            }
          });
        }
      }
    };

    const pasteMedia = (event: ReactClipboardEvent<HTMLTextAreaElement>) => {
      const files = Array.from(event.clipboardData.files);
      if (files.length === 0) return;
      const { accepted, error: selectionError } = selectMediaFiles(
        files,
        mediaItemsRef.current,
      );
      setMediaSelectionError(selectionError);
      if (accepted.length === 0) return;
      event.preventDefault();
      addMedia(accepted, "paste");
    };

    useEffect(() => {
      if (!session || !authExpired) return;
      setAuthExpired(false);
      setError(null);
      setAnnouncement("会话已刷新，可以重试当前操作。");
      window.requestAnimationFrame(() => textareaRef.current?.focus());
    }, [authExpired, session]);

    useEffect(() => {
      const textarea = textareaRef.current;
      if (!textarea) return;
      if (mediaItems.length > 0) {
        textarea.style.height = "";
        return;
      }
      textarea.style.height = "auto";
      textarea.style.height = `${Math.min(textarea.scrollHeight, 288)}px`;
    }, [content, mediaItems.length]);

    useEffect(() => {
      mountedRef.current = true;
      return () => {
        mountedRef.current = false;
        for (const controller of mediaUploadsRef.current.values())
          controller.abort();
        mediaUploadsRef.current.clear();
        releaseDiscardedLocalPreviews(mediaItemsRef.current, []);
        mediaItemsRef.current = [];
      };
    }, []);

    useEffect(() => {
      if (
        active ||
        variant !== "inline" ||
        (mediaItemsRef.current.length === 0 && location === null)
      ) {
        return;
      }
      clearMedia();
      setLocation(null);
      setLocationChanged(false);
      if (content === initialContent) {
        dirtyRef.current = false;
        setDirty(false);
      }
    }, [active, clearMedia, content, initialContent, location, variant]);

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
        setLocation(null);
        setLocationChanged(false);
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
      window.addEventListener(
        COMPOSE_DRAFT_CLEARED_EVENT,
        clearedInThisDocument,
      );
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
    }, [clearMedia, site.instanceId, variant]);

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
      const requestLeave = (resume: () => void) => {
        if (allowLeaveRef.current) return true;
        pendingNavigationRef.current = resume;
        setLeavePromptOpen(true);
        return false;
      };
      setNavigationBlocker(
        navigationBlockerId.current,
        hasUnpublishedChange ? requestLeave : null,
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

    const finishPendingNavigation = (discard: boolean) => {
      const resume = pendingNavigationRef.current;
      pendingNavigationRef.current = null;
      allowLeaveRef.current = true;

      if (variant !== "edit") {
        const currentDraft = activeDraftRef.current;
        if (discard) {
          if (currentDraft) {
            clearComposeDraft(site.instanceId, {
              reason: "discarded",
              draft: currentDraft,
            });
          }
          activeDraftRef.current = null;
          setActiveDraft(null);
          setDraft(null);
          setContent("");
          clearMedia();
          setLocation(null);
          setLocationChanged(false);
          dirtyRef.current = false;
          setDirty(false);
        } else if (currentDraft) {
          saveComposeDraft(site.instanceId, currentDraft);
        }
      }

      setLeavePromptOpen(false);
      window.queueMicrotask(() => resume?.());
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
      setAnnouncement(
        variant === "edit"
          ? "正在保存…"
          : repostOnly
            ? "正在转发…"
            : "正在发布…",
      );
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
        setAnnouncement(
          variant === "edit"
            ? "正在保存…"
            : repostOnly
              ? "正在转发…"
              : "正在发布…",
        );
        const saved = post
          ? await api.update(
              session.csrfToken,
              post.id,
              content,
              usages,
              locationChanged ? location : undefined,
            )
          : repostOnly && quotedPost
            ? await api.setReposted(session.csrfToken, quotedPost.id, true)
            : await api.create(
                session.csrfToken,
                content,
                idempotencyKey.current,
                usages,
                location ?? undefined,
                quotedPost?.id,
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
        announceOwnerPost({
          kind: post ? "updated" : repostOnly ? "reordered" : "created",
          post: saved,
        });
        setAnnouncement(post ? "已保存" : repostOnly ? "已转发" : "已发布");
        notify(
          post
            ? "Moment 已保存"
            : repostOnly
              ? "已转发到时间流顶部"
              : "Moment 已发布",
          {
            action: {
              label: "查看 Moment",
              onAction: () => navigate(`/m/${encodeURIComponent(saved.id)}`),
            },
          },
        );
        clearMedia();
        if (variant === "inline") {
          setContent("");
          setLocation(null);
          setLocationChanged(false);
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
              ? null
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
      setAnnouncement(
        "已恢复未发布草稿；图片需要重新选择。当前版本仅恢复文字。",
      );
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
        className={`${styles.composer} ${variant === "inline" ? styles.inlineComposer : styles.pageComposer} ${mediaItems.length > 0 ? styles.composerHasMedia : ""}`}
      >
        <div aria-hidden="true" className={styles.newTick}>
          <SiteAvatar site={site} />
        </div>
        <div className={styles.composerMain}>
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
            <div className={styles.composerEditor} ref={editorRef}>
              <div
                aria-hidden="true"
                className={styles.composerHighlight}
                ref={highlightRef}
              >
                {mentionSegments(content, site.username).map(
                  (segment, index) =>
                    segment.mention ? (
                      <span
                        className={styles.composerMention}
                        data-mention-highlight={segment.text}
                        key={`${index}-${segment.text}`}
                      >
                        {segment.text}
                      </span>
                    ) : (
                      segment.text
                    ),
                )}
                {"\u200b"}
              </div>
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
                  setMention(
                    activeMention(
                      event.target.value,
                      event.target.selectionStart ?? event.target.value.length,
                    ),
                  );
                  dirtyRef.current = true;
                  setDirty(true);
                  setError(null);
                  idempotencyKey.current = createIdempotencyKey();
                }}
                onCompositionEnd={() => undefined}
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
                    valid &&
                    !pendingRef.current
                  ) {
                    event.preventDefault();
                    event.currentTarget.form?.requestSubmit();
                  }
                }}
                onPaste={pasteMedia}
                onScroll={(event) => {
                  if (!highlightRef.current) return;
                  highlightRef.current.scrollTop =
                    event.currentTarget.scrollTop;
                  highlightRef.current.scrollLeft =
                    event.currentTarget.scrollLeft;
                  updateMentionPosition();
                }}
                placeholder={post ? "编辑 Moment" : "有什么新鲜事？"}
                ref={textareaRef}
                rows={mediaItems.length > 0 ? 1 : variant === "inline" ? 1 : 4}
                value={content}
              />
              {mention ? (
                <div
                  aria-hidden="true"
                  className={styles.composerMentionMeasure}
                >
                  {content.slice(0, mention.end)}
                  <span ref={mentionAnchorRef}>{"\u200b"}</span>
                </div>
              ) : null}
              <MentionSuggestions
                active={mentionActive}
                items={mentionItems}
                onPick={pickMention}
                position={mentionPosition}
              />
            </div>
            <p
              className={styles.composerGuidance}
              id={`${variant}-composer-guidance`}
            >
              支持 Markdown。按 Command 或 Control 加 Enter 发布。
            </p>
            {count >= MAX_CONTENT * 0.9 ? (
              <p
                className={`${styles.composerCount} ${count > MAX_CONTENT ? styles.countInvalid : ""}`}
              >
                {count.toLocaleString("zh-CN")} /{" "}
                {MAX_CONTENT.toLocaleString("zh-CN")}
              </p>
            ) : null}
            {location ? (
              <SelectedLocationChip
                disabled={pending}
                location={location}
                onRemove={() => {
                  setLocation(null);
                  setLocationChanged(true);
                  dirtyRef.current = true;
                  setDirty(true);
                  setAnnouncement("位置已移除");
                  idempotencyKey.current = createIdempotencyKey();
                }}
              />
            ) : null}
            {quotedPost ? (
              <article className={styles.quoteComposerPreview}>
                <header>
                  <SiteAvatar site={site} />
                  <strong>{site.displayName}</strong>
                  <span>@{site.username}</span>
                  <button
                    aria-label="移除引用的 Moment"
                    disabled={pending}
                    onClick={() => {
                      setQuotedPost(null);
                      setError(null);
                      dirtyRef.current = true;
                      setDirty(true);
                      idempotencyKey.current = createIdempotencyKey();
                    }}
                    type="button"
                  >
                    <Icon name="close" />
                  </button>
                </header>
                <MarkdownContent
                  html={quotedPost.contentHtml}
                  mentionUsername={site.username}
                />
                {quotedPost.location ? (
                  <p className={styles.quoteComposerLocation}>
                    <Icon name="location" />
                    <span>{quotedPost.location.name}</span>
                  </p>
                ) : null}
                <ImageGrid media={quotedPost.media} />
              </article>
            ) : null}
            {quoteError ? (
              <p className={styles.formError}>
                无法载入要引用的 Moment，请返回后重试。
              </p>
            ) : null}
            <MediaComposer
              compact={variant === "inline"}
              disabled={pending}
              footerAction={
                <button
                  className={`${styles.primaryButton} ${styles.composerPublishButton}`}
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
              }
              items={mediaItems}
              onAdd={addMedia}
              onSelectionError={setMediaSelectionError}
              selectionError={mediaSelectionError}
              toolbarAction={
                <LocationPicker
                  api={api}
                  disabled={pending || !session}
                  onChange={(nextLocation) => {
                    allowLeaveRef.current = false;
                    setLocation(nextLocation);
                    setLocationChanged(true);
                    dirtyRef.current = true;
                    setDirty(true);
                    setError(null);
                    setAnnouncement(
                      nextLocation
                        ? `已添加位置：${nextLocation.name}`
                        : "位置已移除",
                    );
                    idempotencyKey.current = createIdempotencyKey();
                  }}
                  value={location}
                />
              }
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
                const restoredInitialMedia =
                  next.length === initialMedia.length &&
                  next.every((item, index) => {
                    const initial = initialMedia[index];
                    return (
                      initial !== undefined &&
                      item.mediaId === initial.mediaId &&
                      altValue(item) === initial.altText
                    );
                  });
                if (restoredInitialMedia && content === initialContent) {
                  dirtyRef.current = false;
                  setDirty(false);
                }
              }}
              onRetry={(key) => void retryUpload(key)}
            />
            {mediaItems.some((item) => item.altMode === "missing") ? (
              <p className={styles.srOnly} role="status">
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
            <p aria-live="polite" className={styles.srOnly}>
              {announcement}
            </p>
          </form>
          {!session ? <OwnerLogin embedded site={site} /> : null}
        </div>
        <Dialog.Root
          onOpenChange={(open) => {
            setLeavePromptOpen(open);
            if (!open) pendingNavigationRef.current = null;
          }}
          open={leavePromptOpen}
        >
          <Dialog.Portal>
            <Dialog.Backdrop className={styles.dialogBackdrop} />
            <Dialog.Viewport className={styles.dialogViewport}>
              <Dialog.Popup className={styles.dialogPopup}>
                <Dialog.Title className={styles.dialogTitle}>
                  {variant === "edit" ? "放弃未保存的更改？" : "退出发布？"}
                </Dialog.Title>
                <Dialog.Description className={styles.dialogDescription}>
                  {variant === "edit"
                    ? "离开后，本次编辑不会保存。"
                    : "文字可以保留为这台浏览器里的草稿；已添加的图片不会保存。"}
                </Dialog.Description>
                <div className={styles.dialogActions}>
                  <Dialog.Close className={styles.secondaryButton}>
                    继续编辑
                  </Dialog.Close>
                  {variant !== "edit" ? (
                    <button
                      className={styles.secondaryButton}
                      onClick={() => finishPendingNavigation(false)}
                      type="button"
                    >
                      保留草稿并退出
                    </button>
                  ) : null}
                  <button
                    className={styles.dangerButton}
                    onClick={() => finishPendingNavigation(true)}
                    type="button"
                  >
                    {variant === "edit" ? "放弃更改并离开" : "丢弃并退出"}
                  </button>
                </div>
              </Dialog.Popup>
            </Dialog.Viewport>
          </Dialog.Portal>
        </Dialog.Root>
      </section>
    );
  },
);
