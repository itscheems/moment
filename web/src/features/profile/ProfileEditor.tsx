import { Dialog } from "@base-ui/react/dialog";
import { Menu } from "@base-ui/react/menu";
import { type ChangeEvent, useEffect, useRef, useState } from "react";

import type { AccountProfileUpdate, SiteSettingsUpdate } from "../../api/owner";
import { mediaPreviewUrl } from "../../api/mediaUrls";
import {
  PublicApiError,
  type Account,
  type SiteIdentity,
} from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { SiteAvatar } from "../../components/SiteIdentity";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { fileValidation } from "../media/mediaState";
import { useSession } from "../auth/SessionProvider";
import styles from "./ProfileEditor.module.css";

type AssetKind = "avatar" | "cover";
type UploadState = "idle" | "uploading" | "failed";

const NOT_APPROVED = "账户通过审核后就能上传图片";
const NOT_AN_IMAGE = "这里只能放图片";

/**
 * How big a picture may be, in the reader's units.
 *
 * Phrased from the ceiling the server published rather than from a number
 * kept here, so the sentence a reader is shown and the rule that produced it
 * cannot drift apart. Without a published ceiling there is nothing honest to
 * name, so the message says what happened and not what the rule is.
 */
function tooLargeMessage(ceilingBytes: number | null): string {
  return ceilingBytes === null
    ? "这张图片太大了，请换一张小一些的。"
    : `图片不能超过 ${Math.round(ceilingBytes / 1024 / 1024)} MB`;
}

/**
 * What the editor is editing.
 *
 * The owner's identity is the site's, so their edits land on the site row and
 * their pictures go through the owner media route. A member has an account and
 * nothing else, and it has its own routes. One dialog serves both because the
 * fields a reader fills in are the same either way; only where they are sent
 * differs, and that is what this union names.
 */
type ProfileEditorProps =
  | { site: SiteIdentity; onSaved: (site: SiteIdentity) => void }
  | { account: Account; onSaved: (account: Account) => void };

function mediaURL(id: string | null): string | null {
  return id ? mediaPreviewUrl(id) : null;
}

/**
 * A refusal in the reader's words, or null when there is nothing specific to
 * say.
 *
 * Read from the code and never the status: the server answers MEDIA_TOO_LARGE
 * with 413 when it can weigh the upload before reading it and with 400 when
 * the ingest pipeline is what runs out of room, and MEDIA_TYPE_UNSUPPORTED
 * with 415 or 400 for the same reason. Which of the two happened is not a
 * distinction anyone choosing a photo can act on.
 */
function pictureFailure(
  error: unknown,
  ceilingBytes: number | null,
): string | null {
  if (!(error instanceof PublicApiError)) return null;
  if (error.code === "ACCOUNT_NOT_APPROVED") return NOT_APPROVED;
  if (error.code === "MEDIA_TOO_LARGE") return tooLargeMessage(ceilingBytes);
  if (error.code === "MEDIA_TYPE_UNSUPPORTED") return NOT_AN_IMAGE;
  return null;
}

/**
 * The same two refusals, decided before a byte leaves the browser.
 *
 * HEIC arrives from an iPhone with an empty MIME type often enough that the
 * name has to be allowed to vouch for it; everything else the server will
 * catch anyway, and says so in the same words.
 */
function accountPictureProblem(
  file: File,
  ceilingBytes: number | null,
): string | null {
  if (!file.type.startsWith("image/") && !/\.(heic|heif)$/i.test(file.name)) {
    return NOT_AN_IMAGE;
  }
  // Nothing is assumed about the ceiling. Until the server has named one there
  // is no local rule to apply, and the upload is left to be refused by the
  // side that actually knows -- which answers in the same words anyway.
  if (ceilingBytes === null || file.size <= ceilingBytes) return null;
  return tooLargeMessage(ceilingBytes);
}

function saveFailure(error: unknown): string {
  if (error instanceof PublicApiError) {
    if (error.code === "MEDIA_NOT_FOUND") {
      return "所选图片已不可用，请重新选择。";
    }
    if (error.code === "USERNAME_TAKEN") {
      return "这个用户名已经有人用了，换一个吧。";
    }
    if (error.code === "USERNAME_RESERVED") {
      return "这个用户名被站点保留，换一个吧。";
    }
  }
  return "个人资料没有保存。当前更改仍保留，可以重试。";
}

export function ProfileEditor(props: ProfileEditorProps) {
  const {
    account: viewer,
    api,
    limits,
    session,
    applyAccount,
    expireIfInvalidSession,
  } = useSession();
  const pictureCeiling = limits?.profilePictureBytes ?? null;
  // Falling back to the previous literals only for as long as the server has
  // not answered. These bound an input that has to render either way, unlike
  // the picture ceiling, which simply goes unchecked until it is known.
  const nameLimit = limits?.displayNameLength ?? 80;
  const bioLimit = limits?.bioLength ?? 500;
  // Held as consts so the narrowing survives into the async handlers below.
  const subject: Account | SiteIdentity =
    "account" in props ? props.account : props.site;
  const siteSubject: SiteIdentity | null =
    "account" in props ? null : props.site;
  const saveAccount = "account" in props ? props.onSaved : null;
  const saveSite = "account" in props ? null : props.onSaved;
  const editingAccount = saveAccount !== null;
  // Bytes are gated on approval; words are not. A pending account has
  // validators standing behind every text field and nothing standing behind a
  // file, so it keeps the whole form and loses only the ability to add one --
  // including the ability to clear a picture it already has. Disabled rather
  // than hidden: a control that vanishes reads as a feature this site does not
  // have, which is a different and untrue statement.
  const uploadNote = !editingAccount
    ? null
    : viewer?.status === "pending"
      ? NOT_APPROVED
      : viewer?.status === "suspended"
        ? "账户已停用，暂时不能上传图片"
        : null;
  const canUpload = uploadNote === null;
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState(siteSubject?.title ?? "");
  const [displayName, setDisplayName] = useState(subject.displayName);
  const [username, setUsername] = useState(subject.username);
  const [bio, setBio] = useState(subject.bio);
  const [avatarId, setAvatarId] = useState(subject.avatarMediaId);
  const [coverId, setCoverId] = useState(subject.coverMediaId);
  // What the server is already holding, which in account mode is not the same
  // thing as what the props say. An account picture upload is a completed
  // save, so the mark the dialog measures "unsaved" against has to move with
  // it -- otherwise the save button offers to write a change already written,
  // and 撤销 offers to undo it back to a picture that no longer exists. The
  // owner's two-step flow keeps measuring against the site row, because there
  // nothing is saved until the save button says so.
  const [boundAvatarId, setBoundAvatarId] = useState(subject.avatarMediaId);
  const [boundCoverId, setBoundCoverId] = useState(subject.coverMediaId);
  const [avatarPreview, setAvatarPreview] = useState(
    mediaURL(subject.avatarMediaId),
  );
  const [coverPreview, setCoverPreview] = useState(
    mediaURL(subject.coverMediaId),
  );
  const [avatarState, setAvatarState] = useState<UploadState>("idle");
  const [coverState, setCoverState] = useState<UploadState>("idle");
  const [error, setError] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  const avatarBase = siteSubject ? siteSubject.avatarMediaId : boundAvatarId;
  const coverBase = siteSubject ? siteSubject.coverMediaId : boundCoverId;
  const avatarInput = useRef<HTMLInputElement>(null);
  const coverInput = useRef<HTMLInputElement>(null);
  const avatarMenuTrigger = useRef<HTMLButtonElement>(null);
  const coverMenuTrigger = useRef<HTMLButtonElement>(null);
  const titleInput = useRef<HTMLInputElement>(null);
  const displayNameInput = useRef<HTMLInputElement>(null);
  const uploads = useRef<Record<AssetKind, AbortController | null>>({
    avatar: null,
    cover: null,
  });
  const objectURLs = useRef<string[]>([]);

  useDocumentScrollLock(open);

  const restore = (avatar: string | null, cover: string | null) => {
    uploads.current.avatar?.abort();
    uploads.current.cover?.abort();
    objectURLs.current.forEach((url) => URL.revokeObjectURL(url));
    objectURLs.current = [];
    setTitle(siteSubject?.title ?? "");
    setDisplayName(subject.displayName);
    setUsername(subject.username);
    setBio(subject.bio);
    setBoundAvatarId(avatar);
    setBoundCoverId(cover);
    setAvatarId(avatar);
    setCoverId(cover);
    setAvatarPreview(mediaURL(avatar));
    setCoverPreview(mediaURL(cover));
    setAvatarState("idle");
    setCoverState("idle");
    setError(null);
    setSaving(false);
  };

  // Opening reads the mark off the props, which is where a parent that adopted
  // the last save keeps it. 撤销 reads it off whatever has been confirmed
  // since, so an upload made a moment ago survives an undo of the words.
  const reopen = () => restore(subject.avatarMediaId, subject.coverMediaId);
  const undo = () => restore(avatarBase, coverBase);

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
    const validationError = editingAccount
      ? accountPictureProblem(file, pictureCeiling)
      : fileValidation(file, []);
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
      if (saveAccount) {
        // One request ingests and binds, so the picture belongs to this
        // account the moment the response lands. There is no media id for the
        // save button to carry afterwards, and nothing orphaned on disk if the
        // reader closes the dialog on the next breath.
        const saved =
          kind === "avatar"
            ? await api.uploadAccountAvatar(
                session.csrfToken,
                file,
                controller.signal,
              )
            : await api.uploadAccountCover(
                session.csrfToken,
                file,
                controller.signal,
              );
        if (uploads.current[kind] !== controller) return;
        applyAccount(saved);
        saveAccount(saved);
        if (kind === "avatar") {
          setAvatarId(saved.avatarMediaId);
          setBoundAvatarId(saved.avatarMediaId);
          setAvatarPreview(mediaURL(saved.avatarMediaId));
          setAvatarState("idle");
        } else {
          setCoverId(saved.coverMediaId);
          setBoundCoverId(saved.coverMediaId);
          setCoverPreview(mediaURL(saved.coverMediaId));
          setCoverState("idle");
        }
        notify(kind === "avatar" ? "头像已更新" : "封面已更新");
        return;
      }
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
      setError(
        pictureFailure(nextError, pictureCeiling) ??
          (editingAccount
            ? "图片没有上传完成，头像和封面保持原样，可以重新选择。"
            : "图片没有上传完成。当前资料尚未改变，可以重新选择后再保存。"),
      );
    }
  };

  const pick = (kind: AssetKind, event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (file) void upload(kind, file);
  };

  const removeAsset = (kind: AssetKind) => {
    uploads.current[kind]?.abort();
    uploads.current[kind] = null;
    if (kind === "avatar") {
      setAvatarId(null);
      setAvatarPreview(null);
      setAvatarState("idle");
    } else {
      setCoverId(null);
      setCoverPreview(null);
      setCoverState("idle");
    }
  };

  const changed =
    (siteSubject ? title !== siteSubject.title : false) ||
    displayName !== subject.displayName ||
    username !== subject.username ||
    bio !== subject.bio ||
    avatarId !== avatarBase ||
    coverId !== coverBase;
  const pending = avatarState === "uploading" || coverState === "uploading";
  const uploadFailed = avatarState === "failed" || coverState === "failed";
  const invalid =
    (siteSubject ? !title.trim() || Array.from(title).length > 120 : false) ||
    !displayName.trim() ||
    !/^[a-z][a-z0-9_]{2,29}$/.test(username.trim().toLowerCase()) ||
    Array.from(displayName).length > nameLimit ||
    Array.from(bio).length > bioLimit;

  const save = async () => {
    if (!session || !changed || invalid || pending || uploadFailed || saving)
      return;
    setSaving(true);
    setError(null);
    try {
      if (siteSubject && saveSite) {
        const update: SiteSettingsUpdate = {};
        if (title !== siteSubject.title) update.title = title;
        if (displayName !== siteSubject.displayName) {
          update.displayName = displayName;
        }
        if (username !== siteSubject.username) update.username = username;
        if (bio !== siteSubject.bio) update.bio = bio;
        if (avatarId !== siteSubject.avatarMediaId) {
          update.avatarMediaId = avatarId;
        }
        if (coverId !== siteSubject.coverMediaId) update.coverMediaId = coverId;
        const saved = await api.updateSite(session.csrfToken, update);
        saveSite({
          ...saved,
          instanceId: siteSubject.instanceId,
          instanceInitializedAt: siteSubject.instanceInitializedAt,
        });
      } else if (saveAccount) {
        const update: AccountProfileUpdate = {};
        if (displayName !== subject.displayName) {
          update.displayName = displayName;
        }
        if (username !== subject.username) update.username = username;
        if (bio !== subject.bio) update.bio = bio;
        if (avatarId !== avatarBase) update.avatarMediaId = avatarId;
        if (coverId !== coverBase) update.coverMediaId = coverId;
        const saved = await api.updateAccountProfile(session.csrfToken, update);
        applyAccount(saved);
        saveAccount(saved);
      }
      notify("个人资料已更新");
      setOpen(false);
    } catch (nextError) {
      if (!expireIfInvalidSession(nextError)) {
        setError(saveFailure(nextError));
      }
    } finally {
      setSaving(false);
    }
  };

  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        if (saving) return;
        if (nextOpen) reopen();
        setOpen(nextOpen);
      }}
      open={open}
    >
      <Dialog.Trigger className={styles.trigger}>编辑资料</Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.backdrop} />
        <Dialog.Viewport className={styles.viewport}>
          <Dialog.Popup
            className={styles.popup}
            initialFocus={siteSubject ? titleInput : displayNameInput}
          >
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
                  aria-label="关闭编辑资料"
                  className={styles.iconButton}
                  title="关闭"
                >
                  <Icon name="close" />
                </Dialog.Close>
                <Dialog.Title>编辑资料</Dialog.Title>
                <div className={styles.headerActions}>
                  <button
                    aria-label="重置资料更改"
                    className={styles.resetButton}
                    disabled={saving || (!changed && !pending && !uploadFailed)}
                    onClick={undo}
                    title="重置"
                    type="button"
                  >
                    <Icon name="reset" />
                    <span>撤销</span>
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

              <div className={styles.body}>
                <section
                  aria-label="个人资料预览"
                  className={styles.identityStage}
                >
                  <div className={styles.coverEditor}>
                    {coverPreview ? (
                      <img alt="封面预览" src={coverPreview} />
                    ) : (
                      <div
                        aria-hidden="true"
                        className={styles.coverPlaceholder}
                      />
                    )}
                    <div className={styles.coverActions}>
                      <Menu.Root>
                        <Menu.Trigger
                          aria-label="编辑封面"
                          ref={coverMenuTrigger}
                          title="编辑封面"
                          type="button"
                        >
                          <Icon name="camera" />
                        </Menu.Trigger>
                        <Menu.Portal>
                          <Menu.Positioner
                            align="end"
                            className={styles.mediaMenuPositioner}
                            side="bottom"
                            sideOffset={6}
                          >
                            <Menu.Popup
                              className={styles.mediaMenu}
                              finalFocus={coverMenuTrigger}
                            >
                              <Menu.Item
                                className={styles.mediaMenuItem}
                                disabled={!canUpload}
                                onClick={() => coverInput.current?.click()}
                              >
                                <Icon name="camera" />
                                <span>更换封面</span>
                              </Menu.Item>
                              <Menu.Item
                                className={`${styles.mediaMenuItem} ${styles.mediaMenuDanger}`}
                                disabled={!coverPreview}
                                onClick={() => removeAsset("cover")}
                              >
                                <Icon name="delete" />
                                <span>移除封面</span>
                              </Menu.Item>
                            </Menu.Popup>
                          </Menu.Positioner>
                        </Menu.Portal>
                      </Menu.Root>
                    </div>
                    {coverState === "uploading" ? (
                      <small className={styles.coverUploadStatus}>
                        正在上传封面…
                      </small>
                    ) : null}
                  </div>

                  <div className={styles.identityPreview}>
                    <div className={styles.avatarEditor}>
                      <div className={styles.avatarMedia}>
                        {avatarPreview ? (
                          <img alt="头像预览" src={avatarPreview} />
                        ) : (
                          <SiteAvatar
                            site={{ ...subject, avatarMediaId: null }}
                          />
                        )}
                      </div>
                      <Menu.Root>
                        <Menu.Trigger
                          aria-label="编辑头像"
                          className={styles.avatarChangeButton}
                          ref={avatarMenuTrigger}
                          title="编辑头像"
                          type="button"
                        >
                          <Icon name="camera" />
                        </Menu.Trigger>
                        <Menu.Portal>
                          <Menu.Positioner
                            align="start"
                            className={styles.mediaMenuPositioner}
                            side="bottom"
                            sideOffset={6}
                          >
                            <Menu.Popup
                              className={styles.mediaMenu}
                              finalFocus={avatarMenuTrigger}
                            >
                              <Menu.Item
                                className={styles.mediaMenuItem}
                                disabled={!canUpload}
                                onClick={() => avatarInput.current?.click()}
                              >
                                <Icon name="camera" />
                                <span>更换头像</span>
                              </Menu.Item>
                              <Menu.Item
                                className={`${styles.mediaMenuItem} ${styles.mediaMenuDanger}`}
                                disabled={!avatarPreview}
                                onClick={() => removeAsset("avatar")}
                              >
                                <Icon name="delete" />
                                <span>移除头像</span>
                              </Menu.Item>
                            </Menu.Popup>
                          </Menu.Positioner>
                        </Menu.Portal>
                      </Menu.Root>
                    </div>
                    <div className={styles.identityCopy}>
                      <strong>{displayName.trim() || "未命名"}</strong>
                      <small>@{username.trim() || "username"}</small>
                      {avatarState === "uploading" ? (
                        <em>正在上传头像…</em>
                      ) : null}
                    </div>
                  </div>
                </section>

                {uploadNote ? (
                  <p className={styles.uploadNote}>{uploadNote}</p>
                ) : null}

                <input
                  accept="image/jpeg,image/png,image/webp,image/gif,image/heic,image/heif,.heic,.heif"
                  className={styles.fileInput}
                  data-profile-media-input="cover"
                  onChange={(event) => pick("cover", event)}
                  ref={coverInput}
                  type="file"
                />
                <input
                  accept="image/jpeg,image/png,image/webp,image/gif,image/heic,image/heif,.heic,.heif"
                  className={styles.fileInput}
                  data-profile-media-input="avatar"
                  onChange={(event) => pick("avatar", event)}
                  ref={avatarInput}
                  type="file"
                />

                <section aria-label="个人资料字段" className={styles.fields}>
                  <label>
                    <span>显示名称</span>
                    <input
                      maxLength={nameLimit}
                      onChange={(event) => setDisplayName(event.target.value)}
                      ref={displayNameInput}
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
                  </label>
                  {siteSubject ? (
                    <label className={styles.fieldWide}>
                      <span>站点标题</span>
                      <input
                        maxLength={120}
                        onChange={(event) =>
                          setTitle(event.currentTarget.value)
                        }
                        ref={titleInput}
                        value={title}
                      />
                    </label>
                  ) : null}
                  <label className={`${styles.fieldWide} ${styles.bioField}`}>
                    <span>简介</span>
                    <textarea
                      maxLength={bioLimit}
                      onChange={(event) => setBio(event.currentTarget.value)}
                      rows={3}
                      value={bio}
                    />
                    <small className={styles.counter}>
                      {Array.from(bio).length} / {bioLimit}
                    </small>
                  </label>
                  {error ? (
                    <p className={styles.error} role="alert">
                      {error}
                    </p>
                  ) : null}
                </section>
              </div>
            </form>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
