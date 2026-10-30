import { useTranslation } from "react-i18next";
import {
  type FormEvent,
  type InputHTMLAttributes,
  type ReactNode,
  useCallback,
  useEffect,
  useId,
  useRef,
  useState,
} from "react";
import {
  AnimatePresence,
  motion,
  useIsPresent,
  useReducedMotion,
} from "motion/react";
import {
  authApi,
  type EmailChallenge,
  type RegistrationOptions,
  type VerifiedEmail,
} from "../../api/auth";
import { PublicApiError } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { AuthPageFrame } from "./AuthPageFrame";
import { RegistrationSessionRestoreError, useSession } from "./SessionProvider";
import { authPageHref, safeReturnPath } from "./returnPath";
import forms from "../../components/Forms.module.css";
import styles from "./RegisterPage.module.css";

type FieldName =
  "username" | "password" | "confirm" | "email" | "code" | "display_name";
type FormError = { message: string; field?: FieldName } | null;
const ease = [0.22, 1, 0.36, 1] as const;

function PresenceContent({ children }: { children: ReactNode }) {
  const present = useIsPresent();
  return (
    <div inert={!present} aria-hidden={!present || undefined}>
      {children}
    </div>
  );
}

function Field({
  label,
  error,
  end,
  ...input
}: InputHTMLAttributes<HTMLInputElement> & {
  label: string;
  error?: boolean;
  end?: ReactNode;
}) {
  return (
    <div className={styles.field}>
      <label htmlFor={input.id}>{label}</label>
      <div className={styles.inputShell} data-invalid={error || undefined}>
        <input {...input} aria-invalid={error || undefined} />
        {end}
      </div>
    </div>
  );
}

export function RegisterPage() {
  const { t } = useTranslation();
  const { replace, search } = useNavigation();
  const { account, register, retryStatus, state } = useSession();
  const returnPath = safeReturnPath(new URLSearchParams(search).get("return"));
  const reduced = useReducedMotion();
  const duration = reduced ? 0 : 0.26;
  const [options, setOptions] = useState<RegistrationOptions | null>(null);
  const [optionsError, setOptionsError] = useState(false);
  const [optionsAttempt, setOptionsAttempt] = useState(0);
  const [step, setStep] = useState(0);
  const [direction, setDirection] = useState(1);
  const [autoAdvance, setAutoAdvance] = useState(false);
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [email, setEmail] = useState("");
  const [code, setCode] = useState("");
  const [name, setName] = useState("");
  const [showPassword, setShowPassword] = useState(false);
  const [showConfirm, setShowConfirm] = useState(false);
  const [challenge, setChallenge] = useState<EmailChallenge | null>(null);
  const [verified, setVerified] = useState<VerifiedEmail | null>(null);
  const [clock, setClock] = useState(Date.now());
  const [pending, setPending] = useState<
    "next" | "send" | "verify" | "finish" | null
  >(null);
  const [transitioning, setTransitioning] = useState(false);
  const [error, setError] = useState<FormError>(null);
  const [finished, setFinished] = useState(false);
  const [sessionUnconfirmed, setSessionUnconfirmed] = useState(false);
  const [panelHeight, setPanelHeight] = useState<number | "auto">("auto");
  const panel = useRef<HTMLDivElement>(null);
  const panelObserver = useRef<ResizeObserver | null>(null);
  const focusNextPanel = useRef(false);
  const pendingRef = useRef(false);
  const transitionRef = useRef(false);
  const mounted = useRef(true);
  const id = useId();
  const busy = pending !== null;
  const resendSeconds = challenge
    ? Math.max(0, Math.ceil((Date.parse(challenge.resendAt) - clock) / 1000))
    : 0;
  const emailVerified =
    verified !== null &&
    verified.email === email.trim().toLowerCase() &&
    Date.parse(verified.expiresAt) > clock;
  const titles = [t("设置账号"), t("验证邮箱"), t("你希望大家怎么称呼你？")];
  const visibleError =
    error ??
    (step > 0 && verified && Date.parse(verified.expiresAt) <= clock
      ? { message: t("邮箱验证已过期，请返回上一步重新验证。") }
      : null);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
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
  useEffect(() => {
    if (!challenge && !verified) return;
    const timer = window.setInterval(() => setClock(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [challenge, verified]);
  const observePanel = useCallback((node: HTMLDivElement | null) => {
    panel.current = node;
    panelObserver.current?.disconnect();
    if (!node) return;
    const measure = () => setPanelHeight(node.getBoundingClientRect().height);
    measure();
    panelObserver.current = new ResizeObserver(measure);
    panelObserver.current.observe(node);
    if (focusNextPanel.current) {
      node.querySelector<HTMLElement>("h2")?.focus({ preventScroll: true });
      focusNextPanel.current = false;
    }
  }, []);

  const move = useCallback(
    (next: number) => {
      if (pendingRef.current || transitionRef.current || next === step) return;
      transitionRef.current = true;
      focusNextPanel.current = true;
      setTransitioning(true);
      setAutoAdvance(false);
      setDirection(next > step ? 1 : -1);
      setError(null);
      setStep(next);
    },
    [step],
  );
  useEffect(() => {
    if (!autoAdvance || !emailVerified || step !== 1 || pending) return;
    const timer = window.setTimeout(() => move(2), reduced ? 0 : 600);
    return () => window.clearTimeout(timer);
  }, [autoAdvance, emailVerified, step, pending, reduced, move]);

  const transitionComplete = () => {
    transitionRef.current = false;
    setTransitioning(false);
  };
  useEffect(() => {
    if (!transitioning || !reduced) return;
    transitionComplete();
  }, [transitioning, reduced]);

  const showError = (message: string, field?: FieldName) => {
    setError({ message, field });
    if (field)
      window.requestAnimationFrame(() =>
        document
          .getElementById(`${id}-${field}`)
          ?.focus({ preventScroll: true }),
      );
  };
  const mapError = (failure: unknown) => {
    if (
      failure instanceof RegistrationSessionRestoreError ||
      (failure instanceof PublicApiError &&
        failure.code === "REGISTRATION_SESSION_UNAVAILABLE" &&
        failure.details.account_created === true)
    ) {
      setSessionUnconfirmed(true);
      setPassword("");
      setConfirm("");
      return;
    }
    if (!(failure instanceof PublicApiError)) {
      showError(t("请求未完成，请检查连接后重试。输入内容已保留。"));
      return;
    }
    switch (failure.code) {
      case "USERNAME_TAKEN":
        if (step !== 0) {
          transitionRef.current = false;
          pendingRef.current = false;
          move(0);
        }
        showError(t("这个用户名已被使用，请换一个。"), "username");
        return;
      case "USERNAME_RESERVED":
        showError(t("这个用户名由站点保留，请换一个。"), "username");
        return;
      case "EMAIL_TAKEN":
        showError(t("这个邮箱已注册，可以直接使用邮箱和密码登录。"), "email");
        return;
      case "RATE_LIMITED":
        showError(t("发送太频繁，请稍后再试。"), "email");
        return;
      case "VERIFICATION_INVALID":
        showError(
          t("验证码不正确或已失效，请检查后重试。"),
          step === 1 ? "code" : undefined,
        );
        return;
      case "VERIFICATION_EXPIRED":
        setVerified(null);
        showError(t("验证码已过期，请重新获取。"));
        if (step === 2) {
          transitionRef.current = false;
          pendingRef.current = false;
          move(1);
        }
        return;
      case "EMAIL_UNAVAILABLE":
        showError(t("验证码暂时无法发送，请稍后重试。"));
        return;
      case "REGISTRATION_CLOSED":
        setOptions((previous) =>
          previous ? { ...previous, registrationMode: "closed" } : null,
        );
        return;
      case "ALREADY_SIGNED_IN":
        retryStatus();
        return;
      default:
        showError(t("请求未完成，请稍后重试。输入内容已保留。"));
    }
  };

  const perform = async (
    kind: NonNullable<typeof pending>,
    operation: () => Promise<void>,
  ) => {
    if (pendingRef.current || transitionRef.current) return;
    pendingRef.current = true;
    setPending(kind);
    setError(null);
    try {
      await operation();
    } catch (failure) {
      if (mounted.current) mapError(failure);
    } finally {
      pendingRef.current = false;
      if (mounted.current) setPending(null);
    }
  };

  const sendCode = () => {
    if (resendSeconds > 0) return;
    const address = email.trim().toLowerCase();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(address)) {
      showError(t("请输入有效的邮箱地址。"), "email");
      return;
    }
    void perform("send", async () => {
      const next = await authApi.requestRegistrationEmail(address);
      if (!mounted.current) return;
      setEmail(address);
      setChallenge(next);
      setVerified(null);
      setCode("");
      setClock(Date.now());
      window.requestAnimationFrame(() =>
        document.getElementById(`${id}-code`)?.focus(),
      );
    });
  };

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!options || busy || transitionRef.current) return;
    if (step === 0) {
      const normalized = username.trim().toLowerCase();
      if (
        !new RegExp(
          `^[a-z][a-z0-9_]{${options.usernameMinLength - 1},${options.usernameMaxLength - 1}}$`,
        ).test(normalized)
      ) {
        showError(
          t("用户名需为 3–30 位，以字母开头，只能包含字母、数字和下划线。"),
          "username",
        );
        return;
      }
      const bytes = new TextEncoder().encode(password).length;
      if (
        bytes < options.passwordMinBytes ||
        bytes > options.passwordMaxBytes ||
        password.trim() !== password
      ) {
        showError(t("密码需为 8–72 字节，首尾不能留空白。"), "password");
        return;
      }
      if (password !== confirm) {
        showError(t("两次输入的密码不一致。"), "confirm");
        return;
      }
      void perform("next", async () => {
        const checked = await authApi.checkRegistration(normalized, password);
        if (!mounted.current) return;
        setUsername(checked);
        pendingRef.current = false;
        move(1);
      });
    } else if (step === 1) {
      if (emailVerified) {
        move(2);
        return;
      }
      if (!challenge) {
        sendCode();
        return;
      }
      if (!/^\d{6}$/.test(code)) {
        showError(t("请输入 6 位验证码。"), "code");
        return;
      }
      void perform("verify", async () => {
        const proof = await authApi.verifyRegistrationEmail(
          challenge.challengeId,
          code,
        );
        if (!mounted.current) return;
        setVerified(proof);
        setAutoAdvance(true);
        setClock(Date.now());
      });
    } else {
      if (!emailVerified || !verified) {
        showError(t("邮箱验证已过期，请返回上一步重新验证。"));
        return;
      }
      if ([...name.trim()].length > 80 || /\p{Cc}/u.test(name)) {
        showError(t("名字最多 80 个字，不能包含控制字符。"), "display_name");
        return;
      }
      void perform("finish", async () => {
        await register(
          username.trim().toLowerCase(),
          password,
          verified.token,
          name.trim(),
        );
        if (!mounted.current) return;
        setPassword("");
        setConfirm("");
        setCode("");
        setVerified(null);
        setFinished(true);
      });
    }
  };

  const inputProps = (field: FieldName) => ({
    id: `${id}-${field}`,
    error: error?.field === field,
    "aria-describedby": error?.field === field ? `${id}-error` : undefined,
    readOnly: busy,
  });
  const edit =
    (set: (value: string) => void) =>
    (event: React.ChangeEvent<HTMLInputElement>) => {
      set(event.target.value);
      setError(null);
    };
  const toggle = (
    shown: boolean,
    set: (value: boolean) => void,
    label: string,
  ) => (
    <button
      className={styles.reveal}
      type="button"
      disabled={busy}
      aria-label={label}
      aria-pressed={shown}
      onClick={() => set(!shown)}
    >
      {shown ? t("隐藏") : t("显示")}
    </button>
  );

  let content: ReactNode;
  if (finished || sessionUnconfirmed || (state === "signed-in" && account)) {
    content = (
      <motion.section
        className={styles.success}
        initial={{ opacity: 0, y: reduced ? 0 : 12 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration }}
      >
        <motion.div
          className={styles.successMark}
          initial={{ scale: reduced ? 1 : 0.85 }}
          animate={{ scale: 1 }}
          transition={{ type: "spring", stiffness: 320, damping: 24 }}
        >
          <Icon name="check" />
        </motion.div>
        <h2>
          {finished || sessionUnconfirmed ? t("注册完成") : t("你已经登录")}
        </h2>
        <p role="status">
          {sessionUnconfirmed
            ? t("账号已创建，暂时无法确认登录状态。可以使用邮箱和密码登录。")
            : account?.status === "pending"
              ? t("账号已创建，正在等待管理员批准。批准前仍可登录和浏览。")
              : null}
        </p>
        <button
          className={styles.primary}
          type="button"
          onClick={() =>
            replace(
              sessionUnconfirmed ? authPageHref("/login", search) : returnPath,
            )
          }
        >
          {sessionUnconfirmed ? t("前往登录") : t("继续浏览")}
          <Icon name="arrow-right" />
        </button>
      </motion.section>
    );
  } else if (state === "unavailable" || optionsError) {
    content = (
      <section className={styles.notice}>
        <p role="alert">{t("暂时无法读取注册状态。输入内容已保留。")}</p>
        <button
          className={styles.secondary}
          type="button"
          onClick={() => {
            if (optionsError) setOptionsAttempt((value) => value + 1);
            else retryStatus();
          }}
        >
          {t("重试检查")}
        </button>
      </section>
    );
  } else if (options?.registrationMode === "closed") {
    content = (
      <section className={styles.notice}>
        <p role="status">{t("本站当前不接受新注册。")}</p>
      </section>
    );
  } else if (!options || state === "checking") {
    content = (
      <div
        className={styles.skeleton}
        aria-busy="true"
        aria-label={t("正在准备注册")}
      >
        <span />
        <span />
        <span />
      </div>
    );
  } else {
    content = (
      <section className={styles.wizard} aria-label={t("创建账号")}>
        <div className={styles.progressRow}>
          <div
            className={styles.progress}
            role="progressbar"
            aria-label={t("注册进度")}
            aria-valuemin={0}
            aria-valuemax={3}
            aria-valuenow={step + 1}
          >
            <motion.span
              initial={false}
              animate={{ width: `${((step + 1) / 3) * 100}%` }}
              transition={{ duration, ease }}
            />
          </div>
          <span className={styles.stepCount} aria-hidden="true">
            {step + 1} / 3
          </span>
        </div>
        <span className={forms.srOnly} role="status">
          {t("第 {{step}} 步，共 3 步", { step: step + 1 })}
        </span>
        <motion.div
          className={styles.viewport}
          initial={false}
          animate={{ height: panelHeight }}
          transition={{ duration, ease }}
        >
          <AnimatePresence initial={false} custom={direction} mode="wait">
            <motion.div
              key={step}
              custom={direction}
              initial="enter"
              animate="center"
              exit="exit"
              variants={{
                enter: (dir: number) => ({
                  x: reduced ? 0 : dir * 28,
                  opacity: 0,
                }),
                center: { x: 0, opacity: 1 },
                exit: (dir: number) => ({
                  x: reduced ? 0 : -dir * 28,
                  opacity: 0,
                  transition: { duration: reduced ? 0 : 0.1, ease },
                }),
              }}
              transition={{ duration, ease }}
              onAnimationComplete={(definition) => {
                if (definition === "center") transitionComplete();
              }}
            >
              <PresenceContent>
                <div
                  ref={observePanel}
                  className={styles.panel}
                  data-registration-step={step + 1}
                >
                  <div className={styles.stepHeading}>
                    {step > 0 && (
                      <button
                        aria-label={t("返回上一步")}
                        className={styles.back}
                        type="button"
                        disabled={busy || transitioning}
                        onClick={() => move(step - 1)}
                      >
                        <Icon name="arrow-left" />
                      </button>
                    )}
                    <h2
                      className={step < 2 ? forms.srOnly : undefined}
                      tabIndex={-1}
                    >
                      {titles[step]}
                    </h2>
                  </div>

                  <form
                    className={styles.form}
                    noValidate
                    onSubmit={submit}
                    aria-busy={busy}
                  >
                    {step === 0 && (
                      <>
                        <Field
                          {...inputProps("username")}
                          label={t("用户名")}
                          value={username}
                          onChange={edit(setUsername)}
                          placeholder={t("选择用户名")}
                          autoComplete="username"
                          autoCapitalize="none"
                          spellCheck={false}
                        />
                        <Field
                          {...inputProps("password")}
                          label={t("密码")}
                          value={password}
                          onChange={edit(setPassword)}
                          placeholder={t("至少 8 个字符")}
                          type={showPassword ? "text" : "password"}
                          autoComplete="new-password"
                          end={toggle(
                            showPassword,
                            setShowPassword,
                            showPassword ? t("隐藏密码") : t("显示密码"),
                          )}
                        />
                        <Field
                          {...inputProps("confirm")}
                          label={t("确认密码")}
                          value={confirm}
                          onChange={edit(setConfirm)}
                          placeholder={t("再次输入密码")}
                          type={showConfirm ? "text" : "password"}
                          autoComplete="new-password"
                          end={toggle(
                            showConfirm,
                            setShowConfirm,
                            showConfirm ? t("隐藏确认密码") : t("显示确认密码"),
                          )}
                        />
                      </>
                    )}
                    {step === 1 && (
                      <>
                        <Field
                          {...inputProps("email")}
                          label={t("邮箱地址")}
                          value={email}
                          onChange={(event) => {
                            setEmail(event.target.value);
                            setChallenge(null);
                            setVerified(null);
                            setCode("");
                            setError(null);
                          }}
                          placeholder="name@example.com"
                          autoComplete="email"
                          type="email"
                          autoCapitalize="none"
                          spellCheck={false}
                        />
                        {!emailVerified && (
                          <div className={styles.codeRow}>
                            <Field
                              {...inputProps("code")}
                              label={t("验证码")}
                              value={code}
                              onChange={(event) => {
                                setCode(
                                  event.target.value
                                    .replace(/\D/g, "")
                                    .slice(0, 6),
                                );
                                setError(null);
                              }}
                              placeholder={t("6 位数字")}
                              autoComplete="one-time-code"
                              inputMode="numeric"
                              maxLength={6}
                            />
                            <button
                              className={styles.send}
                              type="button"
                              disabled={
                                busy || resendSeconds > 0 || transitioning
                              }
                              onClick={sendCode}
                            >
                              {pending === "send" ? (
                                <>
                                  <span className={styles.spinner} />
                                  {t("发送中…")}
                                </>
                              ) : resendSeconds > 0 ? (
                                t("{{seconds}} 秒后重发", {
                                  seconds: resendSeconds,
                                })
                              ) : challenge ? (
                                t("重新发送")
                              ) : (
                                t("获取验证码")
                              )}
                            </button>
                          </div>
                        )}
                        <AnimatePresence initial={false} mode="wait">
                          {emailVerified ? (
                            <motion.div
                              className={styles.verified}
                              role="status"
                              key="verified"
                              initial={{ opacity: 0, y: reduced ? 0 : 6 }}
                              animate={{ opacity: 1, y: 0 }}
                              exit={{ opacity: 0 }}
                              transition={{ duration }}
                            >
                              <span className={styles.verifiedIcon}>
                                <Icon name="check" />
                              </span>
                              <span>
                                <strong>{t("邮箱验证成功")}</strong>
                              </span>
                            </motion.div>
                          ) : (
                            <motion.p
                              key={challenge ? "sent" : "hint"}
                              className={styles.hint}
                              role="status"
                              initial={{ opacity: 0 }}
                              animate={{ opacity: 1 }}
                              exit={{ opacity: 0 }}
                              transition={{ duration: duration / 2 }}
                            >
                              {challenge
                                ? t(
                                    "验证码已发送，10 分钟内有效。没收到时请检查垃圾邮件。",
                                  )
                                : null}
                            </motion.p>
                          )}
                        </AnimatePresence>
                      </>
                    )}
                    {step === 2 && (
                      <>
                        <Field
                          {...inputProps("display_name")}
                          label={t("名字")}
                          value={name}
                          onChange={edit(setName)}
                          placeholder={t("输入名字（选填）")}
                          autoComplete="nickname"
                          maxLength={80}
                        />

                        {options.registrationMode === "approval" && (
                          <p className={styles.approval}>
                            {t(
                              "注册后需要管理员批准。等待期间可以登录和浏览，暂时不能发布。",
                            )}
                          </p>
                        )}
                      </>
                    )}
                    <AnimatePresence initial={false}>
                      {visibleError && (
                        <motion.div
                          key="error"
                          initial={{ height: 0, opacity: 0 }}
                          animate={{ height: "auto", opacity: 1 }}
                          exit={{ height: 0, opacity: 0 }}
                          transition={{ duration, ease }}
                          className={styles.errorWrap}
                        >
                          <p
                            role="alert"
                            id={`${id}-error`}
                            className={styles.error}
                          >
                            {visibleError.message}
                          </p>
                        </motion.div>
                      )}
                    </AnimatePresence>
                    <button
                      className={styles.primary}
                      type="submit"
                      disabled={
                        busy || transitioning || (step === 2 && !emailVerified)
                      }
                    >
                      <AnimatePresence mode="popLayout" initial={false}>
                        <motion.span
                          key={
                            pending || (emailVerified ? "verified" : "ready")
                          }
                          initial={{ opacity: 0, y: reduced ? 0 : 5 }}
                          animate={{ opacity: 1, y: 0 }}
                          exit={{ opacity: 0, y: reduced ? 0 : -5 }}
                          transition={{ duration: duration / 2 }}
                        >
                          {busy && <span className={styles.spinner} />}
                          {pending === "next"
                            ? t("正在检查…")
                            : pending === "verify"
                              ? t("正在验证…")
                              : pending === "send"
                                ? t("正在发送…")
                                : pending === "finish"
                                  ? t("正在完成注册…")
                                  : step === 0 || (step === 1 && emailVerified)
                                    ? t("下一步")
                                    : step === 1
                                      ? t("验证邮箱")
                                      : t("完成注册")}
                        </motion.span>
                      </AnimatePresence>
                      {!busy && <Icon name="arrow-right" />}
                    </button>
                  </form>
                </div>
              </PresenceContent>
            </motion.div>
          </AnimatePresence>
        </motion.div>
      </section>
    );
  }
  return (
    <AuthPageFrame headline="Join Moment">
      {content}
      {!finished && !account && (
        <p className={forms.authSwitch}>
          {t("已有账号？")}{" "}
          <InternalLink authSwitch href={authPageHref("/login", search)}>
            {t("立即登录")}
          </InternalLink>
        </p>
      )}
    </AuthPageFrame>
  );
}
