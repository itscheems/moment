import { useEffect, useRef, useState, type FormEvent } from "react";
import { Dialog } from "../../components/Dialog";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  accountSecurity,
  confirmAccountCredential,
  credentialConfirmationStatus,
  accountsApi,
} from "../../api/accounts";
import { useSession } from "../auth/SessionProvider";
import { oauthStartUrl, providerLabel } from "../auth/providers";
import forms from "../../components/Forms.module.css";
import styles from "./Settings.module.css";

/** Confirmation is carried in a short-lived HttpOnly cookie, never web storage. */
export function CredentialConfirmation() {
  const { account, session, api, expireIfInvalidSession } = useSession();
  const input = useRef<HTMLInputElement>(null);
  const [open, setOpen] = useState(false);
  useDocumentScrollLock(open);
  const [passwordEnabled, setPasswordEnabled] = useState<boolean | null>(null);
  const [secret, setSecret] = useState("");
  const [providers, setProviders] = useState<string[]>([]);
  const [expires, setExpires] = useState<number | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setSecret("");
    setExpires(null);
    setPasswordEnabled(null);
    void credentialConfirmationStatus(controller.signal)
      .then((until) => {
        if (!controller.signal.aborted && until) setExpires(Date.parse(until));
      })
      .catch((next: unknown) => {
        if (!controller.signal.aborted && !expireIfInvalidSession(next))
          setError("确认状态暂时无法读取，请重试确认身份。");
      });
    void accountSecurity(controller.signal)
      .then((security) => {
        if (!controller.signal.aborted)
          setPasswordEnabled(security.passwordEnabled);
      })
      .catch((next: unknown) => {
        if (!controller.signal.aborted && !expireIfInvalidSession(next))
          setError("确认方式暂时无法读取，请刷新后重试。");
      });
    void Promise.all([
      accountsApi.accountIdentities(controller.signal),
      api.loginProviders(controller.signal),
    ])
      .then(([identities, available]) => {
        if (!controller.signal.aborted)
          setProviders(
            identities
              .map((identity) => identity.provider)
              .filter((provider) =>
                available.some((entry) => entry.name === provider),
              ),
          );
      })
      .catch(() => {
        if (!controller.signal.aborted) {
          setProviders([]);
          setError("第三方确认方式暂时无法读取，可使用当前凭据确认身份。");
        }
      });
    return () => controller.abort();
  }, [account?.userId, api, expireIfInvalidSession]);
  useEffect(() => {
    if (!expires) return;
    const timer = window.setTimeout(
      () => setExpires(null),
      Math.max(0, expires - Date.now()),
    );
    return () => window.clearTimeout(timer);
  }, [expires]);
  async function confirm(event: FormEvent) {
    event.preventDefault();
    if (!session || passwordEnabled === null || pending) return;
    setPending(true);
    setError(null);
    try {
      const until = await confirmAccountCredential(
        session.csrfToken,
        passwordEnabled ? { password: secret } : { token: secret },
      );
      setSecret("");
      setExpires(Date.parse(until));
      setOpen(false);
    } catch (next) {
      setSecret("");
      if (!expireIfInvalidSession(next))
        setError("凭据确认失败，请输入当前有效的凭据。");
    } finally {
      setPending(false);
    }
  }
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setSecret("");
      }}
    >
      <Dialog.Trigger
        className={`${forms.secondaryButton} ${styles.confirmationTrigger}`}
        disabled={passwordEnabled === null && !error}
      >
        {expires ? "身份已确认" : "确认身份"}
      </Dialog.Trigger>
      <Dialog.Portal>
        <Dialog.Backdrop className={forms.dialogBackdrop} />
        <Dialog.Viewport className={forms.dialogViewport}>
          <Dialog.Popup
            className={`${forms.dialogPopup} ${styles.credentialDialog}`}
            initialFocus={input}
          >
            <Dialog.Close
              aria-label="关闭身份确认"
              className={styles.dialogClose}
              disabled={pending}
            >
              ×
            </Dialog.Close>
            <Dialog.Title className={forms.dialogTitle}>
              确认敏感操作
            </Dialog.Title>
            <Dialog.Description className={forms.dialogDescription}>
              管理登录凭据、关联账号或备份数据前，请先确认身份。确认在当前会话内有效
              5 分钟。
            </Dialog.Description>
            {expires ? (
              <p role="status">已确认，可以继续操作。</p>
            ) : (
              <>
                <form
                  className={styles.settingsForm}
                  onSubmit={(event) => void confirm(event)}
                >
                  <label>
                    {passwordEnabled === null
                      ? "正在读取确认方式…"
                      : passwordEnabled
                        ? "当前密码"
                        : "当前 API 令牌"}
                    <input
                      ref={input}
                      autoComplete={
                        passwordEnabled ? "current-password" : "off"
                      }
                      disabled={pending || passwordEnabled === null}
                      type="password"
                      required
                      value={secret}
                      onChange={(event) => setSecret(event.target.value)}
                    />
                  </label>
                  <button
                    type="submit"
                    className={forms.primaryButton}
                    disabled={
                      !session || !secret || pending || passwordEnabled === null
                    }
                  >
                    {pending ? "正在确认…" : "确认身份"}
                  </button>
                </form>
                {passwordEnabled === false
                  ? providers.map((provider) => (
                      <a
                        key={provider}
                        href={`${oauthStartUrl(provider, "/settings/account")}&confirm=1`}
                      >
                        使用 {providerLabel(provider)} 确认身份
                      </a>
                    ))
                  : null}
                {passwordEnabled === false ? (
                  <p>
                    尚未设置密码时，可使用有效的 API
                    令牌或已关联的登录方式确认。也可在停止服务后通过本机 CLI
                    设置密码。
                  </p>
                ) : null}
              </>
            )}
            {error ? (
              <p className={forms.formError} role="alert">
                {error}
              </p>
            ) : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
