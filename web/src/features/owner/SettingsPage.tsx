import {
  type ChangeEvent,
  type FormEvent,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";

import type {
  OwnerStatus,
  RotatedCredential,
  SiteSettingsUpdate,
} from "../../api/owner";
import { PublicApiError, type SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { fileValidation } from "../media/mediaState";
import { ConfirmDialog } from "./ConfirmDialog";
import { OwnerLogin } from "./OwnerLogin";
import { useOwner } from "./OwnerProvider";
import styles from "./Settings.module.css";

const commonTimezones = [
  "UTC",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Asia/Singapore",
  "Europe/London",
  "Europe/Paris",
  "America/New_York",
  "America/Los_Angeles",
];

interface SettingsForm {
  title: string;
  bio: string;
  timezone: string;
  publicBaseUrl: string;
  commentsEnabled: boolean;
}

interface FieldErrors {
  timezone?: string;
  publicBaseUrl?: string;
}

type AvatarState = "idle" | "uploading" | "ready" | "failed";

function formFromSite(site: SiteIdentity): SettingsForm {
  return {
    title: site.title,
    bio: site.bio,
    timezone: site.timezone,
    publicBaseUrl: site.publicBaseUrl ?? "",
    commentsEnabled: site.commentsEnabled,
  };
}

function normalizedPublicBaseUrl(value: string): string | null {
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

function validateForm(form: SettingsForm): FieldErrors {
  const errors: FieldErrors = {};
  const timezone = form.timezone.trim();
  try {
    if (
      !timezone ||
      timezone === "Local" ||
      timezone.startsWith("/") ||
      timezone.includes("..")
    ) {
      throw new Error("invalid timezone");
    }
    new Intl.DateTimeFormat("zh-CN", { timeZone: timezone }).format();
  } catch {
    errors.timezone = "请输入有效的 IANA 时区，例如 Asia/Shanghai。";
  }

  const publicBaseUrl = normalizedPublicBaseUrl(form.publicBaseUrl);
  if (publicBaseUrl) {
    try {
      const parsed = new URL(publicBaseUrl);
      const localHTTP =
        parsed.protocol === "http:" &&
        (parsed.hostname === "localhost" ||
          parsed.hostname === "127.0.0.1" ||
          parsed.hostname === "::1");
      if (
        (parsed.protocol !== "https:" && !localHTTP) ||
        parsed.username ||
        parsed.password ||
        parsed.search ||
        parsed.hash
      ) {
        throw new Error("invalid public base URL");
      }
    } catch {
      errors.publicBaseUrl =
        "请输入 HTTPS 站点基址；本地开发可使用 localhost HTTP。不要包含账号、查询或片段。";
    }
  }
  return errors;
}

function changedSiteFields(
  form: SettingsForm,
  baseline: SiteIdentity,
  avatarMediaId: string | null,
  includeAvatar: boolean,
): SiteSettingsUpdate {
  const update: SiteSettingsUpdate = {};
  if (form.title !== baseline.title) update.title = form.title;
  if (form.bio !== baseline.bio) update.bio = form.bio;
  if (form.timezone.trim() !== baseline.timezone) {
    update.timezone = form.timezone.trim();
  }
  const publicBaseUrl = normalizedPublicBaseUrl(form.publicBaseUrl);
  if (publicBaseUrl !== baseline.publicBaseUrl) {
    update.publicBaseUrl = publicBaseUrl;
  }
  if (form.commentsEnabled !== baseline.commentsEnabled) {
    update.commentsEnabled = form.commentsEnabled;
  }
  if (includeAvatar && avatarMediaId !== baseline.avatarMediaId) {
    update.avatarMediaId = avatarMediaId;
  }
  return update;
}

function hasFields(update: SiteSettingsUpdate): boolean {
  return Object.keys(update).length > 0;
}

function serverErrorMessage(error: unknown): string {
  if (!(error instanceof PublicApiError)) {
    return "设置没有保存。当前更改仍保留，可以安全重试。";
  }
  if (error.code === "MEDIA_NOT_FOUND") {
    return "刚才的头像已不可用。请重新选择图片后再保存。";
  }
  if (error.code === "CANCELLED") {
    return "保存已取消。当前更改仍保留。";
  }
  if (error.code === "INVALID_ARGUMENT") {
    return "有一项设置未通过服务器校验，请检查标出的字段。";
  }
  return "设置没有保存。当前更改仍保留，可以安全重试。";
}

function formatInstant(value: string | null, timezone: string): string {
  if (!value) return "尚无记录";
  try {
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: timezone,
    }).format(new Date(value));
  } catch {
    return new Intl.DateTimeFormat("zh-CN", {
      dateStyle: "medium",
      timeStyle: "short",
      timeZone: "UTC",
    }).format(new Date(value));
  }
}

function avatarURL(mediaId: string | null): string | null {
  return mediaId
    ? `/api/v1/media/${encodeURIComponent(mediaId)}/preview`
    : null;
}

export function SettingsPage({
  site,
  onSaved,
}: {
  site: SiteIdentity;
  onSaved: (site: SiteIdentity) => void;
}) {
  const navigation = useNavigation();
  const { api, session, expireIfInvalidSession, expireAfterTokenRotation } =
    useOwner();
  const [baseline, setBaseline] = useState(site);
  const [form, setForm] = useState(() => formFromSite(site));
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [savePending, setSavePending] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [saveNotice, setSaveNotice] = useState("设置已与站点同步。");
  const [status, setStatus] = useState<OwnerStatus | null>(null);
  const [statusPending, setStatusPending] = useState(true);
  const [statusError, setStatusError] = useState(false);
  const [statusAttempt, setStatusAttempt] = useState(0);
  const [rotationPending, setRotationPending] = useState(false);
  const [rotationError, setRotationError] = useState<string | null>(null);
  const [rotated, setRotated] = useState<RotatedCredential | null>(null);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  const [avatarMediaId, setAvatarMediaId] = useState(site.avatarMediaId);
  const [avatarPreview, setAvatarPreview] = useState(
    avatarURL(site.avatarMediaId),
  );
  const [avatarFile, setAvatarFile] = useState<File | null>(null);
  const [avatarState, setAvatarState] = useState<AvatarState>("idle");
  const [avatarProgress, setAvatarProgress] = useState(0);
  const [avatarError, setAvatarError] = useState<string | null>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const localPreviewRef = useRef<string | null>(null);
  const avatarUploadRef = useRef<AbortController | null>(null);
  const mountedRef = useRef(true);
  const navigationBlockerId = useRef(Symbol("settings-navigation-blocker"));
  const allowLeaveRef = useRef(false);

  const validation = useMemo(() => validateForm(form), [form]);
  const avatarAttemptPending = avatarFile !== null && avatarState !== "ready";
  const update = changedSiteFields(
    form,
    baseline,
    avatarMediaId,
    !avatarAttemptPending,
  );
  const dirty = hasFields(update) || avatarAttemptPending;
  const canSave =
    hasFields(update) &&
    Object.keys(validation).length === 0 &&
    !savePending &&
    avatarState !== "uploading" &&
    session !== null;

  const releaseLocalPreview = () => {
    if (localPreviewRef.current) {
      URL.revokeObjectURL(localPreviewRef.current);
      localPreviewRef.current = null;
    }
  };

  useEffect(
    () => () => {
      mountedRef.current = false;
      avatarUploadRef.current?.abort();
      releaseLocalPreview();
    },
    [],
  );

  useEffect(() => {
    if (!session || rotated) {
      setStatusPending(false);
      return;
    }
    const controller = new AbortController();
    setStatusPending(true);
    setStatusError(false);
    void api
      .status(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setStatus(next);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (!expireIfInvalidSession(error)) setStatusError(true);
      })
      .finally(() => {
        if (!controller.signal.aborted) setStatusPending(false);
      });
    return () => controller.abort();
  }, [api, expireIfInvalidSession, rotated, session, statusAttempt]);

  useEffect(() => {
    const protectOneTimeCredential = rotated !== null;
    const shouldBlock = dirty || protectOneTimeCredential;
    const confirmLeave = () =>
      window.confirm(
        protectOneTimeCredential
          ? "新的所有者凭据只显示这一次。确认已经保存并离开吗？"
          : "站点设置还没有保存。确定离开吗？",
      );
    navigation.setNavigationBlocker(
      navigationBlockerId.current,
      shouldBlock && !allowLeaveRef.current ? confirmLeave : null,
    );
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!shouldBlock || allowLeaveRef.current) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      window.removeEventListener("beforeunload", beforeUnload);
      navigation.setNavigationBlocker(navigationBlockerId.current, null);
    };
  }, [dirty, navigation, rotated]);

  const updateForm = <Key extends keyof SettingsForm>(
    field: Key,
    value: SettingsForm[Key],
  ) => {
    setForm((current) => ({ ...current, [field]: value }));
    setFieldErrors((current) => ({ ...current, [field]: undefined }));
    setSaveError(null);
    setSaveNotice("有尚未保存的更改。");
  };

  const uploadAvatar = async (file: File) => {
    if (!session) return;
    avatarUploadRef.current?.abort();
    const controller = new AbortController();
    avatarUploadRef.current = controller;
    setAvatarState("uploading");
    setAvatarProgress(0);
    setAvatarError(null);
    setSaveNotice("正在准备受管头像…");
    try {
      const uploaded = await api.uploadMedia(
        session.csrfToken,
        file,
        (progress) => {
          if (mountedRef.current && avatarUploadRef.current === controller) {
            setAvatarProgress(progress);
          }
        },
        controller.signal,
      );
      if (!mountedRef.current || avatarUploadRef.current !== controller) return;
      releaseLocalPreview();
      setAvatarMediaId(uploaded.id);
      setAvatarPreview(uploaded.previewUrl);
      setAvatarState("ready");
      setAvatarProgress(100);
      setSaveNotice("头像已上传，保存设置后才会公开使用。");
    } catch (error) {
      if (!mountedRef.current || controller.signal.aborted) return;
      expireIfInvalidSession(error);
      setAvatarState("failed");
      setAvatarError(
        error instanceof PublicApiError &&
          error.code === "MEDIA_TYPE_UNSUPPORTED"
          ? "服务器不支持这张图片的格式，请换一张。"
          : "头像没有上传完成。其他资料仍可单独保存，也可以重试头像。",
      );
      setSaveNotice("头像仍待处理。");
    }
  };

  const selectAvatar = (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0] ?? null;
    event.currentTarget.value = "";
    if (!file) return;
    const error = fileValidation(file, []);
    if (error) {
      setAvatarError(error);
      setAvatarState("failed");
      return;
    }
    releaseLocalPreview();
    localPreviewRef.current = URL.createObjectURL(file);
    setAvatarFile(file);
    setAvatarPreview(localPreviewRef.current);
    setAvatarError(null);
    void uploadAvatar(file);
  };

  const removeAvatar = () => {
    avatarUploadRef.current?.abort();
    avatarUploadRef.current = null;
    releaseLocalPreview();
    setAvatarFile(null);
    setAvatarMediaId(null);
    setAvatarPreview(null);
    setAvatarState("idle");
    setAvatarProgress(0);
    setAvatarError(null);
    setSaveError(null);
    setSaveNotice("头像将在保存后移除。");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    const errors = validateForm(form);
    setFieldErrors(errors);
    if (Object.keys(errors).length > 0 || !canSave || !session) return;
    const submitted = changedSiteFields(
      form,
      baseline,
      avatarMediaId,
      !avatarAttemptPending,
    );
    if (!hasFields(submitted)) return;
    setSavePending(true);
    setSaveError(null);
    setSaveNotice("正在保存站点设置…");
    try {
      const saved = await api.updateSite(session.csrfToken, submitted);
      if (!mountedRef.current) return;
      const next = { ...saved, instanceId: baseline.instanceId };
      setBaseline(next);
      setForm(formFromSite(next));
      if (submitted.avatarMediaId !== undefined) {
        releaseLocalPreview();
        setAvatarFile(null);
        setAvatarMediaId(next.avatarMediaId);
        setAvatarPreview(avatarURL(next.avatarMediaId));
        setAvatarState("idle");
        setAvatarProgress(0);
        setAvatarError(null);
      }
      setFieldErrors({});
      setSaveNotice(
        avatarAttemptPending
          ? "资料已保存；头像仍待重试或移除。"
          : "设置已保存，公开站点身份已更新。",
      );
      onSaved(next);
    } catch (error) {
      if (!mountedRef.current) return;
      const expired = expireIfInvalidSession(error);
      if (
        error instanceof PublicApiError &&
        error.code === "INVALID_ARGUMENT" &&
        typeof error.details.field === "string"
      ) {
        const field = error.details.field;
        if (field === "timezone") {
          setFieldErrors((current) => ({
            ...current,
            timezone: "这个时区未被服务器接受，请使用有效的 IANA 名称。",
          }));
        }
        if (field === "public_base_url") {
          setFieldErrors((current) => ({
            ...current,
            publicBaseUrl:
              "这个公开地址未被服务器接受；生产站点必须使用 HTTPS。",
          }));
        }
      }
      setSaveError(
        expired
          ? "会话已过期。更改仍保留，请重新登录后重试保存。"
          : serverErrorMessage(error),
      );
      setSaveNotice("保存失败，当前更改仍保留。");
    } finally {
      if (mountedRef.current) setSavePending(false);
    }
  };

  const rotate = async () => {
    if (!session || rotationPending) return;
    setRotationPending(true);
    setRotationError(null);
    setCopyState("idle");
    try {
      const credential = await api.rotateToken(session.csrfToken);
      if (!mountedRef.current) return;
      setRotated(credential);
      setStatus({
        initialized: credential.initialized,
        sessionEpoch: credential.sessionEpoch,
        initializedAt: credential.initializedAt,
        rotatedAt: credential.rotatedAt,
      });
      expireAfterTokenRotation();
    } catch (error) {
      if (!mountedRef.current) return;
      const expired = expireIfInvalidSession(error);
      setRotationError(
        expired
          ? "当前会话已失效，凭据没有轮换。重新登录后可以再试。"
          : "凭据没有轮换，旧凭据与现有会话仍然有效。可以安全重试。",
      );
    } finally {
      if (mountedRef.current) setRotationPending(false);
    }
  };

  const copyToken = async () => {
    if (!rotated) return;
    try {
      await navigator.clipboard.writeText(rotated.token);
      setCopyState("copied");
    } catch {
      setCopyState("failed");
    }
  };

  const goToLogin = () => {
    allowLeaveRef.current = true;
    setRotated(null);
    navigation.navigate("/owner/login?return=/owner/settings");
  };

  const title = form.title.trim() || "Moment";
  const avatarFallback = Array.from(title)[0]?.toLocaleUpperCase() ?? "M";
  const effectiveErrors: FieldErrors = {
    timezone: validation.timezone ?? fieldErrors.timezone,
    publicBaseUrl: validation.publicBaseUrl ?? fieldErrors.publicBaseUrl,
  };

  return (
    <div className={styles.settingsPage}>
      <section aria-label="当前身份预览" className={styles.identityProof}>
        <div className={styles.identityMark}>
          {avatarPreview ? (
            <img alt="" decoding="async" src={avatarPreview} />
          ) : (
            <span aria-hidden="true">{avatarFallback}</span>
          )}
        </div>
        <div className={styles.identityCopy}>
          <p>公开身份预览</p>
          <h2>{title}</h2>
          <span>{form.bio.trim() || "记下此刻，找回后来"}</span>
        </div>
        <dl className={styles.identityCoordinates}>
          <div>
            <dt>时区</dt>
            <dd>{form.timezone.trim() || "UTC"}</dd>
          </div>
          <div>
            <dt>公开地址</dt>
            <dd>{form.publicBaseUrl.trim() || "尚未设置"}</dd>
          </div>
        </dl>
      </section>

      <div className={styles.settingsLayout}>
        <nav aria-label="设置分区" className={styles.settingsNav}>
          <a href="#identity">站点身份</a>
          <a href="#conversation">访客互动</a>
          <a href="#access">访问凭据</a>
          <a href="#data">备份与恢复</a>
        </nav>

        <div className={styles.settingsContent}>
          <form noValidate onSubmit={(event) => void save(event)}>
            <section aria-labelledby="identity-title" id="identity">
              <header className={styles.sectionHeader}>
                <div>
                  <p>Public colophon</p>
                  <h2 id="identity-title">站点身份</h2>
                </div>
                <span>公开可见</span>
              </header>

              <div className={styles.avatarEditor}>
                <div className={styles.avatarPreview}>
                  {avatarPreview ? (
                    <img alt="当前头像预览" src={avatarPreview} />
                  ) : (
                    <span aria-hidden="true">{avatarFallback}</span>
                  )}
                </div>
                <div>
                  <strong>头像</strong>
                  <p>上传后由 Moment 管理原图与预览，不接受远程图片地址。</p>
                  <div className={styles.inlineActions}>
                    <button
                      className={styles.secondaryButton}
                      disabled={!session || avatarState === "uploading"}
                      onClick={() => fileInputRef.current?.click()}
                      type="button"
                    >
                      {avatarState === "uploading" ? "正在上传…" : "选择图片"}
                    </button>
                    {avatarPreview || baseline.avatarMediaId ? (
                      <button
                        className={styles.quietButton}
                        disabled={!session || avatarState === "uploading"}
                        onClick={removeAvatar}
                        type="button"
                      >
                        移除头像
                      </button>
                    ) : null}
                    {avatarState === "failed" && avatarFile ? (
                      <button
                        className={styles.quietButton}
                        disabled={!session}
                        onClick={() => void uploadAvatar(avatarFile)}
                        type="button"
                      >
                        重试头像
                      </button>
                    ) : null}
                  </div>
                  <input
                    accept="image/jpeg,image/png,image/webp,image/gif"
                    aria-label="选择站点头像"
                    disabled={!session || avatarState === "uploading"}
                    onChange={selectAvatar}
                    ref={fileInputRef}
                    tabIndex={-1}
                    type="file"
                  />
                  {avatarState === "uploading" ? (
                    <progress
                      aria-label="头像上传进度"
                      max="100"
                      value={avatarProgress}
                    />
                  ) : null}
                  {avatarError ? (
                    <p className={styles.fieldError} role="alert">
                      {avatarError}
                    </p>
                  ) : null}
                </div>
              </div>

              <div className={styles.fieldGrid}>
                <label className={styles.fullField}>
                  <span>站点标题</span>
                  <input
                    onChange={(event) =>
                      updateForm("title", event.target.value)
                    }
                    value={form.title}
                  />
                  <small>显示在站点导航、时间流署名与浏览器标题中。</small>
                </label>
                <label className={styles.fullField}>
                  <span>简介</span>
                  <textarea
                    onChange={(event) => updateForm("bio", event.target.value)}
                    rows={3}
                    value={form.bio}
                  />
                  <small>用一两句话说明这段个人时间流。</small>
                </label>
                <label>
                  <span>站点时区</span>
                  <input
                    aria-describedby={
                      effectiveErrors.timezone ? "timezone-error" : undefined
                    }
                    aria-invalid={Boolean(effectiveErrors.timezone)}
                    list="moment-timezones"
                    onChange={(event) =>
                      updateForm("timezone", event.target.value)
                    }
                    spellCheck={false}
                    value={form.timezone}
                  />
                  <datalist id="moment-timezones">
                    {commonTimezones.map((timezone) => (
                      <option key={timezone} value={timezone} />
                    ))}
                  </datalist>
                  {effectiveErrors.timezone ? (
                    <small className={styles.fieldError} id="timezone-error">
                      {effectiveErrors.timezone}
                    </small>
                  ) : (
                    <small>日期归档与公开时间会按此 IANA 时区解释。</small>
                  )}
                </label>
                <label>
                  <span>公开站点地址</span>
                  <input
                    aria-describedby={
                      effectiveErrors.publicBaseUrl
                        ? "public-url-error"
                        : undefined
                    }
                    aria-invalid={Boolean(effectiveErrors.publicBaseUrl)}
                    inputMode="url"
                    onChange={(event) =>
                      updateForm("publicBaseUrl", event.target.value)
                    }
                    placeholder="https://moment.example.com"
                    spellCheck={false}
                    type="url"
                    value={form.publicBaseUrl}
                  />
                  {effectiveErrors.publicBaseUrl ? (
                    <small className={styles.fieldError} id="public-url-error">
                      {effectiveErrors.publicBaseUrl}
                    </small>
                  ) : (
                    <small>
                      用于规范链接与分享卡片；留空会安全省略绝对地址。
                    </small>
                  )}
                </label>
              </div>
            </section>

            <section aria-labelledby="conversation-title" id="conversation">
              <header className={styles.sectionHeader}>
                <div>
                  <p>Conversation</p>
                  <h2 id="conversation-title">访客互动</h2>
                </div>
              </header>
              <label className={styles.toggleRow}>
                <span>
                  <strong>允许新评论</strong>
                  <small>
                    关闭后已有评论仍可阅读，只停止新的访客评论与回复。
                  </small>
                </span>
                <input
                  checked={form.commentsEnabled}
                  onChange={(event) =>
                    updateForm("commentsEnabled", event.target.checked)
                  }
                  role="switch"
                  type="checkbox"
                />
              </label>
            </section>

            <footer className={styles.saveBar}>
              <p
                data-state={
                  savePending
                    ? "submitting"
                    : saveError
                      ? "failed"
                      : dirty
                        ? "dirty"
                        : "saved"
                }
                role={saveError ? "alert" : "status"}
              >
                {saveError ?? saveNotice}
              </p>
              <button
                className={styles.primaryButton}
                disabled={!canSave}
                type="submit"
              >
                {savePending ? "正在保存…" : "保存设置"}
              </button>
            </footer>
          </form>

          <section aria-labelledby="access-title" id="access">
            <header className={styles.sectionHeader}>
              <div>
                <p>Owner access</p>
                <h2 id="access-title">访问凭据</h2>
              </div>
              <span>私密</span>
            </header>
            {statusPending ? (
              <p aria-busy="true">正在读取认证状态…</p>
            ) : statusError ? (
              <div className={styles.inlineError} role="alert">
                <span>暂时无法读取认证状态。</span>
                <button
                  className={styles.quietButton}
                  onClick={() => setStatusAttempt((value) => value + 1)}
                  type="button"
                >
                  重试状态
                </button>
              </div>
            ) : status ? (
              <dl className={styles.authFacts}>
                <div>
                  <dt>所有者认证</dt>
                  <dd>{status.initialized ? "已初始化" : "未初始化"}</dd>
                </div>
                <div>
                  <dt>会话代次</dt>
                  <dd>{status.sessionEpoch}</dd>
                </div>
                <div>
                  <dt>当前会话到期</dt>
                  <dd>
                    {formatInstant(session?.expiresAt ?? null, form.timezone)}
                  </dd>
                </div>
                <div>
                  <dt>最近轮换</dt>
                  <dd>{formatInstant(status.rotatedAt, form.timezone)}</dd>
                </div>
              </dl>
            ) : null}

            {rotated ? (
              <div className={styles.oneTimeCredential} role="status">
                <p>只显示这一次</p>
                <h3>新的所有者凭据已经生成</h3>
                <code>{rotated.token}</code>
                <p>
                  立即复制到密码管理器或安全文件。旧凭据和所有旧 Web
                  会话已经撤销，包括当前页面的会话。
                </p>
                <div className={styles.inlineActions}>
                  <button
                    className={styles.primaryButton}
                    onClick={() => void copyToken()}
                    type="button"
                  >
                    复制新凭据
                  </button>
                  <button
                    className={styles.secondaryButton}
                    onClick={goToLogin}
                    type="button"
                  >
                    我已保存，重新登录
                  </button>
                </div>
                <p aria-live="polite">
                  {copyState === "copied"
                    ? "已复制。请确认密码管理器或安全文件中能够找回。"
                    : copyState === "failed"
                      ? "浏览器未允许复制。请手动选择上方凭据并保存。"
                      : "离开本页后，Moment 不会再次显示这段凭据。"}
                </p>
              </div>
            ) : (
              <div className={styles.rotationRow}>
                <div>
                  <strong>轮换所有者凭据</strong>
                  <p>
                    轮换会立即撤销旧 bearer 凭据和全部 Web
                    会话。新凭据只返回一次，必须先安全保存再重新登录。
                  </p>
                </div>
                <ConfirmDialog
                  confirmLabel="确认轮换"
                  danger
                  description="继续后，旧凭据与所有已登录浏览器会立即失效，当前页面也需要使用新凭据重新登录。新凭据只显示一次。"
                  onConfirm={rotate}
                  pending={rotationPending}
                  title="轮换所有者凭据？"
                  trigger={
                    <button
                      className={styles.dangerButton}
                      disabled={!session || rotationPending || dirty}
                      type="button"
                    >
                      轮换凭据
                    </button>
                  }
                />
              </div>
            )}
            {rotationError ? (
              <p className={styles.fieldError} role="alert">
                {rotationError}
              </p>
            ) : null}
            {!session && !rotated ? <OwnerLogin embedded site={site} /> : null}
          </section>

          <section aria-labelledby="data-title" id="data">
            <header className={styles.sectionHeader}>
              <div>
                <p>Portable data</p>
                <h2 id="data-title">备份与恢复</h2>
              </div>
              <span>后续接入</span>
            </header>
            <p className={styles.futureNote}>
              这里已经为整站导出、恢复预检与替换确认保留位置。本页暂不提供这些操作；当前仍可通过现有
              API 或 CLI 管理备份。
            </p>
          </section>
        </div>
      </div>
    </div>
  );
}
