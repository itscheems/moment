import { type FormEvent, useEffect, useRef, useState } from "react";

import type { OAuthLoginProvider } from "../../api/owner";
import { PublicApiError, type SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { Icon, type IconName } from "../../components/icons";
import { useOwner } from "./OwnerProvider";
import styles from "./Owner.module.css";

// GitHub sits above Google on purpose: a meaningful share of this instance's
// visitors are in mainland China, where Google is unreachable, so the option
// that can actually complete has to be the first one they see.
const PROVIDER_ORDER = ["github", "google"];

const PROVIDER_ICONS: Record<string, IconName> = {
  github: "github",
  google: "google",
};

const AUTH_ERROR_MESSAGES: Record<string, string> = {
  OAUTH_PROVIDER_UNAVAILABLE:
    "这个登录方式暂时不可用，请稍后再试或改用密码登录。",
  OAUTH_STATE_INVALID: "登录请求已过期，请重新点击登录。",
  OAUTH_IDENTITY_UNUSABLE: "没能从该账号读到可用的身份信息。",
  ACCOUNT_SUSPENDED: "这个账号已被停用。",
  REGISTRATION_CLOSED: "本站当前不接受新注册。",
  OAUTH_EXCHANGE_FAILED: "与登录服务通信失败，请重试。",
};

function safeReturnPath(value: string | null): string {
  return value && value.startsWith("/") && !value.startsWith("//")
    ? value
    : "/";
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

function authErrorMessage(code: string | null): string | null {
  if (code === null) return null;
  return AUTH_ERROR_MESSAGES[code] ?? "第三方登录没有完成，请重试。";
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

export function OwnerLogin({
  site,
  embedded = false,
}: {
  site: SiteIdentity;
  embedded?: boolean;
}) {
  const { navigate } = useNavigation();
  const { api, state, login, logout, retryStatus } = useOwner();
  const [method, setMethod] = useState<"password" | "token">("password");
  const [credential, setCredential] = useState("");
  const [showCredential, setShowCredential] = useState(false);
  const [pending, setPending] = useState(false);
  const [optionsPending, setOptionsPending] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [providers, setProviders] = useState<OAuthLoginProvider[]>([]);
  const [startingProvider, setStartingProvider] = useState<string | null>(null);
  const [authError] = useState(() =>
    authErrorMessage(
      new URLSearchParams(window.location.search).get("auth_error"),
    ),
  );
  const pendingRef = useRef(false);

  useEffect(() => stripAuthError(), []);

  useEffect(() => {
    const controller = new AbortController();
    void api
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
    void api
      .loginOptions(controller.signal)
      .then((options) => {
        if (!controller.signal.aborted)
          setMethod(options.passwordEnabled ? "password" : "token");
      })
      .catch(() => {
        // Older instances do not expose login options yet. Keep their token
        // login path usable instead of presenting a password form that cannot
        // succeed against that server.
        if (!controller.signal.aborted) setMethod("token");
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
    setError(null);
    try {
      await login(credential, method);
      setCredential("");
      if (!embedded)
        navigate(
          safeReturnPath(
            new URLSearchParams(window.location.search).get("return"),
          ),
        );
    } catch (nextError) {
      setError(
        nextError instanceof PublicApiError && nextError.status === 401
          ? method === "password"
            ? "密码不正确，请重试。"
            : "这个所有者令牌无效，请检查后重试。"
          : "登录没有完成。输入内容仍只留在当前输入框中，可以安全重试。",
      );
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  const startOAuth = (name: string) => {
    if (startingProvider !== null) return;
    setStartingProvider(name);
    const returnPath = safeReturnPath(
      new URLSearchParams(window.location.search).get("return"),
    );
    // A full page load, not a fetch: the provider owns the next screen and the
    // callback has to land back on this origin with its own cookies.
    window.location.assign(
      `/api/v1/login/oauth/${encodeURIComponent(name)}/start?return=${encodeURIComponent(returnPath)}`,
    );
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
      setError("退出没有完成。当前会话仍保持登录，请检查连接后重试。");
    } finally {
      pendingRef.current = false;
      setPending(false);
    }
  };

  if (state === "checking")
    return (
      <section aria-busy="true" className={styles.loginGate}>
        <p className={styles.eyebrow}>正在确认身份</p>
        <h2>检查当前会话…</h2>
      </section>
    );
  if (state === "owner")
    return (
      <section className={styles.loginGate}>
        <p className={styles.eyebrow}>所有者会话</p>
        <h2>可以继续记录</h2>
        <p>此浏览器已通过身份验证。退出会同时清除当前实例的本地草稿。</p>
        {error ? <p role="alert">{error}</p> : null}
        <button
          className={styles.secondaryButton}
          disabled={pending}
          onClick={() => void endSession()}
          type="button"
        >
          {pending ? "正在退出…" : "退出所有者模式"}
        </button>
      </section>
    );

  return (
    <section className={styles.loginGate}>
      <p className={styles.eyebrow}>
        {state === "expired" ? "会话已过期" : "所有者登录"}
      </p>
      <h2>
        {state === "expired"
          ? "重新登录"
          : method === "password"
            ? `欢迎回来，${site.displayName}`
            : "使用所有者令牌"}
      </h2>
      <p>
        {method === "password"
          ? "输入此实例的所有者密码。Moment 不需要公开注册、邮箱或账号选择。"
          : "令牌用于初始化、CLI 和恢复；只在尚未设置密码或紧急访问时使用。"}
      </p>
      {authError ? (
        <p className={styles.authError} role="alert">
          {authError}
        </p>
      ) : null}
      {state === "unavailable" ? (
        <div className={styles.inlineNotice} role="alert">
          <span>暂时无法确认现有会话，仍可尝试登录。</span>
          <button onClick={retryStatus} type="button">
            重试检查
          </button>
        </div>
      ) : null}
      {providers.length > 0 ? (
        <>
          <div className={styles.oauthOptions}>
            {providers.map((provider) => {
              const icon = PROVIDER_ICONS[provider.name];
              const starting = startingProvider === provider.name;
              return (
                <button
                  className={styles.oauthButton}
                  disabled={startingProvider !== null}
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
          <div className={styles.oauthSeparator}>
            <span aria-hidden="true" className={styles.oauthRule} />
            <span>or</span>
            <span aria-hidden="true" className={styles.oauthRule} />
          </div>
        </>
      ) : null}
      <form
        className={styles.loginForm}
        onSubmit={(event) => void submit(event)}
      >
        <label htmlFor="owner-credential">
          {method === "password" ? "密码" : "所有者令牌"}
        </label>
        <div className={styles.tokenField}>
          <input
            autoCapitalize="none"
            autoComplete={method === "password" ? "current-password" : "off"}
            id="owner-credential"
            onChange={(event) => setCredential(event.target.value)}
            required
            spellCheck={false}
            type={showCredential ? "text" : "password"}
            value={credential}
          />
          <button
            aria-pressed={showCredential}
            onClick={() => setShowCredential((current) => !current)}
            type="button"
          >
            {showCredential ? "隐藏" : "显示"}
          </button>
        </div>
        <small>
          {method === "password" ? (
            "密码只发送到当前 Moment 实例，不会进入帖子导出。"
          ) : (
            <>
              本地开发令牌保存在 <code>.moment-local/owner.token</code>。
            </>
          )}
        </small>
        {error ? <p role="alert">{error}</p> : null}
        <button
          className={styles.primaryButton}
          disabled={!credential || pending || optionsPending}
          type="submit"
        >
          {pending ? "正在登录…" : "登录"}
        </button>
        <button
          className={styles.loginMethodSwitch}
          disabled={pending}
          onClick={() => {
            setMethod((current) =>
              current === "password" ? "token" : "password",
            );
            setCredential("");
            setError(null);
          }}
          type="button"
        >
          {method === "password" ? "改用所有者令牌" : "改用密码"}
        </button>
      </form>
    </section>
  );
}
