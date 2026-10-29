import { useTranslation } from "react-i18next";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Dialog } from "../../components/Dialog";
import { notify } from "../../components/Feedback";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import {
  accountSecurity,
  confirmAccountCredential,
  credentialConfirmationStatus,
  accountsApi,
} from "../../api/accounts";
import { registerCredentialConfirmation } from "../../api/credentialConfirmation";
import { useSession } from "../auth/SessionProvider";
import { oauthStartUrl, providerLabel } from "../auth/providers";
import forms from "../../components/Forms.module.css";
import styles from "./Settings.module.css";

/** The server owns the one-hour, session-bound HttpOnly confirmation. */
export function CredentialConfirmation() {
  const { t } = useTranslation();

  const { account, session, api, expireIfInvalidSession } = useSession();
  const input = useRef<HTMLInputElement>(null);
  const finalFocus = useRef<HTMLElement | null>(null);
  const resolve = useRef<((confirmed: boolean) => void) | null>(null);
  const providerWindow = useRef<Window | null>(null);
  const providerPoll = useRef<number | null>(null);
  const providerRequest = useRef<AbortController | null>(null);
  const [open, setOpen] = useState(false);
  const [passwordEnabled, setPasswordEnabled] = useState<boolean | null>(null);
  const [secret, setSecret] = useState("");
  const [providers, setProviders] = useState<string[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  useDocumentScrollLock(open);

  function closeProvider() {
    providerRequest.current?.abort();
    providerRequest.current = null;
    if (providerPoll.current !== null)
      window.clearTimeout(providerPoll.current);
    providerPoll.current = null;
    providerWindow.current?.close();
    providerWindow.current = null;
  }
  function finish(confirmed: boolean) {
    closeProvider();
    const complete = resolve.current;
    resolve.current = null;
    setOpen(false);
    setSecret("");
    setPending(false);
    complete?.(confirmed);
  }
  function verified() {
    if (!resolve.current) return;
    notify(t("身份验证成功"), {
      id: "credential-confirmation",
      placement: "corner",
      timeout: 1800,
    });
    finish(true);
  }
  useEffect(() => {
    setOpen(false);
    setSecret("");
    if (!account || !session) return;
    const unregister = registerCredentialConfirmation(
      () =>
        new Promise<boolean>((complete) => {
          resolve.current = complete;
          finalFocus.current =
            document.activeElement instanceof HTMLElement
              ? document.activeElement
              : null;
          setError(null);
          setSecret("");
          setPasswordEnabled(null);
          setOpen(true);
        }),
    );
    return () => {
      unregister();
      closeProvider();
      resolve.current?.(false);
      resolve.current = null;
    };
  }, [account, session]);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setError(null);
    void accountSecurity(controller.signal)
      .then(async (security) => {
        if (controller.signal.aborted) return;
        setPasswordEnabled(security.passwordEnabled);
        requestAnimationFrame(() => input.current?.focus());
        if (security.passwordEnabled) {
          setProviders([]);
          return;
        }
        const [identities, available] = await Promise.all([
          accountsApi.accountIdentities(controller.signal),
          api.loginProviders(controller.signal),
        ]);
        if (!controller.signal.aborted)
          setProviders(
            identities
              .map((identity) => identity.provider)
              .filter((provider) =>
                available.some((item) => item.name === provider),
              ),
          );
      })
      .catch((next: unknown) => {
        if (!controller.signal.aborted && !expireIfInvalidSession(next))
          setError(t("确认方式暂时无法读取，请重试。"));
      });
    return () => controller.abort();
  }, [open, attempt, api, expireIfInvalidSession]);

  async function confirm(event: FormEvent) {
    event.preventDefault();
    if (!session || passwordEnabled === null || pending) return;
    setPending(true);
    setError(null);
    try {
      await confirmAccountCredential(
        session.csrfToken,
        passwordEnabled ? { password: secret } : { token: secret },
      );
      verified();
    } catch (next) {
      setSecret("");
      if (!expireIfInvalidSession(next))
        setError(t("验证失败，请输入当前有效的凭据。"));
    } finally {
      setPending(false);
    }
  }

  function confirmProvider(provider: string) {
    const popup = window.open(
      `${oauthStartUrl(provider, window.location.pathname)}&confirm=1`,
      "moment-credential-confirmation",
      "popup,width=520,height=720",
    );
    if (!popup) {
      setError(t("验证窗口未能打开，请允许弹出窗口后重试。"));
      return;
    }
    providerWindow.current = popup;
    const controller = new AbortController();
    providerRequest.current = controller;
    setPending(true);
    setError(null);
    const check = async () => {
      try {
        const confirmed = await credentialConfirmationStatus(controller.signal);
        if (controller.signal.aborted) return;
        if (confirmed) {
          verified();
        } else if (popup.closed) {
          closeProvider();
          setPending(false);
        } else {
          providerPoll.current = window.setTimeout(() => void check(), 700);
        }
      } catch (next) {
        if (!controller.signal.aborted) {
          closeProvider();
          setPending(false);
          if (!expireIfInvalidSession(next))
            setError(t("验证状态暂时无法读取，请重试。"));
        }
      }
    };
    void check();
  }
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) finish(false);
      }}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={forms.dialogBackdrop} />
        <Dialog.Viewport className={forms.dialogViewport}>
          <Dialog.Popup
            className={`${forms.dialogPopup} ${styles.credentialDialog}`}
            initialFocus={input}
            finalFocus={finalFocus}
          >
            <Dialog.Close
              aria-label={t("关闭身份确认")}
              className={styles.dialogClose}
              disabled={pending}
            >
              ×
            </Dialog.Close>
            <Dialog.Title className={forms.dialogTitle}>
              {t("验证身份以继续")}
            </Dialog.Title>
            <Dialog.Description className={forms.dialogDescription}>
              {t(
                "此操作需要验证身份。验证后，在当前登录会话内 1 小时无需重复输入。",
              )}
            </Dialog.Description>
            <form
              className={styles.settingsForm}
              onSubmit={(event) => void confirm(event)}
            >
              <label>
                {passwordEnabled === null
                  ? t("正在读取确认方式…")
                  : passwordEnabled
                    ? t("当前密码")
                    : t("当前 API 令牌")}
                <input
                  ref={input}
                  autoComplete={passwordEnabled ? "current-password" : "off"}
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
                {pending ? t("正在验证…") : t("验证并继续")}
              </button>
            </form>
            {passwordEnabled === false
              ? providers.map((provider) => (
                  <button
                    key={provider}
                    type="button"
                    className={styles.secondaryButton}
                    disabled={pending}
                    onClick={() => confirmProvider(provider)}
                  >
                    {t("使用")} {providerLabel(provider)} {t("验证身份")}
                  </button>
                ))
              : null}
            {error ? (
              <p className={forms.formError} role="alert">
                {error}
                <button
                  className={styles.quietButton}
                  type="button"
                  disabled={pending}
                  onClick={() => setAttempt((value) => value + 1)}
                >
                  {t("重试")}
                </button>
              </p>
            ) : null}
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
