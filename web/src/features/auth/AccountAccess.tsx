import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { ensureCredentialConfirmation } from "../../api/credentialConfirmation";
import {
  accountsApi,
  type AccountIdentity,
  type AccountSessionEntry,
} from "../../api/accounts";
import { authApi } from "../../api/auth";
import { Tooltip } from "@base-ui/react/tooltip";
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";

import { PublicApiError } from "../../api/public";
import { Icon } from "../../components/icons";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { formatInstant } from "../compose/instants";
import { useSession } from "./SessionProvider";
import { oauthStartUrl, PROVIDER_ICONS, providerLabel } from "./providers";
import styles from "../settings/Settings.module.css";
import tooltipStyles from "../../components/Tooltip.module.css";

const RETURN_PATH = "/settings/security/identities";

// `surface` is the server's own vocabulary for where a request came from. It
// reads as a category name rather than as a device, which is what a person
// scanning this list is actually trying to recognise.
const SURFACE_LABELS: Record<string, string> = {
  get web() {
    return t("浏览器");
  },
  get api() {
    return t("API 客户端");
  },
  get cli() {
    return t("命令行");
  },
};

function surfaceLabel(surface: string): string {
  return SURFACE_LABELS[surface] ?? (surface || t("未知来源"));
}

type PanelState = "loading" | "ready" | "failed";

/**
 * The facts behind one row, on hover and on focus.
 *
 * They are worth having and not worth reading every time: three stacked lines
 * under every entry turned two short lists into a wall of timestamps nobody
 * was looking for. The trigger is a real button rather than the row itself so
 * that a keyboard reaches everything a pointer reaches.
 */
function RowDetails({
  children,
  label,
  lines,
}: {
  children: ReactNode;
  label: string;
  lines: Array<[string, string]>;
}) {
  return (
    <Tooltip.Root>
      <Tooltip.Trigger
        aria-label={label}
        className={styles.rowDetailsTrigger}
        closeDelay={80}
        delay={200}
      >
        {children}
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Positioner
          align="start"
          className={tooltipStyles.positioner}
          side="bottom"
          sideOffset={6}
        >
          <Tooltip.Popup
            className={`${tooltipStyles.popup} ${styles.rowDetails}`}
            role="tooltip"
          >
            <dl>
              {lines.map(([term, value]) => (
                <div key={term}>
                  <dt>{term}</dt>
                  <dd>{value}</dd>
                </div>
              ))}
            </dl>
          </Tooltip.Popup>
        </Tooltip.Positioner>
      </Tooltip.Portal>
    </Tooltip.Root>
  );
}

function unlinkMessage(error: unknown, expired: boolean): string {
  if (
    error instanceof PublicApiError &&
    error.code === "REAUTHENTICATION_REQUIRED"
  )
    return t("身份验证未完成，操作未执行。");
  if (expired) return t("会话已过期。请重新登录后再解绑。");
  if (error instanceof PublicApiError && error.code === "LAST_CREDENTIAL") {
    return t(
      "这是这个账户唯一的登录方式，解绑后就再也登录不进来了。请先绑定另一个登录方式，或设置密码。",
    );
  }
  return t("解绑没有完成。这个登录方式仍然可用，可以稍后重试。");
}

/**
 * The ways into this account, and the browsers currently holding it.
 *
 * Both lists are read straight from the server rather than kept in sync with
 * anything local: a binding made on a phone and a session opened at work are
 * exactly the things this page exists to show, and neither passes through here.
 */
export function AccountAccess({
  timezone,
  instanceId,
  onSignedOut,
  panel,
}: {
  timezone: string;
  instanceId: string | null;
  onSignedOut: () => void;
  panel?: "identities" | "sessions";
}) {
  const { t } = useTranslation();

  const { api, session, expireIfInvalidSession, forgetSession, logout } =
    useSession();
  const [identities, setIdentities] = useState<AccountIdentity[]>([]);
  const [identitiesState, setIdentitiesState] = useState<PanelState>("loading");
  const [identityAttempt, setIdentityAttempt] = useState(0);
  const [linkable, setLinkable] = useState<string[]>([]);
  const [unlinking, setUnlinking] = useState<string | null>(null);
  const [unlinkError, setUnlinkError] = useState<string | null>(null);
  const [sessions, setSessions] = useState<AccountSessionEntry[]>([]);
  const [sessionsState, setSessionsState] = useState<PanelState>("loading");
  const [sessionAttempt, setSessionAttempt] = useState(0);
  const [revoking, setRevoking] = useState(false);
  const [revokeError, setRevokeError] = useState<string | null>(null);
  const [signingOut, setSigningOut] = useState(false);
  const mountedRef = useRef(true);

  // Set on the way in as well as cleared on the way out. Without the first
  // line, StrictMode's development mount-unmount-remount leaves the ref false
  // for the life of the component, so every guarded update is skipped and the
  // buttons that clear their own pending state latch disabled. Composer.tsx
  // already carries the correct shape.
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setIdentitiesState("loading");
    void accountsApi
      .accountIdentities(controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return;
        setIdentities(next);
        setIdentitiesState("ready");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        expireIfInvalidSession(error);
        setIdentitiesState("failed");
      });
    return () => controller.abort();
  }, [api, expireIfInvalidSession, identityAttempt]);

  useEffect(() => {
    const controller = new AbortController();
    void authApi
      .loginProviders(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) {
          setLinkable(next.map((provider) => provider.name));
        }
      })
      .catch(() => {
        // An instance with no provider configured serves no such route, and
        // there is nothing to offer binding to.
        if (!controller.signal.aborted) setLinkable([]);
      });
    return () => controller.abort();
  }, [api]);

  useEffect(() => {
    const controller = new AbortController();
    setSessionsState("loading");
    void accountsApi
      .accountSessions(controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return;
        setSessions(next);
        setSessionsState("ready");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        expireIfInvalidSession(error);
        setSessionsState("failed");
      });
    return () => controller.abort();
  }, [api, expireIfInvalidSession, sessionAttempt]);

  const unlink = useCallback(
    async (provider: string) => {
      if (!session || unlinking) return;
      setUnlinking(provider);
      setUnlinkError(null);
      try {
        await accountsApi.unlinkIdentity(session.csrfToken, provider);
        if (!mountedRef.current) return;
        setIdentities((current) =>
          current.filter((entry) => entry.provider !== provider),
        );
      } catch (error) {
        if (!mountedRef.current) return;
        setUnlinkError(unlinkMessage(error, expireIfInvalidSession(error)));
      } finally {
        if (mountedRef.current) setUnlinking(null);
      }
    },
    [api, expireIfInvalidSession, session, unlinking],
  );

  const link = async (provider: string) => {
    // The session cookie travels with this navigation, which is what makes the
    // server treat the trip as a binding rather than a sign-in. It names the
    // provider again on the way back, so nothing has to be remembered here.
    try {
      if (!(await ensureCredentialConfirmation())) {
        setUnlinkError(t("身份验证未完成，操作未执行。"));
        return;
      }
      window.location.assign(oauthStartUrl(provider, RETURN_PATH));
    } catch {
      setUnlinkError(t("暂时无法确认登录状态，请重试。"));
    }
  };

  const revokeEverything = useCallback(async () => {
    if (!session || revoking) return;
    setRevoking(true);
    setRevokeError(null);
    try {
      await accountsApi.revokeAccountSessions(session.csrfToken);
      // This browser was one of the sessions just ended, so its stored token is
      // already worthless; dropping it here is what keeps the other tabs from
      // going on believing they are signed in.
      forgetSession(instanceId);
      onSignedOut();
    } catch (error) {
      if (!mountedRef.current) return;
      setRevoking(false);
      setRevokeError(
        expireIfInvalidSession(error)
          ? t("会话已过期。请重新登录后再退出其他设备。")
          : t("没有退出成功。已登录的设备保持不变，可以重试。"),
      );
    }
  }, [
    api,
    expireIfInvalidSession,
    forgetSession,
    instanceId,
    onSignedOut,
    revoking,
    session,
  ]);

  // Ending this one session, as opposed to ending all of them. Leaving only
  // the second meant that signing out on the machine in front of you also
  // signed out the phone in your pocket, so the ordinary case was reachable
  // only through the sign-in page.
  const signOut = useCallback(async () => {
    if (signingOut || revoking) return;
    setSigningOut(true);
    setRevokeError(null);
    try {
      await logout(instanceId);
      onSignedOut();
    } catch (error) {
      if (!mountedRef.current) return;
      setSigningOut(false);
      // An expired session is already signed out. Reporting a failure would be
      // describing the transport rather than the outcome the person asked for.
      if (expireIfInvalidSession(error)) {
        forgetSession(instanceId);
        onSignedOut();
        return;
      }
      setRevokeError(t("没有退出成功。当前会话仍然有效，可以重试。"));
    }
  }, [
    expireIfInvalidSession,
    forgetSession,
    instanceId,
    logout,
    onSignedOut,
    revoking,
    signingOut,
  ]);

  const bound = new Set(identities.map((entry) => entry.provider));
  const offerable = linkable.filter((provider) => !bound.has(provider));

  return (
    <>
      <div className={styles.accountBlock} hidden={panel === "sessions"}>
        <div className={styles.accountBlockHeader}>
          <strong>{t("已关联的登录方式")}</strong>
          <p>{t("用这些账号可以直接登录，不需要记住密码。")}</p>
        </div>
        {identitiesState === "failed" ? (
          <div className={styles.inlineError} role="alert">
            <span>{t("暂时无法读取已关联的登录方式。")}</span>
            <button
              className={styles.quietButton}
              onClick={() => setIdentityAttempt((value) => value + 1)}
              type="button"
            >
              {t("重试")}
            </button>
          </div>
        ) : identitiesState === "loading" ? (
          <p aria-busy="true">{t("正在读取已关联的登录方式…")}</p>
        ) : identities.length === 0 ? (
          <p className={styles.accountEmpty}>
            {t("还没有关联任何第三方账号。")}
          </p>
        ) : (
          <ul className={styles.accountList}>
            {identities.map((identity) => {
              const label = providerLabel(identity.provider);
              const icon = PROVIDER_ICONS[identity.provider];
              return (
                <li className={styles.accountRow} key={identity.provider}>
                  <RowDetails
                    label={t("{{value1}} 的关联详情", { value1: label })}
                    lines={[
                      [
                        t("账号"),
                        identity.displayName || t("该账号没有公开名称"),
                      ],
                      [t("邮箱"), identity.email || t("该账号没有公开邮箱")],
                      [t("关联于"), formatInstant(identity.linkedAt, timezone)],
                      [
                        t("最后登录"),
                        formatInstant(identity.lastLoginAt, timezone),
                      ],
                    ]}
                  >
                    <span className={styles.accountRowMain}>
                      <strong>
                        {icon ? <Icon name={icon} /> : null}
                        {label}
                      </strong>
                      <small>
                        {identity.displayName ||
                          identity.email ||
                          t("该账号没有公开名称")}
                      </small>
                    </span>
                  </RowDetails>
                  <ConfirmDialog
                    confirmLabel={t("解绑")}
                    danger
                    description={t(
                      "解绑后就不能再用这个 {{value1}} 账号登录本站。已发布的内容不受影响。",
                      { value1: label },
                    )}
                    onConfirm={() => unlink(identity.provider)}
                    pending={unlinking === identity.provider}
                    title={t("解绑 {{value1}}？", { value1: label })}
                    trigger={
                      <button
                        className={styles.quietButton}
                        disabled={!session || unlinking !== null}
                        type="button"
                      >
                        {t("解绑")}
                      </button>
                    }
                  />
                </li>
              );
            })}
          </ul>
        )}
        {unlinkError ? (
          <p className={styles.fieldError} role="alert">
            {unlinkError}
          </p>
        ) : null}
        {offerable.length > 0 ? (
          <div className={styles.inlineActions}>
            {offerable.map((provider) => (
              <button
                className={styles.secondaryButton}
                key={provider}
                onClick={() => link(provider)}
                type="button"
              >
                {t("绑定")} {providerLabel(provider)}
              </button>
            ))}
          </div>
        ) : null}
      </div>

      <div className={styles.accountBlock} hidden={panel === "identities"}>
        <div className={styles.accountBlockHeader}>
          <strong>{t("活跃会话")}</strong>
          <p>
            {t(
              "每次登录都会留下一条记录，直到过期或被退出。用API 令牌直接调用 API 不会开启会话，因此不会出现在这里。",
            )}
          </p>
        </div>
        {sessionsState === "failed" ? (
          <div className={styles.inlineError} role="alert">
            <span>{t("暂时无法读取活跃会话。")}</span>
            <button
              className={styles.quietButton}
              onClick={() => setSessionAttempt((value) => value + 1)}
              type="button"
            >
              {t("重试")}
            </button>
          </div>
        ) : sessionsState === "loading" ? (
          <p aria-busy="true">{t("正在读取活跃会话…")}</p>
        ) : (
          <ul className={styles.accountList}>
            {sessions.map((entry) => (
              <li className={styles.accountRow} key={entry.id}>
                <RowDetails
                  label={t("这次登录的详情")}
                  lines={[
                    [t("登录于"), formatInstant(entry.issuedAt, timezone)],
                    [t("最近活动"), formatInstant(entry.lastSeenAt, timezone)],
                    [t("到期"), formatInstant(entry.expiresAt, timezone)],
                  ]}
                >
                  <span className={styles.accountRowMain}>
                    <strong>{surfaceLabel(entry.surface)}</strong>
                    <small>
                      {t("最近活动")}{" "}
                      {formatInstant(entry.lastSeenAt, timezone)}
                    </small>
                  </span>
                </RowDetails>
                {entry.current ? (
                  <span className={styles.accountBadge} data-tone="current">
                    {t("当前设备")}
                  </span>
                ) : null}
              </li>
            ))}
          </ul>
        )}
        {revokeError ? (
          <p className={styles.fieldError} role="alert">
            {revokeError}
          </p>
        ) : null}
        <div className={styles.inlineActions}>
          <button
            className={styles.secondaryButton}
            disabled={!session || signingOut || revoking}
            onClick={() => void signOut()}
            type="button"
          >
            {signingOut ? t("正在退出…") : t("退出登录")}
          </button>
          <ConfirmDialog
            confirmLabel={t("全部退出")}
            danger
            description={t(
              "所有登录过的浏览器都会立即退出，包括正在使用的这一个。重新登录后一切照旧。",
            )}
            onConfirm={revokeEverything}
            pending={revoking}
            title={t("退出所有设备？")}
            trigger={
              <button
                className={styles.dangerButton}
                disabled={!session || revoking}
                type="button"
              >
                {t("退出所有设备")}
              </button>
            }
          />
        </div>
      </div>
    </>
  );
}
