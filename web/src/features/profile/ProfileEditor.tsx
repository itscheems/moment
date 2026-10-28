import { AccountName } from "../../components/AccountName";
import { accountsApi, type AccountProfileUpdate } from "../../api/accounts";

import { Dialog } from "../../components/Dialog";
import { Menu } from "@base-ui/react/menu";
import {
  type ChangeEvent,
  useEffect,
  useEffectEvent,
  useRef,
  useState,
} from "react";

import { mediaPreviewUrl } from "../../api/mediaUrls";
import {
  PublicApiError,
  type Account,
  type PublicAccount,
} from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { SiteAvatar } from "../../components/SiteIdentity";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";

import { useNavigation } from "../../app/navigation";
import {
  isProfileEditPath,
  profileFromPath,
  PROFILE_EDIT_PATH,
} from "../../app/routes";
import { ProfileAvatarCrop } from "./ProfileAvatarCrop";

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

type ProfileEditorProps = {
  account: PublicAccount;
  onSaved: (account: Account) => void;
  triggerClassName?: string;
};

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

    limits,
    session,
    applyAccount,
    expireIfInvalidSession,
  } = useSession();
  const navigation = useNavigation();
  const routeOpen = isProfileEditPath(navigation.pathname);
  const [cropFile, setCropFile] = useState<File | null>(null);
  const pictureCeiling = limits?.profilePictureBytes ?? null;
  // Falling back to the previous literals only for as long as the server has
  // not answered. These bound an input that has to render either way, unlike
  // the picture ceiling, which simply goes unchecked until it is known.
  const nameLimit = limits?.displayNameLength ?? 80;
  const bioLimit = limits?.bioLength ?? 500;
  // Held as consts so the narrowing survives into the async handlers below.
  const subject = props.account;
  const targetId = subject.userId;
  const saveAccount = props.onSaved;
  const uploadNote =
    viewer?.status === "pending"
      ? NOT_APPROVED
      : viewer?.status === "suspended"
        ? "账户已停用，暂时不能上传图片"
        : null;
  const canUpload = uploadNote === null;
  const [open, setOpen] = useState(routeOpen);
  const [displayName, setDisplayName] = useState(subject.displayName);
  const [username, setUsername] = useState(subject.username);
  const [bio, setBio] = useState(subject.bio);
  const [avatarId, setAvatarId] = useState(subject.avatarMediaId);
  const [coverId, setCoverId] = useState(subject.coverMediaId);
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
  const avatarBase = boundAvatarId;
  const coverBase = boundCoverId;
  const avatarInput = useRef<HTMLInputElement>(null);
  const coverInput = useRef<HTMLInputElement>(null);
  const avatarMenuTrigger = useRef<HTMLButtonElement>(null);
  const coverMenuTrigger = useRef<HTMLButtonElement>(null);
  const displayNameInput = useRef<HTMLInputElement>(null);
  const uploads = useRef<Record<AssetKind, AbortController | null>>({
    avatar: null,
    cover: null,
  });
  const objectURLs = useRef<string[]>([]);

  useDocumentScrollLock(open && session !== null);

  const restore = (avatar: string | null, cover: string | null) => {
    uploads.current.avatar?.abort();
    uploads.current.cover?.abort();
    objectURLs.current.forEach((url) => URL.revokeObjectURL(url));
    objectURLs.current = [];
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

  const reopen = () => restore(subject.avatarMediaId, subject.coverMediaId);
  const syncRoute = useEffectEvent(() => {
    if (routeOpen && !open) reopen();
    if (!routeOpen) setCropFile(null);
    setOpen(routeOpen);
  });
  useEffect(() => {
    // Reauthentication keeps the mounted editor and its unsaved fields.
    if (navigation.pathname === "/login") return;
    syncRoute();
  }, [routeOpen, navigation.pathname]);
  const close = (saved?: Account) => {
    setOpen(false);
    setCropFile(null);
    const from = navigation.panel?.from.pathname;
    if (
      navigation.panel?.key === "profile-editor" &&
      (!saved ||
        saved.username === subject.username ||
        profileFromPath(from ?? "")?.username !== subject.username)
    )
      navigation.closePanel("profile-editor");
    else navigation.replace(saved?.profileUrl ?? subject.profileUrl);
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
    const validationError = accountPictureProblem(file, pictureCeiling);
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
      // One request ingests and binds, so the picture belongs to this
      // account the moment the response lands. There is no media id for the
      // save button to carry afterwards, and nothing orphaned on disk if the
      // reader closes the dialog on the next breath.
      const saved =
        kind === "avatar"
          ? await accountsApi.uploadAccountAvatar(
              session.csrfToken,
              file,
              controller.signal,
            )
          : await accountsApi.uploadAccountCover(
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
    } catch (nextError) {
      if (controller.signal.aborted) return;
      expireIfInvalidSession(nextError);
      if (kind === "avatar") setAvatarState("failed");
      else setCoverState("failed");
      setError(
        pictureFailure(nextError, pictureCeiling) ??
          "图片没有上传完成，头像和封面保持原样，可以重新选择。",
      );
    }
  };

  const pick = (kind: AssetKind, event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0];
    event.currentTarget.value = "";
    if (!file || !canUpload) return;
    const problem = accountPictureProblem(file, pictureCeiling);
    if (problem) {
      setError(problem);
      return;
    }
    if (kind === "avatar") {
      setError(null);
      setCropFile(file);
    } else void upload(kind, file);
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
    displayName !== subject.displayName ||
    username !== subject.username ||
    bio !== subject.bio ||
    avatarId !== avatarBase ||
    coverId !== coverBase;
  const pending = avatarState === "uploading" || coverState === "uploading";
  const uploadFailed = avatarState === "failed" || coverState === "failed";
  const invalid =
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
      const update: AccountProfileUpdate = {};
      if (displayName !== subject.displayName) {
        update.displayName = displayName;
      }
      if (username !== subject.username) update.username = username;
      if (bio !== subject.bio) update.bio = bio;
      if (avatarId !== avatarBase) update.avatarMediaId = avatarId;
      if (coverId !== coverBase) update.coverMediaId = coverId;
      const saved = await accountsApi.updateAccountProfile(
        session.csrfToken,
        update,
      );
      applyAccount(saved);
      saveAccount(saved);
      notify("个人资料已更新");
      close(saved);
    } catch (nextError) {
      if (!expireIfInvalidSession(nextError)) {
        setError(saveFailure(nextError));
      }
    } finally {
      setSaving(false);
    }
  };

  if (viewer?.userId !== targetId) return null;
  return (
    <Dialog.Root
      onOpenChange={(nextOpen) => {
        if (saving) return;
        if (nextOpen) {
          reopen();
          navigation.openPanel(PROFILE_EDIT_PATH, "profile-editor");
          setOpen(true);
        } else close();
      }}
      open={open && session !== null}
    >
      <Dialog.Trigger
        className={`${styles.trigger} ${props.triggerClassName ?? ""}`}
      >
        编辑资料
      </Dialog.Trigger>
      <Dialog.Portal keepMounted={open}>
        <Dialog.Backdrop
          className={styles.backdrop}
          style={session && !cropFile ? undefined : { visibility: "hidden" }}
        />
        <Dialog.Viewport
          className={styles.viewport}
          style={session && !cropFile ? undefined : { visibility: "hidden" }}
        >
          <Dialog.Popup
            className={styles.popup}
            initialFocus={displayNameInput}
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
                  <Icon name="media-remove" />
                </Dialog.Close>
                <Dialog.Title>编辑资料</Dialog.Title>
                <div className={styles.headerActions}>
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
                      <strong>
                        <AccountName
                          identity={subject}
                          name={displayName.trim() || "未命名"}
                        />
                      </strong>
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
      {cropFile && session ? (
        <ProfileAvatarCrop
          file={cropFile}
          csrfToken={session.csrfToken}
          onCancel={() => setCropFile(null)}
          onSessionError={expireIfInvalidSession}
          onApply={(cropped) => {
            setCropFile(null);
            void upload("avatar", cropped);
          }}
        />
      ) : null}
    </Dialog.Root>
  );
}
