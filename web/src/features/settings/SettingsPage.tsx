import { useEffect, useState, type FormEvent } from "react";
import { accountsApi } from "../../api/accounts";
import { selectSiteProfile, siteApi } from "../../api/site";
import { type Account, type SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { AccountAccess } from "../auth/AccountAccess";
import { ProfileEditor } from "../profile/ProfileEditor";
import { AccountSecurity } from "./AccountSecurity";
import { MembersSection } from "./MembersSection";
import { BackupRestore } from "./BackupRestore";
import {
  useSettingsSections,
  type SettingsSection,
} from "./useSettingsSections";
import forms from "../../components/Forms.module.css";
import styles from "./Settings.module.css";

const titles: Record<SettingsSection, string> = {
  account: "账号",
  site: "站点",
  members: "成员",
  backups: "备份",
};

function SiteConfiguration({
  site,
  onSaved,
  active,
}: {
  site: SiteIdentity;
  onSaved: (site: SiteIdentity) => void;
  active: boolean;
}) {
  const { session, expireIfInvalidSession } = useSession();
  const [title, setTitle] = useState(site.title);
  const [timezone, setTimezone] = useState(site.timezone);
  const [url, setUrl] = useState(site.publicBaseUrl ?? "");
  const [comments, setComments] = useState(site.commentsEnabled);
  const [profile, setProfile] = useState(site.profileUserId);
  const [roster, setRoster] = useState<Account[]>([]);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    void accountsApi
      .accounts(controller.signal)
      .then((result) => {
        if (!controller.signal.aborted) setRoster(result.accounts);
      })
      .catch((next: unknown) => {
        if (!controller.signal.aborted && !expireIfInvalidSession(next))
          setError("展示账号列表暂时无法读取。");
      });
    return () => controller.abort();
  }, [active, expireIfInvalidSession]);
  async function save(event: FormEvent) {
    event.preventDefault();
    if (!session || pending) return;
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      const saved = await siteApi.updateSite(session.csrfToken, {
        title,
        timezone,
        publicBaseUrl: url.trim() || null,
        commentsEnabled: comments,
      });
      onSaved({
        ...saved,
        instanceId: site.instanceId,
        instanceInitializedAt: site.instanceInitializedAt,
      });
      setNotice("站点设置已保存。");
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError("站点设置没有保存。请检查时区和网址后重试。");
    } finally {
      setPending(false);
    }
  }
  async function selectProfile() {
    if (!session || pending) return;
    setPending(true);
    setError(null);
    setNotice(null);
    try {
      const saved = await selectSiteProfile(session.csrfToken, profile);
      onSaved({
        ...saved,
        instanceId: site.instanceId,
        instanceInitializedAt: site.instanceInitializedAt,
      });
      setNotice("首页展示账号已更新，账号权限保持不变。");
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError("展示账号没有更改，请选择已启用的账号后重试。");
    } finally {
      setPending(false);
    }
  }
  return (
    <section aria-label="站点设置">
      <header className={styles.sectionHeader}>
        <h2>站点设置</h2>
      </header>
      <form
        className={styles.settingsForm}
        onSubmit={(event) => void save(event)}
      >
        <label>
          站点标题
          <input
            disabled={pending}
            maxLength={120}
            onChange={(event) => setTitle(event.target.value)}
            required
            value={title}
          />
        </label>
        <label>
          时区
          <input
            disabled={pending}
            onChange={(event) => setTimezone(event.target.value)}
            placeholder="Asia/Shanghai"
            required
            value={timezone}
          />
        </label>
        <label>
          公开网址
          <input
            disabled={pending}
            onChange={(event) => setUrl(event.target.value)}
            placeholder="https://example.com"
            type="url"
            value={url}
          />
        </label>
        <label className={styles.toggleField}>
          <input
            checked={comments}
            disabled={pending}
            onChange={(event) => setComments(event.target.checked)}
            type="checkbox"
          />
          允许新评论
        </label>
        <button
          className={forms.primaryButton}
          disabled={!session || pending}
          type="submit"
        >
          {pending ? "正在保存…" : "保存站点设置"}
        </button>
      </form>
      <div className={styles.siteProfile}>
        <h3>首页展示账号</h3>
        <p>
          选择首页使用的昵称、简介和图片。展示账号可以是普通成员，其管理权限不会因此变化。
        </p>
        <label>
          展示账号
          <select
            disabled={pending || roster.length === 0}
            onChange={(event) => setProfile(event.target.value)}
            value={profile}
          >
            {roster
              .filter((user) => user.status === "active")
              .map((user) => (
                <option key={user.userId} value={user.userId}>
                  {user.displayName} @{user.username}
                </option>
              ))}
          </select>
        </label>
        <button
          className={forms.secondaryButton}
          disabled={!session || pending || profile === site.profileUserId}
          onClick={() => void selectProfile()}
          type="button"
        >
          更新展示账号
        </button>
      </div>
      {error ? (
        <p className={forms.formError} role="alert">
          {error}
        </p>
      ) : null}
      {notice ? <p role="status">{notice}</p> : null}
    </section>
  );
}

export function SettingsPage({
  site,
  onSaved,
}: {
  site: SiteIdentity;
  onSaved: (site: SiteIdentity) => void;
}) {
  const navigation = useNavigation();
  const { account, applyAccount } = useSession();
  const [restoreBusy, setRestoreBusy] = useState(false);
  const [waiting, setWaiting] = useState(0);
  const admin = isAdministrator(account);
  const { section, navRef, contentRef, goToSection } =
    useSettingsSections(admin);
  const sections: SettingsSection[] = admin
    ? ["account", "site", "members", "backups"]
    : ["account"];
  function adopt(saved: Account) {
    applyAccount(saved);
    if (site.profileUserId === saved.userId)
      onSaved({
        ...site,
        username: saved.username,
        displayName: saved.displayName,
        bio: saved.bio,
        avatarMediaId: saved.avatarMediaId,
        coverMediaId: saved.coverMediaId,
      });
  }
  return (
    <div className={styles.settingsPage}>
      <nav aria-label="设置分类" className={styles.settingsNav} ref={navRef}>
        {sections.map((item) => (
          <a
            aria-current={section === item ? "location" : undefined}
            href={`/settings/${item}`}
            key={item}
            onClick={(event) => goToSection(event, item)}
          >
            {titles[item]}
            {item === "members" && waiting > 0 ? (
              <span aria-hidden="true" className={styles.navBadge}>
                {waiting}
              </span>
            ) : null}
          </a>
        ))}
      </nav>
      <div className={styles.settingsContent} ref={contentRef}>
        {section !== "account" && !admin ? (
          <p role="alert">此设置需要管理员权限。</p>
        ) : null}
        {account ? (
          <div className={styles.settingsGroup} id="settings-account">
            <section aria-label="个人资料">
              <header className={styles.sectionHeader}>
                <h2>个人资料</h2>
              </header>
              <p>
                {account.displayName} @{account.username}
              </p>
              <ProfileEditor account={account} onSaved={adopt} />
            </section>
            <AccountSecurity instanceId={site.instanceId} />
            <section aria-label="登录方式与会话">
              <header className={styles.sectionHeader}>
                <h2>登录方式与会话</h2>
              </header>
              <AccountAccess
                timezone={site.timezone}
                instanceId={site.instanceId}
                onSignedOut={() => navigation.navigate("/login")}
              />
            </section>
          </div>
        ) : null}
        {admin ? (
          <div className={styles.settingsGroup} id="settings-site">
            <SiteConfiguration
              site={site}
              onSaved={onSaved}
              active={section === "site"}
            />
          </div>
        ) : null}
        {admin ? (
          <div className={styles.settingsGroup} id="settings-members">
            <MembersSection onWaitingChange={setWaiting} />
          </div>
        ) : null}
        {admin ? (
          <div className={styles.settingsGroup} id="settings-backups">
            <BackupRestore
              instanceId={site.instanceId}
              settingsBlocked={false}
              onRestoreBusyChange={setRestoreBusy}
            />
          </div>
        ) : null}
        {restoreBusy && section === "backups" ? (
          <p aria-live="polite">正在检查备份状态…</p>
        ) : null}
      </div>
    </div>
  );
}
