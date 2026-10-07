import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";

import type { RegistrationMode } from "../../api/owner";
import type { Account, AccountStatus } from "../../api/public";
import { useSession } from "../auth/SessionProvider";
import styles from "./Settings.module.css";

const statusLabels: Record<AccountStatus, string> = {
  pending: "待批准",
  active: "已启用",
  suspended: "已停用",
};

const statusTones: Record<AccountStatus, string> = {
  pending: "pending",
  active: "neutral",
  suspended: "danger",
};

/**
 * What each registration policy actually does, said in full.
 *
 * Three words on three buttons would not distinguish them: the difference
 * between "open" and "approval" is whether a stranger who signs in can write
 * something the whole site reads before anybody has looked at them, and that
 * is the decision being made here.
 */
const registrationChoices: {
  mode: RegistrationMode;
  label: string;
  detail: string;
}[] = [
  {
    mode: "open",
    label: "开放注册",
    detail: "任何人用第三方账号登录后即可直接发言。",
  },
  {
    mode: "approval",
    label: "需要审核",
    detail: "新账户可以登录和阅读，要等你批准之后才能公开发言。",
  },
  {
    mode: "closed",
    label: "关闭注册",
    detail: "不再接受新账户，已有账户仍可登录。",
  },
];

// Anyone waiting on a decision comes first: the list exists so that nobody sits
// unnoticed behind a page of accounts that need nothing.
const statusOrder: Record<AccountStatus, number> = {
  pending: 0,
  active: 1,
  suspended: 2,
};

function ordered(accounts: Account[]): Account[] {
  return [...accounts].sort((left, right) => {
    const rank =
      statusOrder[left.status ?? "active"] -
      statusOrder[right.status ?? "active"];
    return rank !== 0 ? rank : left.username.localeCompare(right.username);
  });
}

/**
 * Everyone with an account on this instance.
 *
 * Owner-only, and the server says so too -- this renders the same decisions it
 * would enforce anyway, so a member who reached the markup gains nothing.
 */
export function MembersSection({
  onWaitingChange,
}: {
  /**
   * How many accounts are waiting on a decision, reported upward.
   *
   * The list already lives here and already moves when the owner approves
   * somebody, so lifting the number keeps the hint that draws them down the
   * page honest -- a badge fed by its own request would still be advertising
   * three people a moment after the third was approved.
   */
  onWaitingChange?: (waiting: number) => void;
} = {}) {
  const { api, session, account, expireIfInvalidSession } = useSession();
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [registrationMode, setRegistrationMode] =
    useState<RegistrationMode | null>(null);
  const [registrationPending, setRegistrationPending] = useState(false);
  const [registrationError, setRegistrationError] = useState<string | null>(
    null,
  );
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  // Claimed on every mount, not only released on unmount. StrictMode mounts,
  // unmounts and mounts again in development, so a cleanup-only ref stays
  // false for the rest of the component's life -- and every button in the
  // roster is disabled while `busy` is set, which the guarded `finally` would
  // then never clear.
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setFailed(false);
    void api
      .ownerAccounts(controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return;
        setAccounts(next.accounts);
        setRegistrationMode(next.registrationMode);
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        if (!expireIfInvalidSession(error)) setFailed(true);
      });
    return () => controller.abort();
  }, [api, attempt, expireIfInvalidSession]);

  const waiting = useMemo(
    () =>
      (accounts ?? []).filter(
        (entry) => entry.role !== "owner" && entry.status === "pending",
      ).length,
    [accounts],
  );

  useEffect(() => onWaitingChange?.(waiting), [onWaitingChange, waiting]);

  const decide = useCallback(
    async (userId: string, status: AccountStatus) => {
      if (!session || busy) return;
      setBusy(userId);
      setActionError(null);
      try {
        const saved = await api.updateAccountStatus(
          session.csrfToken,
          userId,
          status,
        );
        if (!mountedRef.current) return;
        setAccounts(
          (current) =>
            current?.map((entry) =>
              entry.userId === saved.userId ? saved : entry,
            ) ?? null,
        );
      } catch (error) {
        if (!mountedRef.current) return;
        setActionError(
          expireIfInvalidSession(error)
            ? "会话已过期。请重新登录后再处理成员。"
            : "这次改动没有保存，成员状态保持原样，可以重试。",
        );
      } finally {
        if (mountedRef.current) setBusy(null);
      }
    },
    [api, busy, expireIfInvalidSession, session],
  );

  const chooseRegistration = useCallback(
    async (next: RegistrationMode) => {
      if (!session || registrationPending || next === registrationMode) return;
      setRegistrationPending(true);
      setRegistrationError(null);
      try {
        await api.updateSite(session.csrfToken, { registrationMode: next });
        if (!mountedRef.current) return;
        // The site read this page holds does not carry the mode -- it is not
        // public -- so the server's acceptance is the confirmation, and this
        // is the copy that was accepted.
        setRegistrationMode(next);
      } catch (error) {
        if (!mountedRef.current) return;
        setRegistrationError(
          expireIfInvalidSession(error)
            ? "会话已过期。请重新登录后再修改注册方式。"
            : "注册方式没有保存，站点仍按原来的规则处理新账户，可以重试。",
        );
      } finally {
        if (mountedRef.current) setRegistrationPending(false);
      }
    },
    [
      api,
      expireIfInvalidSession,
      registrationMode,
      registrationPending,
      session,
    ],
  );

  let roster: ReactNode;
  if (failed) {
    roster = (
      <div className={styles.inlineError} role="alert">
        <span>暂时无法读取成员列表。</span>
        <button
          className={styles.quietButton}
          onClick={() => setAttempt((value) => value + 1)}
          type="button"
        >
          重试
        </button>
      </div>
    );
  } else if (accounts === null) {
    roster = <p aria-busy="true">正在读取成员…</p>;
  } else if (accounts.length === 0) {
    roster = <p className={styles.accountEmpty}>本站目前只有你一个账户。</p>;
  } else {
    roster = (
      <>
        <ul className={styles.accountList}>
          {ordered(accounts).map((entry) => {
            const status = entry.status ?? "active";
            // The owner's own row carries no controls: there is no state the
            // owner could put themselves in that the server would accept, and
            // offering the button would only be a way to be told no.
            const governable =
              entry.role !== "owner" && entry.userId !== account?.userId;
            return (
              <li className={styles.accountRow} key={entry.userId}>
                <span className={styles.accountRowMain}>
                  <strong>{entry.displayName}</strong>
                  <small>@{entry.username}</small>
                </span>
                <span
                  className={styles.accountBadge}
                  data-tone={statusTones[status]}
                >
                  {entry.role === "owner" ? "站长" : statusLabels[status]}
                </span>
                {governable ? (
                  <span className={styles.accountRowActions}>
                    {status === "pending" ? (
                      <button
                        className={styles.primaryButton}
                        disabled={!session || busy !== null}
                        onClick={() => void decide(entry.userId, "active")}
                        type="button"
                      >
                        批准
                      </button>
                    ) : null}
                    {status === "active" ? (
                      <button
                        className={styles.quietButton}
                        disabled={!session || busy !== null}
                        onClick={() => void decide(entry.userId, "suspended")}
                        type="button"
                      >
                        停用
                      </button>
                    ) : null}
                    {status === "suspended" ? (
                      <button
                        className={styles.secondaryButton}
                        disabled={!session || busy !== null}
                        onClick={() => void decide(entry.userId, "active")}
                        type="button"
                      >
                        恢复
                      </button>
                    ) : null}
                  </span>
                ) : null}
              </li>
            );
          })}
        </ul>
        {actionError ? (
          <p className={styles.fieldError} role="alert">
            {actionError}
          </p>
        ) : null}
      </>
    );
  }

  return (
    <>
      {registrationMode ? (
        <fieldset className={styles.registrationModes}>
          <legend>谁可以加入</legend>
          {registrationChoices.map((choice) => (
            <label className={styles.registrationMode} key={choice.mode}>
              <input
                checked={registrationMode === choice.mode}
                disabled={!session || registrationPending}
                name="registration-mode"
                onChange={() => void chooseRegistration(choice.mode)}
                type="radio"
                value={choice.mode}
              />
              <span>
                <strong>{choice.label}</strong>
                <small>{choice.detail}</small>
              </span>
            </label>
          ))}
          {registrationError ? (
            <p className={styles.fieldError} role="alert">
              {registrationError}
            </p>
          ) : null}
        </fieldset>
      ) : null}
      {roster}
    </>
  );
}
