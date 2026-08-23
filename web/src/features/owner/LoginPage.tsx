import type { SiteIdentity } from "../../api/public";
import { useNavigation } from "../../app/navigation";
import { SiteAvatar } from "../../components/SiteIdentity";
import { OwnerLogin } from "./OwnerLogin";
import styles from "./Owner.module.css";

/**
 * The sign-in screen, on its own page rather than inside the reading shell.
 *
 * Everything the shell offers -- timeline, search, archive, the composer --
 * either needs the session this page is here to obtain or is a place the
 * visitor was already sent away from. Framing the form with that navigation
 * made it read as one more panel among many instead of the one thing left to
 * do, so the shell is not rendered around it at all.
 */
export function LoginPage({ site }: { site: SiteIdentity }) {
  const { navigate } = useNavigation();
  const title = site.title.trim() || site.displayName.trim() || "Moment";
  return (
    <main className={styles.loginScreen}>
      <div className={styles.loginScreenInner}>
        <div className={styles.loginBrand}>
          <a
            href="/"
            onClick={(event) => {
              if (event.metaKey || event.ctrlKey || event.shiftKey) return;
              event.preventDefault();
              navigate("/");
            }}
          >
            <SiteAvatar eager site={site} />
          </a>
          <span className={styles.loginBrandName}>{title}</span>
        </div>
        <OwnerLogin site={site} />
        <button
          className={styles.loginBack}
          onClick={() => navigate("/")}
          type="button"
        >
          返回时间流
        </button>
      </div>
    </main>
  );
}
