import { SearchField } from "../memory/SearchField";
import { SettingsTextField } from "./SettingsTextField";
import { Autocomplete } from "@base-ui/react/autocomplete";
import { Tabs } from "@base-ui/react/tabs";
import { useTranslation } from "react-i18next";
import { useLoginNavigation } from "../auth/loginNavigation";
import { CredentialConfirmation } from "./CredentialConfirmation";
import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
  type MouseEvent,
} from "react";
import { accountsApi } from "../../api/accounts";
import { selectSiteProfile, siteApi } from "../../api/site";
import {
  PublicApiError,
  type Account,
  type SiteIdentity,
} from "../../api/public";
import { isAdministrator, useSession } from "../auth/SessionProvider";
import { AccountAccess } from "../auth/AccountAccess";
import { ProfileEditor } from "../profile/ProfileEditor";
import { AccountSecurity } from "./AccountSecurity";
import { MembersSection } from "./MembersSection";
import { PersonalData } from "./PersonalData";
import { BackupRestore } from "./BackupRestore";
import { PROFILE_EDIT_PATH } from "../../app/routes";
import { useNavigation, InternalLink } from "../../app/navigation";
import { BackControl } from "../../components/BackControl";
import { Icon } from "../../components/icons";
import { AppearanceDialog } from "../../components/ThemeMenu";
import { LanguageDialog } from "../../components/LanguageDialog";
import {
  useMediaQuery,
  RAIL_VIEWPORT_QUERY,
} from "../../components/useMediaQuery";
import {
  settingsCategories,
  settingsItems,
  settingsItemPath,
  settingsLocation,
  type SettingsItem,
} from "./settingsNavigation";
import forms from "../../components/Forms.module.css";
import styles from "./Settings.module.css";

type SitePanel = "information" | "comments" | "profile";

function SiteConfiguration({
  site,
  onSaved,
  active,
  panel,
}: {
  site: SiteIdentity;
  onSaved: (site: SiteIdentity) => void;
  active: boolean;
  panel: SitePanel;
}) {
  const { t } = useTranslation();

  const { session, expireIfInvalidSession } = useSession();
  const [title, setTitle] = useState(site.title);
  const [timezone, setTimezone] = useState(site.timezone);
  const [url, setUrl] = useState(site.publicBaseUrl ?? "");
  const [comments, setComments] = useState(site.commentsEnabled);
  const [profile, setProfile] = useState(site.profileUserId);
  const [roster, setRoster] = useState<Account[]>([]);
  const [pending, setPending] = useState(false);
  const [errors, setErrors] = useState<
    Partial<Record<SitePanel, string | null>>
  >({});
  const [notices, setNotices] = useState<
    Partial<Record<SitePanel, string | null>>
  >({});
  const error = errors[panel];
  const notice = notices[panel];
  const setError = (value: string | null) =>
    setErrors((current) => ({ ...current, [panel]: value }));
  const setNotice = (value: string | null) =>
    setNotices((current) => ({ ...current, [panel]: value }));
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
          setError(t("展示账号列表暂时无法读取。"));
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
      const saved = await siteApi.updateSite(
        session.csrfToken,
        panel === "comments"
          ? { commentsEnabled: comments }
          : { title, timezone, publicBaseUrl: url.trim() || null },
      );
      onSaved({
        ...saved,
        instanceId: site.instanceId,
        instanceInitializedAt: site.instanceInitializedAt,
      });
      setNotice(t("站点设置已保存。"));
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError(
          next instanceof PublicApiError &&
            next.code === "REAUTHENTICATION_REQUIRED"
            ? t("身份验证未完成，操作未执行。")
            : t("站点设置没有保存。请检查时区和网址后重试。"),
        );
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
      setNotice(t("首页展示账号已更新，账号权限保持不变。"));
    } catch (next) {
      if (!expireIfInvalidSession(next))
        setError(
          next instanceof PublicApiError &&
            next.code === "REAUTHENTICATION_REQUIRED"
            ? t("身份验证未完成，操作未执行。")
            : t("展示账号没有更改，请选择已启用的账号后重试。"),
        );
    } finally {
      setPending(false);
    }
  }
  return (
    <section aria-label={t("站点设置")}>
      <header className={styles.sectionHeader}>
        <h2>{t("站点设置")}</h2>
      </header>
      <form
        className={styles.settingsForm}
        hidden={panel === "profile"}
        onSubmit={(event) => void save(event)}
      >
        <fieldset
          className={styles.settingsFields}
          hidden={panel !== "information"}
          disabled={pending || panel !== "information"}
          aria-label={t("基本信息")}
        >
          <SettingsTextField
            label={t("站点标题")}
            disabled={pending}
            maxLength={120}
            onChange={(event) => setTitle(event.target.value)}
            required
            value={title}
          />
          <SettingsTextField
            label={t("时区")}
            disabled={pending}
            onChange={(event) => setTimezone(event.target.value)}
            required
            value={timezone}
          />
          <SettingsTextField
            label={t("公开网址")}
            disabled={pending}
            onChange={(event) => setUrl(event.target.value)}
            type="url"
            value={url}
          />
        </fieldset>
        <label className={styles.toggleField} hidden={panel !== "comments"}>
          <input
            checked={comments}
            disabled={pending}
            onChange={(event) => setComments(event.target.checked)}
            type="checkbox"
          />
          {t("允许新评论")}
        </label>
        <button
          className={forms.primaryButton}
          disabled={!session || pending}
          type="submit"
        >
          {pending ? t("正在保存…") : t("保存站点设置")}
        </button>
      </form>
      <div className={styles.siteProfile} hidden={panel !== "profile"}>
        <h3>{t("首页展示账号")}</h3>
        <p>
          {t(
            "选择首页使用的昵称、简介和图片。展示账号可以是普通成员，其管理权限不会因此变化。",
          )}
        </p>
        <label>
          {t("展示账号")}
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
          {t("更新展示账号")}
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

function SettingsRowContent({
  item,
  breadcrumb,
}: {
  item: SettingsItem;
  breadcrumb?: string;
}) {
  const { t } = useTranslation();
  return (
    <div className={styles.rowInner}>
      <div className={styles.rowIcon}>
        <Icon name={item.icon} />
      </div>
      <div className={styles.rowText}>
        <span className={styles.rowTitle}>{t(item.title)}</span>
        <span className={styles.rowDescription}>
          {breadcrumb ? `${breadcrumb} · ` : ""}
          {t(item.description)}
        </span>
      </div>
      <Icon name="chevron-right" className={styles.rowChevron} />
    </div>
  );
}
function SettingsRow({
  item,
  onActivate,
}: {
  item: SettingsItem;
  onActivate: (
    event: MouseEvent<HTMLAnchorElement>,
    item: SettingsItem,
  ) => void;
}) {
  return (
    <InternalLink
      className={styles.settingsRow}
      href={settingsItemPath(item)}
      data-focus-key={settingsItemPath(item)}
      onClick={(event) => onActivate(event, item)}
    >
      <SettingsRowContent item={item} />
    </InternalLink>
  );
}

export function SettingsPage({
  site,
  onSaved,
}: {
  site: SiteIdentity;
  onSaved: (site: SiteIdentity) => void;
}) {
  const { t } = useTranslation();
  const navigation = useNavigation();
  const { requestLogin } = useLoginNavigation();
  const { account, applyAccount } = useSession();
  const admin = isAdministrator(account);
  const pathname = navigation.panel?.from.pathname ?? navigation.pathname;
  const { root, category, item } = settingsLocation(pathname);
  const wide = useMediaQuery(RAIL_VIEWPORT_QUERY);
  const [query, setQuery] = useState("");
  const [searching, setSearching] = useState(false);
  const searchInput = useRef<HTMLInputElement>(null);
  const categoryNavigation = useRef<HTMLElement>(null);
  const restoreCategoryFocus = useRef(false);
  const [waiting, setWaiting] = useState(0);
  const modalReturnFocus = useRef<HTMLElement>(null);
  const detailScroll = useRef<HTMLDivElement>(null);
  const detailHeading = useRef<HTMLHeadingElement>(null);
  const parentHeading = useRef<HTMLHeadingElement>(null);
  const scrollPositions = useRef(new Map<string, number>());
  const shownPath = useRef(pathname);
  const categories = settingsCategories.filter(
    (entry) => admin || !entry.admin,
  );
  const allowed = !category.admin || admin;
  const availableItems = settingsItems.filter((entry) =>
    categories.some((category) => category.id === entry.category),
  );
  const search = query.trim().toLocaleLowerCase();
  const searchScore = (entry: SettingsItem) => {
    const title = t(entry.title).toLocaleLowerCase();
    if (title === search) return 0;
    if (title.startsWith(search)) return 1;
    if (title.includes(search)) return 2;
    const parent = settingsCategories.find(
      (category) => category.id === entry.category,
    )!;
    if (t(parent.title).toLocaleLowerCase().includes(search)) return 3;
    return t(entry.description).toLocaleLowerCase().includes(search)
      ? 4
      : Infinity;
  };
  const results = search
    ? availableItems
        .map((entry) => ({ entry, score: searchScore(entry) }))
        .filter(({ score }) => Number.isFinite(score))
        .sort((left, right) => left.score - right.score)
        .map(({ entry }) => entry)
    : [];
  const categoryHref = `/settings/${category.id}`;
  const upHref = item ? categoryHref : "/settings";

  useLayoutEffect(() => {
    const changed = shownPath.current !== pathname;
    if (changed && detailScroll.current)
      detailScroll.current.scrollTop =
        scrollPositions.current.get(pathname) ?? 0;
    shownPath.current = pathname;
    // History restores focus to the link that opened a page. Fresh destinations
    // instead announce their heading, without stealing focus on a resize.
    if (changed && window.history.state?.momentFresh && (!wide || item)) {
      (root ? parentHeading : detailHeading).current?.focus({
        preventScroll: true,
      });
    }
    if (!wide && document.activeElement instanceof HTMLElement) {
      const hiddenPanel = root
        ? "[data-settings-detail]"
        : "[data-settings-categories]";
      if (document.activeElement.closest(hiddenPanel)) {
        (root ? parentHeading : detailHeading).current?.focus({
          preventScroll: true,
        });
      }
    }
  }, [pathname, wide, root, item]);

  useLayoutEffect(() => {
    if (!searching && restoreCategoryFocus.current) {
      restoreCategoryFocus.current = false;
      const nav = categoryNavigation.current;
      const tab =
        nav?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]') ??
        nav?.querySelector<HTMLElement>('[role="tab"]');
      tab?.focus({ preventScroll: true });
    }
  }, [searching]);

  const modalItem = settingsItems.find(
    (entry) => settingsItemPath(entry) === navigation.pathname,
  );
  const preferenceModal =
    navigation.panel?.key === "settings-dialog" ? modalItem?.id : undefined;
  function isDialogItem(entry: SettingsItem) {
    return (
      entry.category === "display" ||
      (entry.category === "account" && entry.id === "profile")
    );
  }
  function activateDialog(entry: SettingsItem) {
    navigation.openPanel(
      entry.category === "account"
        ? PROFILE_EDIT_PATH
        : settingsItemPath(entry),
      entry.category === "account" ? "profile-editor" : "settings-dialog",
    );
  }
  function openItem(event: MouseEvent<HTMLAnchorElement>, entry: SettingsItem) {
    if (
      !isDialogItem(entry) ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    modalReturnFocus.current = event.currentTarget;
    activateDialog(entry);
  }
  useEffect(() => {
    if (item && isDialogItem(item) && !navigation.panel) {
      navigation.replace(`/settings/${item.category}`);
      activateDialog(item);
    }
  }, [item, navigation.panel]);
  function closePreferenceDialog() {
    navigation.closePanel("settings-dialog");
  }

  function closeSearch() {
    restoreCategoryFocus.current = true;
    setSearching(false);
    setQuery("");
    searchInput.current?.blur();
  }
  function goUp() {
    if (navigation.previousHref === upHref) navigation.goBack(upHref);
    else navigation.replace(upHref);
  }
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
  const visible = (categoryId: string, itemIds: string[]) =>
    allowed && item?.category === categoryId && itemIds.includes(item.id);
  const surface = (show: boolean, children: ReactNode) => (
    <div className={styles.settingsSurface} hidden={!show}>
      {children}
    </div>
  );
  return (
    <div
      className={styles.settingsPage}
      data-settings-root={root}
      data-settings-page={item ? `${item.category}/${item.id}` : category.id}
    >
      <section
        className={styles.categoryPanel}
        aria-label={t("设置分类")}
        data-settings-categories
      >
        <header className={styles.panelHeader}>
          <span className={styles.rootBack}>
            <BackControl>
              <button
                type="button"
                aria-label={t("返回上一页")}
                onClick={() => navigation.goBack("/")}
              />
            </BackControl>
          </span>
          <h2 ref={parentHeading} tabIndex={-1}>
            {t("设置")}
          </h2>
        </header>
        <Autocomplete.Root
          items={results}
          filter={null}
          inline
          open={searching}
          mode="none"
          autoHighlight="always"
          value={query}
          onValueChange={setQuery}
          itemToStringValue={(entry) => t(entry.title)}
        >
          <div className={styles.categoryScroll}>
            <div className={styles.settingsSearch}>
              {searching ? (
                <BackControl>
                  <button
                    type="button"
                    aria-label={t("退出设置搜索")}
                    onClick={closeSearch}
                  />
                </BackControl>
              ) : null}
              <SearchField
                className={styles.searchInput}
                inputRef={searchInput}
                data-focus-key="settings:search"
                aria-label={t("搜索设置")}
                placeholder={t("搜索设置")}
                autoComplete="off"
                spellCheck={false}
                clearLabel={t("清除设置搜索")}
                onFocus={() => setSearching(true)}
                onKeyDown={(event) => {
                  if (event.key === "Escape") closeSearch();
                }}
              />
            </div>
            {searching ? (
              <div>
                <p className={styles.searchSummary} role="status">
                  {!search
                    ? t("尝试搜索账号、显示或备份等设置。")
                    : results.length
                      ? t("找到 {{count}} 项设置", { count: results.length })
                      : t("没有找到匹配的设置")}
                </p>
                <Autocomplete.List aria-label={t("设置搜索结果")}>
                  {(entry: SettingsItem) => (
                    <Autocomplete.Item
                      key={settingsItemPath(entry)}
                      value={entry}
                      className={styles.settingsRow}
                      render={
                        <InternalLink
                          href={settingsItemPath(entry)}
                          onClick={(event) => openItem(event, entry)}
                          data-focus-key={`settings-search:${settingsItemPath(entry)}`}
                        />
                      }
                    >
                      <SettingsRowContent
                        item={entry}
                        breadcrumb={t(
                          settingsCategories.find(
                            (category) => category.id === entry.category,
                          )!.title,
                        )}
                      />
                    </Autocomplete.Item>
                  )}
                </Autocomplete.List>
              </div>
            ) : (
              <nav ref={categoryNavigation} aria-label={t("设置分类")}>
                <Tabs.Root
                  orientation="vertical"
                  value={wide || !root ? category.id : null}
                >
                  <Tabs.List aria-label={t("设置分类")}>
                    {categories.map((entry) => (
                      <div
                        className={styles.categoryRow}
                        key={entry.id}
                        data-selected={
                          entry.id === category.id && (wide || !root)
                        }
                      >
                        <Tabs.Tab
                          value={entry.id}
                          nativeButton={false}
                          render={
                            <InternalLink
                              href={`/settings/${entry.id}`}
                              data-focus-key={`/settings/${entry.id}`}
                              onClick={(event) => {
                                if (pathname === `/settings/${entry.id}`)
                                  event.preventDefault();
                              }}
                            />
                          }
                        >
                          <div className={styles.categoryInner}>
                            <span>
                              {t(entry.title)}
                              {entry.id === "members" && waiting > 0 ? (
                                <span
                                  className={styles.navBadge}
                                  aria-label={t("{{count}} 位待审核", {
                                    count: waiting,
                                  })}
                                >
                                  {waiting}
                                </span>
                              ) : null}
                            </span>
                            <Icon
                              name="chevron-right"
                              className={styles.rowChevron}
                            />
                          </div>
                        </Tabs.Tab>
                        {entry.id === category.id && (wide || !root) ? (
                          <div
                            aria-hidden="true"
                            className={styles.selectionIndicator}
                          />
                        ) : null}
                      </div>
                    ))}
                  </Tabs.List>
                </Tabs.Root>
              </nav>
            )}
          </div>
        </Autocomplete.Root>
      </section>
      <section
        className={styles.detailPanel}
        data-settings-detail
        aria-label={
          item ? t(item.title) : t(category.title, { context: "heading" })
        }
      >
        <header className={styles.panelHeader}>
          <span className={item ? styles.detailBack : styles.categoryBack}>
            <BackControl>
              <button
                type="button"
                aria-label={t("返回上一级")}
                onClick={goUp}
              />
            </BackControl>
          </span>
          <h2 ref={detailHeading} tabIndex={-1}>
            {item ? t(item.title) : t(category.title, { context: "heading" })}
          </h2>
        </header>
        <div
          ref={detailScroll}
          className={styles.detailScroll}
          onScroll={(event) =>
            scrollPositions.current.set(
              shownPath.current,
              event.currentTarget.scrollTop,
            )
          }
        >
          {!allowed ? (
            <p className={styles.categoryDescription} role="alert">
              {t("此设置需要管理员权限。")}
            </p>
          ) : !item ? (
            <>
              <p className={styles.categoryDescription}>
                {t(category.description)}
              </p>
              {availableItems
                .filter((entry) => entry.category === category.id)
                .map((entry) => (
                  <SettingsRow
                    key={settingsItemPath(entry)}
                    item={entry}
                    onActivate={openItem}
                  />
                ))}
            </>
          ) : null}
          {account ? <CredentialConfirmation key={account.userId} /> : null}
          {account ? (
            <>
              {surface(
                visible("account", ["password"]) ||
                  visible("security", ["tokens"]),
                <AccountSecurity
                  instanceId={site.instanceId}
                  panel={item?.id === "password" ? "password" : "tokens"}
                />,
              )}
              {surface(
                visible("account", ["export", "import"]),
                <PersonalData
                  key={account.userId}
                  panel={item?.id === "export" ? "export" : "import"}
                />,
              )}
              {surface(
                visible("security", ["identities", "sessions"]),
                <AccountAccess
                  timezone={site.timezone}
                  instanceId={site.instanceId}
                  panel={item?.id === "sessions" ? "sessions" : "identities"}
                  onSignedOut={() => requestLogin()}
                />,
              )}
            </>
          ) : null}
          {admin ? (
            <>
              {surface(
                visible("site", ["information", "comments", "profile"]),
                <SiteConfiguration
                  site={site}
                  onSaved={onSaved}
                  active={visible("site", ["profile"])}
                  panel={
                    item?.id === "comments"
                      ? "comments"
                      : item?.id === "profile"
                        ? "profile"
                        : "information"
                  }
                />,
              )}
              {surface(
                visible("members", ["roster", "registration"]),
                <MembersSection
                  onWaitingChange={setWaiting}
                  panel={
                    item?.id === "registration" ? "registration" : "roster"
                  }
                />,
              )}
              {surface(
                visible("backups", ["export", "restore"]),
                <BackupRestore
                  instanceId={site.instanceId}
                  settingsBlocked={false}
                  panel={
                    item?.category === "backups" && item.id === "export"
                      ? "export"
                      : "restore"
                  }
                />,
              )}
            </>
          ) : null}
        </div>
      </section>
      {account ? (
        <ProfileEditor
          hideTrigger
          account={account}
          onSaved={adopt}
          finalFocus={modalReturnFocus}
        />
      ) : null}
      <AppearanceDialog
        open={preferenceModal === "appearance"}
        onOpenChange={(open) => {
          if (!open) closePreferenceDialog();
        }}
        account={account}
        site={site}
      />
      {preferenceModal === "language" ? (
        <LanguageDialog onClose={closePreferenceDialog} />
      ) : null}
    </div>
  );
}
