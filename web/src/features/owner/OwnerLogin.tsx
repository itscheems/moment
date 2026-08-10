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
          ? "这个所有者凭据无效，请检查后重试。"
          : "登录没有完成。凭据仍只留在当前输入框中，可以安全重试。",
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
        {state === "expired" ? "刷新写入身份" : "私人入口"}
      </p>
      <h2>{state === "expired" ? "重新确认所有者身份" : "用所有者凭据继续"}</h2>
      <p>
        {state === "expired"
          ? "当前文字仍保留在编辑器中。凭据只用于换取新的安全会话。"
          : "凭据只用于换取当前浏览器的安全会话，不会写入地址、草稿或浏览器存储。"}
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
        <label htmlFor="owner-token">所有者凭据</label>
        <input
          autoCapitalize="none"
          autoComplete="off"
          id="owner-token"
          name="owner-token"
          onChange={(event) => setToken(event.target.value)}
          required
          spellCheck={false}
          type="password"
          value={token}
        />
        {error ? <p role="alert">{error}</p> : null}
        <button
          className={styles.primaryButton}
          disabled={!token || pending}
          type="submit"
        >
          {pending ? "正在登录…" : "进入所有者模式"}
        </button>
      </form>
    </section>
  );
}
