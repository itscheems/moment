import { useTranslation } from "react-i18next";
import type { SiteIdentity } from "../../api/public";
import { InternalLink, useNavigation } from "../../app/navigation";
import { CredentialLogin } from "./CredentialLogin";
import { AuthPageFrame } from "./AuthPageFrame";
import { authPageHref } from "./returnPath";
import styles from "../../components/Forms.module.css";

export function LoginPage({ site }: { site: SiteIdentity }) {
  const { t } = useTranslation();

  const { search } = useNavigation();
  return (
    <AuthPageFrame headline="Happening now">
      <CredentialLogin heading={false} site={site} />
      <p className={styles.authSwitch}>
        {t("还没有账号？")}{" "}
        <InternalLink authSwitch href={authPageHref("/register", search)}>
          {t("立即注册")}
        </InternalLink>
      </p>
    </AuthPageFrame>
  );
}
