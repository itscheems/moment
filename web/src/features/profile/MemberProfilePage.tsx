import { getLocale } from "../../i18n";
import { t } from "../../i18n";
import { useTranslation } from "react-i18next";
import { AccountName } from "../../components/AccountName";
import { TimelinePage } from "../timeline/TimelinePage";
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicAccount,
  type SiteIdentity,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { profileFeedPath, profileFromPath } from "../../app/routes";
import { Icon } from "../../components/icons";
import { ProfileTabs } from "./ProfileTabs";
import { ExactTimeTooltip } from "../../components/TimelineInteractions";
import { ShellState } from "../../components/ShellState";
import { useReadRecovery } from "../../components/useReadRecovery";
import {
  SiteAvatar,
  SiteCover,
  siteDisplayName,
  siteHandle,
} from "../../components/SiteIdentity";
import { useSession } from "../auth/SessionProvider";
import {
  PublicError,
  ReadRecoveryToast,
  TimelineLoading,
} from "../timeline/PublicStates";
import { ProfileEditor } from "./ProfileEditor";
import { ProfileHeader } from "./ProfileHeader";
import { SuggestedPeople } from "../people/SuggestedPeople";
import { formatMomentDate } from "../posts/dates";
import type { ProfileFilter } from "./profileTimeline";
import styles from "./ProfilePage.module.css";

const missingRoute = {
  id: "not-found",
  get title() {
    return t("没有这个用户");
  },
  eyebrow: "",
  href: "/",
} as const;

export function MemberProfilePage({
  api,
  username,
  site,
}: {
  api: PublicApi;
  username: string;
  site: SiteIdentity;
}) {
  const { t } = useTranslation();

  const { account: viewer } = useSession();
  const navigation = useNavigation();
  const filter = profileFromPath(navigation.pathname)?.filter ?? "all";
  const [account, setAccount] = useState<PublicAccount | null>(null);
  const [state, setState] = useState<
    "loading" | "ready" | "missing" | "failed"
  >("loading");
  const [attempt, setAttempt] = useState(0);
  const [error, setError] = useState<unknown>(null);
  const retry = useCallback(() => setAttempt((value) => value + 1), []);
  const recovering = useReadRecovery({
    error,
    loading: state === "loading",
    onRetry: retry,
    scope: username,
  });

  useEffect(() => {
    const controller = new AbortController();
    setState("loading");
    setError(null);
    void api
      .user(username, controller.signal)
      .then((found) => {
        if (controller.signal.aborted) return;
        setAccount(found);
        setState("ready");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setError(error);
        setState(
          error instanceof PublicApiError && error.status === 404
            ? "missing"
            : "failed",
        );
      });
    return () => controller.abort();
  }, [api, attempt, username]);

  /**
   * Adopt what the server just saved, and follow the handle if it moved.
   *
   * A handle is the address of this page, so renaming from here leaves the
   * browser standing at a path that now belongs to nobody. Taking the account
   * first and then navigating means the new page is already rendered from the
   * saved copy by the time the refetch confirms it.
   */
  const adopt = useCallback(
    (saved: PublicAccount) => {
      setAccount(saved);
      if (saved.username !== username) navigation.navigate(saved.profileUrl);
    },
    [navigation, username],
  );

  const hasAccount = account?.username === username;
  if (state === "loading" && !hasAccount) return <TimelineLoading />;
  if (state === "missing")
    return <ShellState route={missingRoute} state={null} />;
  if (
    (state === "failed" && (!recovering || !hasAccount)) ||
    !account ||
    !hasAccount
  ) {
    return <PublicError kind="timeline" recovering={recovering} />;
  }

  const title = siteDisplayName(account);
  const joined = account.joinedAt
    ? new Intl.DateTimeFormat(getLocale(), {
        year: "numeric",
        month: "long",
        timeZone: site.timezone,
      }).format(new Date(account.joinedAt))
    : null;
  const joinedExact = account.joinedAt
    ? formatMomentDate(account.joinedAt, site.timezone).absolute
    : null;
  const selectFilter = (next: ProfileFilter) => {
    navigation.navigate(profileFeedPath(account.username, next), {
      preserveScroll: true,
    });
  };
  return (
    <section aria-label={t("{{value1}} 的个人资料", { value1: title })}>
      <ProfileHeader api={api} userId={account.userId} title={title} />
      <SuggestedPeople
        api={api}
        kind="profile"
        profileUsername={username}
        active={account.username === username}
      />
      {state === "loading" || state === "failed" ? (
        <ReadRecoveryToast error={error} />
      ) : null}
      <div className={styles.cover}>
        <SiteCover eager site={account} />
      </div>
      <div className={styles.identity}>
        <div className={styles.profileTop}>
          <div className={styles.avatar}>
            <SiteAvatar eager site={account} />
          </div>
          {viewer?.userId === account.userId ? (
            <div className={styles.profileActions}>
              <div className={styles.profileEditorSlot}>
                <ProfileEditor
                  account={account}
                  onSaved={adopt}
                  triggerClassName={styles.editProfile}
                />
              </div>
            </div>
          ) : null}
        </div>
        <h2>
          <AccountName identity={account} name={title} prominent />
        </h2>
        <p className={styles.handle}>{siteHandle(account)}</p>
        {account.bio.trim() ? <p>{account.bio.trim()}</p> : null}
        <div className={styles.profileMeta}>
          {joined && joinedExact && account.joinedAt ? (
            <ExactTimeTooltip
              exact={joinedExact}
              children={
                <time dateTime={account.joinedAt}>
                  <Icon name="calendar" /> {joined}
                  {t("加入")}
                </time>
              }
            />
          ) : null}
        </div>
      </div>
      <ProfileTabs
        key={`${account.userId}:${viewer?.userId ?? "guest"}`}
        filter={filter}
        onSelect={selectFilter}
      >
        {(kind) => (
          <MemberProfileFeed
            api={api}
            userId={account.userId}
            kind={kind}
            site={site}
            canPublish={viewer?.status === "active"}
          />
        )}
      </ProfileTabs>
    </section>
  );
}

function MemberProfileFeed({
  api,
  userId,
  kind,
  site,
  canPublish,
}: {
  api: PublicApi;
  userId: string;
  kind: ProfileFilter;
  site: SiteIdentity;
  canPublish: boolean;
}) {
  const feedApi = useMemo(
    () => api.forAuthor(userId, kind === "all" ? undefined : kind),
    [api, userId, kind],
  );
  return (
    <TimelinePage
      api={feedApi}
      site={site}
      initialSearch=""
      canPublish={canPublish}
    />
  );
}
