import { Menu } from "@base-ui/react/menu";
import type { EditorEvents } from "@tiptap/core";
import { useEditor } from "@tiptap/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type ArticleDocument,
  type PostVisibility,
  type SiteIdentity,
} from "../../../api/public";
import { useNavigation } from "../../../app/navigation";
import { notify } from "../../../components/Feedback";
import { Icon } from "../../../components/icons";
import { mediaPreviewUrl } from "../../../api/mediaUrls";
import { SiteAvatar, siteDisplayName } from "../../../components/SiteIdentity";
import { AudienceSelector } from "../../owner/AudienceSelector";
import { useOwner } from "../../owner/OwnerProvider";
import { ConfirmDialog } from "../../owner/ConfirmDialog";
import {
  ArticleDraftApi,
  MAX_ARTICLE_TITLE_CODE_POINTS,
  articleDraftErrorMessage,
  isDraftMissingError,
  isStaleDraftError,
  type ArticleDraft,
  type ArticleDraftInput,
} from "../articleDrafts";
import { ARTICLE_COMPOSE_PATH, articleDraftPath } from "../../../app/routes";
import { autosaveDelay, saveStatusLabel, type SaveState } from "./autosave";
import { ConflictNotice } from "./components/ConflictNotice";
import { CoverField } from "./components/CoverField";
import { DraftRail } from "./components/DraftRail";
type UploadTask = { id: string; failed: string | null };
import { CharacterCount } from "./components/CharacterCount";
import {
  emptyArticleDocument,
  hasArticleSubstance,
  toArticleDocument,
  toEditorContent,
} from "./document";
import { articleEditorExtensions } from "./schema";
import styles from "./Editor.module.css";
import { ArticleSheet } from "../../../components/content/ArticleSheet";
import { RichContent } from "../../../components/content/RichContent";
import { MentionProvider } from "../../../app/mentions";
import { NotionSurface, NotionFormatToolbar } from "./NotionSurface";

function createIdempotencyKey(): string {
  if (typeof crypto.randomUUID === "function") {
    return `web-article-${crypto.randomUUID()}`;
  }
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return `web-article-${Array.from(bytes, (value) => value.toString(16).padStart(2, "0")).join("")}`;
}

/** Code points, not UTF-16 units: the server counts the title the same way. */
function codePoints(value: string): number {
  return Array.from(value).length;
}

/** Full-page writing surface; persistence stays separate from the shared paper. */
export function ArticleEditor({
  site,
  draftId,
}: {
  site: SiteIdentity;
  draftId: string | null;
}) {
  const owner = useOwner();
  const location = useNavigation();
  const api = useMemo(() => new ArticleDraftApi(), []);
  const publicApi = useMemo(() => new PublicApi(), []);
  const csrfToken = owner.session?.csrfToken ?? "";

  const [phase, setPhase] = useState<"loading" | "ready" | "gone">(
    draftId === null ? "ready" : "loading",
  );
  const [loadError, setLoadError] = useState<string | null>(null);
  const [draft, setDraft] = useState<ArticleDraft | null>(null);
  const [title, setTitle] = useState("");
  const [coverMediaId, setCoverMediaId] = useState<string | null>(null);
  const [visibility, setVisibility] = useState<PostVisibility>("everyone");
  const [saveState, setSaveState] = useState<SaveState>({ kind: "idle" });
  const [uploads, setUploads] = useState<readonly UploadTask[]>([]);
  const [coverBusy, setCoverBusy] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [discarding, setDiscarding] = useState(false);
  const [resolving, setResolving] = useState(false);
  const [previewing, setPreviewing] = useState(false);
  const [railOpen, setRailOpen] = useState(false);
  const [formatOpen, setFormatOpen] = useState(false);
  const [drafts, setDrafts] = useState<readonly ArticleDraft[]>([]);
  const [railError, setRailError] = useState<string | null>(null);
  const [railLoading, setRailLoading] = useState(true);
  const [now, setNow] = useState(() => new Date());

  // The clocks the autosave runs on. Refs and not state: a keystroke must not
  // re-render the page, only reschedule the save.
  const firstChangeAt = useRef(0);
  const lastChangeAt = useRef(0);
  const [changeTick, setChangeTick] = useState(0);
  const saving = useRef<Promise<ArticleDraft | null> | null>(null);
  const draftRef = useRef<ArticleDraft | null>(null);
  const revision = useRef(0);
  const conflicted = useRef(false);
  const saveLatest = useRef<
    (reason: "auto" | "force") => Promise<ArticleDraft | null>
  >(async () => null);
  const blockerId = useRef(Symbol("article-editor"));
  const leaving = useRef(false);
  const allowLeave = useRef(false);
  const titleInput = useRef<HTMLTextAreaElement>(null);
  const topBar = useRef<HTMLElement>(null);
  const libraryButton = useRef<HTMLButtonElement>(null);
  const libraryPanel = useRef<HTMLDivElement>(null);
  const coverInput = useRef<HTMLInputElement>(null);
  const publishKey = useRef(createIdempotencyKey());
  const uploadGeneration = useRef(0);
  const createdDraftId = useRef<string | null>(null);

  const uploadOwner = useRef(owner);
  uploadOwner.current = owner;
  const extensions = useMemo(
    () =>
      articleEditorExtensions({
        upload: async (file, onProgress, signal) => {
          const current = uploadOwner.current;
          const token = current.session?.csrfToken;
          if (!token) throw new Error("登录状态已经失效。");
          const id = createIdempotencyKey();
          setUploads((tasks) => [...tasks, { id, failed: null }]);
          try {
            const media = await current.api.uploadMedia(
              token,
              file,
              (percent) => onProgress?.({ progress: percent }),
              signal,
            );
            return mediaPreviewUrl(media.id);
          } catch (error) {
            current.expireIfInvalidSession(error);
            throw error;
          } finally {
            setUploads((tasks) => tasks.filter((task) => task.id !== id));
          }
        },
      }),
    [],
  );
  const editor = useEditor({
    extensions,
    shouldRerenderOnTransaction: false,
    // Built in an effect, not during the render that asks for it.
    //
    // useEditor's default builds the editor while rendering and starts a 1ms
    // timer that destroys it unless the component has mounted by then. This
    // page arrives through React.lazy, so the gap between render and commit is
    // a chunk download: the timer wins, the editor is destroyed, and the
    // effects below run holding the corpse. Deferring creation to the effect
    // means the first render sees null -- which they already handle -- and the
    // second sees a live editor.
    immediatelyRender: false,
    editorProps: {
      attributes: {
        "aria-label": "文章正文",
        class: "tiptap ProseMirror notion-like-editor",
        role: "textbox",
        "aria-multiline": "true",
      },
    },
  });

  const markChanged = useCallback(() => {
    revision.current += 1;
    const at = Date.now();
    if (firstChangeAt.current === 0) firstChangeAt.current = at;
    lastChangeAt.current = at;
    setChangeTick((tick) => tick + 1);
    setSaveState((current) =>
      // A conflict is a question the owner has not answered yet. Typing on is
      // allowed; quietly resuming the saves that would overwrite the other
      // copy is not.
      current.kind === "conflict" ? current : { kind: "dirty" },
    );
  }, []);

  useEffect(() => {
    // A destroyed editor still answers to a stale closure, and every call on
    // it throws. Tiptap guards its own chain() for this; the rest is ours.
    if (!editor || editor.isDestroyed) return;
    const onUpdate = ({ transaction }: EditorEvents["update"]) => {
      // UniqueID/TOC metadata initialization is not a user edit.
      if (transaction.getMeta("addToHistory") === false) return;
      markChanged();
    };
    editor.on("update", onUpdate);
    return () => {
      editor.off("update", onUpdate);
    };
  }, [editor, markChanged]);

  /** Load the draft named in the address, or start an empty one. */
  useEffect(() => {
    if (!editor || editor.isDestroyed) return;
    if (draftId !== null && draftId === createdDraftId.current) {
      createdDraftId.current = null;
      return;
    }
    uploadGeneration.current += 1;
    draftRef.current = null;
    conflicted.current = false;
    allowLeave.current = false;
    setVisibility("everyone");
    setLoadError(null);
    publishKey.current = createIdempotencyKey();
    setUploads([]);
    setCoverBusy(false);
    let abandoned = false;
    const controller = new AbortController();
    if (draftId === null) {
      setDraft(null);
      setTitle("");
      setCoverMediaId(null);
      setSaveState({ kind: "idle" });
      firstChangeAt.current = 0;
      editor.commands.setContent(toEditorContent(emptyArticleDocument()), {
        emitUpdate: false,
      });
      setPhase("ready");
      return;
    }
    setPhase("loading");
    setLoadError(null);
    void (async () => {
      try {
        const loaded = await api.read(draftId, controller.signal);
        if (abandoned) return;
        if (loaded.publishedEntryId !== null) {
          const published = await publicApi.post(
            loaded.publishedEntryId,
            controller.signal,
          );
          if (abandoned) return;
          setVisibility(published.visibility);
        }
        draftRef.current = loaded;
        setDraft(loaded);
        setTitle(loaded.title);
        setCoverMediaId(loaded.coverMediaId);
        setSaveState({ kind: "saved", at: loaded.updatedAt });
        firstChangeAt.current = 0;
        // Loading is not an edit, so the update that would mark the draft
        // dirty -- and schedule a save of what was just read -- is suppressed.
        editor.commands.setContent(toEditorContent(loaded.body), {
          emitUpdate: false,
        });
        setPhase("ready");
      } catch (error) {
        if (abandoned || controller.signal.aborted) return;
        if (isDraftMissingError(error)) {
          setPhase("gone");
          return;
        }
        owner.expireIfInvalidSession(error);
        setLoadError(articleDraftErrorMessage(error));
        setPhase("ready");
      }
    })();
    return () => {
      abandoned = true;
      controller.abort();
    };
    // `owner` is deliberately not a dependency: it is read for its
    // session-expiry hook only, and re-running this on every session tick
    // would reload the draft over what is being typed.
  }, [api, publicApi, draftId, editor]);

  const refreshDrafts = useCallback(async () => {
    setRailLoading(true);
    try {
      const listed = await api.list();
      setDrafts(listed);
      setRailError(null);
    } catch (error) {
      owner.expireIfInvalidSession(error);
      setRailError(articleDraftErrorMessage(error));
    } finally {
      setRailLoading(false);
    }
  }, [api]);

  useEffect(() => {
    void refreshDrafts();
  }, [refreshDrafts]);

  // Read mode uses the publication renderer; the editor retains its selection.
  useEffect(() => {
    // Without the second argument setEditable emits an "update", and "update"
    // is what marks the draft dirty -- so merely opening the page, or looking
    // at the preview and coming back, would report unsaved changes nobody
    // made. Whether the sheet can be typed on is not a change to what it says.
    editor?.setEditable(
      !previewing &&
        !publishing &&
        !discarding &&
        phase === "ready" &&
        loadError === null,
      false,
    );
  }, [editor, previewing, publishing, discarding, phase, loadError]);

  /** The status line counts in minutes, so it has to be told the time. */
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  const currentInput = useCallback((): ArticleDraftInput | null => {
    if (!editor || editor.isDestroyed) return null;
    let body: ArticleDocument;
    try {
      body = toArticleDocument(editor.getJSON());
    } catch {
      return null;
    }
    return { title, body, coverMediaId };
  }, [coverMediaId, editor, title]);

  const save = useCallback(
    async (reason: "auto" | "force"): Promise<ArticleDraft | null> => {
      if (conflicted.current) return null;
      if (saving.current) {
        const stored = await saving.current;
        return stored && reason === "force"
          ? saveLatest.current(reason)
          : stored;
      }
      if (firstChangeAt.current === 0 && draftRef.current)
        return draftRef.current;
      const input = currentInput();
      if (input === null) {
        setSaveState({ kind: "failed", message: "正文里有存不下的内容。" });
        return null;
      }
      if (csrfToken === "") {
        setSaveState({ kind: "failed", message: "登录状态已经失效。" });
        return null;
      }
      const savedThrough = revision.current;
      setSaveState({ kind: "saving" });
      const attempt = async (): Promise<ArticleDraft | null> => {
        try {
          const current = draftRef.current;
          const stored =
            current === null
              ? await api.create(csrfToken, input)
              : await api.replace(
                  csrfToken,
                  current.id,
                  input,
                  current.updatedAt,
                );
          draftRef.current = stored;
          setDraft(stored);
          setSaveState({ kind: "saved", at: stored.updatedAt });
          // Only the work that went into this request is saved. Anything typed
          // while it was in flight is still unsaved, and the ceiling restarts
          // from when that happened rather than from now.
          if (revision.current === savedThrough) {
            firstChangeAt.current = 0;
          } else {
            firstChangeAt.current = lastChangeAt.current;
            setSaveState({ kind: "dirty" });
            setChangeTick((tick) => tick + 1);
          }
          if (current === null) {
            // The address follows the draft rather than the other way round, so
            // a reload comes back to what was just written. Replaced, not
            // pushed: nobody navigated anywhere.
            createdDraftId.current = stored.id;
            location.replace(articleDraftPath(stored.id));
          }
          void refreshDrafts();
          return stored;
        } catch (error) {
          if (isStaleDraftError(error)) {
            conflicted.current = true;
            setSaveState({
              kind: "conflict",
              serverUpdatedAt:
                error instanceof PublicApiError &&
                typeof error.details.updated_at === "string"
                  ? error.details.updated_at
                  : null,
            });
            return null;
          }
          owner.expireIfInvalidSession(error);
          const message = articleDraftErrorMessage(error);
          setSaveState({ kind: "failed", message });
          if (reason === "force") notify(message, { type: "error" });
          return null;
        }
      };
      saving.current = attempt();
      try {
        const stored = await saving.current;
        return stored;
      } finally {
        saving.current = null;
      }
    },
    [api, csrfToken, currentInput, location, refreshDrafts],
  );

  useEffect(() => {
    saveLatest.current = save;
  }, [save]);

  // Internal navigation saves first. Failed/conflicted saves keep the page open.
  useEffect(() => {
    const id = blockerId.current;
    location.setNavigationBlocker(id, (resume) => {
      if (allowLeave.current) return true;
      if (discarding || publishing) return false;
      if (coverBusy || uploads.some((task) => task.failed === null)) {
        notify("图片还在上传，请稍候再离开。", { type: "error" });
        return false;
      }
      if (conflicted.current) {
        notify("请先选择要保留哪一份草稿。", { type: "error" });
        return false;
      }
      if (firstChangeAt.current === 0 && !saving.current) return true;
      if (!leaving.current) {
        leaving.current = true;
        void saveLatest
          .current("force")
          .then(async (stored) => {
            if (!stored) return;
            while (firstChangeAt.current !== 0) {
              if (!(await saveLatest.current("force"))) return;
            }
            allowLeave.current = true;
            resume();
          })
          .finally(() => {
            leaving.current = false;
          });
      }
      return false;
    });
    return () => location.setNavigationBlocker(id, null);
  }, [
    location.setNavigationBlocker,
    coverBusy,
    uploads,
    discarding,
    publishing,
  ]);

  useEffect(() => {
    if (!railOpen) return;
    const dismiss = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      setRailOpen(false);
      libraryButton.current?.focus();
    };
    const outside = (event: PointerEvent) => {
      const target = event.target as Node;
      if (
        !libraryPanel.current?.contains(target) &&
        !libraryButton.current?.contains(target)
      )
        setRailOpen(false);
    };
    document.addEventListener("keydown", dismiss);
    document.addEventListener("pointerdown", outside);
    return () => {
      document.removeEventListener("keydown", dismiss);
      document.removeEventListener("pointerdown", outside);
    };
  }, [railOpen]);

  useEffect(() => {
    const bar = topBar.current;
    if (!bar) return;
    const measure = () =>
      bar.parentElement?.style.setProperty(
        "--editor-toolbar-top",
        `${bar.getBoundingClientRect().height}px`,
      );
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(bar);
    return () => observer.disconnect();
  }, [phase]);

  // Keep a long title visible at narrow widths as well as after a resize.
  useEffect(() => {
    const field = titleInput.current;
    if (!field) return;
    const resize = () => {
      field.style.height = "auto";
      field.style.height = `${field.scrollHeight}px`;
    };
    resize();
    let width = field.clientWidth;
    const observer = new ResizeObserver(() => {
      if (field.clientWidth === width) return;
      width = field.clientWidth;
      resize();
    });
    observer.observe(field);
    return () => observer.disconnect();
  }, [title, previewing]);

  /** Debounced: 2s idle, and never more than 15s after the first change. */
  useEffect(() => {
    if (phase !== "ready") return;
    if (saveState.kind !== "dirty") return;
    if (firstChangeAt.current === 0) return;
    const delay = autosaveDelay(
      firstChangeAt.current,
      lastChangeAt.current,
      Date.now(),
    );
    const timer = window.setTimeout(() => void save("auto"), delay);
    return () => window.clearTimeout(timer);
  }, [changeTick, phase, save, saveState.kind]);

  /** A tab closed mid-sentence should say so rather than lose the sentence. */
  useEffect(() => {
    const risky =
      saveState.kind === "dirty" ||
      saveState.kind === "saving" ||
      saveState.kind === "conflict" ||
      saveState.kind === "failed" ||
      coverBusy ||
      uploads.some((task) => task.failed === null);
    if (!risky) return;
    const warn = (event: BeforeUnloadEvent) => event.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => window.removeEventListener("beforeunload", warn);
  }, [saveState.kind, coverBusy, uploads]);

  const uploadCover = useCallback(
    async (file: File) => {
      if (!csrfToken) {
        notify("登录状态已经失效。", { type: "error" });
        return;
      }
      const generation = uploadGeneration.current;
      setCoverBusy(true);
      try {
        const uploaded = await owner.api.uploadMedia(csrfToken, file);
        if (generation !== uploadGeneration.current) return;
        setCoverMediaId(uploaded.id);
        markChanged();
      } catch (error) {
        owner.expireIfInvalidSession(error);
        notify(
          error instanceof PublicApiError
            ? error.message
            : "封面上传没有成功。",
          { type: "error" },
        );
      } finally {
        if (generation === uploadGeneration.current) setCoverBusy(false);
      }
    },
    [csrfToken, owner, markChanged],
  );

  const publish = useCallback(async () => {
    if (publishing || conflicted.current) return;
    const trimmed = title.trim();
    if (trimmed === "") {
      notify("文章要有标题才能发布。", { type: "error" });
      return;
    }
    const input = currentInput();
    if (input === null || !hasArticleSubstance(input.body)) {
      notify("正文还没有内容。", { type: "error" });
      return;
    }
    setPublishing(true);
    try {
      // Saved first, so the article published is the article on screen: the
      // publish endpoint reads the stored draft and not this browser's copy.
      const stored =
        draft === null || saveState.kind !== "saved"
          ? await save("force")
          : draft;
      if (stored === null) return;
      const post = await api.publish(
        csrfToken,
        stored.id,
        publishKey.current,
        visibility,
      );
      notify("文章已发布。");
      allowLeave.current = true;
      location.replace(`/m/${encodeURIComponent(post.id)}`);
    } catch (error) {
      owner.expireIfInvalidSession(error);
      notify(articleDraftErrorMessage(error), { type: "error" });
      // A refused publish is a new attempt, not a replay of the refused one.
      publishKey.current = createIdempotencyKey();
    } finally {
      setPublishing(false);
    }
  }, [
    api,
    csrfToken,
    currentInput,
    draft,
    location,
    publishing,
    save,
    saveState.kind,
    title,
    visibility,
  ]);

  const discardCurrent = useCallback(async () => {
    if (draft === null) {
      firstChangeAt.current = 0;
      setTitle("");
      setCoverMediaId(null);
      setSaveState({ kind: "idle" });
      editor?.commands.setContent(toEditorContent(emptyArticleDocument()), {
        emitUpdate: false,
      });
      location.replace(ARTICLE_COMPOSE_PATH);
      return;
    }
    if (csrfToken === "") return;
    if (saving.current) await saving.current;
    const unsavedSince = firstChangeAt.current;
    firstChangeAt.current = 0;
    setSaveState({ kind: "idle" });
    setDiscarding(true);
    try {
      await api.discard(csrfToken, draft.id);
      notify("草稿已删除。");
      void refreshDrafts();
      location.replace(ARTICLE_COMPOSE_PATH);
    } catch (error) {
      firstChangeAt.current = unsavedSince;
      setSaveState(saveState);
      owner.expireIfInvalidSession(error);
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      setDiscarding(false);
    }
  }, [api, csrfToken, draft, editor, location, refreshDrafts, saveState]);

  const takeServerCopy = useCallback(async () => {
    if (draft === null || !editor || editor.isDestroyed) return;
    setResolving(true);
    try {
      const stored = await api.read(draft.id);
      draftRef.current = stored;
      conflicted.current = false;
      setDraft(stored);
      setTitle(stored.title);
      setCoverMediaId(stored.coverMediaId);
      editor.commands.setContent(toEditorContent(stored.body), {
        emitUpdate: false,
      });
      firstChangeAt.current = 0;
      setSaveState({ kind: "saved", at: stored.updatedAt });
    } catch (error) {
      owner.expireIfInvalidSession(error);
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      setResolving(false);
    }
  }, [api, draft, editor]);

  const keepMyCopy = useCallback(async () => {
    if (draft === null) return;
    setResolving(true);
    try {
      // Read only for its revision. The body stays the one on screen; what is
      // taken from the server is permission to write over it.
      const stored = await api.read(draft.id);
      draftRef.current = { ...draft, updatedAt: stored.updatedAt };
      conflicted.current = false;
      setDraft(draftRef.current);
      firstChangeAt.current = Date.now();
      lastChangeAt.current = Date.now();
      setSaveState({ kind: "dirty" });
      setChangeTick((tick) => tick + 1);
    } catch (error) {
      owner.expireIfInvalidSession(error);
      notify(articleDraftErrorMessage(error), { type: "error" });
    } finally {
      setResolving(false);
    }
  }, [api, draft]);

  const previewDocument = useMemo(() => {
    // A clock/save-status update must not replace selected reading text.
    void changeTick;
    void draft;
    return previewing && editor ? toArticleDocument(editor.getJSON()) : null;
  }, [previewing, editor, changeTick, draft]);

  const titleLength = codePoints(title);
  const titleTooLong = titleLength > MAX_ARTICLE_TITLE_CODE_POINTS;
  const statusLine = saveStatusLabel(saveState, site.timezone, now);

  if (phase === "gone") {
    return (
      <div className={styles.page}>
        <div className={styles.pageError} role="alert">
          <p>这份草稿已经不在了。</p>
          <button
            className={styles.secondaryAction}
            onClick={() => location.replace(ARTICLE_COMPOSE_PATH)}
            type="button"
          >
            写一篇新的
          </button>
        </div>
      </div>
    );
  }

  return (
    <MentionProvider known={[site.username]}>
      <div className={styles.page}>
        <header className={styles.topBar} ref={topBar}>
          <button
            aria-label="返回时间流"
            className={styles.toolButton}
            onClick={() => location.goBack("/")}
            type="button"
          >
            <Icon name="back" />
          </button>
          <button
            aria-expanded={railOpen}
            ref={libraryButton}
            aria-controls="article-library"
            className={styles.toolButton}
            onClick={() => setRailOpen((value) => !value)}
            type="button"
          >
            文章
          </button>
          <p
            className={`${styles.status} ${
              saveState.kind === "failed" || saveState.kind === "conflict"
                ? styles.statusAlert
                : ""
            }`}
            role="status"
          >
            {statusLine}
          </p>
          <div className={styles.topActions}>
            {!previewing && (
              <button
                aria-pressed={formatOpen}
                aria-label="格式工具"
                className={styles.toolButton}
                onClick={() => setFormatOpen((value) => !value)}
                type="button"
              >
                Aa
              </button>
            )}
            <span className={styles.audience}>
              <AudienceSelector
                disabled={
                  publishing ||
                  discarding ||
                  phase !== "ready" ||
                  loadError !== null
                }
                onChange={setVisibility}
                value={visibility}
              />
            </span>
            <button
              aria-pressed={previewing}
              className={styles.secondaryAction}
              onClick={() => setPreviewing((current) => !current)}
              type="button"
            >
              {previewing ? "继续编辑" : "预览"}
            </button>
            <button
              className={styles.primaryAction}
              disabled={
                publishing ||
                discarding ||
                titleTooLong ||
                phase !== "ready" ||
                loadError !== null ||
                saveState.kind === "conflict" ||
                coverBusy ||
                uploads.some((task) => task.failed === null)
              }
              onClick={() => void publish()}
              type="button"
            >
              {publishing ? "正在发布…" : "发布"}
            </button>
            <Menu.Root>
              <Menu.Trigger aria-label="更多" className={styles.toolButton}>
                <Icon name="more" />
              </Menu.Trigger>
              <Menu.Portal>
                <Menu.Positioner align="end" side="bottom" sideOffset={6}>
                  <Menu.Popup
                    aria-label="更多选项"
                    className={styles.menuPopup}
                  >
                    <Menu.Item
                      className={styles.menuItem}
                      disabled={
                        saveState.kind === "saving" ||
                        saveState.kind === "conflict" ||
                        publishing ||
                        discarding ||
                        phase !== "ready" ||
                        loadError !== null
                      }
                      onClick={() => void save("force")}
                    >
                      立即保存
                    </Menu.Item>
                    <ConfirmDialog
                      title="删除这份草稿？"
                      description="删除后无法找回。已发布的文章不受影响。"
                      confirmLabel="删除草稿"
                      danger
                      onConfirm={discardCurrent}
                      trigger={
                        <Menu.Item
                          closeOnClick={false}
                          disabled={
                            saveState.kind === "saving" ||
                            publishing ||
                            discarding ||
                            coverBusy ||
                            uploads.some((task) => task.failed === null)
                          }
                          className={`${styles.menuItem} ${styles.menuItemDanger}`}
                        >
                          删除这份草稿
                        </Menu.Item>
                      }
                    />
                  </Menu.Popup>
                </Menu.Positioner>
              </Menu.Portal>
            </Menu.Root>
          </div>
        </header>

        {editor &&
        !previewing &&
        formatOpen &&
        !publishing &&
        !discarding &&
        phase === "ready" &&
        !loadError ? (
          <NotionFormatToolbar editor={editor} />
        ) : null}

        <div className={styles.main}>
          <div
            className={styles.railPanel}
            id="article-library"
            ref={libraryPanel}
            hidden={!railOpen || previewing}
          >
            <button
              className={styles.railClose}
              aria-label="收起文章列表"
              onClick={() => setRailOpen(false)}
              type="button"
            >
              <Icon name="close" />
            </button>
            <DraftRail
              currentId={draft?.id ?? null}
              drafts={drafts}
              error={railError}
              loading={railLoading}
              onDiscard={(one) => {
                if (one.id === draft?.id) {
                  void discardCurrent();
                  return;
                }
                void api
                  .discard(csrfToken, one.id)
                  .then(refreshDrafts)
                  .catch((error: unknown) => {
                    notify(articleDraftErrorMessage(error), { type: "error" });
                  });
              }}
              onNew={() => {
                setRailOpen(false);
                location.navigate(ARTICLE_COMPOSE_PATH);
              }}
              onOpen={(one) => {
                setRailOpen(false);
                location.navigate(articleDraftPath(one.id));
              }}
              onOpenPublished={async (id) => {
                try {
                  // The navigation guard saves any current work before loading the copy.
                  const workingCopy = await api.openPublished(csrfToken, id);
                  void refreshDrafts();
                  setRailOpen(false);
                  location.navigate(articleDraftPath(workingCopy.id));
                } catch (error) {
                  notify(articleDraftErrorMessage(error), { type: "error" });
                }
              }}
              disabled={publishing || discarding || saving.current !== null}
              timezone={site.timezone}
            />
          </div>

          <div className={styles.document}>
            {phase === "loading" ? (
              <p className={styles.loading}>正在读取草稿…</p>
            ) : null}
            {loadError ? (
              <p className={styles.pageError} role="alert">
                {loadError}
              </p>
            ) : null}
            {saveState.kind === "conflict" ? (
              <ConflictNotice
                busy={resolving}
                onKeepMine={() => void keepMyCopy()}
                onTakeServer={() => void takeServerCopy()}
              />
            ) : null}

            <ArticleSheet
              outline={previewing ? undefined : <></>}
              cover={
                <CoverField
                  busy={coverBusy}
                  mediaId={coverMediaId}
                  onPick={() => coverInput.current?.click()}
                  onRemove={() => {
                    setCoverMediaId(null);
                    markChanged();
                  }}
                  readOnly={
                    previewing ||
                    publishing ||
                    discarding ||
                    phase !== "ready" ||
                    loadError !== null
                  }
                />
              }
              title={
                <>
                  {previewing ? (
                    <h1>{title.trim() || "无标题"}</h1>
                  ) : (
                    <>
                      <label className={styles.srOnly} htmlFor="article-title">
                        文章标题
                      </label>
                      <textarea
                        id="article-title"
                        ref={titleInput}
                        disabled={
                          publishing ||
                          discarding ||
                          phase !== "ready" ||
                          loadError !== null
                        }
                        onChange={(event) => {
                          setTitle(event.target.value.replace(/[\r\n]+/g, " "));
                          markChanged();
                        }}
                        placeholder="标题"
                        rows={1}
                        value={title}
                      />
                    </>
                  )}
                  {titleTooLong ? (
                    <p className={styles.titleOverflow} role="alert">
                      标题最多 {MAX_ARTICLE_TITLE_CODE_POINTS} 个字符，现在是{" "}
                      {titleLength} 个。
                    </p>
                  ) : null}
                </>
              }
              byline={
                <div className={styles.byline}>
                  <SiteAvatar site={site} />
                  <span className={styles.bylineName}>
                    {siteDisplayName(site)}
                  </span>
                  <span aria-hidden="true">·</span>
                  <span>@{site.username}</span>
                </div>
              }
            >
              <div className={styles.body}>
                <div hidden={previewing}>
                  {editor && (
                    <NotionSurface
                      editor={editor}
                      readOnly={
                        previewing ||
                        publishing ||
                        discarding ||
                        phase !== "ready" ||
                        loadError !== null
                      }
                    />
                  )}
                </div>
                {previewing && previewDocument ? (
                  <RichContent document={previewDocument} />
                ) : null}
                {editor && !previewing ? (
                  <CharacterCount editor={editor} />
                ) : null}
              </div>
            </ArticleSheet>
          </div>
        </div>

        <input
          accept="image/*"
          className={styles.srOnly}
          onChange={(event) => {
            const file = event.target.files?.[0];
            event.target.value = "";
            if (file) void uploadCover(file);
          }}
          ref={coverInput}
          tabIndex={-1}
          type="file"
        />
      </div>
    </MentionProvider>
  );
}

export default ArticleEditor;
