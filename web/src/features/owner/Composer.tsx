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
  type PostVisibility,
  type PublicMomentPost,
  type PublicPost,
  type SiteIdentity,
} from "../../api/public";
import type { OwnerLocation } from "../../api/owner";
import { InternalLink, useNavigation } from "../../app/navigation";
import { ARTICLE_COMPOSE_PATH } from "../../app/routes";
import { notify } from "../../components/Feedback";
import { MarkdownBody } from "../../components/content/MarkdownBody";
import { MediaComposer } from "../media/MediaComposer";
import { ImageGrid } from "../media/ImageGrid";
import { SiteAvatar } from "../../components/SiteIdentity";
import { Icon } from "../../components/icons";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  altValue,
  applyUpload,
  isVideoMimeType,
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
import { AudienceSelector, audienceLabel } from "./AudienceSelector";
import { ReplyAudience } from "./ReplyAudience";
import { ComposerLinkCard } from "./ComposerLinkCard";
import { useLinkPreview } from "./useLinkPreview";
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
import { ComposeDrafts } from "./ComposeDrafts";
import {
  dropKeptDraft,
  dropKeptDraftByContent,
  keepDraft,
  listKeptDrafts,
} from "./drafts";
import { useSession } from "../auth/SessionProvider";
import { OwnerLogin } from "./OwnerLogin";
import { announceOwnerPost } from "./postEvents";
import styles from "./Owner.module.css";

const MAX_CONTENT = 20_000;
const DRAFT_REMOVAL_REPAIR_DELAY_MS = 50;

// Where a press is allowed to move the caret or start a selection: fields,
// links, and the notices whose text is worth copying. Everything else in the
// composer — buttons, the pill, blank space — is a press that leaves focus
// where it was.
const TYPED_INTO =
  'textarea, input, select, a, [contenteditable], [role="status"], [role="alert"]';

function isTypedInto(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest(TYPED_INTO) !== null;
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
  /**
   * Put a kept draft back in the editor, ready to finish. Passing its id
   * takes it out of 草稿: it is the live text now, and filing it again on
   * the way out replaces it rather than leaving a near-identical copy.
   */
  loadDraft: (contentMd: string, keptId?: string) => void;
}

interface ComposerProps {
  site: SiteIdentity;
  variant: "inline" | "page" | "edit";
  /**
   * The post being edited. A moment and nothing else: this editor writes
   * `content_md` back, which an article has no room for.
   */
  post?: PublicMomentPost;
  active?: boolean;
  quotedPostId?: string;
  /**
   * Carry the way into 草稿. The compose dialog puts it in its own header,
   * so the overlay turns this off; the compose screen -- the only compose
   * surface a phone gets -- carries its own.
   */
  draftsEntry?: boolean;
  /** Open that list straight away, because the address asked for it. */
  draftsListStart?: boolean;
}

export const Composer = forwardRef<ComposerHandle, ComposerProps>(
  function Composer(
    {
      site,
      variant,
      post,
      active = true,
      quotedPostId,
      draftsEntry = true,
      draftsListStart = false,
    },
    ref,
  ) {
    const { navigate, goBack, setNavigationBlocker } = useNavigation();
    const { api, session, expireIfInvalidSession } = useSession();
    const publicApi = useMemo(() => new PublicApi(), []);
    const initialContent = post?.contentMd ?? "";
    const initialVisibility: PostVisibility = post?.visibility ?? "everyone";
    const [content, setContent] = useState(initialContent);
    const [visibility, setVisibility] =
      useState<PostVisibility>(initialVisibility);
    // The inline composer opens on focus: the audience pill
    // and the toolbar rule appear when the owner steps in and go away again
    // when they step out with nothing written.
    //
    // Focus is the signal, and the settings cards are the things that can
    // hold the composer open without it: the card lives in a portal, and it
    // takes focus with it while it is up. Nothing else needs a hand, because
    // a press inside the composer never moves focus in the first place (see
    // the section's mousedown handler).
    const [focusWithin, setFocusWithin] = useState(false);
    const [audienceOpen, setAudienceOpen] = useState(false);
    const openSettingsRef = useRef(
      new Set<"audience" | "replies" | "location">(),
    );
    const audienceCloseFrameRef = useRef<number | null>(null);
    const [dirty, setDirty] = useState(false);
    const [pending, setPending] = useState(false);
    const [error, setError] = useState<string | null>(null);
    const [authExpired, setAuthExpired] = useState(false);
    const [draftUnavailable, setDraftUnavailable] = useState(
      variant !== "edit" && !site.instanceId,
    );
    const [activeDraft, setActiveDraft] = useState<ComposeDraft | null>(null);
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
    const linkPreview = useLinkPreview(api, content, !quotedPost);
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
    const drafting = variant === "page" && draftsEntry;
    const [draftsListOpen, setDraftsListOpen] = useState(draftsListStart);
    const [draftsEditing, setDraftsEditing] = useState(false);
    const [keptCount, setKeptCount] = useState(() =>
      drafting ? listKeptDrafts(site.instanceId).length : 0,
    );
    const pendingRef = useRef(false);
    const pendingNavigationRef = useRef<(() => void) | null>(null);
    const allowLeaveRef = useRef(false);
    const dirtyRef = useRef(false);
    const contentRef = useRef(content);
    contentRef.current = content;
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
    const visibilityChanged = visibility !== initialVisibility;
    const hasUnpublishedChange =
      dirty &&
      (content !== initialContent ||
        mediaChanged ||
        locationChanged ||
        visibilityChanged) &&
      !allowLeaveRef.current;
    const expanded =
      variant !== "inline" ||
      focusWithin ||
      audienceOpen ||
      content.trim().length > 0 ||
      mediaItems.length > 0 ||
      location !== null ||
      quotedPost !== null ||
      linkPreview.card !== null;

    const onSettingsOpenChange = (
      setting: "audience" | "replies" | "location",
      open: boolean,
    ) => {
      if (audienceCloseFrameRef.current !== null) {
        window.cancelAnimationFrame(audienceCloseFrameRef.current);
        audienceCloseFrameRef.current = null;
      }
      if (open) {
        openSettingsRef.current.add(setting);
        setAudienceOpen(true);
      }
    };

    const onSettingsOpenChangeComplete = (
      setting: "audience" | "replies" | "location",
      open: boolean,
    ) => {
      if (open) return;
      openSettingsRef.current.delete(setting);
      if (openSettingsRef.current.size > 0) return;
      // Base UI returns focus when the exit transition finishes. Keep the
      // empty editor expanded until then so its trigger never becomes inert
      // while the card is still returning focus.
      audienceCloseFrameRef.current = window.requestAnimationFrame(() => {
        audienceCloseFrameRef.current = null;
        if (mountedRef.current && openSettingsRef.current.size === 0) {
          setAudienceOpen(false);
        }
      });
    };

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

    // Text that survived in the recovery slot -- a reload, a crash, a tab
    // closed mid-sentence -- goes straight into 草稿 rather than being
    // offered back as a notice above the composer. The notice was a second
    // place drafts lived, and it sat across the composer's own layout.
    const filedStrandedDraft = useRef(false);
    useEffect(() => {
      // `site.instanceId` arrives with /api/v1/site, a render after the
      // first: claiming the guard before it lands would skip the only
      // pass that can read anything.
      if (variant === "edit" || filedStrandedDraft.current || !site.instanceId)
        return;
      filedStrandedDraft.current = true;
      const stranded = loadComposeDraft(site.instanceId);
      if (!stranded) return;
      // Already filed: keep its place in the list rather than refiling it
      // and making an old draft look like it was just written.
      const filed = listKeptDrafts(site.instanceId).some(
        (kept) => kept.fingerprint === stranded.fingerprint,
      );
      if (!filed) keepDraft(site.instanceId, stranded.contentMd);
      clearComposeDraft(site.instanceId, {
        reason: "discarded",
        draft: stranded,
      });
    }, [site.instanceId, variant]);

    // Putting a kept draft back in the editor. Reached from the 草稿 list
    // on this surface, and from the compose dialog's list through the ref.
    const restoreDraftText = useCallback(
      (contentMd: string, keptId?: string) => {
        // Whatever is in the editor right now is not thrown away for it.
        const current = contentRef.current;
        if (current.trim() && current !== contentMd) {
          keepDraft(site.instanceId, current);
        }
        // The draft leaves 草稿 as it enters the editor: it is the live
        // text now, and filing it again on the way out replaces it rather
        // than adding a second, near-identical copy.
        if (keptId) dropKeptDraft(site.instanceId, keptId);
        const restored = createComposeDraft(
          contentMd,
          activeDraftRef.current?.savedAt,
        );
        activeDraftRef.current = restored;
        setActiveDraft(restored);
        saveComposeDraft(site.instanceId, restored);
        setContent(contentMd);
        dirtyRef.current = true;
        setDirty(true);
        allowLeaveRef.current = false;
        setAnnouncement("已载入草稿；图片需要重新选择。");
        window.requestAnimationFrame(() => textareaRef.current?.focus());
      },
      [site.instanceId],
    );

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
          // Keeping is a decision, so it also files a copy under 草稿 --
          // the recovery slot only ever holds the most recent text, and a
          // second keep would otherwise erase the first.
          keepDraft(site.instanceId, content);
        },
        leaveAndDiscard: () => {
          allowLeaveRef.current = true;
          const current = activeDraftRef.current;
          if (variant !== "edit" && current) {
            clearComposeDraft(site.instanceId, {
              reason: "discarded",
              draft: current,
            });
            dropKeptDraftByContent(site.instanceId, current.contentMd);
          }
          clearMedia();
          setLocation(initialLocation);
          setLocationChanged(false);
          setVisibility(initialVisibility);
        },
        loadDraft: restoreDraftText,
      }),
      [
        clearMedia,
        content,
        hasUnpublishedChange,
        initialLocation,
        initialVisibility,
        restoreDraftText,
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
          isVideoMimeType(current.mimeType)
            ? (processingPosterUrl) => {
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
              }
            : undefined,
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
        if (audienceCloseFrameRef.current !== null) {
          window.cancelAnimationFrame(audienceCloseFrameRef.current);
        }
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
      if (active || variant !== "inline") return;
      setVisibility("everyone");
    }, [active, variant]);

    useEffect(() => {
      const instanceId = site.instanceId;
      if (variant === "edit" || !instanceId) return;
      const resetDraftSurface = () => {
        allowLeaveRef.current = true;
        activeDraftRef.current = null;
        setActiveDraft(null);
        setContent("");
        dirtyRef.current = false;
        setDirty(false);
        setError(null);
        clearMedia();
        setLocation(null);
        setLocationChanged(false);
        setVisibility("everyone");
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
    }, [activeDraft, dirty, site.instanceId, variant]);

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
            if (variant === "page") {
              dropKeptDraftByContent(site.instanceId, currentDraft.contentMd);
            }
          }
          activeDraftRef.current = null;
          setActiveDraft(null);
          setContent("");
          clearMedia();
          setLocation(null);
          setLocationChanged(false);
          setVisibility("everyone");
          dirtyRef.current = false;
          setDirty(false);
        } else if (currentDraft) {
          saveComposeDraft(site.instanceId, currentDraft);
          // Keeping files a copy under 草稿, because the recovery slot only
          // ever holds the most recent text and the next keep would erase
          // this one.
          //
          // The compose screen only. The inline box asks this same question
          // when it hands off to the full composer -- pressing n over the
          // timeline routes to /compose through the same blocker --
          // and that is not a decision to shelve anything, it is the same
          // sentence continuing on a bigger surface.
          if (variant === "page") {
            keepDraft(site.instanceId, currentDraft.contentMd);
          }
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
              linkPreview.changed
                ? linkPreview.card
                  ? [linkPreview.card]
                  : []
                : undefined,
              visibilityChanged ? visibility : undefined,
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
                linkPreview.card ? [linkPreview.card] : undefined,
                visibility,
              );
        allowLeaveRef.current = true;
        dirtyRef.current = false;
        setDirty(false);
        if (!post && submittedDraft) {
          clearComposeDraft(site.instanceId, {
            reason: "published",
            draft: submittedDraft,
          });
          dropKeptDraftByContent(site.instanceId, submittedDraft.contentMd);
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
          setVisibility("everyone");
          idempotencyKey.current = createIdempotencyKey();
          window.setTimeout(() => {
            allowLeaveRef.current = false;
            setAnnouncement("");
          }, 1_000);
        } else {
          // A page composer is a detour from wherever it was opened -- the
          // post being edited, or the page under the compose overlay -- so
          // saving returns there. Pushing a new page instead left the
          // detour in history, and the next back went to the editor rather
          // than to where the reader had been. Opened by URL, with no page
          // behind it, the detour ends on the post it produced.
          goBack(post ? `/m/${encodeURIComponent(saved.id)}` : "/");
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

    useEffect(() => {
      if (!drafting) return;
      setKeptCount(listKeptDrafts(site.instanceId).length);
    }, [drafting, draftsEditing, draftsListOpen, site.instanceId]);

    return (
      <section
        aria-label={post ? "编辑 Moment" : "发布 Moment"}
        className={`${styles.composer} ${variant === "inline" ? styles.inlineComposer : styles.pageComposer} ${mediaItems.length > 0 ? styles.composerHasMedia : ""}`}
        data-expanded={expanded ? "true" : "false"}
        onBlur={(event) => {
          const section = event.currentTarget;
          if (section.contains(event.relatedTarget)) return;
          // A blur with no destination is not always focus leaving: a window
          // or tab going to the background blurs the field too, and leaves
          // document.activeElement on it, ready to be focused again when the
          // window comes back. Reading where focus settled, a tick later,
          // tells the two apart — so the composer only lets go for a press
          // or a Tab that actually took focus elsewhere on the page, and a
          // switch away and back is not answered with a shut and a reopen.
          window.setTimeout(() => {
            if (!mountedRef.current || section.contains(document.activeElement))
              return;
            setFocusWithin(false);
          }, 0);
        }}
        onFocus={() => setFocusWithin(true)}
        onMouseDown={(event) => {
          // A press inside the composer that is not on something the owner
          // types into keeps the caret exactly where it is. Pressing the
          // pill, a toolbar button or blank space would otherwise move focus
          // — Safari blurs the textarea for a button, every browser does for
          // blank space — and an empty composer would shut on that blur under
          // the very click meant for it. The click itself still fires, so
          // buttons work as before; they just no longer take focus on click,
          // which is what Safari has always done. Text the owner may want to
          // select is left alone, and so are fields and links.
          if (!isTypedInto(event.target)) event.preventDefault();
        }}
      >
        {/* The compose screen's own way into 草稿. The dialog keeps its own
            in the header, but a phone never gets the dialog, and filing
            text you cannot read back is a trap. Spans both columns so the
            avatar stays level with the audience pill under it. */}
        {variant === "page" ? (
          <div className={styles.pageDrafts}>
            {/* Articles start inside composing and land on the same timeline. */}
            <InternalLink
              className={styles.pageArticleLink}
              href={ARTICLE_COMPOSE_PATH}
            >
              <Icon name="edit" />
              写文章
            </InternalLink>
            {drafting && draftsListOpen && keptCount > 0 ? (
              <button
                className={styles.pageDraftsEdit}
                onClick={() => setDraftsEditing((on) => !on)}
                type="button"
              >
                {draftsEditing ? "完成" : "编辑"}
              </button>
            ) : null}
            {drafting && (keptCount > 0 || draftsListOpen) ? (
              <button
                aria-expanded={draftsListOpen}
                className={styles.pageDraftsToggle}
                onClick={() => {
                  setDraftsEditing(false);
                  setDraftsListOpen((open) => !open);
                }}
                type="button"
              >
                草稿
                {keptCount > 0 ? <small>{keptCount}</small> : null}
                <Icon name="chevron-down" />
              </button>
            ) : null}
          </div>
        ) : null}
        {drafting && draftsListOpen ? (
          <div className={styles.pageDraftsList}>
            <ComposeDrafts
              editing={draftsEditing}
              instanceId={site.instanceId}
              onEditingChange={setDraftsEditing}
              onOpenDraft={(kept) => {
                restoreDraftText(kept.contentMd, kept.id);
                setDraftsListOpen(false);
              }}
            />
          </div>
        ) : null}
        <div aria-hidden="true" className={styles.newTick}>
          <SiteAvatar site={site} />
        </div>
        <div className={styles.composerMain}>
          <form
            className={styles.composerForm}
            onSubmit={(event) => void submit(event)}
          >
            {/* Always mounted so the row can animate shut and so a press on
                the pill is never answered by removing the pill. Collapsed, it
                is hidden from assistive tech and unreachable by keyboard. */}
            <div
              aria-hidden={expanded ? undefined : true}
              className={`${styles.audienceRow} ${expanded ? "" : styles.audienceRowCollapsed}`}
              inert={!expanded}
            >
              <div>
                <AudienceSelector
                  disabled={pending}
                  onChange={(next) => {
                    if (next === visibility) return;
                    allowLeaveRef.current = false;
                    setVisibility(next);
                    setError(null);
                    setAnnouncement(`受众已设为${audienceLabel(next)}`);
                    idempotencyKey.current = createIdempotencyKey();
                    if (variant === "edit") {
                      dirtyRef.current = true;
                      setDirty(true);
                    }
                  }}
                  onOpenChange={(open) =>
                    onSettingsOpenChange("audience", open)
                  }
                  onOpenChangeComplete={(open) =>
                    onSettingsOpenChangeComplete("audience", open)
                  }
                  value={visibility}
                />
              </div>
            </div>
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
            {linkPreview.status === "loading" ? (
              <p className={styles.linkPreviewPending} role="status">
                正在获取链接预览…
              </p>
            ) : null}
            {linkPreview.card ? (
              <ComposerLinkCard
                card={linkPreview.card}
                disabled={pending}
                onRemove={() => {
                  linkPreview.dismiss();
                  setAnnouncement("链接卡片已移除");
                }}
              />
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
                <MarkdownBody html={quotedPost.contentHtml} />
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
            {variant === "inline" ? (
              <div
                aria-hidden={
                  !expanded || visibility !== "everyone" ? true : undefined
                }
                className={`${styles.replyAudienceRow} ${expanded && visibility === "everyone" ? "" : styles.replyAudienceRowCollapsed}`}
                inert={!expanded || visibility !== "everyone"}
              >
                <div>
                  <ReplyAudience
                    disabled={pending}
                    onOpenChange={(open) =>
                      onSettingsOpenChange("replies", open)
                    }
                    onOpenChangeComplete={(open) =>
                      onSettingsOpenChangeComplete("replies", open)
                    }
                  />
                </div>
              </div>
            ) : null}
            <MediaComposer
              compact={variant === "inline"}
              className={variant === "inline" ? styles.inlineMedia : undefined}
              disabled={pending}
              divider={variant === "inline" && expanded}
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
                  onOpenChange={(open) =>
                    onSettingsOpenChange("location", open)
                  }
                  onOpenChangeComplete={(open) =>
                    onSettingsOpenChangeComplete("location", open)
                  }
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
