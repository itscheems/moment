import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import {
  authApi,
  type OAuthLoginProvider,
  type SignInMethod,
} from "../../api/auth";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";

import { PublicApiError, type SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { feedback, notify } from "../../components/Feedback";
import { useSession } from "./SessionProvider";
import { oauthStartUrl, PROVIDER_ICONS } from "./providers";
import { safeReturnPath } from "./returnPath";
import styles from "../../components/Forms.module.css";

// GitHub sits above Google on purpose: a meaningful share of this instance's
// visitors are in mainland China, where Google is unreachable, so the option
// that can actually complete has to be the first one they see.
const PROVIDER_ORDER = ["github", "google"];
const LOGIN_OPTIONS_RETRY_MS = 3000;

const AUTH_ERROR_MESSAGES: Record<string, string> = {
  get OAUTH_PROVIDER_UNAVAILABLE() {
    return t("这个登录方式暂时不可用，请稍后再试或改用密码登录。");
  },
  get OAUTH_STATE_INVALID() {
    return t("登录请求已过期，请重新点击登录。");
  },
  get OAUTH_IDENTITY_UNUSABLE() {
    return t("没能从该账号读到可用的身份信息。");
  },
  get ACCOUNT_SUSPENDED() {
    return t("这个账号已被停用。");
  },
  get REGISTRATION_CLOSED() {
    return t("本站当前不接受新注册。");
  },
  get OAUTH_EXCHANGE_FAILED() {
    return t("与登录服务通信失败，请重试。");
  },
  // Binding, not signing in: this comes back when the provider account is
  // already attached to a different account on this instance.
  get IDENTITY_ALREADY_LINKED() {
    return t("这个账号已经绑定到本站的另一个账户了。");
  },
};

const METHOD_LABELS: Record<SignInMethod, string> = {
  get password() {
    return t("密码");
  },
  get token() {
    return t("API 令牌");
  },
};

// The server names the credential kinds that would have worked, so this reads
// them rather than assuming a third-party provider is always the answer. On the
// day an account carries a token of its own, the message says so without this
// file learning anything.
function unavailableCredentialMessage(
  details: Record<string, unknown>,
): string {
  const available = Array.isArray(details.available) ? details.available : [];
  const named = available
    .filter(
      (value): value is SignInMethod =>
        value === "password" || value === "token",
    )
    .map((value) => METHOD_LABELS[value]);
  if (named.length === 0) {
    return t("这个账户只能用第三方账号登录，请在下方选择。");
  }
  return t("这个账户不能这样登录，可以改用{{value1}}。", {
    value1: named.join(t("或")),
  });
}

function orderedProviders(
  providers: OAuthLoginProvider[],
): OAuthLoginProvider[] {
  const rank = (provider: OAuthLoginProvider) => {
    const index = PROVIDER_ORDER.indexOf(provider.name);
    return index === -1 ? PROVIDER_ORDER.length : index;
  };
  // Array#sort is stable, so anything the server adds later keeps its own
  // order behind the two we position deliberately.
  return [...providers].sort((left, right) => rank(left) - rank(right));
}

// The handle the server rejected is worth naming apart from the secret: being
// told the password is wrong when the username is the part that is wrong sends
// people to reset a credential that was never the problem.
function credentialErrorMessage(
  error: unknown,
  method: "password" | "token",
): string {
  if (!(error instanceof PublicApiError)) {
    return t("登录没有完成，请重试。输入内容已保留。");
  }
  if (error.code === "ACCOUNT_NOT_FOUND") return t("本站没有这个用户名。");
  if (error.code === "CREDENTIAL_UNAVAILABLE") {
    return unavailableCredentialMessage(error.details);
  }
  if (error.status === 401) {
    return method === "password"
      ? t("密码不正确，请重试。")
      : t("这个API 令牌无效，请检查后重试。");
  }
  return t("登录没有完成，请重试。输入内容已保留。");
}

function authErrorMessage(code: string | null): string | null {
  if (code === null) return null;
  return AUTH_ERROR_MESSAGES[code] ?? t("第三方登录没有完成，请重试。");
}

function invalidCredentialField(
  error: unknown,
): "username" | "credential" | null {
  if (!(error instanceof PublicApiError)) return null;
  if (error.code === "ACCOUNT_NOT_FOUND") return "username";
  if (error.code === "CREDENTIAL_UNAVAILABLE") return null;
  return error.status === 401 ? "credential" : null;
}

// The failure code is a one-shot message. Leaving it on the URL would replay
// the same alert on every refresh, so it is consumed as soon as it is read.
function stripAuthError() {
  const url = new URL(window.location.href);
  if (!url.searchParams.has("auth_error")) return;
  url.searchParams.delete("auth_error");
  window.history.replaceState(
    window.history.state,
    "",
    `${url.pathname}${url.search}${url.hash}`,
  );
}

export function CredentialLogin({
  site,
  heading = true,
}: {
  site: SiteIdentity;
  /**
   * Whether this renders its own title. The standalone sign-in page carries a
   * display headline of its own, and two headings stacked would say the same
   * thing twice in two sizes.
   */
  heading?: boolean;
}) {
  const { t } = useTranslation();

  const { navigate, replace, search } = useNavigation();
  const returnPath = safeReturnPath(new URLSearchParams(search).get("return"));
  const { account, api, state, login, logout, retryStatus } = useSession();
  const canPublish = account?.status === "active";
  const [method, setMethod] = useState<SignInMethod>("password");
  const [methods, setMethods] = useState<SignInMethod[]>([]);
  const [showOtherMethods, setShowOtherMethods] = useState(false);
  const [username, setUsername] = useState("");
  const [credential, setCredential] = useState("");
  const [showCredential, setShowCredential] = useState(false);
  const [pending, setPending] = useState(false);
  const [optionsPending, setOptionsPending] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [invalidField, setInvalidField] = useState<
    "username" | "credential" | null
  >(null);
  const [providers, setProviders] = useState<OAuthLoginProvider[]>([]);
  const [providersPending, setProvidersPending] = useState(true);
  const [providerAttempt, setProviderAttempt] = useState(0);
  const [methodAttempt, setMethodAttempt] = useState(0);
  // How long the form may be held back for its answers. Past this it is
  // drawn with what is known -- a server that never answers must not leave
  // a blank where a sign-in form should be.
  const [holdExpired, setHoldExpired] = useState(false);
  const [startingProvider, setStartingProvider] = useState<string | null>(null);
  const [authError] = useState(() =>
    authErrorMessage(
      new URLSearchParams(window.location.search).get("auth_error"),
    ),
  );
  const pendingRef = useRef(false);
  const mountedRef = useRef(true);
  const knownMethodsRef = useRef<SignInMethod[]>([]);
  const methodChosenRef = useRef(false);
  const errorId = useId();
  const toastId = `login-error-${errorId}`;
  const authAnnouncedRef = useRef(false);

  const clearError = () => {
    setError(null);
    setInvalidField(null);
    feedback.close(toastId);
  };

  const showError = (
    message: string,
    field: "username" | "credential" | null = null,
  ) => {
    setError(message);
    setInvalidField(field);
    notify(message, {
      id: toastId,
      type: "error",
      placement: "login",
    });
  };

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      feedback.close(toastId);
    };
  }, [toastId]);

  useEffect(() => stripAuthError(), []);

  useEffect(() => {
    if (!authError || authAnnouncedRef.current) return;
    authAnnouncedRef.current = true;
    // The toast provider subscribes after this child's effects mount.
    queueMicrotask(() => {
      if (mountedRef.current)
        notify(authError, {
          id: toastId,
          type: "error",
          placement: "login",
        });
    });
  }, [authError, toastId]);

  useEffect(() => {
    // A cookie restore or another tab can complete sign-in without submitting
    // this form. The standalone page resumes its return path in either case.
    if (state === "signed-in" && account !== null) {
      replace(returnPath);
    }
  }, [account, replace, returnPath, state]);

  useEffect(() => {
    const timer = window.setTimeout(() => setHoldExpired(true), 2000);
    return () => window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let retryTimer: number | undefined;
    void authApi
      .loginProviders(controller.signal)
      .then((available) => {
        if (!controller.signal.aborted) {
          setProviders(orderedProviders(available));
        }
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        // Older instances may not serve this endpoint. A temporary outage is
        // different: keep any visible buttons and ask again when it recovers.
        if (error instanceof PublicApiError && error.status === 404) {
          setProviders([]);
          return;
        }
        retryTimer = window.setTimeout(
          () => setProviderAttempt((attempt) => attempt + 1),
          LOGIN_OPTIONS_RETRY_MS,
        );
      })
      .finally(() => {
        if (!controller.signal.aborted) setProvidersPending(false);
      });
    return () => {
      controller.abort();
      window.clearTimeout(retryTimer);
    };
  }, [api, providerAttempt]);

  useEffect(() => {
    const refresh = () => {
      setProviderAttempt((attempt) => attempt + 1);
      setMethodAttempt((attempt) => attempt + 1);
    };
    window.addEventListener("focus", refresh);
    return () => window.removeEventListener("focus", refresh);
  }, []);

  useEffect(() => {
    // A back navigation can restore this page from the bfcache with the button
    // still disabled from the redirect that led away from it.
    const restore = () => setStartingProvider(null);
    window.addEventListener("pageshow", restore);
    return () => window.removeEventListener("pageshow", restore);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let retryTimer: number | undefined;
    void authApi
      .signInMethods(controller.signal)
      .then((available) => {
        if (controller.signal.aborted) return;
        knownMethodsRef.current = available.methods;
        setMethods(available.methods);
        // A password is what somebody signing in today is looking for. The
        // token is a recovery credential, so it is only the default when this
        // instance has no password set at all.
        const preferred = available.methods.includes("password")
          ? "password"
          : (available.methods[0] ?? "token");
        setMethod((current) =>
          methodChosenRef.current && available.methods.includes(current)
            ? current
            : preferred,
        );
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        // An instance that will not say keeps its token path usable, rather
        // than presenting a password form that cannot succeed against it.
        // A transient outage is retried so it cannot strand a password user
        // on the token form after the server returns.
        if (knownMethodsRef.current.length === 0) {
          setMethods(["token"]);
          setMethod("token");
        }
        if (!(error instanceof PublicApiError && error.status === 404)) {
          retryTimer = window.setTimeout(
            () => setMethodAttempt((attempt) => attempt + 1),
            LOGIN_OPTIONS_RETRY_MS,
          );
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setOptionsPending(false);
      });
    return () => {
      controller.abort();
      window.clearTimeout(retryTimer);
    };
  }, [api, methodAttempt]);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!credential || pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    try {
      await login(credential, method, username.trim() || undefined);
      if (!mountedRef.current) return;
      clearError();
      setCredential("");
      // The sign-in page leaves the history with the signing in: back from
      // the page it returns to should not be a sign-in form for a session
      // that already exists.
      replace(returnPath);
    } catch (nextError) {
      if (mountedRef.current)
        showError(
          credentialErrorMessage(nextError, method),
          invalidCredentialField(nextError),
        );
    } finally {
      pendingRef.current = false;
      if (mountedRef.current) setPending(false);
    }
  };

  const startOAuth = (name: string) => {
    if (startingProvider !== null) return;
    clearError();
    setStartingProvider(name);
    // A full page load, not a fetch: the provider owns the next screen and the
    // callback has to land back on this origin with its own cookies.
    window.location.assign(oauthStartUrl(name, returnPath));
  };

  const endSession = async () => {
    if (pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      await logout(site.instanceId);
      navigate("/");
    } catch {
      showError(t("退出没有完成。当前会话仍保持登录，请检查连接后重试。"));
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  // The panel's own title needs no answer from the server, so it is drawn
  // from the first frame; only the form under it waits.

  const title = heading ? (
    <>
      <p className={styles.eyebrow}>
        {state === "expired" ? t("会话已过期") : t("登录")}
      </p>
      <h2>
        {state === "expired"
          ? t("重新登录")
          : t("登录 {{value1}}", { value1: site.title || "Moment" })}
      </h2>
    </>
  ) : null;

  // The form is not drawn until everything it depends on has answered: the
  // session check, which credentials this instance takes, which providers it
  // offers. Drawn piecemeal -- a line saying the session was being checked,
  // then the fields, then GitHub and Google a request later -- the page moved
  // under the reader with each arrival; drawn once, it lands whole. The wait
  // is tens of milliseconds and gets no spinner, which would be one more
  // thing to appear and vanish.
  if (
    !holdExpired &&
    (state === "checking" || optionsPending || providersPending)
  )
    return (
      <section
        aria-busy="true"
        aria-label={t("正在准备登录")}
        className={styles.loginGate}
      >
        {title}
      </section>
    );
  if (state === "signed-in")
    return (
      <section className={styles.loginGate}>
        <p className={styles.eyebrow}>
          {canPublish ? t("账号会话") : t("已登录")}
        </p>
        <h2>{canPublish ? t("可以继续记录") : t("账号尚未获准发布")}</h2>
        <p>
          {canPublish
            ? t("此浏览器已通过身份验证。退出会同时清除当前实例的本地草稿。")
            : t("账号正在等待管理员批准，批准后就能发布和回复。")}
        </p>
        <button
          className={styles.secondaryButton}
          disabled={pending}
          onClick={() => void endSession()}
          type="button"
        >
          {pending
            ? t("正在退出…")
            : canPublish
              ? t("退出登录")
              : t("退出登录")}
        </button>
      </section>
    );

  const otherMethods = methods.filter((candidate) => candidate !== method);

  return (
    <section className={styles.loginGate}>
      {title}
      {state === "unavailable" ? (
        <div className={styles.inlineNotice} role="alert">
          <span>{t("暂时无法确认现有会话，仍可尝试登录。")}</span>
          <button onClick={retryStatus} type="button">
            {t("重试检查")}
          </button>
        </div>
      ) : null}
      <form
        className={styles.loginForm}
        onSubmit={(event) => void submit(event)}
      >
        <label htmlFor="login-username">
          {method === "password" ? t("邮箱或用户名") : t("用户名")}
        </label>
        <div className={styles.tokenField}>
          <input
            aria-describedby={invalidField === "username" ? errorId : undefined}
            aria-invalid={invalidField === "username" || undefined}
            autoCapitalize="none"
            autoComplete="username"
            id="login-username"
            onChange={(event) => {
              setUsername(event.target.value);
              clearError();
            }}
            placeholder={
              method === "password" ? t("输入邮箱或用户名") : t("你的用户名")
            }
            readOnly={pending}
            spellCheck={false}
            type="text"
            value={username}
          />
        </div>
        <label htmlFor="login-credential">{METHOD_LABELS[method]}</label>
        <div className={styles.tokenField}>
          <input
            aria-describedby={
              invalidField === "credential" ? errorId : undefined
            }
            aria-invalid={invalidField === "credential" || undefined}
            autoCapitalize="none"
            autoComplete={method === "password" ? "current-password" : "off"}
            id="login-credential"
            onChange={(event) => {
              setCredential(event.target.value);
              clearError();
            }}
            readOnly={pending}
            required
            spellCheck={false}
            type={showCredential ? "text" : "password"}
            value={credential}
          />
          <button
            aria-controls="login-credential"
            aria-label={
              method === "password"
                ? showCredential
                  ? t("隐藏密码")
                  : t("显示密码")
                : showCredential
                  ? t("隐藏")
                  : t("显示")
            }
            aria-pressed={showCredential}
            className={styles.passwordToggle}
            onMouseDown={(event) => event.preventDefault()}
            disabled={pending}
            onClick={() => setShowCredential((current) => !current)}
            type="button"
          >
            <Icon name={showCredential ? "eye-off" : "eye"} />
          </button>
        </div>
        <p className={styles.srOnly} id={errorId}>
          {invalidField ? error : null}
        </p>
        <button
          className={`${styles.primaryButton} ${styles.loginSubmitButton}`}
          disabled={!credential || pending || optionsPending}
          type="submit"
        >
          {pending ? t("正在登录…") : t("登录")}
        </button>
      </form>
      {providers.length > 0 ? (
        <>
          <div className={styles.oauthSeparator}>
            <span aria-hidden="true" className={styles.oauthRule} />
            <span>{t("或")}</span>
            <span aria-hidden="true" className={styles.oauthRule} />
          </div>
          <div className={styles.oauthOptions}>
            {providers.map((provider) => {
              const icon = PROVIDER_ICONS[provider.name];
              const starting = startingProvider === provider.name;
              return (
                <button
                  className={styles.oauthButton}
                  disabled={pending || startingProvider !== null}
                  key={provider.name}
                  onClick={() => startOAuth(provider.name)}
                  type="button"
                >
                  {icon ? <Icon name={icon} /> : null}
                  <span>
                    {starting
                      ? t("正在前往 {{value1}}…", { value1: provider.label })
                      : t("使用 {{value1}} 登录", { value1: provider.label })}
                  </span>
                </button>
              );
            })}
          </div>
        </>
      ) : null}
      {otherMethods.length > 0 ? (
        <div className={styles.otherMethods}>
          {showOtherMethods ? (
            otherMethods.map((candidate) => (
              <button
                className={styles.secondaryButton}
                disabled={pending}
                key={candidate}
                onClick={() => {
                  methodChosenRef.current = true;
                  setMethod(candidate);
                  setShowOtherMethods(false);
                  setCredential("");
                  clearError();
                }}
                type="button"
              >
                {t("使用")}
                {METHOD_LABELS[candidate]}
                {t("登录")}
              </button>
            ))
          ) : (
            <button
              className={styles.loginMethodSwitch}
              disabled={pending}
              onClick={() => setShowOtherMethods(true)}
              type="button"
            >
              {t("选择其他登录方式")}
            </button>
          )}
        </div>
      ) : null}
    </section>
  );
}
