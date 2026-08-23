import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { Account, AccountStatus } from "../../api/public";
import { useOwner } from "./OwnerProvider";
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
  const { api, session, account, expireIfInvalidSession } = useOwner();
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const mountedRef = useRef(true);

  useEffect(
    () => () => {
      mountedRef.current = false;
    },
    [],
  );

  useEffect(() => {
    const controller = new AbortController();
    setFailed(false);
    void api
      .ownerAccounts(controller.signal)
      .then((next) => {
        if (!controller.signal.aborted) setAccounts(next);
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

  if (failed) {
    return (
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
  }
  if (accounts === null) return <p aria-busy="true">正在读取成员…</p>;
  if (accounts.length === 0) {
    return <p className={styles.accountEmpty}>本站目前只有你一个账户。</p>;
  }

  return (
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
