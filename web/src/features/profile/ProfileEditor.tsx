import { Dialog } from "@base-ui/react/dialog";
import { type ChangeEvent, useEffect, useRef, useState } from "react";

import type { SiteSettingsUpdate } from "../../api/owner";
import { PublicApiError, type SiteIdentity } from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { SiteAvatar } from "../../components/SiteIdentity";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { fileValidation } from "../media/mediaState";
import { useOwner } from "../owner/OwnerProvider";
import styles from "./ProfileEditor.module.css";

type AssetKind = "avatar" | "cover";
type UploadState = "idle" | "uploading" | "failed";

function mediaURL(id: string | null): string | null {
  return id ? `/api/v1/media/${encodeURIComponent(id)}/preview` : null;
}

export function ProfileEditor({
  site,
  onSaved,
}: {
  site: SiteIdentity;
  onSaved: (site: SiteIdentity) => void;
}) {
  const { api, session, expireIfInvalidSession } = useOwner();
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState(site.title);
  const [displayName, setDisplayName] = useState(site.displayName);
  const [username, setUsername] = useState(site.username);
  const [bio, setBio] = useState(site.bio);
  const [avatarId, setAvatarId] = useState(site.avatarMediaId);
  const [coverId, setCoverId] = useState(site.coverMediaId);
  const [avatarPreview, setAvatarPreview] = useState(
    mediaURL(site.avatarMediaId),
  );
  const [coverPreview, setCoverPreview] = useState(mediaURL(site.coverMediaId));
  const [avatarState, setAvatarState] = useState<UploadState>("idle");
  const [coverState, setCoverState] = useState<UploadState>("idle");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const avatarInput = useRef<HTMLInputElement>(null);
  const coverInput = useRef<HTMLInputElement>(null);
  const titleInput = useRef<HTMLInputElement>(null);
  const uploads = useRef<Record<AssetKind, AbortController | null>>({
    avatar: null,
    cover: null,
  });
  const objectURLs = useRef<string[]>([]);

  useDocumentScrollLock(open);

  const reset = () => {
    uploads.current.avatar?.abort();
    uploads.current.cover?.abort();
    objectURLs.current.forEach((url) => URL.revokeObjectURL(url));
    objectURLs.current = [];
    setTitle(site.title);
    setDisplayName(site.displayName);
    setUsername(site.username);
    setBio(site.bio);
    setAvatarId(site.avatarMediaId);
    setCoverId(site.coverMediaId);
    setAvatarPreview(mediaURL(site.avatarMediaId));
    setCoverPreview(mediaURL(site.coverMediaId));
    setAvatarState("idle");
    setCoverState("idle");
    setError(null);
    setSaving(false);
  };

  useEffect(
    () => () => {
      uploads.current.avatar?.abort();
      uploads.current.cover?.abort();
      objectURLs.current.forEach((url) => URL.revokeObjectURL(url));
    },
    [],
  );

  const upload = async (kind: AssetKind, file: File) => {
    if (!session) return;
    const validationError = fileValidation(file, []);
    if (validationError) {
      setError(validationError);
      return;
    }
    uploads.current[kind]?.abort();
    const controller = new AbortController();
    uploads.current[kind] = controller;
    const local = URL.createObjectURL(file);
    objectURLs.current.push(local);
    if (kind === "avatar") {
      setAvatarPreview(local);
      setAvatarState("uploading");
    } else {
      setCoverPreview(local);
      setCoverState("uploading");
    }
    setError(null);
    try {
      const result = await api.uploadMedia(
        session.csrfToken,
        file,
        () => undefined,
        controller.signal,
      );
      if (uploads.current[kind] !== controller) return;
      if (kind === "avatar") {
        setAvatarId(result.id);
        setAvatarPreview(result.previewUrl);
        setAvatarState("idle");
      } else {
        setCoverId(result.id);
        setCoverPreview(result.previewUrl);
        setCoverState("idle");
      }
    } catch (nextError) {
      if (controller.signal.aborted) return;
      expireIfInvalidSession(nextError);
      if (kind === "avatar") setAvatarState("failed");
      else setCoverState("failed");
      setError("图片没有上传完成。当前资料尚未改变，可以重新选择后再保存。");
    }
  };

  const pick = (kind: AssetKind, event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (file) void upload(kind, file);
  };

  const changed =
    title !== site.title ||
    displayName !== site.displayName ||
    username !== site.username ||
    bio !== site.bio ||
    avatarId !== site.avatarMediaId ||
    coverId !== site.coverMediaId;
  const pending = avatarState === "uploading" || coverState === "uploading";
  const uploadFailed = avatarState === "failed" || coverState === "failed";
  const invalid =
    !title.trim() ||
    !displayName.trim() ||
    !/^[a-z][a-z0-9_]{2,29}$/.test(username.trim().toLowerCase()) ||
    Array.from(title).length > 120 ||
    Array.from(displayName).length > 80 ||
    Array.from(bio).length > 500;

  const save = async () => {
    if (!session || !changed || invalid || pending || uploadFailed || saving)
      return;
    const update: SiteSettingsUpdate = {};
    if (title !== site.title) update.title = title;
    if (displayName !== site.displayName) update.displayName = displayName;
    if (username !== site.username) update.username = username;
    if (bio !== site.bio) update.bio = bio;
    if (avatarId !== site.avatarMediaId) update.avatarMediaId = avatarId;
    if (coverId !== site.coverMediaId) update.coverMediaId = coverId;
    setSaving(true);
    setError(null);
    try {
      const saved = await api.updateSite(session.csrfToken, update);
      onSaved({
        ...saved,
        instanceId: site.instanceId,
        instanceInitializedAt: site.instanceInitializedAt,
      });
      notify("个人资料已更新");
      setOpen(false);
    } catch (nextError) {
      if (!expireIfInvalidSession(nextError)) {
        setError(
          nextError instanceof PublicApiError &&
            nextError.code === "MEDIA_NOT_FOUND"
            ? "所选图片已不可用，请重新选择。"
            : "个人资料没有保存。当前更改仍保留，可以重试。",
        );
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        if (saving) return;
        if (nextOpen) reset();
        setOpen(nextOpen);
      }}
      open={open}
    >
      <Dialog.Trigger className={styles.trigger}>编辑个人资料</Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup className={styles.popup} initialFocus={titleInput}>
            <form
              className={styles.form}
              onKeyDown={(event) => {
                if (
                  event.key === "Enter" &&
                  (event.metaKey || event.ctrlKey) &&
                  !event.nativeEvent.isComposing
                ) {
                  event.preventDefault();
                  void save();
                }
              }}
            >
              <header className={styles.header}>
                <Dialog.Close
                  aria-label="关闭编辑个人资料"
                  className={styles.iconButton}
                  title="关闭"
                >
                  <Icon name="close" />
                </Dialog.Close>
                <Dialog.Title>编辑个人资料</Dialog.Title>
                <div className={styles.headerActions}>
                  <button
                    aria-label="重置个人资料更改"
                    className={styles.iconButton}
                    disabled={saving || (!changed && !pending && !uploadFailed)}
                    onClick={reset}
                    title="重置"
                    type="button"
                  >
                    <Icon name="reset" />
                  </button>
                  <button
                    className={styles.saveButton}
                    disabled={
                      !changed || invalid || pending || uploadFailed || saving
                    }
                    onClick={() => void save()}
                    type="button"
                  >
                    {saving ? "保存中" : "保存"}
                  </button>
                </div>
              </header>

              <div className={styles.coverEditor}>
                {coverPreview ? (
                  <img alt="封面预览" src={coverPreview} />
                ) : (
                  <span />
                )}
                <div className={styles.mediaActions}>
                  <button
                    aria-label="选择封面图片"
                    onClick={() => coverInput.current?.click()}
                    title="添加封面图片"
                    type="button"
                  >
                    <Icon name="camera" />
                  </button>
                  {coverPreview ? (
                    <button
                      aria-label="移除封面图片"
                      onClick={() => {
                        setCoverId(null);
                        setCoverPreview(null);
                        setCoverState("idle");
                      }}
                      title="移除封面图片"
                      type="button"
                    >
                      <Icon name="close" />
                    </button>
                  ) : null}
                </div>
                {coverState === "uploading" ? (
                  <small>正在上传封面…</small>
                ) : null}
              </div>

              <div className={styles.avatarEditor}>
                {avatarPreview ? (
                  <img alt="头像预览" src={avatarPreview} />
                ) : (
                  <SiteAvatar site={{ ...site, avatarMediaId: null }} />
                )}
                <div className={styles.mediaActions}>
                  <button
                    aria-label="选择头像图片"
                    onClick={() => avatarInput.current?.click()}
                    title="添加头像图片"
                    type="button"
                  >
                    <Icon name="camera" />
                  </button>
                  {avatarPreview ? (
                    <button
                      aria-label="移除头像图片"
                      onClick={() => {
                        setAvatarId(null);
                        setAvatarPreview(null);
                        setAvatarState("idle");
                      }}
                      title="移除头像图片"
                      type="button"
                    >
                      <Icon name="close" />
                    </button>
                  ) : null}
                </div>
                {avatarState === "uploading" ? (
                  <small>正在上传头像…</small>
                ) : null}
              </div>

              <input
                accept="image/jpeg,image/png,image/webp,image/gif"
                className={styles.fileInput}
                onChange={(event) => pick("cover", event)}
                ref={coverInput}
                type="file"
              />
              <input
                accept="image/jpeg,image/png,image/webp,image/gif"
                className={styles.fileInput}
                onChange={(event) => pick("avatar", event)}
                ref={avatarInput}
                type="file"
              />

              <div className={styles.fields}>
                <label>
                  <span>显示名称</span>
                  <input
                    maxLength={80}
                    onChange={(event) => setDisplayName(event.target.value)}
                    required
                    value={displayName}
                  />
                </label>
                <label>
                  <span>用户名</span>
                  <div className={styles.usernameField}>
                    <span aria-hidden="true">@</span>
                    <input
                      autoCapitalize="none"
                      maxLength={30}
                      onChange={(event) =>
                        setUsername(
                          event.target.value
                            .toLowerCase()
                            .replace(/[^a-z0-9_]/g, ""),
                        )
                      }
                      pattern="[a-z][a-z0-9_]{2,29}"
                      required
                      spellCheck={false}
                      value={username}
                    />
                  </div>
                  <small>唯一公开标识，用于个人资料链接和 @ 提及。</small>
                </label>
                <label>
                  <span>站点标题</span>
                  <input
                    maxLength={120}
                    onChange={(event) => setTitle(event.currentTarget.value)}
                    ref={titleInput}
                    value={title}
                  />
                </label>
                <label>
                  <span>简介</span>
                  <textarea
                    maxLength={500}
                    onChange={(event) => setBio(event.currentTarget.value)}
                    rows={4}
                    value={bio}
                  />
                </label>
                <p className={styles.counter}>{Array.from(bio).length} / 500</p>
                {error ? (
                  <p className={styles.error} role="alert">
                    {error}
                  </p>
                ) : null}
              </div>
            </form>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
