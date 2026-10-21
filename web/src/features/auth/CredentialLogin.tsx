import {
  authApi,
  type OAuthLoginProvider,
  type SignInMethod,
} from "../../api/auth";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";

import { PublicApiError, type SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
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

const AUTH_ERROR_MESSAGES: Record<string, string> = {
  OAUTH_PROVIDER_UNAVAILABLE:
    "这个登录方式暂时不可用，请稍后再试或改用密码登录。",
  OAUTH_STATE_INVALID: "登录请求已过期，请重新点击登录。",
  OAUTH_IDENTITY_UNUSABLE: "没能从该账号读到可用的身份信息。",
  ACCOUNT_SUSPENDED: "这个账号已被停用。",
  REGISTRATION_CLOSED: "本站当前不接受新注册。",
  OAUTH_EXCHANGE_FAILED: "与登录服务通信失败，请重试。",
  // Binding, not signing in: this comes back when the provider account is
  // already attached to a different account on this instance.
  IDENTITY_ALREADY_LINKED: "这个账号已经绑定到本站的另一个账户了。",
};

const METHOD_LABELS: Record<SignInMethod, string> = {
  password: "密码",
  token: "API 令牌",
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
    return "这个账户只能用第三方账号登录，请在下方选择。";
  }
  return `这个账户不能这样登录，可以改用${named.join("或")}。`;
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
    return "登录没有完成，请重试。输入内容已保留。";
  }
  if (error.code === "ACCOUNT_NOT_FOUND") return "本站没有这个用户名。";
  if (error.code === "CREDENTIAL_UNAVAILABLE") {
    return unavailableCredentialMessage(error.details);
  }
  if (error.status === 401) {
    return method === "password"
      ? "密码不正确，请重试。"
      : "这个API 令牌无效，请检查后重试。";
  }
  return "登录没有完成，请重试。输入内容已保留。";
}

function authErrorMessage(code: string | null): string | null {
  if (code === null) return null;
  return AUTH_ERROR_MESSAGES[code] ?? "第三方登录没有完成，请重试。";
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
  embedded = false,
  heading = true,
}: {
  site: SiteIdentity;
  embedded?: boolean;
  /**
   * Whether this renders its own title. The standalone sign-in page carries a
   * display headline of its own, and two headings stacked would say the same
   * thing twice in two sizes.
   */
  heading?: boolean;
}) {
  const { navigate, replace, pathname, search } = useNavigation();
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
      placement: embedded ? undefined : "login",
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
    if (
      !authError ||
      authAnnouncedRef.current ||
      (embedded && pathname === "/login")
    )
      return;
    authAnnouncedRef.current = true;
    // The toast provider subscribes after this child's effects mount.
    queueMicrotask(() => {
      if (mountedRef.current)
        notify(authError, {
          id: toastId,
          type: "error",
          placement: embedded ? undefined : "login",
        });
    });
  }, [authError, embedded, pathname, toastId]);

  useEffect(() => {
    // A cookie restore or another tab can complete sign-in without submitting
    // this form. The standalone page resumes its return path in either case.
    if (!embedded && state === "signed-in" && account !== null) {
      replace(returnPath);
    }
  }, [account, embedded, replace, returnPath, state]);

  useEffect(() => {
    const timer = window.setTimeout(() => setHoldExpired(true), 2000);
    return () => window.clearTimeout(timer);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void authApi
      .loginProviders(controller.signal)
      .then((available) => {
        if (!controller.signal.aborted) {
          setProviders(orderedProviders(available));
        }
      })
      .catch(() => {
        // Instances with no OAuth configured do not serve this endpoint at all.
        // Falling back to an empty list degrades the page to the password form
        // instead of showing a failure the visitor cannot act on.
        if (!controller.signal.aborted) setProviders([]);
      })
      .finally(() => {
        if (!controller.signal.aborted) setProvidersPending(false);
      });
    return () => controller.abort();
  }, [api]);

  useEffect(() => {
    // A back navigation can restore this page from the bfcache with the button
    // still disabled from the redirect that led away from it.
    const restore = () => setStartingProvider(null);
    window.addEventListener("pageshow", restore);
    return () => window.removeEventListener("pageshow", restore);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void authApi
      .signInMethods(controller.signal)
      .then((available) => {
        if (controller.signal.aborted) return;
        setMethods(available.methods);
        // A password is what somebody signing in today is looking for. The
        // token is a recovery credential, so it is only the default when this
        // instance has no password set at all.
        setMethod(
          available.methods.includes("password")
            ? "password"
            : (available.methods[0] ?? "token"),
        );
      })
      .catch(() => {
        // An instance that will not say keeps its token path usable, rather
        // than presenting a password form that cannot succeed against it.
        if (!controller.signal.aborted) {
          setMethods(["token"]);
          setMethod("token");
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) setOptionsPending(false);
      });
    return () => controller.abort();
  }, [api]);

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
      if (!embedded) replace(returnPath);
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
      if (!embedded) navigate("/");
    } catch {
      showError("退出没有完成。当前会话仍保持登录，请检查连接后重试。");
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  // The panel's own title needs no answer from the server, so it is drawn
  // from the first frame; only the form under it waits.
  if (embedded && pathname === "/login") return null;
  if (embedded && state === "expired")
    return (
      <p className={styles.storageNotice} role="status">
        登录已过期。{" "}
        <InternalLink
          href={`/login?return=${encodeURIComponent(`${pathname}${window.location.search}`)}`}
        >
          重新登录
        </InternalLink>
      </p>
    );

  const title = heading ? (
    <>
      <p className={styles.eyebrow}>
        {state === "expired" ? "会话已过期" : "登录"}
      </p>
      <h2>
        {state === "expired" ? "重新登录" : `登录 ${site.title || "Moment"}`}
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
        aria-label="正在准备登录"
        className={styles.loginGate}
      >
        {title}
      </section>
    );
  if (state === "signed-in")
    return (
      <section className={styles.loginGate}>
        <p className={styles.eyebrow}>{canPublish ? "账号会话" : "已登录"}</p>
        <h2>{canPublish ? "可以继续记录" : "账号尚未获准发布"}</h2>
        <p>
          {canPublish
            ? "此浏览器已通过身份验证。退出会同时清除当前实例的本地草稿。"
            : "账号正在等待管理员批准，批准后就能发布和回复。"}
        </p>
        <button
          className={styles.secondaryButton}
          disabled={pending}
          onClick={() => void endSession()}
          type="button"
        >
          {pending ? "正在退出…" : canPublish ? "退出登录" : "退出登录"}
        </button>
      </section>
    );

  const otherMethods = methods.filter((candidate) => candidate !== method);

  return (
    <section className={styles.loginGate}>
      {title}
      {state === "unavailable" ? (
        <div className={styles.inlineNotice} role="alert">
          <span>暂时无法确认现有会话，仍可尝试登录。</span>
          <button onClick={retryStatus} type="button">
            重试检查
          </button>
        </div>
      ) : null}
      <form
        className={styles.loginForm}
        onSubmit={(event) => void submit(event)}
      >
        <label htmlFor="login-username">用户名</label>
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
            placeholder="你的用户名"
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
            aria-pressed={showCredential}
            disabled={pending}
            onClick={() => setShowCredential((current) => !current)}
            type="button"
          >
            {showCredential ? "隐藏" : "显示"}
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
          {pending ? "正在登录…" : "登录"}
        </button>
      </form>
      {providers.length > 0 ? (
        <>
          <div className={styles.oauthSeparator}>
            <span aria-hidden="true" className={styles.oauthRule} />
            <span>或</span>
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
                      ? `正在前往 ${provider.label}…`
                      : `使用 ${provider.label} 登录`}
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
                  setMethod(candidate);
                  setShowOtherMethods(false);
                  setCredential("");
                  clearError();
                }}
                type="button"
              >
                使用{METHOD_LABELS[candidate]}登录
              </button>
            ))
          ) : (
            <button
              className={styles.loginMethodSwitch}
              disabled={pending}
              onClick={() => setShowOtherMethods(true)}
              type="button"
            >
              选择其他登录方式
            </button>
          )}
        </div>
      ) : null}
    </section>
  );
}
