import type { SiteIdentity } from "../../api/public";
import { InternalLink } from "../../app/navigation";
import { OwnerLogin } from "./OwnerLogin";
import styles from "./Owner.module.css";

/**
 * The sign-in screen, on its own page rather than inside the reading shell.
 *
 * Everything the shell offers either needs the session this page is here to
 * obtain or is a place the visitor was already sent away from. Framing the form
 * with that navigation made it read as one more panel among many instead of the
 * one thing left to do, so the shell is not rendered around it at all.
 *
 * Nothing here decides who the visitor is. The page used to call itself the
 * owner's, which told every other account they were in the wrong place before
 * anybody had typed a handle; what an account may do once it is signed in is
 * the server's question and is answered there.
 */
export function LoginPage({ site }: { site: SiteIdentity }) {
  const title = site.title.trim() || site.displayName.trim() || "Moment";
  return (
    <div className={styles.loginScreen}>
      <div className={styles.loginSplit}>
        {/* Decorative: the mark is repeated at a readable size in the column
            beside it, so a screen reader gains nothing from meeting it twice. */}
        <div aria-hidden="true" className={styles.brandPanel}>
          <img alt="" src="/moment-mark.svg" />
        </div>
        <main className={styles.actionPanel}>
          <img alt="" className={styles.actionMark} src="/moment-mark.svg" />
          <div className={styles.headlineBlock}>
            <h1 className={styles.displayHeadline}>{title}</h1>
            <p className={styles.subHeadline}>登录后继续记录此刻。</p>
          </div>
          <OwnerLogin heading={false} site={site} />
        </main>
      </div>
      <footer className={styles.loginFoot}>
        <InternalLink className={styles.loginBack} href="/">
          返回时间流
        </InternalLink>
      </footer>
    </div>
  );
}
