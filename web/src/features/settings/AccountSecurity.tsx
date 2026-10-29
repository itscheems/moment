import { getLocale } from "../../i18n";
import { useTranslation } from "react-i18next";
import { useLoginNavigation } from "../auth/loginNavigation";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { PublicApiError } from "../../api/public";
import { useEffect, useState, type FormEvent } from "react";
import {
  accountSecurity,
  createApiCredential,
  listApiCredentials,
  revokeApiCredential,
  setAccountPassword,
  revokeAllCredentials,
  type ApiCredential,
} from "../../api/accounts";
import { useSession } from "../auth/SessionProvider";
import forms from "../../components/Forms.module.css";
import styles from "./Settings.module.css";

export function AccountSecurity({ instanceId }: { instanceId: string | null }) {
  const { t } = useTranslation();

  const { session, account, forgetSession, expireIfInvalidSession } =
    useSession();
  const { requestLogin } = useLoginNavigation("/settings/account");
  const [passwordEnabled, setPasswordEnabled] = useState(false);
  const [password, setPassword] = useState("");
  const [label, setLabel] = useState("");
  const [tokens, setTokens] = useState<ApiCredential[]>([]);
  const [receipt, setReceipt] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);
  const [pending, setPending] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    void Promise.all([
      accountSecurity(controller.signal),
      listApiCredentials(controller.signal),
    ])
      .then(([security, list]) => {
        if (controller.signal.aborted) return;
        setPasswordEnabled(security.passwordEnabled);
        setTokens(list);
        setError(null);
      })
      .catch((next: unknown) => {
        if (!controller.signal.aborted && !expireIfInvalidSession(next))
          setError(t("登录凭据暂时无法读取，可以重试。"));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [attempt, expireIfInvalidSession]);
  async function savePassword(event: FormEvent) {
    event.preventDefault();
    if (!session || pending) return;
    setPending("password");
    setError(null);
    try {
      await setAccountPassword(session.csrfToken, password);
      setPassword("");
      forgetSession(instanceId);
      requestLogin();
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError(failure(next, t("密码没有更新，请检查输入后重试。")));
    } finally {
      setPending(null);
    }
  }
  async function issue(event: FormEvent) {
    event.preventDefault();
    if (!session || pending) return;
    setPending("create");
    setError(null);
    try {
      const created = await createApiCredential(session.csrfToken, label);
      setReceipt(created.token);
      setCopied(false);
      setTokens((current) => [created.credential, ...current]);
      setLabel("");
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError(failure(next, t("API 令牌没有创建，请重试。")));
    } finally {
      setPending(null);
    }
  }
  async function revoke(token: ApiCredential) {
    if (!session || pending) return;
    setPending(token.id);
    setError(null);
    try {
      if (await revokeApiCredential(session.csrfToken, token.id)) {
        setReceipt(null);
        forgetSession(instanceId);
        requestLogin();
        return;
      }
      setTokens((current) =>
        current.map((item) =>
          item.id === token.id ? { ...item, revoked_at_ms: Date.now() } : item,
        ),
      );
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError(failure(next, t("令牌未撤销，可以重试。")));
    } finally {
      setPending(null);
    }
  }
  function failure(next: unknown, fallback: string) {
    return next instanceof PublicApiError &&
      next.code === "REAUTHENTICATION_REQUIRED"
      ? t("身份验证未完成，操作未执行。")
      : fallback;
  }
  async function revokeAll() {
    if (!session || pending) return;
    setPending("all");
    setError(null);
    try {
      await revokeAllCredentials(session.csrfToken);
      setReceipt(null);
      forgetSession(instanceId);
      requestLogin();
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError(failure(next, t("凭据未全部撤销，请重试。")));
    } finally {
      setPending(null);
    }
  }
  return (
    <section
      aria-label={t("密码和 API 令牌")}
      className={styles.securitySection}
    >
      <header className={styles.sectionHeader}>
        <h2>{t("密码和 API 令牌")}</h2>
      </header>
      {loading ? <p aria-busy="true">{t("正在读取账号凭据…")}</p> : null}
      {error ? (
        <p className={forms.formError} role="alert">
          {error}
          <button
            className={styles.quietButton}
            onClick={() => setAttempt((value) => value + 1)}
            type="button"
          >
            {t("重试")}
          </button>
        </p>
      ) : null}
      <form
        className={styles.settingsForm}
        onSubmit={(event) => void savePassword(event)}
      >
        <label>
          {t("新密码")}
          <input
            autoComplete="new-password"
            disabled={pending !== null}
            onChange={(event) => setPassword(event.target.value)}
            required
            type="password"
            value={password}
          />
        </label>
        <p>
          {passwordEnabled
            ? t(
                "修改密码会撤销全部 API 令牌和浏览器会话，需要重新登录并重新配置 API 客户端。",
              )
            : t(
                "设置密码后，可使用用户名和密码登录。现有 API 令牌和浏览器会话将全部撤销。",
              )}
        </p>
        <button
          className={styles.primaryButton}
          disabled={!session || !password || pending !== null}
          type="submit"
        >
          {pending === "password" ? t("正在保存…") : t("保存密码并重新登录")}
        </button>
      </form>
      <h3 className={styles.securityHeading}>{t("API 令牌")}</h3>
      <p>
        {t(
          "每个令牌仅代表你的账号，并使用账号当前的权限。撤销令牌也会结束通过它登录的会话。",
        )}
      </p>
      <form
        className={`${styles.settingsForm} ${styles.tokenForm}`}
        onSubmit={(event) => void issue(event)}
      >
        <label>
          {t("令牌名称")}
          <input
            disabled={pending !== null || account?.status !== "active"}
            maxLength={80}
            onChange={(event) => setLabel(event.target.value)}
            placeholder={t("例如：我的电脑")}
            value={label}
          />
        </label>
        <button
          className={styles.secondaryButton}
          disabled={
            !session || pending !== null || account?.status !== "active"
          }
          type="submit"
        >
          {pending === "create" ? t("正在创建…") : t("创建 API 令牌")}
        </button>
      </form>
      {receipt ? (
        <div className={styles.oneTimeCredential} role="status">
          <p>{t("请保存此令牌，它只显示这一次。")}</p>
          <textarea
            aria-label={t("新 API 令牌")}
            readOnly
            rows={2}
            value={receipt}
            spellCheck={false}
          />
          <div className={styles.inlineActions}>
            <button
              className={styles.primaryButton}
              onClick={() =>
                void navigator.clipboard
                  .writeText(receipt)
                  .then(() => setCopied(true))
                  .catch(() => setError(t("未能复制，请选择令牌后手动复制。")))
              }
              type="button"
            >
              {copied ? t("已复制") : t("复制令牌")}
            </button>
            <button
              className={styles.quietButton}
              onClick={() => setReceipt(null)}
              type="button"
            >
              {t("已保存，隐藏令牌")}
            </button>
          </div>
        </div>
      ) : null}
      <ul className={styles.credentialList}>
        {tokens.map((token) => (
          <li key={token.id}>
            <div className={styles.credentialInfo}>
              <span id={`token-label-${token.id}`}>
                {token.label || t("未命名令牌")}
              </span>
              <small>
                {new Date(token.created_at_ms).toLocaleDateString(getLocale())}{" "}
                {t("创建")}
              </small>
            </div>
            {token.revoked_at_ms === null ? (
              <button
                className={styles.revokeToken}
                aria-describedby={`token-label-${token.id}`}
                disabled={pending !== null}
                onClick={() => void revoke(token)}
                type="button"
              >
                {pending === token.id ? t("正在撤销…") : t("撤销")}
              </button>
            ) : (
              <span className={styles.revokedToken}>{t("已撤销")}</span>
            )}
          </li>
        ))}
      </ul>
      <div className={styles.revokeAll}>
        <ConfirmDialog
          title={t("撤销全部访问凭据？")}
          description={t(
            "全部 API 令牌和浏览器会话都会失效，包括当前会话。密码和已关联的第三方登录方式会保留；如果账号没有其他登录方式，请先设置密码。",
          )}
          confirmLabel={t("撤销全部凭据并退出")}
          cancelLabel={t("取消")}
          danger
          pending={pending !== null}
          trigger={
            <button
              type="button"
              className={styles.revokeToken}
              disabled={!session || pending !== null}
            >
              {t("撤销全部访问凭据")}
            </button>
          }
          onConfirm={revokeAll}
        />
      </div>
    </section>
  );
}
