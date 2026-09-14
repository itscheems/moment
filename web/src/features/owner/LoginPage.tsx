import type { SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
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
  const { goBack } = useNavigation();
  return (
    <div className={styles.loginScreen}>
      {/* The way out is where the reader came from, in the corner every
          other page keeps its arrow -- not a strip under the form, which
          read as a third panel and sat below the fold on a short screen. */}
      <InternalLink
        aria-label="返回时间流"
        className={styles.loginBack}
        href="/"
        onClick={(event) => {
          event.preventDefault();
          goBack("/");
        }}
      >
        <Icon name="back" />
      </InternalLink>
      <div className={styles.loginSplit}>
        <div aria-hidden="true" className={styles.brandPanel} />
        <main className={styles.actionPanel}>
          <div className={styles.headlineBlock}>
            {/* The site's own name is not the headline. A visitor arrives here
                before knowing whose instance they are on, and the name they
                want is on the timeline behind them, not above the form. */}
            <h1 className={styles.displayHeadline}>Happening now</h1>
            <p className={styles.subHeadline}>登录后继续记录此刻。</p>
          </div>
          <OwnerLogin heading={false} site={site} />
        </main>
      </div>
    </div>
  );
}
