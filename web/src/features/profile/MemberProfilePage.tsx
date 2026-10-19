import { TimelinePage } from "../timeline/TimelinePage";
import { useCallback, useEffect, useMemo, useState } from "react";

import {
  PublicApi,
  PublicApiError,
  type PublicAccount,
  type SiteIdentity,
} from "../../api/public";
import { useNavigation } from "../../app/navigation";
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
  ReadRecoveryNotice,
  TimelineLoading,
} from "../timeline/PublicStates";
import { ProfileEditor } from "./ProfileEditor";
import styles from "./ProfilePage.module.css";

const missingRoute = {
  id: "not-found",
  title: "没有这个用户",
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
  const { account: viewer } = useSession();
  const navigation = useNavigation();
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

  const feedApi = useMemo(
    () => api.forAuthor(account?.userId ?? ""),
    [api, account?.userId],
  );

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
  return (
    <section aria-label={`${title} 的个人资料`}>
      {state === "loading" || state === "failed" ? (
        <ReadRecoveryNotice error={error} />
      ) : null}
      <div className={styles.cover}>
        <SiteCover eager site={account} />
      </div>
      <div className={styles.identity}>
        <div className={styles.avatar}>
          <SiteAvatar eager site={account} />
        </div>
        {viewer?.userId === account.userId ? (
          <div className={styles.profileActions}>
            <div className={styles.profileEditorSlot}>
              <ProfileEditor account={account} onSaved={adopt} />
            </div>
          </div>
        ) : null}
        <h2>{title}</h2>
        <p className={styles.handle}>{siteHandle(account)}</p>
        {account.bio.trim() ? <p>{account.bio.trim()}</p> : null}
        <header className={styles.contentHeading}>
          <h3>成员</h3>
          <span>参与本站的对话</span>
        </header>
      </div>
      <TimelinePage
        api={feedApi}
        site={site}
        initialSearch=""
        canPublish={viewer?.status === "active"}
        key={account.userId}
      />
    </section>
  );
}
