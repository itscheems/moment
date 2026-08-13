import { type FormEvent, useEffect, useRef, useState } from "react";

import { PublicApiError, type SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { useOwner } from "./OwnerProvider";
import styles from "./Owner.module.css";

function safeReturnPath(value: string | null): string {
  return value && value.startsWith("/") && !value.startsWith("//")
    ? value
    : "/";
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
  const pendingRef = useRef(false);

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
