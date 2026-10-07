import { useCallback, useEffect, useState } from "react";

import { PublicApi, PublicApiError, type Account } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { ShellState } from "../../components/ShellState";
import {
  SiteAvatar,
  SiteCover,
  siteDisplayName,
  siteHandle,
} from "../../components/SiteIdentity";
import { useSession } from "../auth/SessionProvider";
import { PublicError, TimelineLoading } from "../timeline/PublicStates";
import { ProfileEditor } from "./ProfileEditor";
import styles from "./ProfilePage.module.css";

const missingRoute = {
  id: "not-found",
  title: "没有这个用户",
  eyebrow: "",
  href: "/",
} as const;

/**
 * Somebody else's profile.
 *
 * The site timeline belongs to the owner, so a member's page is an identity
 * and nothing else -- there is no per-account stream behind it to page
 * through. An account that does not exist and one that is not active are the
 * same answer here, deliberately: the server will not say which.
 *
 * It is also the only place a member can edit that identity. The owner edits
 * theirs from the site profile, and settings is about the account rather than
 * the face on it -- so without the editor here, a member had a page bearing
 * their name and no way to change what it said.
 */
export function MemberProfilePage({
  api,
  username,
}: {
  api: PublicApi;
  username: string;
}) {
  const { account: viewer } = useSession();
  const navigation = useNavigation();
  const [account, setAccount] = useState<Account | null>(null);
  const [state, setState] = useState<
    "loading" | "ready" | "missing" | "failed"
  >("loading");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setState("loading");
    void api
      .user(username, controller.signal)
      .then((found) => {
        if (controller.signal.aborted) return;
        setAccount(found);
        setState("ready");
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setState(
          error instanceof PublicApiError && error.status === 404
            ? "missing"
            : "failed",
        );
      });
    return () => controller.abort();
  }, [api, attempt, username]);

  const retry = useCallback(() => setAttempt((value) => value + 1), []);

  /**
   * Adopt what the server just saved, and follow the handle if it moved.
   *
   * A handle is the address of this page, so renaming from here leaves the
   * browser standing at a path that now belongs to nobody. Taking the account
   * first and then navigating means the new page is already rendered from the
   * saved copy by the time the refetch confirms it.
   */
  const adopt = useCallback(
    (saved: Account) => {
      setAccount(saved);
      if (saved.username !== username) navigation.navigate(saved.profileUrl);
    },
    [navigation, username],
  );

  if (state === "loading") return <TimelineLoading />;
  if (state === "missing")
    return <ShellState route={missingRoute} state={null} />;
  if (state === "failed" || !account) {
    return <PublicError kind="timeline" onRetry={retry} />;
  }

  const title = siteDisplayName(account);
  return (
    <section aria-label={`${title} 的个人资料`} className={styles.profile}>
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
          <span>
            {account.role === "owner" ? "本站站长" : "参与本站的对话"}
          </span>
        </header>
      </div>
    </section>
  );
}
