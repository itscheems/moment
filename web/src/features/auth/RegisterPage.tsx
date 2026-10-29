import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { type FormEvent, useEffect, useId, useRef, useState } from "react";

import { authApi, type RegistrationOptions } from "../../api/auth";
import { PublicApiError } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { feedback, notify } from "../../components/Feedback";
import { AuthPageFrame } from "./AuthPageFrame";
import { RegistrationSessionRestoreError, useSession } from "./SessionProvider";
import { authPageHref, safeReturnPath } from "./returnPath";
import styles from "../../components/Forms.module.css";

type InvalidField = "username" | "password" | "confirm" | null;

function registrationError(error: unknown): {
  message: string;
  field: InvalidField;
} {
  if (!(error instanceof PublicApiError)) {
    return {
      message: t(
        "没有收到注册结果。请先尝试用刚才的用户名和密码登录，再决定是否重试。",
      ),
      field: null,
    };
  }
  if (error.code === "USERNAME_TAKEN") {
    return {
      message: t("这个用户名已被使用。如果刚提交过注册，请先尝试用它登录。"),
      field: "username",
    };
  }
  if (error.code === "USERNAME_RESERVED") {
    return {
      message: t("这个用户名由站点保留，请换一个。"),
      field: "username",
    };
  }
  if (error.code === "REGISTRATION_CLOSED") {
    return { message: t("本站当前不接受新注册。"), field: null };
  }
  if (error.code === "ALREADY_SIGNED_IN") {
    return {
      message: t("当前已有账号登录。请先退出，再注册新账号。"),
      field: null,
    };
  }
  if (error.code === "INVALID_ARGUMENT") {
    const field = error.details.field;
    if (field === "username") {
      return {
        message: t(
          "用户名需为 3–30 位，以字母开头，只能包含字母、数字和下划线。",
        ),
        field,
      };
    }
    if (field === "password") {
      return { message: t("密码需为 8–72 字节，首尾不能留空白。"), field };
    }
  }
  return {
    message:
      error.status === 0
        ? t("无法确认注册结果。请先尝试用刚才的用户名和密码登录。")
        : t("注册没有完成，请稍后重试。输入内容已保留。"),
    field: null,
  };
}

export function RegisterPage() {
  const { t } = useTranslation();

  const { replace, search } = useNavigation();
  const returnPath = safeReturnPath(new URLSearchParams(search).get("return"));
  const { account, register, retryStatus, state } = useSession();
  const [options, setOptions] = useState<RegistrationOptions | null>(null);
  const [optionsError, setOptionsError] = useState(false);
  const [optionsAttempt, setOptionsAttempt] = useState(0);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [pending, setPending] = useState(false);
  const [createdUsername, setCreatedUsername] = useState<string | null>(null);
  const [sessionUnconfirmed, setSessionUnconfirmed] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [invalidField, setInvalidField] = useState<InvalidField>(null);
  const pendingRef = useRef(false);
  const mountedRef = useRef(true);
  const usernameInput = useRef<HTMLInputElement>(null);
  const passwordInput = useRef<HTMLInputElement>(null);
  const confirmInput = useRef<HTMLInputElement>(null);
  const errorId = useId();
  const toastId = `register-error-${errorId}`;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      feedback.close(toastId);
    };
  }, [toastId]);

  useEffect(() => {
    const controller = new AbortController();
    setOptionsError(false);
    void authApi
      .registrationOptions(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setOptions(next);
      })
      .catch(() => {
        if (!controller.signal.aborted) setOptionsError(true);
      });
    return () => controller.abort();
  }, [optionsAttempt]);

  const clearError = () => {
    setError(null);
    setInvalidField(null);
    feedback.close(toastId);
  };

  const showError = (message: string, field: InvalidField = null) => {
    setError(message);
    setInvalidField(field);
    notify(message, { id: toastId, type: "error", placement: "login" });
    if (field === "username") usernameInput.current?.focus();
    if (field === "password") passwordInput.current?.focus();
    if (field === "confirm") confirmInput.current?.focus();
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pendingRef.current || !options || options.registrationMode === "closed")
      return;
    const normalizedUsername = username.trim().toLowerCase();
    const usernamePattern = new RegExp(
      `^[a-z][a-z0-9_]{${options.usernameMinLength - 1},${options.usernameMaxLength - 1}}$`,
    );
    if (!usernamePattern.test(normalizedUsername)) {
      showError(
        t(
          "用户名需为 {{value1}}–{{value2}} 位，以字母开头，只能包含字母、数字和下划线。",
          {
            value1: options.usernameMinLength,
            value2: options.usernameMaxLength,
          },
        ),
        "username",
      );
      return;
    }
    const passwordBytes = new TextEncoder().encode(password).length;
    if (
      passwordBytes < options.passwordMinBytes ||
      passwordBytes > options.passwordMaxBytes ||
      password.trim() !== password
    ) {
      showError(
        t("密码需为 {{value1}}–{{value2}} 字节，首尾不能留空白。", {
          value1: options.passwordMinBytes,
          value2: options.passwordMaxBytes,
        }),
        "password",
      );
      return;
    }
    if (confirm !== password) {
      showError(t("两次输入的密码不一致。"), "confirm");
      return;
    }
    pendingRef.current = true;
    setPending(true);
    try {
      const registered = await register(normalizedUsername, password);
      if (!mountedRef.current) return;
      clearError();
      setPassword("");
      setConfirm("");
      if (registered.status === "active") {
        replace(returnPath);
      } else {
        setCreatedUsername(registered.username);
        notify(t("账号已创建，正在等待管理员批准。"), { placement: "login" });
      }
    } catch (nextError) {
      if (!mountedRef.current) return;
      if (
        nextError instanceof RegistrationSessionRestoreError ||
        (nextError instanceof PublicApiError &&
          nextError.code === "REGISTRATION_SESSION_UNAVAILABLE" &&
          nextError.details.account_created === true)
      ) {
        setCreatedUsername(normalizedUsername);
        setSessionUnconfirmed(true);
        setPassword("");
        setConfirm("");
        notify(
          t("账号已创建，但暂时无法确认登录状态。请重试检查或用密码登录。"),
          {
            id: toastId,
            type: "error",
            placement: "login",
          },
        );
      } else {
        const mapped = registrationError(nextError);
        if (
          nextError instanceof PublicApiError &&
          nextError.code === "REGISTRATION_CLOSED"
        ) {
          setOptions({ ...options, registrationMode: "closed" });
        }
        if (
          nextError instanceof PublicApiError &&
          nextError.code === "ALREADY_SIGNED_IN"
        ) {
          retryStatus();
        }
        showError(mapped.message, mapped.field);
      }
    } finally {
      pendingRef.current = false;
      if (mountedRef.current) setPending(false);
    }
  };

  let content;
  if (state === "signed-in" && account !== null) {
    content = (
      <section className={styles.loginGate}>
        <p className={styles.registerStatus} role="status">
          {account.status === "pending"
            ? createdUsername
              ? t("账号已创建，正在等待管理员批准。批准后即可发布和回复。")
              : t("你已登录，账号正在等待管理员批准。")
            : t("你已经登录，可以继续浏览。")}
        </p>
        <InternalLink className={styles.registerContinue} href={returnPath}>
          {t("继续浏览")}
        </InternalLink>
      </section>
    );
  } else if (sessionUnconfirmed) {
    content = (
      <section className={styles.loginGate}>
        <p className={styles.registerStatus} role="status">
          {t("账号")} {createdUsername}{" "}
          {t(
            "已创建，暂时无法确认登录状态。请重试检查，或使用刚才设置的密码登录。",
          )}
        </p>
        <button
          className={styles.secondaryButton}
          onClick={retryStatus}
          type="button"
        >
          {t("重试检查")}
        </button>
      </section>
    );
  } else if (state === "checking" || (!options && !optionsError)) {
    content = (
      <section
        aria-busy="true"
        aria-label={t("正在准备注册")}
        className={styles.loginGate}
      />
    );
  } else if (state === "unavailable" || optionsError) {
    content = (
      <section className={styles.loginGate}>
        <p className={styles.registerStatus} role="alert">
          {optionsError
            ? t("暂时无法读取注册状态。")
            : t("暂时无法确认当前登录状态。")}
        </p>
        <button
          className={styles.secondaryButton}
          onClick={() => {
            if (optionsError) {
              setOptions(null);
              setOptionsAttempt((value) => value + 1);
            } else {
              retryStatus();
            }
          }}
          type="button"
        >
          {t("重试检查")}
        </button>
      </section>
    );
  } else if (options?.registrationMode === "closed") {
    content = (
      <section className={styles.loginGate}>
        <p className={styles.registerStatus} role="status">
          {t("本站当前不接受新注册。")}
        </p>
      </section>
    );
  } else {
    content = (
      <section className={styles.loginGate}>
        {options?.registrationMode === "approval" ? (
          <p className={styles.registerStatus}>
            {t("注册后需要管理员批准。等待期间可以登录和浏览，暂时不能发布。")}
          </p>
        ) : null}
        <form
          className={styles.loginForm}
          noValidate
          onSubmit={(event) => void submit(event)}
        >
          <label htmlFor="register-username">{t("用户名")}</label>
          <div className={styles.tokenField}>
            <input
              aria-describedby={
                invalidField === "username" ? errorId : undefined
              }
              aria-invalid={invalidField === "username" || undefined}
              autoCapitalize="none"
              autoComplete="username"
              id="register-username"
              onChange={(event) => {
                setUsername(event.target.value);
                clearError();
              }}
              placeholder={t("选择用户名")}
              readOnly={pending}
              ref={usernameInput}
              spellCheck={false}
              type="text"
              value={username}
            />
          </div>
          <label htmlFor="register-password">{t("密码")}</label>
          <div className={styles.tokenField}>
            <input
              aria-describedby={
                invalidField === "password" ? errorId : undefined
              }
              aria-invalid={invalidField === "password" || undefined}
              autoComplete="new-password"
              id="register-password"
              onChange={(event) => {
                setPassword(event.target.value);
                clearError();
              }}
              readOnly={pending}
              ref={passwordInput}
              type={showPassword ? "text" : "password"}
              value={password}
            />
            <button
              aria-label={showPassword ? t("隐藏密码") : t("显示密码")}
              aria-pressed={showPassword}
              onClick={() => setShowPassword((value) => !value)}
              type="button"
            >
              {showPassword ? t("隐藏") : t("显示")}
            </button>
          </div>
          <label htmlFor="register-confirm">{t("确认密码")}</label>
          <div className={styles.tokenField}>
            <input
              aria-describedby={
                invalidField === "confirm" ? errorId : undefined
              }
              aria-invalid={invalidField === "confirm" || undefined}
              autoComplete="new-password"
              id="register-confirm"
              onChange={(event) => {
                setConfirm(event.target.value);
                clearError();
              }}
              readOnly={pending}
              ref={confirmInput}
              type={showConfirm ? "text" : "password"}
              value={confirm}
            />
            <button
              aria-label={showConfirm ? t("隐藏确认密码") : t("显示确认密码")}
              aria-pressed={showConfirm}
              onClick={() => setShowConfirm((value) => !value)}
              type="button"
            >
              {showConfirm ? t("隐藏") : t("显示")}
            </button>
          </div>
          <p className={styles.srOnly} id={errorId}>
            {invalidField ? error : null}
          </p>
          <button
            className={`${styles.primaryButton} ${styles.loginSubmitButton}`}
            disabled={pending}
            type="submit"
          >
            {pending ? t("正在注册…") : t("注册")}
          </button>
        </form>
      </section>
    );
  }

  return (
    <AuthPageFrame
      headline="Join Moment"
      subtitle={t("创建账号，开始记录此刻。")}
    >
      {content}
      {state === "signed-in" && account !== null ? null : (
        <p className={styles.authSwitch}>
          {t("已有账号？")}{" "}
          <InternalLink authSwitch href={authPageHref("/login", search)}>
            {t("立即登录")}
          </InternalLink>
        </p>
      )}
    </AuthPageFrame>
  );
}
