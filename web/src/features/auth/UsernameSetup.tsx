import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { accountsApi } from "../../api/accounts";
import { Dialog } from "../../components/Dialog";
import { type FormEvent, useRef, useState } from "react";

import {
  PublicApiError,
  type Account,
  type PublicApi,
  type SiteIdentity,
} from "../../api/public";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { useSession } from "./SessionProvider";
import styles from "../../components/Forms.module.css";

const USERNAME_PATTERN = /^[a-z][a-z0-9_]{2,29}$/;

function localProblem(value: string): string | null {
  if (!value) return t("请先填写用户名。");
  if (Array.from(value).length < 3 || Array.from(value).length > 30) {
    return t("用户名需要 3 到 30 个字符。");
  }
  if (!USERNAME_PATTERN.test(value)) {
    return t("只能使用小写字母、数字和下划线，并且以字母开头。");
  }
  return null;
}

function serverProblem(error: unknown): string {
  if (!(error instanceof PublicApiError)) {
    return t("没有保存成功。刚才填写的内容还在，可以直接重试。");
  }
  if (error.code === "USERNAME_TAKEN")
    return t("这个用户名已经有人用了，换一个吧。");
  if (error.code === "USERNAME_RESERVED")
    return t("这个用户名被站点保留，换一个吧。");
  if (error.code === "INVALID_ARGUMENT") {
    return t("服务器没有接受这个用户名，换一个再试。");
  }
  if (error.status === 401 || error.status === 403) {
    return t("登录状态已经失效，请重新登录后再设置用户名。");
  }
  return t("没有保存成功。刚才填写的内容还在，可以直接重试。");
}

/**
 * The one prompt a provider sign-up gets to pick its own handle.
 *
 * A first sign-in through a provider arrives with a handle derived from
 * whatever name that provider published, which is a guess and reads like one.
 * Skipping is a real answer, so it saves the guess rather than doing nothing:
 * the server flips `username_chosen` on any accepted profile write, and that
 * flag -- not a browser-local "dismissed" note -- is what keeps this from
 * asking again on the next device.
 */
export function UsernameSetup({
  account,
  api: publicApi,
  onSiteChanged,
}: {
  account: Account;
  api: PublicApi;
  onSiteChanged: (site: SiteIdentity) => void;
}) {
  const { t } = useTranslation();

  const { session, applyAccount, isCurrentSession, expireIfInvalidSession } =
    useSession();
  const [open, setOpen] = useState(true);
  const [username, setUsername] = useState(account.username);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const usernameInput = useRef<HTMLInputElement>(null);

  useDocumentScrollLock(open);

  const problem = localProblem(username);

  const submit = async (value: string) => {
    if (pending || !session) return;
    setPending(true);
    setError(null);
    try {
      const saved = await accountsApi.updateAccountProfile(session.csrfToken, {
        username: value,
      });
      if (!applyAccount(saved)) return;
      setOpen(false);
      if (saved.role === "admin") {
        publicApi
          .site()
          .then((site) => {
            if (isCurrentSession()) onSiteChanged(site);
          })
          .catch(() => {
            // Cosmetic until the next load; the rename itself is already saved.
          });
      }
    } catch (nextError) {
      if (!isCurrentSession()) return;
      expireIfInvalidSession(nextError);
      setError(serverProblem(nextError));
    } finally {
      setPending(false);
    }
  };

  const choose = (event: FormEvent) => {
    event.preventDefault();
    if (problem) {
      setError(problem);
      return;
    }
    void submit(username);
  };

  return (
    <Dialog.Root
      onOpenChange={(next) => {
        // Dismissing is skipping, and skipping still has to be written down.
        if (!next) void submit(account.username);
      }}
      open={open}
    >
      <Dialog.Portal>
        <Dialog.Backdrop className={styles.dialogBackdrop} />
        <Dialog.Viewport className={styles.dialogViewport}>
          <Dialog.Popup
            className={styles.dialogPopup}
            initialFocus={usernameInput}
          >
            <Dialog.Title className={styles.dialogTitle}>
              {t("给自己起个用户名")}
            </Dialog.Title>
            <Dialog.Description className={styles.dialogDescription}>
              {t("这是你在本站的主页地址和 @ 提及名。现在的")}
              <code> @{account.username} </code>
              {t("是根据登录账号自动生成的，可以改成你想要的。")}
            </Dialog.Description>
            <form className={styles.usernameForm} onSubmit={choose}>
              <label htmlFor="account-username">{t("用户名")}</label>
              <div className={styles.usernameField}>
                <span aria-hidden="true">@</span>
                <input
                  aria-describedby="account-username-hint"
                  aria-invalid={Boolean(error)}
                  autoCapitalize="none"
                  autoComplete="off"
                  ref={usernameInput}
                  id="account-username"
                  onChange={(event) => {
                    setUsername(event.target.value.trim().toLowerCase());
                    setError(null);
                  }}
                  spellCheck={false}
                  value={username}
                />
              </div>
              <small id="account-username-hint">
                {problem ?? t("主页地址将是 /{{value1}}", { value1: username })}
              </small>
              {error ? (
                <p className={styles.dialogError} role="alert">
                  {error}
                </p>
              ) : null}
              <div className={styles.dialogActions}>
                <button
                  className={styles.secondaryButton}
                  disabled={pending}
                  onClick={() => void submit(account.username)}
                  type="button"
                >
                  {t("跳过")}
                </button>
                <button
                  className={styles.primaryButton}
                  disabled={pending || Boolean(problem)}
                  type="submit"
                >
                  {pending ? t("正在保存…") : t("使用这个用户名")}
                </button>
              </div>
            </form>
          </Dialog.Popup>
        </Dialog.Viewport>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
