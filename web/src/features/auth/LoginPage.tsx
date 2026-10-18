import type { SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { Icon } from "../../components/icons";
import { CredentialLogin } from "./CredentialLogin";
import styles from "../../components/Forms.module.css";

export function LoginPage({ site }: { site: SiteIdentity }) {
  const { goBack } = useNavigation();
  return (
    <div className={styles.loginScreen}>
      <div aria-hidden="true" className={styles.brandPanel} />
      {/* The way out is where the reader came from, in the corner of the
          page, where every other page keeps its arrow -- not a strip under
          the form, which read as a third panel and sat below the fold on a
          short screen. On the wide layout that corner is over the artwork,
          and the arrow stands there on a white disc rather than the dark
          blurred one it used to have, which read as a rough badge. */}
      <InternalLink
        aria-label="返回时间流"
        className={styles.loginBack}
        href="/"
        onClick={(event) => {
          if (
            event.defaultPrevented ||
            event.button !== 0 ||
            event.metaKey ||
            event.ctrlKey ||
            event.altKey ||
            event.shiftKey
          )
            return;
          event.preventDefault();
          goBack("/");
        }}
      >
        <Icon name="back" />
      </InternalLink>
      <div className={styles.loginSplit}>
        <main className={styles.actionPanel}>
          <div className={styles.headlineBlock}>
            {/* The site's own name is not the headline. A visitor arrives here
                before knowing whose instance they are on, and the name they
                want is on the timeline behind them, not above the form. */}
            <h1 className={styles.displayHeadline}>Happening now</h1>
            <p className={styles.subHeadline}>登录后继续记录此刻。</p>
          </div>
          <CredentialLogin heading={false} site={site} />
        </main>
      </div>
    </div>
  );
}
