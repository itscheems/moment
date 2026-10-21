import type { SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { CredentialLogin } from "./CredentialLogin";
import { AuthPageFrame } from "./AuthPageFrame";
import { authPageHref } from "./returnPath";
import styles from "../../components/Forms.module.css";

export function LoginPage({ site }: { site: SiteIdentity }) {
  const { search } = useNavigation();
  return (
    <AuthPageFrame headline="Happening now" subtitle="登录后继续记录此刻。">
      <CredentialLogin heading={false} site={site} />
      <p className={styles.authSwitch}>
        还没有账号？{" "}
        <InternalLink authSwitch href={authPageHref("/register", search)}>
          立即注册
        </InternalLink>
      </p>
    </AuthPageFrame>
  );
}
