import { type FormEvent, useRef, useState } from "react";

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
  const { state, login, logout, retryStatus } = useOwner();
  const [token, setToken] = useState("");
  const [showToken, setShowToken] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pendingRef = useRef(false);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!token || pendingRef.current) return;
    pendingRef.current = true;
    setPending(true);
    setError(null);
    try {
      await login(token);
      setToken("");
      if (!embedded) {
        const returnTo = safeReturnPath(
          new URLSearchParams(window.location.search).get("return"),
        );
        navigate(returnTo);
      }
    } catch (nextError) {
      setError(
        nextError instanceof PublicApiError && nextError.status === 401
          ? "这个所有者令牌无效，请检查后重试。"
          : "登录没有完成。令牌仍只留在当前输入框中，可以安全重试。",
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

  if (state === "checking") {
    return (
      <section aria-busy="true" className={styles.loginGate}>
        <p className={styles.eyebrow}>正在确认身份</p>
        <h2>检查当前会话…</h2>
      </section>
    );
  }

  if (state === "owner") {
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
  }

  return (
    <section className={styles.loginGate}>
      <p className={styles.eyebrow}>
        {state === "expired" ? "会话已过期" : "所有者登录"}
      </p>
      <h2>{state === "expired" ? "重新登录" : "输入所有者令牌"}</h2>
      <p>
        {state === "expired"
          ? "当前文字仍保留在编辑器中。重新输入令牌即可继续。"
          : "这不是账号密码。令牌在初始化或轮换时生成，只用于登录当前浏览器。"}
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
        <label htmlFor="owner-token">所有者令牌</label>
        <div className={styles.tokenField}>
          <input
            autoCapitalize="none"
            autoComplete="off"
            id="owner-token"
            name="owner-token"
            onChange={(event) => setToken(event.target.value)}
            required
            spellCheck={false}
            type={showToken ? "text" : "password"}
            value={token}
          />
          <button
            aria-pressed={showToken}
            onClick={() => setShowToken((current) => !current)}
            type="button"
          >
            {showToken ? "隐藏" : "显示"}
          </button>
        </div>
        <small>
          本地开发时，启动命令会显示令牌，并保存到
          <code>.moment-local/owner.token</code>。
        </small>
        {error ? <p role="alert">{error}</p> : null}
        <button
          className={styles.primaryButton}
          disabled={!token || pending}
          type="submit"
        >
          {pending ? "正在登录…" : "登录"}
        </button>
      </form>
    </section>
  );
}
