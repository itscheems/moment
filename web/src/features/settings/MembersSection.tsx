import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { AccountName } from "../../components/AccountName";
import { Dialog } from "../../components/Dialog";
import { Collapsible } from "@base-ui/react/collapsible";
import { Menu } from "@base-ui/react/menu";
import { useEffect, useMemo, useRef, useState } from "react";
import { accountsApi, setAccountRole } from "../../api/accounts";
import { siteApi } from "../../api/site";
import type { RegistrationMode } from "../../api/client";
import {
  PublicApiError,
  type Account,
  type AccountStatus,
  type AccountRole,
} from "../../api/public";
import { notify } from "../../components/Feedback";
import { Icon } from "../../components/icons";
import { SiteAvatar } from "../../components/SiteIdentity";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { useSession } from "../auth/SessionProvider";
import forms from "../../components/Forms.module.css";
import settings from "./Settings.module.css";
import styles from "./Members.module.css";

const statusLabels: Record<AccountStatus, string> = {
  get pending() {
    return t("待批准");
  },
  get active() {
    return t("已启用");
  },
  get suspended() {
    return t("已停用");
  },
};
const statusOrder: Record<AccountStatus, number> = {
  pending: 0,
  active: 1,
  suspended: 2,
};
const registrationChoices: {
  mode: RegistrationMode;
  label: string;
  detail: string;
}[] = [
  {
    mode: "open",
    get label() {
      return t("开放注册");
    },
    get detail() {
      return t("新账号登录后即可发布内容和回复。");
    },
  },
  {
    mode: "approval",
    get label() {
      return t("需要审核");
    },
    get detail() {
      return t("新账号可以登录和阅读，经你批准后才能发布内容。");
    },
  },
  {
    mode: "closed",
    get label() {
      return t("关闭注册");
    },
    get detail() {
      return t("暂停接受新账号，已有账号仍可登录。");
    },
  },
];
type MemberChange =
  | { kind: "status"; value: AccountStatus }
  | { kind: "role"; value: AccountRole };
type Decision = {
  entry: Account;
  change: MemberChange;
  returnFocus?: HTMLElement | null;
};
type ActionFailure = Decision & { message: string };

function MemberRow({
  entry,
  current,
  lastAdmin,
  busy,
  onChange,
  onConfirm,
}: {
  entry: Account;
  current: boolean;
  lastAdmin: boolean;
  busy: string | null;
  onChange: (entry: Account, change: MemberChange) => Promise<boolean>;
  onConfirm: (decision: Decision) => void;
}) {
  const { t } = useTranslation();

  const [menuOpen, setMenuOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const pending = busy === entry.userId;
  const ask = (change: MemberChange) => {
    setMenuOpen(false);
    onConfirm({ entry, change, returnFocus: triggerRef.current });
  };
  const apply = async () => {
    if (await onChange(entry, { kind: "status", value: "active" }))
      window.requestAnimationFrame(() => {
        const target =
          triggerRef.current ?? document.getElementById("members-roster-title");
        target?.focus({ preventScroll: true });
      });
  };
  return (
    <li
      aria-busy={pending}
      className={styles.memberRow}
      data-status={entry.status}
    >
      <SiteAvatar site={entry} size={40} />
      <div className={styles.identity}>
        <div className={styles.nameLine}>
          <strong>
            <AccountName identity={entry} />
          </strong>
          {current ? <span className={styles.self}>{t("你")}</span> : null}
        </div>
        <span className={styles.handle}>@{entry.username}</span>
        <div className={styles.memberMeta}>
          <span className={styles.status} data-status={entry.status}>
            {statusLabels[entry.status]}
          </span>
          {entry.role === "admin" ? <span>{t("管理员")}</span> : null}
          {pending ? <span role="status">{t("正在保存…")}</span> : null}
        </div>
      </div>
      <div className={styles.rowActions}>
        {!current && entry.status !== "active" ? (
          <button
            className={
              entry.status === "pending"
                ? forms.primaryButton
                : forms.secondaryButton
            }
            disabled={busy !== null}
            onClick={() => void apply()}
            type="button"
          >
            {pending
              ? t("处理中…")
              : entry.status === "pending"
                ? t("批准")
                : t("恢复")}
          </button>
        ) : null}
        <Menu.Root open={menuOpen} onOpenChange={setMenuOpen}>
          <Menu.Trigger
            aria-label={t("管理 {{value1}}", { value1: entry.displayName })}
            className={forms.actionsMenuTrigger}
            disabled={busy !== null}
            ref={triggerRef}
          >
            <Icon name="ellipsis" />
          </Menu.Trigger>
          <Menu.Portal>
            <Menu.Positioner
              align="end"
              side="bottom"
              sideOffset={4}
              className={forms.actionsMenuPositioner}
            >
              <Menu.Popup
                aria-label={t("{{value1}} 的管理操作", {
                  value1: entry.displayName,
                })}
                className={forms.actionsMenuPopup}
              >
                <Menu.Item
                  className={forms.actionsMenuItem}
                  disabled={lastAdmin}
                  onClick={() =>
                    ask({
                      kind: "role",
                      value: entry.role === "admin" ? "member" : "admin",
                    })
                  }
                >
                  <Icon name="account" />
                  <span>
                    {entry.role === "admin" ? t("设为成员") : t("设为管理员")}
                  </span>
                </Menu.Item>
                {lastAdmin ? (
                  <p className={styles.menuHint}>
                    {t("至少保留一位已启用的管理员")}
                  </p>
                ) : null}
                {!current && entry.status === "active" ? (
                  <Menu.Item
                    className={`${forms.actionsMenuItem} ${forms.actionsMenuDanger}`}
                    onClick={() => ask({ kind: "status", value: "suspended" })}
                  >
                    <Icon name="close" />
                    <span>{t("停用账号")}</span>
                  </Menu.Item>
                ) : null}
              </Menu.Popup>
            </Menu.Positioner>
          </Menu.Portal>
        </Menu.Root>
      </div>
    </li>
  );
}

export function MembersSection({
  onWaitingChange,
}: { onWaitingChange?: (waiting: number) => void } = {}) {
  const { t } = useTranslation();

  const { session, account, applyAccount, expireIfInvalidSession } =
    useSession();
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [registrationMode, setRegistrationMode] =
    useState<RegistrationMode | null>(null);
  const [registrationPending, setRegistrationPending] = useState(false);
  const [registrationError, setRegistrationError] = useState<string | null>(
    null,
  );
  const [registrationNotice, setRegistrationNotice] = useState<string | null>(
    null,
  );
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [busy, setBusy] = useState<string | null>(null);
  const [actionFailure, setActionFailure] = useState<ActionFailure | null>(
    null,
  );
  const [notice, setNotice] = useState<string | null>(null);
  const [decision, setDecision] = useState<Decision | null>(null);
  const [filter, setFilter] = useState<"all" | AccountStatus>("all");
  const [query, setQuery] = useState("");
  const [policyOpen, setPolicyOpen] = useState(false);
  const mounted = useRef(false);
  const pendingWrite = useRef(false);
  const returnFocusRef = useRef<HTMLElement | null>(null);
  useDocumentScrollLock(decision !== null);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setFailed(false);
    void accountsApi
      .accounts(controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return;
        setAccounts(next.accounts);
        setRegistrationMode(next.registrationMode);
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted && !expireIfInvalidSession(error))
          setFailed(true);
      });
    return () => controller.abort();
  }, [attempt, expireIfInvalidSession]);
  const counts = useMemo(
    () => ({
      all: accounts?.length ?? 0,
      pending:
        accounts?.filter((entry) => entry.status === "pending").length ?? 0,
      active:
        accounts?.filter((entry) => entry.status === "active").length ?? 0,
      suspended:
        accounts?.filter((entry) => entry.status === "suspended").length ?? 0,
    }),
    [accounts],
  );
  useEffect(
    () => onWaitingChange?.(counts.pending),
    [onWaitingChange, counts.pending],
  );
  const activeAdmins =
    accounts?.filter(
      (entry) => entry.role === "admin" && entry.status === "active",
    ).length ?? 0;
  const search = query.trim().toLocaleLowerCase();
  const visible = (accounts ?? [])
    .filter(
      (entry) =>
        (filter === "all" || entry.status === filter) &&
        (!search ||
          `${entry.displayName} @${entry.username}`
            .toLocaleLowerCase()
            .includes(search)),
    )
    .sort(
      (left, right) =>
        statusOrder[left.status] - statusOrder[right.status] ||
        left.username.localeCompare(right.username),
    );

  async function changeMember(
    entry: Account,
    change: MemberChange,
  ): Promise<boolean> {
    if (!session || pendingWrite.current) return false;
    pendingWrite.current = true;
    setBusy(entry.userId);
    setActionFailure(null);
    setNotice(null);
    try {
      const saved =
        change.kind === "role"
          ? await setAccountRole(session.csrfToken, entry.userId, change.value)
          : await accountsApi.updateAccountStatus(
              session.csrfToken,
              entry.userId,
              change.value,
            );
      if (!mounted.current) return false;
      setAccounts(
        (current) =>
          current?.map((user) =>
            user.userId === saved.userId ? saved : user,
          ) ?? null,
      );
      if (saved.userId === account?.userId) applyAccount(saved);
      const message =
        change.kind === "role"
          ? t("已将 {{value1}} 设为{{value2}}。", {
              value1: saved.displayName,
              value2: saved.role === "admin" ? t("管理员") : t("成员"),
            })
          : t("已{{value1}} {{value2}}。", {
              value1:
                change.value === "suspended"
                  ? t("停用")
                  : entry.status === "pending"
                    ? t("批准")
                    : t("恢复"),
              value2: saved.displayName,
            });
      setNotice(message);
      notify(message, { id: "members-action" });
      return true;
    } catch (error) {
      if (mounted.current && !expireIfInvalidSession(error)) {
        const message =
          error instanceof PublicApiError &&
          error.code === "REAUTHENTICATION_REQUIRED"
            ? t("身份验证未完成，操作未执行。")
            : error instanceof PublicApiError &&
                error.code === "LAST_ADMINISTRATOR"
              ? t("至少需要保留一位已启用的管理员。")
              : t("这次改动没有保存，账号保持原样。请重试。");
        setActionFailure({ entry, change, message });
        notify(message, { id: "members-action", type: "error" });
      }
      return false;
    } finally {
      pendingWrite.current = false;
      if (mounted.current) setBusy(null);
    }
  }
  async function chooseRegistration(next: RegistrationMode) {
    if (!session || registrationPending || next === registrationMode) return;
    setRegistrationPending(true);
    setRegistrationError(null);
    setRegistrationNotice(null);
    try {
      await siteApi.updateSite(session.csrfToken, { registrationMode: next });
      if (!mounted.current) return;
      setRegistrationMode(next);
      const label = registrationChoices.find(
        (choice) => choice.mode === next,
      )?.label;
      setRegistrationNotice(t("已保存：{{value1}}", { value1: label }));
      notify(t("加入方式已更新为{{value1}}。", { value1: label }), {
        id: "registration-mode",
      });
    } catch (error) {
      if (mounted.current && !expireIfInvalidSession(error)) {
        const message =
          error instanceof PublicApiError &&
          error.code === "REAUTHENTICATION_REQUIRED"
            ? t("身份验证未完成，操作未执行。")
            : t("注册方式没有保存，站点仍按原来的规则处理新账号，可以重试。");
        setRegistrationError(message);
        notify(message, { id: "registration-mode", type: "error" });
      }
    } finally {
      if (mounted.current) setRegistrationPending(false);
    }
  }
  const confirmLabel =
    decision?.change.kind === "role"
      ? decision.change.value === "admin"
        ? t("设为管理员")
        : t("设为成员")
      : t("停用账号");
  const confirmationError =
    decision && actionFailure?.entry.userId === decision.entry.userId
      ? actionFailure.message
      : null;
  return (
    <section aria-labelledby="members-title" className={styles.membersSection}>
      <header className={`${settings.sectionHeader} ${styles.heading}`}>
        <div>
          <h2 id="members-title">{t("成员管理")}</h2>
          <p>{t("审核新成员，管理账号状态与权限。")}</p>
        </div>
        {counts.pending > 0 ? (
          <span className={styles.waitingCount}>
            {counts.pending} {t("位待审核")}
          </span>
        ) : null}
      </header>
      {registrationMode ? (
        <Collapsible.Root
          className={styles.policyPanel}
          open={policyOpen}
          onOpenChange={setPolicyOpen}
        >
          <div className={styles.policySummary}>
            <div>
              <h3>
                {t("加入方式 ·")}{" "}
                {
                  registrationChoices.find(
                    (choice) => choice.mode === registrationMode,
                  )?.label
                }
              </h3>
              <p>
                {
                  registrationChoices.find(
                    (choice) => choice.mode === registrationMode,
                  )?.detail
                }
              </p>
            </div>
            <Collapsible.Trigger
              aria-label={policyOpen ? t("收起加入方式") : t("更改加入方式")}
              className={styles.policyTrigger}
            >
              <span>{policyOpen ? t("收起") : t("更改")}</span>
              <Icon name="chevron-down" />
            </Collapsible.Trigger>
          </div>
          <Collapsible.Panel className={styles.policyDetails}>
            <fieldset
              className={styles.registrationModes}
              aria-busy={registrationPending}
            >
              <legend>{t("谁可以加入")}</legend>
              <p className={styles.sectionNote}>
                {t("选择新账号加入站点的方式，更改后自动保存。")}
              </p>
              <div className={styles.policyOptions}>
                {registrationChoices.map((choice) => (
                  <label
                    className={styles.registrationMode}
                    key={choice.mode}
                    data-selected={registrationMode === choice.mode}
                  >
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
                    {registrationMode === choice.mode ? (
                      <Icon name="check" />
                    ) : null}
                  </label>
                ))}
              </div>
              <p aria-live="polite" className={styles.policyFeedback}>
                {registrationPending
                  ? t("正在保存加入方式…")
                  : registrationNotice}
              </p>
              {registrationError ? (
                <p className={forms.formError} role="alert">
                  {registrationError}
                </p>
              ) : null}
            </fieldset>
          </Collapsible.Panel>
        </Collapsible.Root>
      ) : null}
      <div className={styles.rosterHeader}>
        <h3 id="members-roster-title" tabIndex={-1}>
          {t("站点成员")}
        </h3>
        {accounts ? (
          <span>
            {counts.all} {t("个账号")}
          </span>
        ) : null}
      </div>
      {failed ? (
        <div className={styles.emptyState} role="alert">
          <Icon name="info" />
          <strong>{t("暂时无法读取成员列表")}</strong>
          <p>{t("请检查网络连接后重试。")}</p>
          <button
            className={forms.secondaryButton}
            onClick={() => setAttempt((value) => value + 1)}
            type="button"
          >
            {t("重试")}
          </button>
        </div>
      ) : accounts === null ? (
        <div className={styles.loadingState} aria-busy="true" role="status">
          {t("正在读取成员…")}
          <span />
          <span />
          <span />
        </div>
      ) : (
        <>
          <div className={styles.searchField}>
            <Icon name="search" />
            <input
              aria-label={t("搜索成员")}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t("搜索昵称或用户名")}
              type="search"
              value={query}
            />
            {query ? (
              <button
                aria-label={t("清除成员搜索")}
                onClick={() => setQuery("")}
                type="button"
              >
                <Icon name="close" />
              </button>
            ) : null}
          </div>
          <div
            aria-label={t("筛选成员")}
            className={styles.filters}
            role="group"
          >
            {(["all", "pending", "active", "suspended"] as const).map(
              (value) => (
                <button
                  aria-pressed={filter === value}
                  key={value}
                  onClick={() => setFilter(value)}
                  type="button"
                >
                  {value === "all" ? t("全部") : statusLabels[value]}
                  <span>{counts[value]}</span>
                </button>
              ),
            )}
          </div>
          {visible.length > 0 ? (
            <ul aria-label={t("站点成员")} className={styles.memberList}>
              {visible.map((entry) => (
                <MemberRow
                  key={entry.userId}
                  entry={entry}
                  current={entry.userId === account?.userId}
                  lastAdmin={
                    entry.role === "admin" &&
                    entry.status === "active" &&
                    activeAdmins <= 1
                  }
                  busy={busy}
                  onChange={changeMember}
                  onConfirm={(next) => {
                    returnFocusRef.current = next.returnFocus ?? null;
                    setActionFailure(null);
                    setDecision(next);
                  }}
                />
              ))}
            </ul>
          ) : (
            <div className={styles.emptyState}>
              <Icon name="account" />
              <strong>
                {search
                  ? t("没有找到匹配的成员")
                  : filter === "pending"
                    ? t("没有待审核的成员")
                    : t("这里还没有成员")}
              </strong>
              <p>
                {search
                  ? t("试试其他昵称或用户名。")
                  : filter === "pending"
                    ? t("新的加入申请会显示在这里。")
                    : t("可以切换到其他状态查看。")}
              </p>
              {search ? (
                <button
                  className={forms.secondaryButton}
                  onClick={() => setQuery("")}
                  type="button"
                >
                  {t("清除搜索")}
                </button>
              ) : null}
            </div>
          )}
        </>
      )}
      {!decision && actionFailure ? (
        <div className={styles.actionError} role="alert">
          <span>{actionFailure.message}</span>
          <button
            onClick={() =>
              void changeMember(actionFailure.entry, actionFailure.change)
            }
            disabled={busy !== null}
            type="button"
          >
            {t("重试")}
          </button>
        </div>
      ) : null}
      <p className={styles.announcement} role="status">
        {notice}
      </p>
      <Dialog.Root
        open={decision !== null}
        onOpenChange={(open) => {
          if (!open && !busy) {
            setDecision(null);
            setActionFailure(null);
          }
        }}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={forms.dialogBackdrop} />
          <Dialog.Viewport className={forms.dialogViewport}>
            <Dialog.Popup
              className={forms.dialogPopup}
              finalFocus={returnFocusRef}
            >
              <Dialog.Title className={forms.dialogTitle}>
                {confirmLabel}：{decision?.entry.displayName}？
              </Dialog.Title>
              <Dialog.Description className={forms.dialogDescription}>
                {decision?.change.kind === "status"
                  ? t(
                      "停用后，该账号将无法登录或发布新内容。已发布的内容会保留。",
                    )
                  : decision?.change.value === "admin"
                    ? t(
                        "管理员可以管理站点、成员和备份。其他账号的私密内容与草稿仍仅作者可见。",
                      )
                    : t("该账号将不再拥有站点管理权限，已发布的内容会保留。")}
              </Dialog.Description>
              {confirmationError ? (
                <p className={forms.dialogError} role="alert">
                  {confirmationError}
                </p>
              ) : null}
              <div className={forms.dialogActions}>
                <button
                  className={
                    decision?.change.kind === "status"
                      ? forms.dangerButton
                      : forms.primaryButton
                  }
                  disabled={busy !== null}
                  onClick={() => {
                    if (decision)
                      void changeMember(decision.entry, decision.change).then(
                        (saved) => {
                          if (saved) setDecision(null);
                        },
                      );
                  }}
                  type="button"
                >
                  {busy
                    ? t("正在保存…")
                    : confirmationError
                      ? t("重试")
                      : confirmLabel}
                </button>
                <Dialog.Close
                  className={forms.secondaryButton}
                  disabled={busy !== null}
                >
                  {t("取消")}
                </Dialog.Close>
              </div>
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
    </section>
  );
}
