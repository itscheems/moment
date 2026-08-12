import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";

import type {
  OwnerStatus,
  RotatedCredential,
  SiteSettingsUpdate,
} from "../../api/owner";
import { PublicApiError, type SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { BackupRestore } from "./BackupRestore";
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
  timezone: string;
  publicBaseUrl: string;
  commentsEnabled: boolean;
}

interface FieldErrors {
  timezone?: string;
  publicBaseUrl?: string;
}

function formFromSite(site: SiteIdentity): SettingsForm {
  return {
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
    errors.timezone = "请输入有效的时区，例如 Asia/Shanghai。";
  }

  const publicBaseUrl = normalizedPublicBaseUrl(form.publicBaseUrl);
  if (publicBaseUrl) {
    try {
      const parsed = new URL(publicBaseUrl);
      const hostname = parsed.hostname.replace(/^\[|\]$/g, "");
      const localHTTP =
        parsed.protocol === "http:" &&
        (hostname === "localhost" ||
          hostname === "127.0.0.1" ||
          hostname === "::1");
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
): SiteSettingsUpdate {
  const update: SiteSettingsUpdate = {};
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
  return update;
}

function hasFields(update: SiteSettingsUpdate): boolean {
  return Object.keys(update).length > 0;
}

function rebaseEditedForm(
  latest: SettingsForm,
  submitted: SettingsForm,
  saved: SiteIdentity,
): SettingsForm {
  const confirmed = formFromSite(saved);
  return {
    timezone:
      latest.timezone === submitted.timezone
        ? confirmed.timezone
        : latest.timezone,
    publicBaseUrl:
      latest.publicBaseUrl === submitted.publicBaseUrl
        ? confirmed.publicBaseUrl
        : latest.publicBaseUrl,
    commentsEnabled:
      latest.commentsEnabled === submitted.commentsEnabled
        ? confirmed.commentsEnabled
        : latest.commentsEnabled,
  };
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
  const [rotationOutcomeUnknown, setRotationOutcomeUnknown] = useState(false);
  const [rotated, setRotated] = useState<RotatedCredential | null>(null);
  const [copyState, setCopyState] = useState<"idle" | "copied" | "failed">(
    "idle",
  );
  const [dataRestoreBusy, setDataRestoreBusy] = useState(true);
  const formRef = useRef(form);
  const formRevisionRef = useRef(0);
  const mountedRef = useRef(true);
  const navigationBlockerId = useRef(Symbol("settings-navigation-blocker"));
  const allowLeaveRef = useRef(false);

  const validation = useMemo(() => validateForm(form), [form]);
  const update = changedSiteFields(form, baseline);
  const dirty = hasFields(update);
  const canSave =
    hasFields(update) &&
    Object.keys(validation).length === 0 &&
    !savePending &&
    !dataRestoreBusy &&
    session !== null;

  useEffect(
    () => () => {
      mountedRef.current = false;
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
    const shouldBlock = dirty || protectOneTimeCredential || dataRestoreBusy;
    const confirmLeave = () =>
      window.confirm(
        dataRestoreBusy
          ? "恢复状态仍在确认。离开后服务器会继续运行，返回本页可重新查询。确定离开吗？"
          : protectOneTimeCredential
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
  }, [dataRestoreBusy, dirty, navigation, rotated]);

  const updateForm = <Key extends keyof SettingsForm>(
    field: Key,
    value: SettingsForm[Key],
  ) => {
    if (dataRestoreBusy) return;
    const next = { ...formRef.current, [field]: value };
    formRef.current = next;
    formRevisionRef.current += 1;
    setForm(next);
    setFieldErrors((current) => ({ ...current, [field]: undefined }));
    setSaveError(null);
    setSaveNotice("有尚未保存的更改。");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    if (dataRestoreBusy) return;
    const errors = validateForm(form);
    setFieldErrors(errors);
    if (Object.keys(errors).length > 0 || !canSave || !session) return;
    const submitted = changedSiteFields(form, baseline);
    if (!hasFields(submitted)) return;
    const submittedForm = formRef.current;
    const submittedFormRevision = formRevisionRef.current;
    setSavePending(true);
    setSaveError(null);
    setSaveNotice("正在保存站点设置…");
    try {
      const saved = await api.updateSite(session.csrfToken, submitted);
      if (!mountedRef.current) return;
      const next = { ...saved, instanceId: baseline.instanceId };
      const editedWhileSaving =
        formRevisionRef.current !== submittedFormRevision;
      const nextForm = editedWhileSaving
        ? rebaseEditedForm(formRef.current, submittedForm, next)
        : formFromSite(next);
      setBaseline(next);
      formRef.current = nextForm;
      setForm(nextForm);
      setFieldErrors({});
      setSaveNotice(
        editedWhileSaving
          ? "之前提交的设置已保存；新的更改仍待保存。"
          : "设置已保存。",
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
            timezone: "这个时区不可用，请选择其他时区。",
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
    if (!session || rotationPending || dataRestoreBusy) return;
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
      const deterministicServerFailure =
        error instanceof PublicApiError &&
        error.status >= 400 &&
        error.code !== "API_RESPONSE_INVALID";
      if (!deterministicServerFailure) {
        setRotationOutcomeUnknown(true);
        setRotationError(null);
        expireAfterTokenRotation();
      } else {
        const expired = expireIfInvalidSession(error);
        setRotationError(
          expired
            ? "服务器拒绝了当前会话，未返回新凭据。请重新登录后再确认是否轮换。"
            : "服务器明确返回轮换失败，未返回新凭据。修复服务问题后可重新确认。",
        );
      }
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
    setRotationOutcomeUnknown(false);
    navigation.navigate("/owner/login?return=/owner/settings");
  };

  const effectiveErrors: FieldErrors = {
    timezone: validation.timezone ?? fieldErrors.timezone,
    publicBaseUrl: validation.publicBaseUrl ?? fieldErrors.publicBaseUrl,
  };

  return (
    <div className={styles.settingsPage}>
      <div className={styles.settingsLayout}>
        <nav aria-label="设置分区" className={styles.settingsNav}>
          <a href="#identity">网站</a>
          <a href="#conversation">回复</a>
          <a href="#access">登录与安全</a>
          <a href="#data">备份</a>
        </nav>

        <div className={styles.settingsContent}>
          <form
            aria-disabled={dataRestoreBusy}
            inert={dataRestoreBusy ? true : undefined}
            noValidate
            onSubmit={(event) => void save(event)}
          >
            <section aria-labelledby="identity-title" id="identity">
              <header className={styles.sectionHeader}>
                <h2 id="identity-title">网站</h2>
              </header>

              <div className={styles.fieldGrid}>
                <label>
                  <span>时区</span>
                  <input
                    aria-describedby={
                      effectiveErrors.timezone ? "timezone-error" : undefined
                    }
                    aria-invalid={Boolean(effectiveErrors.timezone)}
                    list="moment-timezones"
                    disabled={dataRestoreBusy}
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
                    <small>用于显示时间和归档日期。</small>
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
                    disabled={dataRestoreBusy}
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
                    <small>用于分享链接；留空时使用当前网站地址。</small>
                  )}
                </label>
              </div>
            </section>

            <section aria-labelledby="conversation-title" id="conversation">
              <header className={styles.sectionHeader}>
                <h2 id="conversation-title">回复</h2>
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
                  disabled={dataRestoreBusy}
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
              <h2 id="access-title">登录与安全</h2>
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
              <>
                <dl className={styles.authFacts}>
                  <div>
                    <dt>登录状态</dt>
                    <dd>{status.initialized ? "已初始化" : "未初始化"}</dd>
                  </div>
                  <div>
                    <dt>当前登录到期</dt>
                    <dd>
                      {formatInstant(session?.expiresAt ?? null, form.timezone)}
                    </dd>
                  </div>
                  <div>
                    <dt>最近轮换</dt>
                    <dd>{formatInstant(status.rotatedAt, form.timezone)}</dd>
                  </div>
                </dl>
                <details className={styles.technicalDetails}>
                  <summary>技术信息</summary>
                  <p>登录版本 {status.sessionEpoch}</p>
                </details>
              </>
            ) : null}

            {rotated ? (
              <div className={styles.oneTimeCredential} role="status">
                <p>只显示这一次</p>
                <h3>新的登录密钥已经生成</h3>
                <code>{rotated.token}</code>
                <p>
                  立即复制到密码管理器或安全文件。旧登录密钥和全部已登录浏览器已经失效，包括当前页面。
                </p>
                <div className={styles.inlineActions}>
                  <button
                    className={styles.primaryButton}
                    onClick={() => void copyToken()}
                    type="button"
                  >
                    复制登录密钥
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
                      ? "浏览器未允许复制。请手动选择上方密钥并保存。"
                      : "离开本页后，Moment 不会再次显示这段密钥。"}
                </p>
              </div>
            ) : rotationOutcomeUnknown ? (
              <div className={styles.rotationRecovery} role="alert">
                <p>结果无法确认</p>
                <h3>不要再次从浏览器轮换</h3>
                <p>
                  请求可能已在服务器完成，但结果尚未确认。当前页面已退出登录，也不会重复更换密钥。
                </p>
                <p>
                  先停止 Moment
                  服务，在实例主机离线运行以下命令；立即保存只显示一次的新密钥，再回来重新登录。
                </p>
                <code>
                  {"moment owner rotate-token --data-dir <数据目录> --json"}
                </code>
                <button
                  className={styles.secondaryButton}
                  onClick={goToLogin}
                  type="button"
                >
                  离线恢复完成，重新登录
                </button>
              </div>
            ) : (
              <div className={styles.rotationRow}>
                <div>
                  <strong>更换登录密钥</strong>
                  <p>
                    更换后，旧密钥和全部已登录浏览器会立即失效。新密钥只显示一次，请保存后再重新登录。
                  </p>
                </div>
                <ConfirmDialog
                  confirmLabel="确认更换"
                  danger
                  description="继续后，旧密钥与所有已登录浏览器会立即失效，当前页面也需要使用新密钥重新登录。新密钥只显示一次。"
                  onConfirm={rotate}
                  pending={rotationPending}
                  title="更换登录密钥？"
                  trigger={
                    <button
                      className={styles.dangerButton}
                      disabled={
                        !session || rotationPending || dirty || dataRestoreBusy
                      }
                      type="button"
                    >
                      更换密钥
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
            {!session && !rotated && !rotationOutcomeUnknown ? (
              <OwnerLogin embedded site={site} />
            ) : null}
          </section>

          <section aria-labelledby="data-title" id="data">
            <header className={styles.sectionHeader}>
              <h2 id="data-title">备份与恢复</h2>
            </header>
            <BackupRestore
              onRestoreBusyChange={setDataRestoreBusy}
              settingsBlocked={
                dirty || savePending || rotationPending || rotated !== null
              }
            />
          </section>
        </div>
      </div>
    </div>
  );
}
