import { useRef } from "react";
import { useTranslation } from "react-i18next";
import { Menu } from "@base-ui/react/menu";
import { type PublicAccount, type SiteIdentity } from "../../api/public";
import { AccountName } from "../../components/AccountName";
import { SiteAvatar } from "../../components/SiteIdentity";
import { Icon } from "../../components/icons";
import { Dialog } from "../../components/Dialog";
import { ConfirmDialog } from "../../components/ConfirmDialog";
import { useDocumentScrollLock } from "../../components/useDocumentScrollLock";
import { useSession } from "./SessionProvider";
import { useBrowserAccounts } from "./BrowserAccountsProvider";
import { CredentialLogin } from "./CredentialLogin";
import { notificationBadgeText } from "../notifications/NotificationBadge";
import styles from "./AccountSwitcher.module.css";

function orderedAccounts(
  current: PublicAccount | null,
  accounts: { account: PublicAccount; available: boolean }[],
) {
  return [
    ...(current ? [{ account: current, available: true }] : []),
    ...accounts.filter((entry) => entry.account.userId !== current?.userId),
  ];
}

function Identity({
  account,
  selected,
  count = 0,
  unavailable = false,
}: {
  account: PublicAccount;
  selected: boolean;
  count?: number;
  unavailable?: boolean;
}) {
  const { t } = useTranslation();
  const badge = notificationBadgeText(count);
  return (
    <>
      <span className={styles.avatar}>
        <SiteAvatar site={{ ...account, title: account.displayName }} />
      </span>
      <span className={styles.identity}>
        <span className={styles.name}>
          <AccountName identity={account} />
        </span>
        <span className={styles.handle}>@{account.username}</span>
        {unavailable ? (
          <span className={styles.expired}>{t("需要重新登录")}</span>
        ) : null}
      </span>
      {selected ? (
        <span className={styles.selected} aria-label={t("当前账户")}>
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M12 2a10 10 0 1 0 0 20 10 10 0 0 0 0-20Zm-1.25 14.2-4-4 1.4-1.4 2.4 2.4 5-6 1.5 1.3-6.3 7.7Z" />
          </svg>
        </span>
      ) : badge ? (
        <span
          className={styles.badge}
          data-size={count >= 20 ? "capped" : count >= 10 ? "double" : "single"}
          aria-label={t("{{count}} 条新通知", { count })}
        >
          {badge}
        </span>
      ) : null}
    </>
  );
}

export function AccountMenuAccounts() {
  const browser = useBrowserAccounts();
  const { account } = useSession();
  const { t } = useTranslation();
  if (!browser) return null;
  const entries = orderedAccounts(account, browser.snapshot?.accounts ?? []);
  return (
    <>
      <div className={styles.menuAccounts}>
        {entries.map((entry) => {
          const selected = entry.account.userId === account?.userId;
          return (
            <Menu.Item
              key={entry.account.userId}
              disabled={selected || browser.busy}
              className={styles.menuRow}
              data-current={selected || undefined}
              onClick={() =>
                entry.available
                  ? browser.activate(entry.account.userId)
                  : browser.openAdd(entry.account.username)
              }
              aria-label={
                selected
                  ? t("当前账户 @{{username}}", {
                      username: entry.account.username,
                    })
                  : t("切换到 @{{username}}", {
                      username: entry.account.username,
                    })
              }
            >
              <Identity
                account={entry.account}
                selected={selected}
                count={browser.counts[entry.account.userId]}
                unavailable={!entry.available}
              />
            </Menu.Item>
          );
        })}
      </div>
      {browser.error ? (
        <button className={styles.retry} onClick={() => void browser.refresh()}>
          {t("账户列表暂时无法刷新，请重试。")}
        </button>
      ) : null}
      <Menu.Separator className={styles.separator} />
    </>
  );
}

export function AccountMenuActions({ className }: { className: string }) {
  const browser = useBrowserAccounts();
  const { account } = useSession();
  const { t } = useTranslation();
  if (!browser) return null;
  const multiple =
    orderedAccounts(account, browser.snapshot?.accounts ?? []).length > 1;
  return (
    <>
      <Menu.Item className={className} onClick={() => browser.openAdd()}>
        {t("添加已有账户")}
      </Menu.Item>
      {multiple ? (
        <Menu.Item className={className} onClick={browser.openManage}>
          {t("管理账户")}
        </Menu.Item>
      ) : null}
      {account ? (
        <Menu.Item
          className={className}
          onClick={() => browser.requestRemove(account)}
        >
          {t("退出 @")}
          {account.username}
        </Menu.Item>
      ) : null}
    </>
  );
}

export function BrowserAccountDialogs({ site }: { site: SiteIdentity }) {
  const browser = useBrowserAccounts();
  const { account } = useSession();
  const { t } = useTranslation();
  const popup = useRef<HTMLDivElement>(null);
  const lastRemoval = useRef(browser?.removal ?? null);
  if (browser?.removal) lastRemoval.current = browser.removal;
  useDocumentScrollLock(!!browser?.mode || !!browser?.removal);
  if (!browser) return null;
  const entries = orderedAccounts(account, browser.snapshot?.accounts ?? []);
  const adding = browser.mode === "add";
  const removal = browser.removal ?? lastRemoval.current;
  const closeFocus = () => {
    const triggers = [
      ...document.querySelectorAll<HTMLElement>("[data-account-menu-trigger]"),
    ];
    return (
      triggers.find((el) => el.getClientRects().length > 0) ??
      triggers[0] ??
      document.querySelector<HTMLElement>('a[href^="/login"]')
    );
  };
  return (
    <>
      <Dialog.Root
        modal="trap-focus"
        open={browser.mode !== null}
        onOpenChange={(open) => {
          if (!open) browser.close();
        }}
      >
        <Dialog.Portal>
          <Dialog.Backdrop className={styles.backdrop} />
          <Dialog.Viewport className={styles.viewport}>
            <Dialog.Popup
              ref={popup}
              finalFocus={closeFocus}
              className={`${styles.popup} ${adding ? styles.addPopup : ""}`}
            >
              <header className={styles.header}>
                <Dialog.Close
                  className={styles.close}
                  disabled={browser.busy}
                  aria-label={adding ? t("返回") : t("关闭")}
                >
                  <Icon name={adding ? "back" : "close"} />
                </Dialog.Close>
                <Dialog.Title
                  className={adding ? styles.hiddenTitle : styles.title}
                >
                  {adding ? t("添加已有账户") : t("多个账户")}
                </Dialog.Title>
              </header>
              {adding ? (
                <div className={styles.addBody}>
                  <span className={styles.brand} aria-hidden="true" />
                  <h2>{t("登录 Moment")}</h2>
                  <CredentialLogin
                    key={browser.username}
                    site={site}
                    heading={false}
                    initialUsername={browser.username}
                    authenticate={browser.add}
                    authorizeProvider={browser.startOAuth}
                  />
                </div>
              ) : (
                <>
                  <div className={styles.manageAccounts}>
                    {entries.map((entry) => {
                      const selected = entry.account.userId === account?.userId;
                      return (
                        <div
                          key={entry.account.userId}
                          className={styles.manageRow}
                        >
                          <button
                            type="button"
                            className={styles.accountButton}
                            data-account-choice
                            data-current={selected || undefined}
                            disabled={selected || browser.busy}
                            onClick={() =>
                              entry.available
                                ? browser.activate(entry.account.userId)
                                : browser.openAdd(entry.account.username)
                            }
                            aria-label={
                              selected
                                ? t("当前账户 @{{username}}", {
                                    username: entry.account.username,
                                  })
                                : t("切换到 @{{username}}", {
                                    username: entry.account.username,
                                  })
                            }
                          >
                            <Identity
                              account={entry.account}
                              selected={selected}
                              unavailable={!entry.available}
                            />
                          </button>
                          {!selected ? (
                            <button
                              type="button"
                              className={styles.remove}
                              disabled={browser.busy}
                              aria-label={t("退出 @{{username}}", {
                                username: entry.account.username,
                              })}
                              title={t("退出 @{{username}}", {
                                username: entry.account.username,
                              })}
                              onClick={(event) =>
                                browser.requestRemove(
                                  entry.account,
                                  event.currentTarget,
                                )
                              }
                            >
                              <Icon name="close" />
                            </button>
                          ) : null}
                        </div>
                      );
                    })}
                  </div>
                  {browser.error ? (
                    <button
                      className={styles.retry}
                      onClick={() => void browser.refresh()}
                    >
                      {t("账户列表暂时无法刷新，请重试。")}
                    </button>
                  ) : null}
                  <div className={styles.managementActions}>
                    <button
                      type="button"
                      className={styles.addAccount}
                      disabled={browser.busy}
                      onClick={() => browser.openAdd()}
                    >
                      {t("添加已有账户")}
                    </button>
                    <button
                      type="button"
                      className={styles.logoutAll}
                      disabled={browser.busy}
                      onClick={(event) =>
                        browser.requestRemove(null, event.currentTarget)
                      }
                    >
                      {t("退出此浏览器中的全部账户")}
                    </button>
                  </div>
                </>
              )}
            </Dialog.Popup>
          </Dialog.Viewport>
        </Dialog.Portal>
      </Dialog.Root>
      <ConfirmDialog
        open={browser.removal !== null}
        title={
          removal?.account
            ? t("退出 @{{username}}？", {
                username: removal.account.username,
              })
            : t("退出全部账户？")
        }
        description={
          removal?.account
            ? t(
                "此操作只退出该账户在此浏览器中的登录，不影响其他设备。该账户的本地草稿会被清除。",
              )
            : t(
                "此操作会退出此浏览器中的全部账户并清除它们的本地草稿，不影响其他设备。",
              )
        }
        confirmLabel={t("退出登录")}
        cancelLabel={t("取消")}
        danger
        pending={browser.busy}
        onOpenChange={(open) => {
          if (!open) browser.cancelRemove();
        }}
        finalFocus={() =>
          browser.mode !== null && removal?.focus?.isConnected
            ? removal.focus
            : browser.mode !== null && popup.current?.isConnected
              ? (popup.current.querySelector<HTMLElement>(
                  "[data-account-choice]:not(:disabled)",
                ) ?? popup.current)
              : closeFocus()
        }
        onConfirm={() =>
          browser.remove(removal?.account?.userId, site.instanceId)
        }
      />
    </>
  );
}
